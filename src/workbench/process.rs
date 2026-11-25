use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
};
use std::{
    io::Read,
    process::{Command, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

#[cfg(unix)]
#[path = "process_unix.rs"]
mod unix_tree;

const OUTPUT_LIMIT: usize = 2 * 1024 * 1024;

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
fn taskkill(pid: u32) {
    use std::{ffi::OsString, os::windows::ffi::OsStringExt, path::PathBuf};
    let mut buffer = vec![0u16; 32768];
    let length = unsafe {
        windows_sys::Win32::System::SystemInformation::GetSystemDirectoryW(
            buffer.as_mut_ptr(),
            buffer.len() as u32,
        )
    } as usize;
    if length == 0 || length >= buffer.len() {
        return;
    }
    let executable = PathBuf::from(OsString::from_wide(&buffer[..length])).join("taskkill.exe");
    let _ = Command::new(executable)
        .args(["/PID", &pid.to_string(), "/T", "/F"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status();
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
    #[cfg(windows)]
    pid: u32,
    terminated: bool,
    #[cfg(windows)]
    job: std::os::windows::io::OwnedHandle,
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
                taskkill(child.id());
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
                taskkill(child.id());
                let _ = child.kill();
                let _ = child.wait();
                return Err(YssError::new(
                    ErrorCode::NotReady,
                    "无法将外部进程纳入受控进程树，已停止执行",
                ));
            }
            Ok(Self {
                pid: child.id(),
                terminated: false,
                job,
            })
        }
        #[cfg(not(windows))]
        {
            Ok(Self {
                #[cfg(windows)]
                pid: child.id(),
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
        {
            use std::os::windows::io::AsRawHandle;
            unsafe {
                windows_sys::Win32::System::JobObjects::TerminateJobObject(
                    self.job.as_raw_handle(),
                    1,
                );
            }
            taskkill(self.pid);
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
    let mut command = Command::new(program);
    command.args(args);
    run_command(command, cancel, timeout)
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

pub(crate) fn capture_command(
    command: Command,
    cancel: &Cancellation,
    timeout: Duration,
) -> Result<(std::process::ExitStatus, Vec<u8>, Vec<u8>)> {
    capture_command_output(command, cancel, timeout, |_| Ok(()))
}

pub(crate) fn capture_command_output(
    mut command: Command,
    cancel: &Cancellation,
    timeout: Duration,
    mut on_output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<(std::process::ExitStatus, Vec<u8>, Vec<u8>)> {
    cancel.check()?;
    let program = command.get_program().to_string_lossy().into_owned();
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let scope = isolate(&mut command)?;
    let mut child = command.spawn().map_err(|_| {
        YssError::new(
            ErrorCode::NotReady,
            format!("无法启动 {program}，请先安装并登录"),
        )
    })?;
    let mut tree = ProcessTree::attach(&mut child, scope)?;
    let (tx, rx) = mpsc::sync_channel(16);
    reader(child.stdout.take().expect("piped stdout"), true, tx.clone());
    reader(child.stderr.take().expect("piped stderr"), false, tx);
    let started = Instant::now();
    let mut buffers = [Vec::new(), Vec::new()];
    let mut done = [false, false];
    let mut status = None;
    while !done.iter().all(|done| *done) || status.is_none() {
        if cancel.is_cancelled() {
            stop(&mut child, &mut tree);
            cancel.check()?;
        }
        if started.elapsed() > timeout {
            stop(&mut child, &mut tree);
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
            Ok((stdout, Ok(Some(bytes))))
                if buffers[usize::from(!stdout)].len() + bytes.len() <= OUTPUT_LIMIT =>
            {
                buffers[usize::from(!stdout)].extend_from_slice(&bytes);
                Some((stdout, bytes))
            }
            Ok((stdout, Ok(None))) => {
                done[usize::from(!stdout)] = true;
                None
            }
            Ok(_) => {
                stop(&mut child, &mut tree);
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
    let [stdout, stderr] = buffers;
    Ok((status.unwrap(), stdout, stderr))
}
