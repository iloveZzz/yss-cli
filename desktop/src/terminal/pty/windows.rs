use portable_pty::{Child, CommandBuilder, MasterPty};
use std::{
    ffi::OsString,
    io,
    mem::{self, MaybeUninit},
    os::windows::{
        ffi::OsStringExt,
        io::{AsRawHandle, FromRawHandle, OwnedHandle},
    },
    path::PathBuf,
    ptr,
    sync::{Arc, mpsc},
    thread,
    time::{Duration, Instant},
};
use windows_sys::{
    Wdk::System::SystemServices::RtlGetVersion,
    Win32::Foundation::STATUS_SUCCESS,
    Win32::System::{
        JobObjects::{
            CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
            JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
            JobObjectBasicAccountingInformation, JobObjectExtendedLimitInformation,
            QueryInformationJobObject, SetInformationJobObject, TerminateJobObject,
        },
        SystemInformation::{GetSystemDirectoryW, OSVERSIONINFOW},
    },
};

type Master = Box<dyn MasterPty + Send>;

pub(super) fn build_number() -> Option<u32> {
    // xterm uses the running Windows build to distinguish old and new ConPTY behavior.
    let mut version = OSVERSIONINFOW {
        dwOSVersionInfoSize: mem::size_of::<OSVERSIONINFOW>() as u32,
        ..Default::default()
    };
    // SAFETY: RtlGetVersion writes to the initialized structure of the declared size.
    let status = unsafe { RtlGetVersion(&mut version) };
    (status == STATUS_SUCCESS && version.dwBuildNumber != 0).then_some(version.dwBuildNumber)
}

pub(super) struct Launch {
    job: Arc<OwnedHandle>,
    release: mpsc::SyncSender<Master>,
    released: mpsc::Receiver<()>,
}

pub(super) fn prepare() -> io::Result<(CommandBuilder, Launch)> {
    let system = system_directory()?;
    let windows_powershell = system
        .join("WindowsPowerShell")
        .join("v1.0")
        .join("powershell.exe");
    let powershell_7 = std::env::var_os("ProgramFiles")
        .map(PathBuf::from)
        .filter(|directory| directory.is_absolute())
        .map(|directory| directory.join("PowerShell").join("7").join("pwsh.exe"));
    let exe = powershell_7
        .filter(|path| path.is_file())
        .unwrap_or(windows_powershell);
    if !exe.is_file() {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            "未找到 PowerShell 7 或 Windows PowerShell",
        ));
    }
    let mut command = CommandBuilder::new(exe);
    command.arg("-NoLogo");

    let job = Arc::new(create_job()?);
    command.set_windows_job(Arc::clone(&job));
    let (release, close) = mpsc::sync_channel::<Master>(1);
    let (notify, released) = mpsc::sync_channel(1);
    // Older ConPTY versions block in ClosePseudoConsole. The reader can keep draining
    // independently, and the session manager can return a bounded cleanup result.
    thread::Builder::new()
        .name("terminal-conpty-close".into())
        .spawn(move || {
            if let Ok(master) = close.recv() {
                drop(master);
                let _ = notify.send(());
            }
        })?;
    Ok((
        command,
        Launch {
            job,
            release,
            released,
        },
    ))
}

pub(super) struct Process {
    child: Option<Box<dyn Child + Send + Sync>>,
    launch: Option<Launch>,
    exit: Option<i32>,
    release_confirmed: bool,
}

impl Process {
    pub(super) fn new(child: Box<dyn Child + Send + Sync>, launch: Launch) -> Self {
        Self {
            child: Some(child),
            launch: Some(launch),
            exit: None,
            release_confirmed: false,
        }
    }

    pub(super) fn poll_exit(&mut self) -> io::Result<Option<i32>> {
        if let Some(child) = &mut self.child
            && self.exit.is_none()
            && let Some(status) = child.try_wait()?
        {
            self.exit = Some(status.exit_code() as i32);
        }
        Ok(self.exit)
    }

