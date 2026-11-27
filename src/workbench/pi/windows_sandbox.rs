//! Windows AppContainer process primitive.
//!
//! This module alone is not an isolation boundary for a workbench job. The
//! caller must prepare and validate per-job filesystem access, run the complete
//! isolation probe, and refuse execution if any check fails. The process is
//! created with the policy that opts out of ALL APPLICATION PACKAGES grants.
//!
//! No network capabilities are granted. Standard handles are duplicated into
//! a suspended process that already belongs to the kill-on-close job. The
//! AppContainer inherits those handles and that job before it can run.

#[path = "windows_network.rs"]
mod network;

use crate::model::{RelPath, Sha256};
use std::{
    ffi::{OsStr, OsString, c_void},
    fs::{self, OpenOptions},
    io::{self, PipeReader, Read, Write},
    mem::size_of,
    net::SocketAddr,
    os::windows::{
        ffi::{OsStrExt, OsStringExt},
        io::{AsHandle, AsRawHandle, BorrowedHandle, FromRawHandle, OwnedHandle},
        process::ExitStatusExt,
    },
    path::{Path, PathBuf},
    process::{ExitStatus, Output},
    ptr,
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};
use windows_sys::Wdk::Storage::FileSystem::NtSetSecurityObject;
use windows_sys::Win32::{
    Foundation::{
        DUPLICATE_SAME_ACCESS, DuplicateHandle, ERROR_INSUFFICIENT_BUFFER, HANDLE, LocalFree,
        RtlNtStatusToDosError, WAIT_OBJECT_0, WAIT_TIMEOUT,
    },
    Security::{
        ACCESS_ALLOWED_ACE, ACE_HEADER, ACL, AccessCheck,
        Authorization::{
            ConvertStringSecurityDescriptorToSecurityDescriptorW, EXPLICIT_ACCESS_W, GRANT_ACCESS,
            GetSecurityInfo, SDDL_REVISION_1, SE_FILE_OBJECT, SetEntriesInAclW, SetSecurityInfo,
            TRUSTEE_IS_SID, TRUSTEE_IS_UNKNOWN, TRUSTEE_W,
        },
        CONTAINER_INHERIT_ACE, CreateWellKnownSid, DACL_SECURITY_INFORMATION, DuplicateToken,
        EqualSid, FreeSid, GENERIC_MAPPING, GetAce, GetSecurityDescriptorSacl, GetTokenInformation,
        InitializeSecurityDescriptor, IsValidAcl, IsValidSid,
        Isolation::{CreateAppContainerProfile, DeleteAppContainerProfile},
        LABEL_SECURITY_INFORMATION, OBJECT_INHERIT_ACE, PRIVILEGE_SET,
        PROTECTED_DACL_SECURITY_INFORMATION, PSECURITY_DESCRIPTOR, PSID, SE_DACL_PROTECTED,
        SECURITY_CAPABILITIES, SECURITY_DESCRIPTOR, SID, SYSTEM_MANDATORY_LABEL_ACE,
        SecurityImpersonation, SetSecurityDescriptorControl, SetSecurityDescriptorDacl,
        SetSecurityDescriptorSacl, TOKEN_APPCONTAINER_INFORMATION, TOKEN_DUPLICATE,
        TOKEN_INFORMATION_CLASS, TOKEN_QUERY, TokenAppContainerSid, TokenCapabilities,
        TokenIsAppContainer, WinLowLabelSid,
    },
    Storage::FileSystem::{
        DELETE, FILE_ALL_ACCESS, FILE_DELETE_CHILD, FILE_GENERIC_EXECUTE, FILE_GENERIC_READ,
        FILE_GENERIC_WRITE, FILE_READ_DATA,
    },
    System::{
        JobObjects::{
            CreateJobObjectW, IsProcessInJob, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
            JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
            JobObjectBasicAccountingInformation, JobObjectExtendedLimitInformation,
            QueryInformationJobObject, SetInformationJobObject, TerminateJobObject,
        },
        SystemInformation::{GetSystemDirectoryW, GetWindowsDirectoryW},
        Threading::{
            CREATE_NO_WINDOW, CREATE_SUSPENDED, CREATE_UNICODE_ENVIRONMENT, CreateProcessW,
            DeleteProcThreadAttributeList, EXTENDED_STARTUPINFO_PRESENT, GetCurrentProcess,
            GetExitCodeProcess, InitializeProcThreadAttributeList, LPPROC_THREAD_ATTRIBUTE_LIST,
            OpenProcessToken, PROC_THREAD_ATTRIBUTE_ALL_APPLICATION_PACKAGES_POLICY,
            PROC_THREAD_ATTRIBUTE_HANDLE_LIST, PROC_THREAD_ATTRIBUTE_JOB_LIST,
            PROC_THREAD_ATTRIBUTE_PARENT_PROCESS, PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES,
            PROCESS_INFORMATION, ResumeThread, STARTF_USESTDHANDLES, STARTUPINFOEXW,
            TerminateProcess, UpdateProcThreadAttribute, WaitForSingleObject,
        },
        WindowsProgramming::PROCESS_CREATION_ALL_APPLICATION_PACKAGES_OPT_OUT,
    },
};

