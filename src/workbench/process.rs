use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
};
use serde::{Deserialize, Serialize};
use std::{
    env,
    ffi::OsStr,
    io::Read,
    path::PathBuf,
    process::{Command, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

#[cfg(unix)]
#[path = "process_unix.rs"]
mod unix_tree;

#[cfg(target_os = "macos")]
pub mod host;
#[cfg(windows)]
#[path = "process/windows_path.rs"]
mod windows_path;
#[cfg(windows)]
pub use windows_path::apply_windows_user_path;

const OUTPUT_LIMIT: usize = 2 * 1024 * 1024;

#[derive(Clone, Copy, Deserialize, Eq, PartialEq, Serialize)]
pub(crate) struct ProcessIdentity {
    pid: u32,
    start: (u64, u64),
}

#[derive(Deserialize, Serialize)]
pub(crate) struct HostBootSession {
    host: crate::model::Sha256,
    uuid: String,
}

impl HostBootSession {
    pub(crate) fn rebooted_since(&self, previous: &Self) -> Option<bool> {
        if self.host != previous.host
            || !valid_boot_uuid(&self.uuid)
            || !valid_boot_uuid(&previous.uuid)
        {
            return None;
        }
        Some(!self.uuid.eq_ignore_ascii_case(&previous.uuid))
    }
}

fn valid_boot_uuid(uuid: &str) -> bool {
    uuid.len() == 36
        && uuid.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
        && uuid.bytes().any(|byte| !matches!(byte, b'0' | b'-'))
}

pub(crate) fn host_boot_session() -> Option<HostBootSession> {
    #[cfg(target_os = "macos")]
    {
        let mut boot = [0u8; 37];
        let mut length = boot.len();
        let queried = unsafe {
            libc::sysctlbyname(
                c"kern.bootsessionuuid".as_ptr(),
                boot.as_mut_ptr().cast(),
                &mut length,
                std::ptr::null_mut(),
                0,
            )
        };
        if queried != 0 {
            return None;
        }
        let uuid = std::ffi::CStr::from_bytes_with_nul(boot.get(..length)?)
            .ok()?
            .to_str()
            .ok()?;
        if !valid_boot_uuid(uuid) {
            return None;
        }
        // A different boot UUID could be another Mac reading the same store.
        let mut host = [0u8; 16];
        let timeout = libc::timespec {
            tv_sec: 0,
            tv_nsec: 100_000_000,
        };
        if unsafe { libc::gethostuuid(host.as_mut_ptr(), &timeout) } != 0 || host == [0; 16] {
            return None;
        }
        Some(HostBootSession {
            host: crate::model::Sha256::of(&host),
            uuid: uuid.to_owned(),
        })
    }
    #[cfg(not(target_os = "macos"))]
    {
        None
    }
}

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
pub(crate) fn process_group_identity(pid: u32) -> Option<ProcessIdentity> {
    let group = i32::try_from(pid).ok().filter(|pid| *pid > 0)?;
    if unsafe { libc::getpgid(group) } == group {
        return unix_tree::identity(pid);
    }
    None
}

pub(crate) fn group_gone(child: &ProcessIdentity, scope: &str) -> bool {
    #[cfg(unix)]
    {
        let Ok(group) = i32::try_from(child.pid) else {
            return false;
        };
        if group <= 0 || unix_tree::identity(child.pid).as_ref() == Some(child) {
            return false;
        }
        // Only an absent process group proves cleanup. A reused or inaccessible
        // group is ambiguous and must retain the checkout reservation.
        let result = unsafe { libc::kill(-group, 0) };
        result < 0
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
            && unix_tree::scope_gone(scope)
    }
    #[cfg(not(unix))]
    {
        let _ = (child, scope);
        false
    }
}

pub(crate) fn system_tool_path(program: &str, path: &OsStr) -> Option<PathBuf> {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    if std::env::var_os("YSS_DESKTOP_MANAGED").as_deref() == Some(OsStr::new("1")) {
        let tool = match program {
            "gh" => Some(super::managed_tools::Tool::Gh),
            "glab" => Some(super::managed_tools::Tool::Glab),
            _ => None,
        };
        if let Some(tool) = tool
            && let Some(path) = std::env::var_os(tool.environment())
            && let Ok(Some(path)) =
                super::managed_tools::resolve_pinned(tool, std::path::Path::new(&path))
        {
            return Some(path);
        }
    }
    #[cfg(windows)]
    let program = format!("{program}.exe");
    env::split_paths(path)
        .filter(|directory| directory.is_absolute())
        .map(|directory| directory.join(AsRef::<str>::as_ref(&program)))
        .find(|path| {
            let Ok(metadata) = path.metadata() else {
                return false;
            };
            if !metadata.is_file() {
                return false;
            }
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                metadata.permissions().mode() & 0o111 != 0
            }
            #[cfg(not(unix))]
            {
                true
            }
        })
}

pub(crate) fn isolate(command: &mut Command) -> Result<String> {
    let scope = crate::fs::unique_id()?;
    command.env("YSS_PROCESS_SCOPE", &scope);
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(
            windows_sys::Win32::System::Threading::CREATE_NEW_PROCESS_GROUP
                | windows_sys::Win32::System::Threading::CREATE_SUSPENDED,
        );
    }
    Ok(scope)
}