    pub(super) fn close(&mut self, master: Option<Master>) -> io::Result<()> {
        let Some(launch) = &self.launch else {
            return Ok(());
        };
        let job = Arc::clone(&launch.job);
        if let Some(master) = master {
            // Disconnection is also visible when collecting the release confirmation below.
            let _ = launch.release.send(master);
        }
        let deadline = Instant::now() + Duration::from_secs(2);
        let processes = self.close_processes(&job, deadline);
        let released = self.confirm_release(deadline);
        processes.and(released)?;
        // Keep the Job and Shell handle until both checks pass so a later retry can verify them.
        self.launch = None;
        self.child = None;
        Ok(())
    }

    fn confirm_release(&mut self, deadline: Instant) -> io::Result<()> {
        if self.release_confirmed {
            return Ok(());
        }
        let launch = self.launch.as_ref().expect("close retains the Windows Job");
        launch
            .released
            .recv_timeout(deadline.saturating_duration_since(Instant::now()))
            .map_err(|error| match error {
                mpsc::RecvTimeoutError::Timeout => {
                    io::Error::other("未能在时限内释放 Windows 伪终端")
                }
                mpsc::RecvTimeoutError::Disconnected => io::Error::other("终端关闭线程不可用"),
            })?;
        self.release_confirmed = true;
        Ok(())
    }

    fn close_processes(&mut self, job: &OwnedHandle, deadline: Instant) -> io::Result<()> {
        let graceful = Instant::now() + Duration::from_millis(250);
        if matches!(self.wait_for_empty(job, graceful), Ok(true)) {
            return Ok(());
        }
        if unsafe { TerminateJobObject(job.as_raw_handle(), 1) } == 0 {
            return Err(io::Error::last_os_error());
        }
        if self.wait_for_empty(job, deadline)? {
            return Ok(());
        }
        Err(io::Error::other("未能确认 Windows 终端进程已退出"))
    }

    fn wait_for_empty(&mut self, job: &OwnedHandle, deadline: Instant) -> io::Result<bool> {
        loop {
            if job_is_empty(job)? && self.poll_exit()?.is_some() {
                return Ok(true);
            }
            if Instant::now() >= deadline {
                return Ok(false);
            }
            thread::sleep(Duration::from_millis(20));
        }
    }
}

fn create_job() -> io::Result<OwnedHandle> {
    let raw = unsafe { CreateJobObjectW(ptr::null(), ptr::null()) };
    if raw.is_null() {
        return Err(io::Error::last_os_error());
    }
    let job = unsafe { OwnedHandle::from_raw_handle(raw) };
    let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { mem::zeroed() };
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if unsafe {
        SetInformationJobObject(
            job.as_raw_handle(),
            JobObjectExtendedLimitInformation,
            ptr::from_ref(&limits).cast(),
            mem::size_of_val(&limits) as u32,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(job)
}

fn job_is_empty(job: &OwnedHandle) -> io::Result<bool> {
    let mut info = MaybeUninit::<JOBOBJECT_BASIC_ACCOUNTING_INFORMATION>::uninit();
    if unsafe {
        QueryInformationJobObject(
            job.as_raw_handle(),
            JobObjectBasicAccountingInformation,
            info.as_mut_ptr().cast(),
            mem::size_of::<JOBOBJECT_BASIC_ACCOUNTING_INFORMATION>() as u32,
            ptr::null_mut(),
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(unsafe { info.assume_init() }.ActiveProcesses == 0)
}

fn system_directory() -> io::Result<PathBuf> {
    let mut buffer = [0u16; 260];
    let count = unsafe { GetSystemDirectoryW(buffer.as_mut_ptr(), buffer.len() as u32) };
    if count == 0 {
        return Err(io::Error::last_os_error());
    }
    if count as usize >= buffer.len() {
        return Err(io::Error::other("Windows 系统目录超出支持长度"));
    }
    Ok(PathBuf::from(OsString::from_wide(
        &buffer[..count as usize],
    )))
}