fn invalid(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn wide(value: &OsStr) -> io::Result<Vec<u16>> {
    let mut chars: Vec<_> = value.encode_wide().collect();
    if chars.contains(&0) {
        return Err(invalid("embedded NUL in Windows string"));
    }
    chars.push(0);
    Ok(chars)
}

fn dos_path_units(path: &[u16]) -> io::Result<&[u16]> {
    const VERBATIM: &[u16] = &[b'\\' as u16, b'\\' as u16, b'?' as u16, b'\\' as u16];
    let dos = path.strip_prefix(VERBATIM).unwrap_or(path);
    let [drive, colon, separator, ..] = dos else {
        return Err(invalid("process path must be an absolute local drive path"));
    };
    if !u8::try_from(*drive).is_ok_and(|letter| letter.is_ascii_alphabetic())
        || *colon != b':' as u16
        || *separator != b'\\' as u16
        || dos.contains(&0)
    {
        return Err(invalid("process path must be an absolute local drive path"));
    }
    Ok(dos)
}

/// CreateProcessW's current directory and some Windows shell APIs require DOS
/// syntax. Confirm that removing a verbatim prefix preserves the target, since
/// ordinary DOS paths can normalize names that a verbatim path would keep.
fn local_process_path(path: &Path) -> io::Result<PathBuf> {
    let original: Vec<_> = path.as_os_str().encode_wide().collect();
    let dos = dos_path_units(&original)?;
    if dos.len() == original.len() {
        return Ok(path.to_owned());
    }
    let converted = PathBuf::from(OsString::from_wide(dos));
    if fs::canonicalize(&converted)? != fs::canonicalize(path)? {
        return Err(invalid("DOS process path resolves to a different target"));
    }
    Ok(converted)
}

fn planned_scratch_path(path: &Path) -> io::Result<PathBuf> {
    let original: Vec<_> = path.as_os_str().encode_wide().collect();
    let dos = dos_path_units(&original)?;
    let components = String::from_utf16(&dos[3..])
        .map_err(|_| invalid("planned scratch path is not Unicode"))?;
    if components.contains('/') {
        return Err(invalid("planned scratch path must use Windows separators"));
    }
    RelPath::new(components.replace('\\', "/"))
        .map_err(|_| invalid("planned scratch path contains an unsafe component"))?;
    Ok(PathBuf::from(OsString::from_wide(dos)))
}

// CommandLineToArgvW / MS C runtime quoting. Do not construct shell commands here.
fn quote_arg(value: &OsStr) -> io::Result<Vec<u16>> {
    let chars: Vec<u16> = value.encode_wide().collect();
    if chars.contains(&0) {
        return Err(invalid("embedded NUL in argument"));
    }
    let mut out = vec![b'"' as u16];
    let mut slashes = 0;
    for c in chars {
        if c == b'\\' as u16 {
            slashes += 1;
        } else {
            let count = if c == b'"' as u16 {
                slashes * 2 + 1
            } else {
                slashes
            };
            out.extend(std::iter::repeat_n(b'\\' as u16, count));
            out.push(c);
            slashes = 0;
        }
    }
    out.extend(std::iter::repeat_n(b'\\' as u16, slashes * 2));
    out.push(b'"' as u16);
    Ok(out)
}

pub(super) const CMD_FLAGS: &str = " /d /e:on /v:off /s /c ";

#[derive(Clone, Copy)]
pub(super) enum Arguments<'a> {
    Process(&'a [OsString]),
    Cmd(&'a str),
}

fn command_line(program: &Path, args: Arguments<'_>) -> io::Result<Vec<u16>> {
    let mut out = quote_arg(program.as_os_str())?;
    match args {
        Arguments::Process(args) => {
            for arg in args {
                out.push(b' ' as u16);
                out.extend(quote_arg(arg)?);
            }
        }
        Arguments::Cmd(command) => {
            if command.trim().is_empty() || command.contains(['\0', '\r', '\n']) {
                return Err(invalid("cmd.exe requires a nonempty single-line command"));
            }
            if out.len() + CMD_FLAGS.len() + command.encode_utf16().count() + 2 > 8191 {
                return Err(invalid("cmd.exe command exceeds its UTF-16 length limit"));
            }
            // The /S switch strips this outer pair. Inner quotes and metacharacters
            // are deliberately parsed only by the confined shell.
            out.extend(CMD_FLAGS.encode_utf16());
            out.push(b'"' as u16);
            out.extend(command.encode_utf16());
            out.push(b'"' as u16);
        }
    }
    out.push(0);
    Ok(out)
}

pub(super) fn validate_command(program: &Path, command: &str) -> io::Result<()> {
    command_line(&local_process_path(program)?, Arguments::Cmd(command)).map(drop)
}

/// Only OS-resolved system executables are candidates for the initial probe.
/// Build toolchains are intentionally not inferred from user PATH.
pub(super) struct SystemTools {
    pub windows_dir: PathBuf,
    pub system_dir: PathBuf,
    pub command: PathBuf,
}

impl SystemTools {
    pub(super) fn discover() -> io::Result<Self> {
        fn read_directory(
            read: unsafe extern "system" fn(*mut u16, u32) -> u32,
        ) -> io::Result<PathBuf> {
            let mut buf = vec![0u16; 32768];
            let length = unsafe { read(buf.as_mut_ptr(), buf.len() as u32) } as usize;
            if length == 0 || length >= buf.len() {
                return Err(io::Error::last_os_error());
            }
            std::fs::canonicalize(PathBuf::from(OsString::from_wide(&buf[..length])))
        }
        let windows_dir = read_directory(GetWindowsDirectoryW)?;
        let system_dir = read_directory(GetSystemDirectoryW)?;
        let expected_system = std::fs::canonicalize(windows_dir.join("System32"))?;
        if !windows_dir.is_absolute() || system_dir != expected_system {
            return Err(io::Error::other("unsupported Windows system directory"));
        }
        let find = |relative: &str| -> io::Result<PathBuf> {
            let path = std::fs::canonicalize(system_dir.join(relative))?;
            if !path.starts_with(&system_dir) || !std::fs::metadata(&path)?.is_file() {
                return Err(io::Error::other(
                    "system executable is outside the trusted directory",
                ));
            }
            Ok(path)
        };
        let command = find("cmd.exe")?;
        Ok(Self {
            windows_dir,
            system_dir,
            command,
        })
    }
}

/// Probe material must be new, job-owned scratch files, not real credentials.
/// The caller must verify the scratch handle and identities and ensure both
/// sentinel files are inaccessible to the profile before calling.
pub(super) struct ProbeInputs<'a> {
    pub id: &'a str,
    pub scratch: &'a Path,
    pub outside_sentinel: &'a Path,
    pub credential_sentinel: &'a Path,
    pub sentinel_contents: &'a [u8],
}

/// The caller pins and verifies the private copy of Node and its directory.
pub(super) struct ProbeToolchain<'a> {
    pub node: &'a Path,
    pub tools_dir: BorrowedHandle<'a>,
    pub node_file: BorrowedHandle<'a>,
}

impl ProbeToolchain<'_> {
    fn grant<'a>(
        &'a self,
        directory: BorrowedHandle<'a>,
        profile: &Profile,
    ) -> io::Result<[ScratchGrant<'a>; 3]> {
        Ok([
            ScratchGrant::apply_one(self.tools_dir, profile, true, true)?,
            ScratchGrant::apply_one(self.node_file, profile, false, true)?,
            ScratchGrant::apply_one(directory, profile, true, false)?,
        ])
    }
}

fn probe_path(path: &Path) -> io::Result<String> {
    local_process_path(path)?
        .into_os_string()
        .into_string()
        .map_err(|_| invalid("probe path is not Unicode"))
}

fn node_probe_args(script: &str, input: &str) -> [OsString; 5] {
    ["--input-type=commonjs", "-e", script, "--", input].map(OsString::from)
}

const FILE_NETWORK_PROBE: &str = r#"
const fs = require('node:fs');
const { Socket } = require('node:net');
const { sentinels, endpoints } = JSON.parse(process.argv[1]);
const denied = (error) => error.code === 'EACCES' || error.code === 'EPERM';

function checkConnection({ host, port, family }) {
  return new Promise((resolve) => {
    const socket = new Socket();
    const done = (code) => { socket.destroy(); resolve(code); };
    socket.once('connect', () => done(13));
    socket.once('error', (error) => done(denied(error) ? 0 : 14));
    socket.setTimeout(1500, () => done(14));
    try {
      socket.connect({ host, port, family, autoSelectFamily: false });
    } catch (error) {
      done(denied(error) ? 0 : 14);
    }
  });
}

async function probe() {
  try {
    fs.writeFileSync('yss-probe.txt', 'YSSPROBE', { flag: 'wx' });
    if (fs.readFileSync('yss-probe.txt', 'utf8') !== 'YSSPROBE') return 10;
  } catch {
    return 10;
  }
  for (const path of sentinels) {
    try { fs.readFileSync(path); return 11; }
    catch (error) { if (!denied(error)) return 12; }
    try { fs.appendFileSync(path, 'YSS DENIAL PROBE'); return 15; }
    catch (error) { if (!denied(error)) return 16; }
  }
  for (const endpoint of endpoints) {
    const code = await checkConnection(endpoint);
    if (code !== 0) return code;
  }
  return 0;
}
probe().then((code) => { process.exitCode = code; }, () => { process.exitCode = 17; });
"#;

const PROCESS_CLEANUP_PROBE: &str = r#"
const fs = require('node:fs');
const { spawn } = require('node:child_process');
const { abnormal } = JSON.parse(process.argv[1]);
const child = spawn(process.execPath, ['--input-type=commonjs', '-e', `
  require('node:fs').writeFileSync('yss-child.txt', 'YSSCHILD', { flag: 'wx' });
  setTimeout(() => process.exit(18), 60000);
`], { stdio: 'inherit', windowsHide: true });
child.once('error', () => process.exit(19));
child.once('exit', () => process.exit(19));
if (abnormal) {
  setInterval(() => {
    if (fs.existsSync('yss-exit-root.txt')) process.exit(27);
  }, 25);
}
"#;