#[cfg(windows)]
fn resume(pid: u32) -> bool {
    use std::os::windows::io::{AsRawHandle, FromRawHandle, OwnedHandle};
    use windows_sys::Win32::{
        Foundation::INVALID_HANDLE_VALUE,
        System::{
            Diagnostics::ToolHelp::{
                CreateToolhelp32Snapshot, TH32CS_SNAPTHREAD, THREADENTRY32, Thread32First,
                Thread32Next,
            },
            Threading::{OpenThread, ResumeThread, THREAD_SUSPEND_RESUME},
        },
    };
    let snapshot = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0) };
    if snapshot == INVALID_HANDLE_VALUE {
        return false;
    }
    let snapshot = unsafe { OwnedHandle::from_raw_handle(snapshot) };
    let mut entry: THREADENTRY32 = unsafe { std::mem::zeroed() };
    entry.dwSize = std::mem::size_of_val(&entry) as u32;
    let mut found = unsafe { Thread32First(snapshot.as_raw_handle(), &mut entry) } != 0;
    while found {
        if entry.th32OwnerProcessID == pid {
            let thread = unsafe { OpenThread(THREAD_SUSPEND_RESUME, 0, entry.th32ThreadID) };
            if thread.is_null() {
                return false;
            }
            let thread = unsafe { OwnedHandle::from_raw_handle(thread) };
            return unsafe { ResumeThread(thread.as_raw_handle()) } != u32::MAX;
        }
        found = unsafe { Thread32Next(snapshot.as_raw_handle(), &mut entry) } != 0;
    }
    false
}

pub(crate) struct ProcessTree {
    terminated: bool,
    #[cfg(windows)]
    job: Option<std::os::windows::io::OwnedHandle>,
    #[cfg(unix)]
    unix: unix_tree::UnixTree,
}
impl ProcessTree {
    pub(crate) fn attach(child: &mut std::process::Child, _scope: String) -> Result<Self> {
        #[cfg(windows)]
        {
            use std::os::windows::io::{AsRawHandle, FromRawHandle, OwnedHandle};
            use windows_sys::Win32::System::JobObjects::{
                AssignProcessToJobObject, CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
                JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectExtendedLimitInformation,
                SetInformationJobObject,
            };
            let raw = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
            if raw.is_null() {
                // isolate() creates the primary thread suspended until assignment.
                let _ = child.kill();
                let _ = child.wait();
                return Err(YssError::new(
                    ErrorCode::NotReady,
                    "无法建立受控进程树，已停止外部进程",
                ));
            }
            let job = unsafe { OwnedHandle::from_raw_handle(raw) };
            let mut information: JOBOBJECT_EXTENDED_LIMIT_INFORMATION =
                unsafe { std::mem::zeroed() };
            information.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            let configured = unsafe {
                SetInformationJobObject(
                    raw,
                    JobObjectExtendedLimitInformation,
                    &information as *const _ as *const std::ffi::c_void,
                    std::mem::size_of_val(&information) as u32,
                )
            } != 0;
            let assigned =
                configured && unsafe { AssignProcessToJobObject(raw, child.as_raw_handle()) } != 0;
            if !assigned || !resume(child.id()) {
                drop(job);
                let _ = child.kill();
                let _ = child.wait();
                return Err(YssError::new(
                    ErrorCode::NotReady,
                    "无法将外部进程纳入受控进程树，已停止执行",
                ));
            }
            Ok(Self {
                terminated: false,
                job: Some(job),
            })
        }
        #[cfg(not(windows))]
        {
            Ok(Self {
                terminated: false,
                #[cfg(unix)]
                unix: unix_tree::UnixTree::new(child.id(), _scope),
            })
        }
    }
    fn terminate(&mut self) {
        if self.terminated {
            return;
        }
        #[cfg(unix)]
        self.unix.terminate();
        #[cfg(windows)]
        if let Some(job) = self.job.take() {
            use std::os::windows::io::AsRawHandle;
            // Closing this owned handle also invokes kill-on-close. Do not
            // look up a PID that may now belong to an unrelated process.
            unsafe {
                windows_sys::Win32::System::JobObjects::TerminateJobObject(job.as_raw_handle(), 1);
            }
        }
        self.terminated = true;
    }
}
impl Drop for ProcessTree {
    fn drop(&mut self) {
        self.terminate();
    }
}