/// Checks a fresh isolated directory, two private sentinel files, and local
/// TCP endpoints known to be reachable by the parent. On a disconnected host
/// there may be only loopback endpoints. The launcher verifies that every
/// child has the expected AppContainer identity and zero token capabilities.
pub(super) fn probe_files_and_network(
    directory: BorrowedHandle<'_>,
    inputs: &ProbeInputs<'_>,
    toolchain: &ProbeToolchain<'_>,
    cancelled: impl FnMut() -> bool,
) -> io::Result<()> {
    let scratch = fs::canonicalize(inputs.scratch)?;
    let outside = fs::canonicalize(inputs.outside_sentinel)?;
    let credential = fs::canonicalize(inputs.credential_sentinel)?;
    let expected_len = inputs.sentinel_contents.len() as u64;
    let outside_metadata = fs::metadata(&outside)?;
    let credential_metadata = fs::metadata(&credential)?;
    if !scratch.is_dir()
        || outside.starts_with(&scratch)
        || credential.starts_with(&scratch)
        || outside == credential
        || inputs.sentinel_contents.is_empty()
        || inputs.sentinel_contents.len() > 1024
        || !outside_metadata.is_file()
        || !credential_metadata.is_file()
        || outside_metadata.len() != expected_len
        || credential_metadata.len() != expected_len
        || fs::read(&outside)? != inputs.sentinel_contents
        || fs::read(&credential)? != inputs.sentinel_contents
        || fs::read_dir(&scratch)?.next().is_some()
    {
        return Err(invalid("invalid or non-isolated probe material"));
    }
    let tools = SystemTools::discover()?;
    let listeners = network::listeners()?;
    let endpoints = listeners
        .iter()
        .map(|listener| {
            let socket = listener.local_addr()?;
            let address = match socket {
                SocketAddr::V6(socket) if socket.scope_id() != 0 => {
                    format!("{}%{}", socket.ip(), socket.scope_id())
                }
                _ => socket.ip().to_string(),
            };
            Ok(serde_json::json!({
                "host": address,
                "port": socket.port(),
                "family": if socket.is_ipv4() { 4 } else { 6 },
            }))
        })
        .collect::<io::Result<Vec<_>>>()?;
    let input = serde_json::json!({
        "sentinels": [probe_path(&outside)?, probe_path(&credential)?],
        "endpoints": endpoints,
    });
    let path =
        std::env::join_paths([&tools.system_dir]).map_err(|_| invalid("invalid system PATH"))?;
    let environment = Environment {
        system_root: &tools.windows_dir,
        path: &path,
        scratch: &scratch,
        projects: None,
    };
    let args = node_probe_args(FILE_NETWORK_PROBE, &input.to_string());
    let profile = Profile::create(inputs.id)?;
    let grants = toolchain.grant(directory, &profile)?;
    let mut stopped = false;
    let result = capture_input(
        profile,
        Invocation {
            program: toolchain.node,
            args: Arguments::Process(&args),
            cwd: &scratch,
            environment,
            input: &[],
            timeout: Duration::from_secs(15),
        },
        &mut stopped,
        cancelled,
        |_| false,
    );
    for grant in grants {
        grant.restore()?;
    }
    let output = result?;
    let status = output.status.code().unwrap_or(-1).cast_unsigned();
    if !stopped {
        return Err(io::Error::other(
            "file and network probe cleanup was not confirmed",
        ));
    }
    if status != 0 {
        let reason = match status {
            10 => "无法读写作业临时目录",
            11 => "可以读取工程外检测文件",
            12 => "工程外文件读取未返回预期的拒绝",
            13 => "可以连接本机检测端口",
            14 => "网络连接未返回预期的拒绝",
            15 => "可以写入工程外检测文件",
            16 => "工程外文件写入未返回预期的拒绝",
            0x8000_0003 if output.stderr.starts_with(b"WSAStartup: (10107)") => {
                "受检 Node.js 无法在无网络能力的 AppContainer 中初始化 Winsock（WSAStartup 10107）"
            }
            0x8000_0003 => "受检 Node.js 异常退出，无法完成文件与网络自检",
            _ => "无法完成文件与网络自检",
        };
        return Err(io::Error::other(format!(
            "{reason}（AppContainer 退出码 {status:#010X}）"
        )));
    }
    let marker = scratch.join("yss-probe.txt");
    let metadata = fs::symlink_metadata(&marker)?;
    if !metadata.is_file()
        || metadata.file_type().is_symlink()
        || metadata.len() != 8
        || fs::read(&marker)? != b"YSSPROBE"
    {
        return Err(io::Error::other(
            "AppContainer did not write expected probe output",
        ));
    }
    fs::remove_file(marker)?;
    if fs::read(fs::canonicalize(inputs.outside_sentinel)?)? != inputs.sentinel_contents
        || fs::read(fs::canonicalize(inputs.credential_sentinel)?)? != inputs.sentinel_contents
    {
        return Err(io::Error::other(
            "AppContainer changed an inaccessible sentinel",
        ));
    }
    for listener in listeners {
        match listener.accept() {
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => (),
            Ok(_) => return Err(io::Error::other("AppContainer reached a test endpoint")),
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

/// Verifies a running descendant is killed on cancellation and, with another
/// profile ID, on an abnormal root exit. This does not authorize commands by
/// itself; both checks and filesystem/network probes must be run successfully.
pub(super) fn probe_process_cleanup(
    directory: BorrowedHandle<'_>,
    scratch: &Path,
    cancellation_id: &str,
    abnormal_exit_id: &str,
    toolchain: &ProbeToolchain<'_>,
    mut cancelled: impl FnMut() -> bool,
) -> io::Result<()> {
    if cancellation_id == abnormal_exit_id {
        return Err(invalid(
            "cleanup probes require distinct profile identities",
        ));
    }
    let scratch = fs::canonicalize(scratch)?;
    if !scratch.is_dir() || fs::read_dir(&scratch)?.next().is_some() {
        return Err(invalid("cleanup probe requires a fresh empty directory"));
    }
    let marker = scratch.join("yss-child.txt");
    let exit_request = scratch.join("yss-exit-root.txt");
    let tools = SystemTools::discover()?;
    let path =
        std::env::join_paths([&tools.system_dir]).map_err(|_| invalid("invalid system PATH"))?;
    let env = Environment {
        system_root: &tools.windows_dir,
        path: &path,
        scratch: &scratch,
        projects: None,
    };
    for (id, abnormal) in [(cancellation_id, false), (abnormal_exit_id, true)] {
        let null = OpenOptions::new().read(true).write(true).open("NUL")?;
        let input = serde_json::json!({ "abnormal": abnormal });
        let args = node_probe_args(PROCESS_CLEANUP_PROBE, &input.to_string());
        let profile = Profile::create(id)?;
        let grants = toolchain.grant(directory, &profile)?;
        let child = SandboxedChild::spawn(
            profile,
            toolchain.node,
            Arguments::Process(&args),
            &scratch,
            &env,
            [null.as_handle(), null.as_handle(), null.as_handle()],
        );
        let result = (|| -> io::Result<()> {
            let mut child = child?;
            let start = Instant::now();
            let mut observed = false;
            while start.elapsed() < Duration::from_secs(10) && !cancelled() {
                if marker.is_file() && child.active_processes()? >= 2 {
                    observed = true;
                    break;
                }
                std::thread::sleep(Duration::from_millis(50));
            }
            if !observed {
                return Err(io::Error::other("running descendant was not observed"));
            }
            if abnormal {
                OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(&exit_request)?;
                if child.wait(Duration::from_secs(5), &mut cancelled)? != 27 {
                    return Err(io::Error::other("unexpected abnormal probe exit"));
                }
            } else {
                match child.wait(Duration::from_secs(2), || true) {
                    Err(error) if error.kind() == io::ErrorKind::Interrupted => (),
                    Err(error) => return Err(error),
                    Ok(_) => return Err(io::Error::other("unexpected cancellation probe exit")),
                }
            }
            if child.active_processes()? != 0 {
                return Err(io::Error::other("cleanup probe did not stop every process"));
            }
            Ok(())
        })();
        for grant in grants {
            grant.restore()?;
        }
        result?;
        let metadata = fs::symlink_metadata(&marker)?;
        if !metadata.is_file()
            || metadata.file_type().is_symlink()
            || metadata.len() != 8
            || fs::read(&marker)? != b"YSSCHILD"
        {
            return Err(io::Error::other("cleanup probe child marker is invalid"));
        }
        fs::remove_file(&marker)?;
        if abnormal {
            fs::remove_file(&exit_request)?;
        }
    }
    Ok(())
}

/// Check that the selected toolchain image is a Windows x64 executable.
pub(super) fn verify_node_image(node_image: &[u8], expected_digest: &Sha256) -> io::Result<()> {
    if node_image.len() > 128 * 1024 * 1024
        || node_image.len() < 128
        || &node_image[..2] != b"MZ"
        || Sha256::of(node_image) != *expected_digest
    {
        return Err(invalid("unverified Node executable"));
    }
    let pe_offset = u32::from_le_bytes(node_image[0x3c..0x40].try_into().unwrap()) as usize;
    let pe = node_image
        .get(pe_offset..pe_offset.saturating_add(26))
        .ok_or_else(|| invalid("invalid Node PE header"))?;
    if &pe[..4] != b"PE\0\0"
        || u16::from_le_bytes(pe[4..6].try_into().unwrap()) != 0x8664
        || u16::from_le_bytes(pe[22..24].try_into().unwrap()) & 0x2000 != 0
        || u16::from_le_bytes(pe[24..26].try_into().unwrap()) != 0x20b
    {
        return Err(invalid("Node is not a Windows x64 executable"));
    }
    Ok(())
}

fn read_output(
    mut reader: PipeReader,
    stdout: bool,
    sender: mpsc::Sender<(bool, Vec<u8>)>,
) -> io::Result<Vec<u8>> {
    const LIMIT: usize = 256 * 1024;
    let mut output = Vec::new();
    let mut buffer = [0; 8192];
    loop {
        let read = reader.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        let read = read.min(LIMIT - output.len());
        if read != 0 {
            let chunk = &buffer[..read];
            output.extend_from_slice(chunk);
            let _ = sender.send((stdout, chunk.to_vec()));
        }
    }
    Ok(output)
}

pub(super) struct Invocation<'a> {
    pub program: &'a Path,
    pub args: Arguments<'a>,
    pub cwd: &'a Path,
    pub environment: Environment<'a>,
    pub input: &'a [u8],
    pub timeout: Duration,
}

pub(super) fn capture_input(
    profile: Profile,
    invocation: Invocation<'_>,
    stopped: &mut bool,
    mut cancelled: impl FnMut() -> bool,
    mut output: impl FnMut(Option<(bool, &[u8])>) -> bool,
) -> io::Result<Output> {
    *stopped = false;
    let mut profile = Some(profile);
    let result = thread::scope(|scope| {
        let (child_stdin, mut stdin) = io::pipe()?;
        let (stdout, child_stdout) = io::pipe()?;
        let (stderr, child_stderr) = io::pipe()?;
        let (sender, events) = mpsc::channel();
        let stdout_sender = sender.clone();
        let stdout = thread::Builder::new()
            .name("pi-sandbox-stdout".into())
            .spawn_scoped(scope, move || read_output(stdout, true, stdout_sender))?;
        let stderr = thread::Builder::new()
            .name("pi-sandbox-stderr".into())
            .spawn_scoped(scope, move || read_output(stderr, false, sender))?;
        let stdin = thread::Builder::new()
            .name("pi-sandbox-stdin".into())
            .spawn_scoped(scope, move || stdin.write_all(invocation.input))?;
        let profile = profile
            .take()
            .ok_or_else(|| invalid("missing AppContainer profile"))?;
        let child = SandboxedChild::spawn(
            profile,
            invocation.program,
            invocation.args,
            invocation.cwd,
            &invocation.environment,
            [
                child_stdin.as_handle(),
                child_stdout.as_handle(),
                child_stderr.as_handle(),
            ],
        );
        drop(child_stdin);
        drop(child_stdout);
        drop(child_stderr);
        let mut child = child?;
        let exit = child.wait_confirmed(invocation.timeout, stopped, || {
            if cancelled() {
                return true;
            }
            for (stdout, chunk) in events.try_iter() {
                if output(Some((stdout, &chunk))) {
                    return true;
                }
            }
            output(None)
        });
        drop(child);
        let stdout = stdout
            .join()
            .map_err(|_| io::Error::other("sandbox stdout reader stopped"))??;
        let stderr = stderr
            .join()
            .map_err(|_| io::Error::other("sandbox stderr reader stopped"))??;
        let input_result = stdin
            .join()
            .map_err(|_| io::Error::other("sandbox stdin writer stopped"))?;
        for (stdout, chunk) in events.try_iter() {
            if output(Some((stdout, &chunk))) {
                break;
            }
        }
        let status = ExitStatus::from_raw(exit?);
        // The child can fail before reading all of stdin. Keep its confirmed
        // failure status and stderr when the unread end of the pipe closes.
        match input_result {
            Err(error) if !status.success() && error.kind() == io::ErrorKind::BrokenPipe => {}
            result => result?,
        }
        Ok(Output {
            status,
            stdout,
            stderr,
        })
    });
    if let Some(profile) = profile {
        // Pipe or thread setup failed before any child could be created.
        profile.close()?;
        *stopped = true;
    }
    result
}

/// Supplies a fresh, deliberately small environment. The caller must validate
/// each directory against project and credential boundaries before invoking.
pub(super) struct Environment<'a> {
    pub system_root: &'a Path,
    pub path: &'a OsStr,
    pub scratch: &'a Path,
    pub projects: Option<&'a OsStr>,
}

impl Environment<'_> {
    /// The command recovery record binds the environment before creating the
    /// job home. Only that path is normalized lexically during planning.
    pub(super) fn planned_block(&self) -> io::Result<Vec<u16>> {
        self.block_for_scratch(planned_scratch_path(self.scratch)?)
    }

    pub(super) fn block(&self) -> io::Result<Vec<u16>> {
        // Windows shell and framework libraries use DOS paths when they reopen
        // these directories. The conversion checks the original target identity.
        self.block_for_scratch(local_process_path(self.scratch)?)
    }

    fn block_for_scratch(&self, scratch: PathBuf) -> io::Result<Vec<u16>> {
        let system_root = local_process_path(self.system_root)?;
        let command = system_root.join(r"System32\cmd.exe");
        let path = std::env::split_paths(self.path)
            .map(|path| local_process_path(&path))
            .collect::<io::Result<Vec<_>>>()?;
        let path = std::env::join_paths(path).map_err(|_| invalid("invalid command PATH"))?;
        // Already sorted case-insensitively, as required for Windows environment blocks.
        let entries: [(&str, &OsStr); 11] = [
            ("APPDATA", scratch.as_os_str()),
            ("ComSpec", command.as_os_str()),
            ("HOME", scratch.as_os_str()),
            ("LOCALAPPDATA", scratch.as_os_str()),
            ("PATH", &path),
            ("PATHEXT", OsStr::new(".COM;.EXE;.BAT;.CMD")),
            ("SystemRoot", system_root.as_os_str()),
            ("TEMP", scratch.as_os_str()),
            ("TMP", scratch.as_os_str()),
            ("USERPROFILE", scratch.as_os_str()),
            ("WINDIR", system_root.as_os_str()),
        ];
        let mut block = Vec::new();
        for (key, value) in entries {
            block.extend(key.encode_utf16());
            block.push(b'=' as u16);
            block.extend(wide(value)?);
        }
        if let Some(projects) = self.projects {
            block.extend("YSS_PROJECTS_JSON=".encode_utf16());
            block.extend(wide(projects)?);
        }
        block.push(0);
        Ok(block)
    }
}

pub(super) struct Profile {
    name: Vec<u16>,
    sid: PSID,
    deleted: bool,
}

impl Profile {
    pub(super) fn create(id: &str) -> io::Result<Self> {
        if id.len() != 32
            || !id
                .bytes()
                .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
        {
            return Err(invalid("invalid unique profile identifier"));
        }
        let name = wide(OsStr::new(&format!("YSS.Workbench.Job.{id}")))?;
        let mut sid: PSID = ptr::null_mut();
        // A collision is an error, never attach to a profile created by another actor.
        let result = unsafe {
            CreateAppContainerProfile(
                name.as_ptr(),
                name.as_ptr(),
                name.as_ptr(),
                ptr::null(),
                0,
                &mut sid,
            )
        };
        if result < 0 {
            return Err(io::Error::other(format!(
                "CreateAppContainerProfile failed: {result:#x}"
            )));
        }
        if sid.is_null() {
            unsafe { DeleteAppContainerProfile(name.as_ptr()) };
            return Err(io::Error::other("AppContainer profile has no SID"));
        }
        Ok(Self {
            name,
            sid,
            deleted: false,
        })
    }

    /// The callback may apply a per-profile ACL to verified job-owned paths.
    /// It must not retain the SID pointer after the callback returns.
    pub(super) fn with_sid<T>(
        &self,
        callback: impl FnOnce(PSID) -> io::Result<T>,
    ) -> io::Result<T> {
        callback(self.sid)
    }