pub(crate) fn stop(child: &mut std::process::Child, tree: &mut ProcessTree) {
    tree.terminate();
    let _ = child.kill();
    let _ = child.wait();
}

type OutputChunk = (bool, std::io::Result<Option<Vec<u8>>>);

fn reader(mut stream: impl Read + Send + 'static, stdout: bool, tx: mpsc::SyncSender<OutputChunk>) {
    thread::spawn(move || {
        let mut bytes = [0; 8192];
        loop {
            let chunk = match stream.read(&mut bytes) {
                Ok(0) => Ok(None),
                Ok(size) => Ok(Some(bytes[..size].to_vec())),
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(error) => Err(error),
            };
            let end = !matches!(&chunk, Ok(Some(_)));
            if tx.send((stdout, chunk)).is_err() || end {
                break;
            }
        }
    });
}

/// Executes an internal, fixed command argument list without a shell or credential output.
pub fn run(
    program: &str,
    args: &[String],
    cancel: &Cancellation,
    timeout: Duration,
) -> Result<Vec<u8>> {
    let mut command = tool_command(program);
    command.args(args);
    run_command(command, cancel, timeout)
}

pub(crate) fn tool_command(program: &str) -> Command {
    let selected = (matches!(program, "gh" | "glab")
        && std::env::var_os("YSS_DESKTOP_MANAGED").as_deref() == Some(OsStr::new("1")))
    .then(|| system_tool_path(program, &std::env::var_os("PATH").unwrap_or_default()))
    .flatten();
    Command::new(
        selected
            .as_deref()
            .unwrap_or_else(|| std::path::Path::new(program)),
    )
}

pub(crate) fn run_command(
    mut command: Command,
    cancel: &Cancellation,
    timeout: Duration,
) -> Result<Vec<u8>> {
    command
        .env("GH_PROMPT_DISABLED", "1")
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GLAB_CHECK_UPDATE", "false")
        .env("NO_COLOR", "1")
        .env_remove("GH_DEBUG")
        .env_remove("GLAB_DEBUG_HTTP")
        .env_remove("GIT_TRACE")
        .env_remove("GIT_TRACE_CURL");
    let (status, stdout, stderr) = capture_command(command, cancel, timeout)?;
    if !status.success() {
        // Authenticated helpers can include secrets in their diagnostics.
        let stderr = String::from_utf8_lossy(&stderr).to_ascii_lowercase();
        let (code, message) = if stderr.contains("429")
            || stderr.contains("rate limit")
            || stderr.contains("rate_limit")
        {
            (
                ErrorCode::Network,
                "外部服务限流，请稍后刷新，已保留上次状态",
            )
        } else if stderr.contains("401")
            || stderr.contains("not logged")
            || stderr.contains("authentication")
            || stderr.contains("auth login")
        {
            (
                ErrorCode::NotReady,
                "外部实例尚未认证或认证已失效，请使用对应CLI登录后刷新",
            )
        } else {
            (
                ErrorCode::Network,
                "外部查询失败，请检查实例、项目路径和访问权限",
            )
        };
        return Err(YssError::new(code, message));
    }
    Ok(stdout)
}

/// Capture a caller-constructed command without a shell, bounded by a timeout and process-tree cleanup.
pub fn capture_command(
    command: Command,
    cancel: &Cancellation,
    timeout: Duration,
) -> Result<(std::process::ExitStatus, Vec<u8>, Vec<u8>)> {
    capture_command_output(command, cancel, timeout, |_| Ok(()))
}