    pub(super) fn close(mut self) -> io::Result<()> {
        let result = unsafe { DeleteAppContainerProfile(self.name.as_ptr()) };
        if result < 0 {
            return Err(io::Error::other(format!(
                "DeleteAppContainerProfile failed: {result:#x}"
            )));
        }
        self.deleted = true;
        Ok(())
    }
}

impl Drop for Profile {
    fn drop(&mut self) {
        unsafe {
            FreeSid(self.sid);
            // Best effort only. A separate recovery path must report and remove
            // profiles left behind by a crash or a failed delete before enablement.
            if !self.deleted {
                DeleteAppContainerProfile(self.name.as_ptr());
            }
        }
    }
}

struct LocalAllocation(*mut c_void);
impl Drop for LocalAllocation {
    fn drop(&mut self) {
        if !self.0.is_null() {
            unsafe { LocalFree(self.0) };
        }
    }
}

/// A temporary grant on a newly created, private, job-owned directory only.
///
/// The caller must supply a verified no-follow handle with WRITE_DAC,
/// WRITE_OWNER and READ_CONTROL; verify ownership and identity before and
/// after this guard, and never pass an existing project or toolchain directory.
/// A crash can leave this scratch directory behind, so startup recovery must
/// find and remove abandoned job directories before production enablement.
pub(super) struct ScratchGrant<'a> {
    directory: BorrowedHandle<'a>,
    original: LocalAllocation,
    old_dacl: *mut ACL,
    old_label: *mut ACL,
    restored: bool,
    single_object: bool,
}

// Scratch data may be read, written, executed and removed. Changing security
// descriptors or their owner is outside the grant made to the package SID.
const SCRATCH_ACCESS: u32 =
    FILE_GENERIC_READ | FILE_GENERIC_WRITE | FILE_GENERIC_EXECUTE | FILE_DELETE_CHILD | DELETE;

impl<'a> ScratchGrant<'a> {
    /// Apply to the already verified open object. Inheritable ACEs only apply to
    /// future creations; existing children are opened and verified separately.
    pub(super) fn apply_one(
        object: BorrowedHandle<'a>,
        profile: &Profile,
        directory: bool,
        read_only: bool,
    ) -> io::Result<Self> {
        Self::grant(object, profile, directory, read_only, true)
    }

    fn grant(
        directory: BorrowedHandle<'a>,
        profile: &Profile,
        is_directory: bool,
        read_only: bool,
        single_object: bool,
    ) -> io::Result<Self> {
        let mut descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
        let mut old_dacl = ptr::null_mut();
        let mut old_label = ptr::null_mut();
        let result = unsafe {
            GetSecurityInfo(
                directory.as_raw_handle(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION | LABEL_SECURITY_INFORMATION,
                ptr::null_mut(),
                ptr::null_mut(),
                &mut old_dacl,
                &mut old_label,
                &mut descriptor,
            )
        };
        if result != 0 {
            return Err(io::Error::from_raw_os_error(result as i32));
        }
        let original = LocalAllocation(descriptor);
        if old_dacl.is_null() || original.0.is_null() {
            return Err(io::Error::other("scratch directory has no protected DACL"));
        }
        let mut guard = Self {
            directory,
            original,
            old_dacl,
            old_label,
            restored: false,
            single_object,
        };
        let access = if read_only {
            FILE_GENERIC_READ | FILE_GENERIC_EXECUTE
        } else {
            SCRATCH_ACCESS
        };
        let inheritance = if is_directory {
            OBJECT_INHERIT_ACE | CONTAINER_INHERIT_ACE
        } else {
            0
        };
        let entry = EXPLICIT_ACCESS_W {
            grfAccessPermissions: access,
            grfAccessMode: GRANT_ACCESS,
            grfInheritance: inheritance,
            Trustee: TRUSTEE_W {
                TrusteeForm: TRUSTEE_IS_SID,
                TrusteeType: TRUSTEE_IS_UNKNOWN,
                ptstrName: profile.sid.cast(),
                ..Default::default()
            },
        };
        let mut new_dacl: *mut ACL = ptr::null_mut();
        let result = unsafe { SetEntriesInAclW(1, &entry, old_dacl, &mut new_dacl) };
        if result != 0 {
            return Err(io::Error::from_raw_os_error(result as i32));
        }
        let allocated_dacl = LocalAllocation(new_dacl.cast());
        if new_dacl.is_null() {
            return Err(io::Error::other("failed to construct scratch DACL"));
        }
        let label_sddl = wide(OsStr::new(if is_directory {
            "S:(ML;OICI;NW;;;LW)"
        } else {
            "S:(ML;;NW;;;LW)"
        }))?;
        let mut label_descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
        if unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                label_sddl.as_ptr(),
                SDDL_REVISION_1,
                &mut label_descriptor,
                ptr::null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let allocated_label = LocalAllocation(label_descriptor);
        let mut present = 0;
        let mut defaulted = 0;
        let mut label: *mut ACL = ptr::null_mut();
        if unsafe {
            GetSecurityDescriptorSacl(label_descriptor, &mut present, &mut label, &mut defaulted)
        } == 0
            || present == 0
            || label.is_null()
        {
            return Err(io::Error::other("failed to construct low integrity label"));
        }
        let result = guard.set_acls(new_dacl, label);
        drop(allocated_dacl);
        drop(allocated_label);
        if let Err(error) = result {
            guard.restore_inner()?;
            return Err(error);
        }
        if let Err(error) = guard.verify(profile, access, inheritance as u8) {
            guard.restore_inner()?;
            return Err(error);
        }
        Ok(guard)
    }

    fn verify(&self, profile: &Profile, access: u32, inheritance: u8) -> io::Result<()> {
        let mut descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
        let mut dacl: *mut ACL = ptr::null_mut();
        let mut label: *mut ACL = ptr::null_mut();
        let result = unsafe {
            GetSecurityInfo(
                self.directory.as_raw_handle(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION | LABEL_SECURITY_INFORMATION,
                ptr::null_mut(),
                ptr::null_mut(),
                &mut dacl,
                &mut label,
                &mut descriptor,
            )
        };
        if result != 0 {
            return Err(io::Error::from_raw_os_error(result as i32));
        }
        let _descriptor = LocalAllocation(descriptor);
        if dacl.is_null()
            || label.is_null()
            || unsafe { IsValidAcl(dacl) } == 0
            || unsafe { IsValidAcl(label) } == 0
        {
            return Err(io::Error::other("scratch security descriptor is invalid"));
        }
        let old_aces = acl_entries(self.old_dacl)?;
        let mut new_aces = acl_entries(dacl)?;
        if new_aces.len() != old_aces.len() + 1 {
            return Err(io::Error::other("scratch DACL changed unexpectedly"));
        }
        for old in old_aces {
            let Some(index) = new_aces.iter().position(|candidate| *candidate == old) else {
                return Err(io::Error::other("scratch DACL lost an existing entry"));
            };
            new_aces.remove(index);
        }
        let entry = &new_aces[0];
        let mut entry_sid = ace_sid(entry)?;
        let ace = unsafe { ptr::read_unaligned(entry.as_ptr().cast::<ACCESS_ALLOWED_ACE>()) };
        let sid = entry_sid.as_mut_ptr().cast();
        if ace.Header.AceType != 0
            || ace.Header.AceFlags != inheritance
            || ace.Mask != access
            || unsafe { IsValidSid(sid) } == 0
            || unsafe { EqualSid(sid, profile.sid) } == 0
        {
            return Err(io::Error::other(
                "scratch DACL does not match the job profile",
            ));
        }
        let labels = acl_entries(label)?;
        if labels.len() != 1 {
            return Err(io::Error::other("scratch integrity label is invalid"));
        }
        let mut label_sid = ace_sid(&labels[0])?;
        let ace =
            unsafe { ptr::read_unaligned(labels[0].as_ptr().cast::<SYSTEM_MANDATORY_LABEL_ACE>()) };
        let mut low_sid = [0usize; 16];
        let mut length = size_of::<[usize; 16]>() as u32;
        if unsafe {
            CreateWellKnownSid(
                WinLowLabelSid,
                ptr::null_mut(),
                low_sid.as_mut_ptr().cast(),
                &mut length,
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let sid = label_sid.as_mut_ptr().cast();
        // SYSTEM_MANDATORY_LABEL_ACE_TYPE = 17, NO_WRITE_UP = 1.
        if ace.Header.AceType != 17
            || ace.Header.AceFlags != inheritance
            || ace.Mask != 1
            || unsafe { IsValidSid(sid) } == 0
            || unsafe { EqualSid(sid, low_sid.as_mut_ptr().cast()) } == 0
        {
            return Err(io::Error::other("scratch integrity label is not low"));
        }
        Ok(())
    }

    fn restore_inner(&mut self) -> io::Result<()> {
        if self.restored {
            return Ok(());
        }
        self.set_acls(self.old_dacl, self.old_label)?;
        let mut descriptor = ptr::null_mut();
        let mut dacl = ptr::null_mut();
        let mut label = ptr::null_mut();
        let result = unsafe {
            GetSecurityInfo(
                self.directory.as_raw_handle(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION | LABEL_SECURITY_INFORMATION,
                ptr::null_mut(),
                ptr::null_mut(),
                &mut dacl,
                &mut label,
                &mut descriptor,
            )
        };
        if result != 0 {
            return Err(io::Error::from_raw_os_error(result as i32));
        }
        let _descriptor = LocalAllocation(descriptor);
        let labels = |value: *mut ACL| {
            if value.is_null() {
                Ok(Vec::new())
            } else {
                acl_entries(value)
            }
        };
        if acl_entries(dacl)? != acl_entries(self.old_dacl)?
            || labels(label)? != labels(self.old_label)?
        {
            return Err(io::Error::other(
                "restored sandbox descriptor did not match its original",
            ));
        }
        self.restored = true;
        Ok(())
    }

    fn set_acls(&self, dacl: *mut ACL, label: *mut ACL) -> io::Result<()> {
        if self.single_object {
            let mut descriptor = SECURITY_DESCRIPTOR::default();
            let descriptor = (&mut descriptor as *mut SECURITY_DESCRIPTOR).cast();
            if unsafe {
                InitializeSecurityDescriptor(descriptor, SDDL_REVISION_1) == 0
                    || SetSecurityDescriptorDacl(descriptor, 1, dacl, 0) == 0
                    || SetSecurityDescriptorControl(
                        descriptor,
                        SE_DACL_PROTECTED,
                        SE_DACL_PROTECTED,
                    ) == 0
                    || SetSecurityDescriptorSacl(descriptor, 1, label, 0) == 0
            } {
                return Err(io::Error::last_os_error());
            }
            // Set only this object's descriptor. SetSecurityInfo can walk the
            // entire subtree, including entries produced by the command.
            let status = unsafe {
                NtSetSecurityObject(
                    self.directory.as_raw_handle(),
                    DACL_SECURITY_INFORMATION | LABEL_SECURITY_INFORMATION,
                    descriptor,
                )
            };
            return if status < 0 {
                Err(io::Error::from_raw_os_error(unsafe {
                    RtlNtStatusToDosError(status) as i32
                }))
            } else {
                Ok(())
            };
        }
        let label = unsafe {
            SetSecurityInfo(
                self.directory.as_raw_handle(),
                SE_FILE_OBJECT,
                LABEL_SECURITY_INFORMATION,
                ptr::null_mut(),
                ptr::null_mut(),
                ptr::null(),
                label,
            )
        };
        let dacl = unsafe {
            SetSecurityInfo(
                self.directory.as_raw_handle(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                ptr::null_mut(),
                ptr::null_mut(),
                dacl,
                ptr::null(),
            )
        };
        if label != 0 || dacl != 0 {
            return Err(io::Error::other(format!(
                "scratch ACL restore failed (label {label}, DACL {dacl})"
            )));
        }
        Ok(())
    }

    pub(super) fn restore(mut self) -> io::Result<()> {
        self.restore_inner()
    }

    pub(super) fn keep_in_disposable_copy(mut self) {
        self.restored = true;
    }
}

fn acl_entries(acl: *mut ACL) -> io::Result<Vec<Vec<u8>>> {
    if acl.is_null() || unsafe { IsValidAcl(acl) } == 0 {
        return Err(io::Error::other("invalid ACL"));
    }
    let count = unsafe { (*acl).AceCount };
    let mut entries = Vec::with_capacity(count as usize);
    for index in 0..count {
        let mut raw = ptr::null_mut();
        if unsafe { GetAce(acl, index as u32, &mut raw) } == 0 || raw.is_null() {
            return Err(io::Error::last_os_error());
        }
        let size = unsafe { (*(raw.cast::<ACE_HEADER>())).AceSize } as usize;
        if size < size_of::<ACE_HEADER>() || size > unsafe { (*acl).AclSize as usize } {
            return Err(io::Error::other("invalid ACL entry size"));
        }
        entries.push(unsafe { std::slice::from_raw_parts(raw.cast::<u8>(), size) }.to_vec());
    }
    Ok(entries)
}

fn ace_sid(ace: &[u8]) -> io::Result<Vec<usize>> {
    // An ACCESS_ALLOWED or mandatory-label ACE starts with an 8-byte header
    // and mask followed by the variable-length SID. Copy it to aligned storage
    // before passing it to the Windows security APIs.
    if ace.len() < 16 {
        return Err(io::Error::other("ACL entry SID is truncated"));
    }
    let length = 8usize + usize::from(ace[9]) * 4;
    if length > ace.len() - 8 {
        return Err(io::Error::other("ACL entry SID exceeds entry size"));
    }
    let mut sid = vec![0usize; length.div_ceil(size_of::<usize>())];
    unsafe { ptr::copy_nonoverlapping(ace[8..].as_ptr(), sid.as_mut_ptr().cast::<u8>(), length) };
    Ok(sid)
}

impl Drop for ScratchGrant<'_> {
    fn drop(&mut self) {
        let _ = self.restore_inner();
        // Keep pointers into the security descriptor valid until restoration.
        let _ = &self.original;
    }
}

struct Attributes {
    storage: Vec<usize>,
    list: LPPROC_THREAD_ATTRIBUTE_LIST,
}

impl Attributes {
    fn new(count: u32) -> io::Result<Self> {
        let mut bytes = 0;
        unsafe { InitializeProcThreadAttributeList(ptr::null_mut(), count, 0, &mut bytes) };
        if bytes == 0 || bytes > 1024 * 1024 {
            return Err(io::Error::other("invalid process attribute buffer size"));
        }
        let mut storage = vec![0usize; bytes.div_ceil(size_of::<usize>())];
        let list = storage.as_mut_ptr().cast();
        if unsafe { InitializeProcThreadAttributeList(list, count, 0, &mut bytes) } == 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(Self { storage, list })
    }

    fn update(&mut self, key: u32, value: *const c_void, size: usize) -> io::Result<()> {
        if unsafe {
            UpdateProcThreadAttribute(
                self.list,
                0,
                key as usize,
                value,
                size,
                ptr::null_mut(),
                ptr::null(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }
}

impl Drop for Attributes {
    fn drop(&mut self) {
        unsafe { DeleteProcThreadAttributeList(self.list) };
        // Keep the backing allocation alive until after DeleteProcThreadAttributeList.
        let _ = &self.storage;
    }
}

fn duplicate_into_parent(
    handle: BorrowedHandle<'_>,
    parent: BorrowedHandle<'_>,
) -> io::Result<HANDLE> {
    let mut copy: HANDLE = ptr::null_mut();
    let process = unsafe { GetCurrentProcess() };
    if unsafe {
        DuplicateHandle(
            process,
            handle.as_raw_handle(),
            parent.as_raw_handle(),
            &mut copy,
            0,
            1,
            DUPLICATE_SAME_ACCESS,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    // The value belongs to the suspended parent's handle table. It is closed
    // by the kernel when that process terminates.
    Ok(copy)
}

fn token_information(
    token: BorrowedHandle<'_>,
    class: TOKEN_INFORMATION_CLASS,
) -> io::Result<(Vec<usize>, usize)> {
    // Token structures may contain aligned pointers into the returned buffer.
    let mut needed = 0;
    let result = unsafe {
        GetTokenInformation(
            token.as_raw_handle(),
            class,
            ptr::null_mut(),
            0,
            &mut needed,
        )
    };
    if result != 0 || needed == 0 || needed > 64 * 1024 {
        return Err(io::Error::other("invalid sandbox token buffer size"));
    }
    let error = io::Error::last_os_error();
    if error.raw_os_error() != Some(ERROR_INSUFFICIENT_BUFFER as i32) {
        return Err(error);
    }
    let mut buffer = vec![0usize; (needed as usize).div_ceil(size_of::<usize>())];
    let mut length = 0;
    if unsafe {
        GetTokenInformation(
            token.as_raw_handle(),
            class,
            buffer.as_mut_ptr().cast(),
            needed,
            &mut length,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    if length == 0 || length > needed {
        return Err(io::Error::other("invalid sandbox token information length"));
    }
    Ok((buffer, length as usize))
}

fn token_dword(token: BorrowedHandle<'_>, class: TOKEN_INFORMATION_CLASS) -> io::Result<u32> {
    let (buffer, length) = token_information(token, class)?;
    if length < size_of::<u32>() {
        return Err(io::Error::other("sandbox token information is incomplete"));
    }
    Ok(unsafe { buffer.as_ptr().cast::<u32>().read() })
}

fn verify_appcontainer(token: BorrowedHandle<'_>, profile: &Profile) -> io::Result<()> {
    // TokenCapabilities is a variable-length TOKEN_GROUPS. Its first DWORD is
    // GroupCount, including when the OS returns no SID_AND_ATTRIBUTES rows.
    if token_dword(token, TokenIsAppContainer)? == 0 || token_dword(token, TokenCapabilities)? != 0
    {
        return Err(io::Error::other(
            "expected an AppContainer with no token capabilities",
        ));
    }
    let (information, length) = token_information(token, TokenAppContainerSid)?;
    let prefix = size_of::<TOKEN_APPCONTAINER_INFORMATION>();
    if length < prefix {
        return Err(io::Error::other(
            "sandbox package SID information is incomplete",
        ));
    }
    let sid = unsafe {
        information
            .as_ptr()
            .cast::<TOKEN_APPCONTAINER_INFORMATION>()
            .read()
    }
    .TokenAppContainer;
    let header = std::mem::offset_of!(SID, SubAuthority);
    let offset = sid
        .addr()
        .checked_sub(information.as_ptr().addr())
        .filter(|offset| *offset >= prefix && *offset <= length.saturating_sub(header))
        .ok_or_else(|| io::Error::other("sandbox package SID is outside its token buffer"))?;
    let count_offset = std::mem::offset_of!(SID, SubAuthorityCount);
    let count = unsafe { sid.cast::<u8>().add(count_offset).read() } as usize;
    if count > (length - offset - header) / size_of::<u32>()
        || unsafe { IsValidSid(sid) } == 0
        || unsafe { EqualSid(sid, profile.sid) } == 0
    {
        return Err(io::Error::other(
            "sandbox package SID does not match its profile",
        ));
    }
    Ok(())
}

fn verify_package_grants(process: BorrowedHandle<'_>, profile: &Profile) -> io::Result<()> {
    let mut primary = ptr::null_mut();
    if unsafe {
        OpenProcessToken(
            process.as_raw_handle(),
            TOKEN_QUERY | TOKEN_DUPLICATE,
            &mut primary,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    let primary = unsafe { OwnedHandle::from_raw_handle(primary) };
    verify_appcontainer(primary.as_handle(), profile)?;
    let mut token = ptr::null_mut();
    if unsafe { DuplicateToken(primary.as_raw_handle(), SecurityImpersonation, &mut token) } == 0 {
        return Err(io::Error::last_os_error());
    }
    let token = unsafe { OwnedHandle::from_raw_handle(token) };
    // Everyone satisfies the normal user check. A regular AppContainer can
    // also satisfy the package check via S-1-15-2-1. Require the restricted
    // packages control to allow access so a broken check cannot pass twice.
    for (sddl, expected) in [
        ("O:SYG:SYD:(A;;0x1;;;WD)(A;;0x1;;;S-1-15-2-1)", false),
        ("O:SYG:SYD:(A;;0x1;;;WD)(A;;0x1;;;S-1-15-2-2)", true),
    ] {
        let sddl = wide(OsStr::new(sddl))?;
        let mut descriptor = ptr::null_mut();
        if unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl.as_ptr(),
                SDDL_REVISION_1,
                &mut descriptor,
                ptr::null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        let descriptor = LocalAllocation(descriptor);
        let mapping = GENERIC_MAPPING {
            GenericRead: FILE_GENERIC_READ,
            GenericWrite: FILE_GENERIC_WRITE,
            GenericExecute: FILE_GENERIC_EXECUTE,
            GenericAll: FILE_ALL_ACCESS,
        };
        let mut privileges = PRIVILEGE_SET::default();
        let mut length = size_of::<PRIVILEGE_SET>() as u32;
        let mut granted = 0;
        let mut allowed = 0;
        if unsafe {
            AccessCheck(
                descriptor.0,
                token.as_raw_handle(),
                FILE_READ_DATA,
                &mapping,
                &mut privileges,
                &mut length,
                &mut granted,
                &mut allowed,
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        if (allowed != 0) != expected || granted != if expected { FILE_READ_DATA } else { 0 } {
            return Err(io::Error::other("unexpected sandbox package access"));
        }
    }
    Ok(())
}

fn job() -> io::Result<OwnedHandle> {
    let handle = unsafe { CreateJobObjectW(ptr::null(), ptr::null()) };
    if handle.is_null() {
        return Err(io::Error::last_os_error());
    }
    let job = unsafe { OwnedHandle::from_raw_handle(handle) };
    let mut limits = JOBOBJECT_EXTENDED_LIMIT_INFORMATION::default();
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if unsafe {
        SetInformationJobObject(
            job.as_raw_handle(),
            JobObjectExtendedLimitInformation,
            (&limits as *const JOBOBJECT_EXTENDED_LIMIT_INFORMATION).cast(),
            size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(job)
}

fn create_suspended(
    program: &Path,
    args: Arguments<'_>,
    cwd: &Path,
    environment: &Environment<'_>,
    attributes: &Attributes,
    stdio: Option<[HANDLE; 3]>,
) -> io::Result<(OwnedHandle, OwnedHandle)> {
    let mut startup = STARTUPINFOEXW::default();
    startup.StartupInfo.cb = size_of::<STARTUPINFOEXW>() as u32;
    startup.lpAttributeList = attributes.list;
    if let Some([stdin, stdout, stderr]) = stdio {
        startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
        startup.StartupInfo.hStdInput = stdin;
        startup.StartupInfo.hStdOutput = stdout;
        startup.StartupInfo.hStdError = stderr;
    }
    let program = local_process_path(program)?;
    let application = wide(program.as_os_str())?;
    let directory = wide(local_process_path(cwd)?.as_os_str())?;
    let mut command = command_line(&program, args)?;
    let env = environment.block()?;
    let mut info = PROCESS_INFORMATION::default();
    let created = unsafe {
        CreateProcessW(
            application.as_ptr(),
            command.as_mut_ptr(),
            ptr::null(),
            ptr::null(),
            i32::from(stdio.is_some()),
            CREATE_SUSPENDED
                | CREATE_NO_WINDOW
                | CREATE_UNICODE_ENVIRONMENT
                | EXTENDED_STARTUPINFO_PRESENT,
            env.as_ptr().cast(),
            directory.as_ptr(),
            &startup.StartupInfo,
            &mut info,
        )
    };
    if created == 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(unsafe {
        (
            OwnedHandle::from_raw_handle(info.hProcess),
            OwnedHandle::from_raw_handle(info.hThread),
        )
    })
}

fn terminate_suspended(process: BorrowedHandle<'_>) -> io::Result<()> {
    if unsafe { TerminateProcess(process.as_raw_handle(), 1) } == 0 {
        return Err(io::Error::last_os_error());
    }
    match unsafe { WaitForSingleObject(process.as_raw_handle(), 5000) } {
        WAIT_OBJECT_0 => Ok(()),
        WAIT_TIMEOUT => Err(io::Error::new(
            io::ErrorKind::TimedOut,
            "suspended process did not terminate",
        )),
        _ => Err(io::Error::last_os_error()),
    }
}

fn verify_job(process: BorrowedHandle<'_>, job: BorrowedHandle<'_>) -> io::Result<()> {
    let mut assigned = 0;
    let result =
        unsafe { IsProcessInJob(process.as_raw_handle(), job.as_raw_handle(), &mut assigned) };
    if result != 0 && assigned != 0 {
        return Ok(());
    }
    let error = if result == 0 {
        io::Error::last_os_error()
    } else {
        io::Error::other("suspended process is outside the sandbox job")
    };
    // A process outside the job will not be stopped by closing the job.
    terminate_suspended(process)?;
    Err(error)
}

fn create_handle_parent(
    job: BorrowedHandle<'_>,
    environment: &Environment<'_>,
) -> io::Result<OwnedHandle> {
    let tools = SystemTools::discover()?;
    let jobs = [job.as_raw_handle()];
    let mut attributes = Attributes::new(1)?;
    // Assign the host to the job atomically. Its primary thread never runs.
    attributes.update(
        PROC_THREAD_ATTRIBUTE_JOB_LIST,
        jobs.as_ptr().cast(),
        size_of::<[HANDLE; 1]>(),
    )?;
    let (parent, _thread) = create_suspended(
        &tools.command,
        Arguments::Process(&[]),
        &tools.system_dir,
        environment,
        &attributes,
        None,
    )?;
    verify_job(parent.as_handle(), job)?;
    Ok(parent)
}

pub(super) struct SandboxedChild {
    process: OwnedHandle,
    job: OwnedHandle,
    // Job must be stopped before the AppContainer profile is removed.
    profile: Option<Profile>,
}

impl SandboxedChild {
    /// Must only be called after identity, directory ACL and isolation probes.
    /// `stdio` must consist solely of private, purpose-created handles.
    pub(super) fn spawn(
        profile: Profile,
        program: &Path,
        args: Arguments<'_>,
        cwd: &Path,
        environment: &Environment<'_>,
        stdio: [BorrowedHandle<'_>; 3],
    ) -> io::Result<Self> {
        if !program.is_absolute() || !cwd.is_absolute() {
            return Err(invalid("executable and working directory must be absolute"));
        }
        let process_job = job()?;
        // Keep the inheritable duplicates out of this service's handle table:
        // concurrent std::process::Command launches inherit all such handles.
        let parent = create_handle_parent(process_job.as_handle(), environment)?;
        let inherited = [
            duplicate_into_parent(stdio[0], parent.as_handle())?,
            duplicate_into_parent(stdio[1], parent.as_handle())?,
            duplicate_into_parent(stdio[2], parent.as_handle())?,
        ];
        let capabilities = SECURITY_CAPABILITIES {
            AppContainerSid: profile.sid,
            Capabilities: ptr::null_mut(),
            CapabilityCount: 0,
            Reserved: 0,
        };
        let parent_handle = parent.as_raw_handle();
        let package_policy = PROCESS_CREATION_ALL_APPLICATION_PACKAGES_OPT_OUT;
        let mut attributes = Attributes::new(4)?;
        attributes.update(
            PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES,
            (&capabilities as *const SECURITY_CAPABILITIES).cast(),
            size_of::<SECURITY_CAPABILITIES>(),
        )?;
        attributes.update(
            PROC_THREAD_ATTRIBUTE_ALL_APPLICATION_PACKAGES_POLICY,
            (&package_policy as *const u32).cast(),
            size_of::<u32>(),
        )?;
        attributes.update(
            PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
            inherited.as_ptr().cast(),
            size_of::<[HANDLE; 3]>(),
        )?;
        // Windows inherits the handle table entries and job membership from
        // the specified parent at process creation.
        attributes.update(
            PROC_THREAD_ATTRIBUTE_PARENT_PROCESS,
            (&parent_handle as *const HANDLE).cast(),
            size_of::<HANDLE>(),
        )?;
        let (process, thread) = create_suspended(
            program,
            args,
            cwd,
            environment,
            &attributes,
            Some(inherited),
        )?;
        verify_job(process.as_handle(), process_job.as_handle())?;
        let started = (|| {
            verify_package_grants(process.as_handle(), &profile)?;
            terminate_suspended(parent.as_handle())?;
            match unsafe { ResumeThread(thread.as_raw_handle()) } {
                1 => Ok(()),
                u32::MAX => Err(io::Error::last_os_error()),
                _ => Err(io::Error::other("unexpected sandbox thread suspend count")),
            }
        })();
        if let Err(error) = started {
            unsafe {
                TerminateJobObject(process_job.as_raw_handle(), 1);
                WaitForSingleObject(process.as_raw_handle(), 5000);
            }
            return Err(error);
        }
        Ok(Self {
            process,
            job: process_job,
            profile: Some(profile),
        })
    }

    /// Polls cancellation and deadline; always terminates descendants when the
    /// root exits. A caller must drain both output pipes concurrently.
    pub(super) fn wait(
        &mut self,
        timeout: Duration,
        cancelled: impl FnMut() -> bool,
    ) -> io::Result<u32> {
        self.wait_confirmed(timeout, &mut false, cancelled)
    }

    fn wait_confirmed(
        &mut self,
        timeout: Duration,
        stopped: &mut bool,
        cancelled: impl FnMut() -> bool,
    ) -> io::Result<u32> {
        let result = self.wait_root(timeout, cancelled);
        self.terminate()?;
        self.close_profile()?;
        *stopped = true;
        result
    }

    fn wait_root(&self, timeout: Duration, mut cancelled: impl FnMut() -> bool) -> io::Result<u32> {
        let start = Instant::now();
        loop {
            let was_cancelled = cancelled();
            if was_cancelled || start.elapsed() >= timeout {
                return Err(io::Error::new(
                    if was_cancelled {
                        io::ErrorKind::Interrupted
                    } else {
                        io::ErrorKind::TimedOut
                    },
                    "sandbox process stopped",
                ));
            }
            match unsafe { WaitForSingleObject(self.process.as_raw_handle(), 100) } {
                WAIT_OBJECT_0 => {
                    let mut exit = 0;
                    if unsafe { GetExitCodeProcess(self.process.as_raw_handle(), &mut exit) } == 0 {
                        return Err(io::Error::last_os_error());
                    }
                    return Ok(exit);
                }
                WAIT_TIMEOUT => (),
                _ => return Err(io::Error::last_os_error()),
            }
        }
    }

    fn terminate(&mut self) -> io::Result<()> {
        if unsafe { TerminateJobObject(self.job.as_raw_handle(), 1) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            if self.active_processes()? == 0 {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "sandbox process tree did not stop",
                ));
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    fn active_processes(&self) -> io::Result<u32> {
        let mut info = JOBOBJECT_BASIC_ACCOUNTING_INFORMATION::default();
        if unsafe {
            QueryInformationJobObject(
                self.job.as_raw_handle(),
                JobObjectBasicAccountingInformation,
                (&mut info as *mut JOBOBJECT_BASIC_ACCOUNTING_INFORMATION).cast(),
                size_of::<JOBOBJECT_BASIC_ACCOUNTING_INFORMATION>() as u32,
                ptr::null_mut(),
            )
        } == 0
        {
            return Err(io::Error::last_os_error());
        }
        Ok(info.ActiveProcesses)
    }

    fn close_profile(&mut self) -> io::Result<()> {
        match self.profile.take() {
            Some(profile) => profile.close(),
            None => Ok(()),
        }
    }
}

impl Drop for SandboxedChild {
    fn drop(&mut self) {
        let _ = self.terminate();
        // Fields drop in declaration order. Even on a teardown error, close
        // the kill-on-close job handle before dropping the profile.
    }
}