pub(crate) fn capture_command_output(
    command: Command,
    cancel: &Cancellation,
    timeout: Duration,
    on_output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<(std::process::ExitStatus, Vec<u8>, Vec<u8>)> {
    let mut buffers = [Vec::new(), Vec::new()];
    let status = command_output(command, cancel, timeout, Some(&mut buffers), on_output)?;
    let [stdout, stderr] = buffers;
    Ok((status, stdout, stderr))
}

pub(crate) fn stream_command_output(
    command: Command,
    cancel: &Cancellation,
    timeout: Duration,
    on_output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<std::process::ExitStatus> {
    command_output(command, cancel, timeout, None, on_output)
}

fn command_output(
    mut command: Command,
    cancel: &Cancellation,
    timeout: Duration,
    mut buffers: Option<&mut [Vec<u8>; 2]>,
    mut on_output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<std::process::ExitStatus> {
    cancel.check()?;
    let program = command.get_program().to_string_lossy().into_owned();
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let scope = isolate(&mut command)?;
    let mut child = command.spawn().map_err(|_| {
        let message = if buffers.is_some() {
            format!("无法启动 {program}，请先安装并登录")
        } else {
            format!("无法启动 {program}，请核对本机工具和工作目录")
        };
        YssError::new(ErrorCode::NotReady, message)
    })?;
    let mut tree = ProcessTree::attach(&mut child, scope)?;
    let (tx, rx) = mpsc::sync_channel(16);
    reader(child.stdout.take().expect("piped stdout"), true, tx.clone());
    reader(child.stderr.take().expect("piped stderr"), false, tx);
    let started = Instant::now();
    let mut done = [false, false];
    let mut status = None;
    while !done.iter().all(|done| *done) || status.is_none() {
        if cancel.is_cancelled() {
            stop(&mut child, &mut tree);
            cancel.check()?;
        }
        if started.elapsed() > timeout {
            stop(&mut child, &mut tree);
            if buffers.is_none() {
                return Err(YssError::new(ErrorCode::Io, "命令执行超时"));
            }
            return Err(YssError::new(ErrorCode::Network, "外部查询超时"));
        }
        if status.is_none()
            && let Some(exited) = child.try_wait()?
        {
            status = Some(exited);
            // Descendants can otherwise keep pipes open and continue writing after the command exits.
            stop(&mut child, &mut tree);
        }
        let chunk = match rx.recv_timeout(Duration::from_millis(30)) {
            Ok((stdout, Ok(Some(bytes)))) => {
                if let Some(buffers) = buffers.as_mut() {
                    let buffer = &mut buffers[usize::from(!stdout)];
                    if buffer.len() + bytes.len() > OUTPUT_LIMIT {
                        stop(&mut child, &mut tree);
                        return Err(YssError::new(
                            ErrorCode::Network,
                            "外部查询输出超过限制或读取失败",
                        ));
                    }
                    buffer.extend_from_slice(&bytes);
                }
                Some((stdout, bytes))
            }
            Ok((stdout, Ok(None))) => {
                done[usize::from(!stdout)] = true;
                None
            }
            Ok(_) => {
                stop(&mut child, &mut tree);
                if buffers.is_none() {
                    return Err(YssError::new(ErrorCode::Io, "无法读取命令输出"));
                }
                return Err(YssError::new(
                    ErrorCode::Network,
                    "外部查询输出超过限制或读取失败",
                ));
            }
            Err(mpsc::RecvTimeoutError::Timeout) => None,
            Err(mpsc::RecvTimeoutError::Disconnected) if done.iter().all(|done| *done) => {
                if status.is_none() {
                    thread::sleep(Duration::from_millis(20));
                }
                None
            }
            Err(_) => {
                stop(&mut child, &mut tree);
                if buffers.is_none() {
                    return Err(YssError::new(ErrorCode::Io, "命令输出中断"));
                }
                return Err(YssError::new(ErrorCode::Network, "外部查询输出中断"));
            }
        };
        // Idle ticks let a throttled consumer flush a short burst while the process still runs.
        if let Err(error) = on_output(
            chunk
                .as_ref()
                .map(|(stdout, bytes)| (*stdout, bytes.as_slice())),
        ) {
            stop(&mut child, &mut tree);
            return Err(error);
        }
    }
    Ok(status.unwrap())
}
