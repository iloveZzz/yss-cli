use portable_pty::{Child, CommandBuilder, MasterPty};
use std::{
    io::{self, Read, Write},
    mem::{self, MaybeUninit},
    net::Shutdown,
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd},
        unix::net::UnixStream,
    },
    sync::Arc,
    thread,
    time::{Duration, Instant},
};

const CHECK_INTERVAL: Duration = Duration::from_millis(25);
const REAP_TIMEOUT: Duration = Duration::from_millis(250);

type Reader = Box<dyn Read + Send>;
type Writer = Box<dyn Write + Send>;

pub(super) fn prepare() -> io::Result<(CommandBuilder, Launch)> {
    let (stop, notify) = UnixStream::pair()?;
    Ok((CommandBuilder::new_default_prog(), Launch { stop, notify }))
}

pub(super) struct Launch {
    stop: UnixStream,
    notify: UnixStream,
}

impl Launch {
    pub(super) fn streams(
        &self,
        master: &dyn MasterPty,
        reader: Reader,
        writer: Writer,
    ) -> io::Result<(Reader, Writer)> {
        let fd = master
            .as_raw_fd()
            .ok_or_else(|| io::Error::other("终端缺少控制描述符"))?;
        let copy = unsafe { libc::fcntl(fd, libc::F_DUPFD_CLOEXEC, 0) };
        if copy == -1 {
            return Err(io::Error::last_os_error());
        }
        let device = unsafe { OwnedFd::from_raw_fd(copy) };
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags == -1 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } == -1
        {
            return Err(io::Error::last_os_error());
        }
        let readiness = Arc::new(Readiness {
            device,
            notify: self.notify.try_clone()?,
        });
        Ok((
            Box::new(Stream {
                inner: reader,
                readiness: Arc::clone(&readiness),
            }),
            Box::new(Stream {
                inner: writer,
                readiness,
            }),
        ))
    }
}

struct Readiness {
    device: OwnedFd,
    notify: UnixStream,
}

impl Readiness {
    fn wait(&self, events: libc::c_short) -> io::Result<bool> {
        let mut descriptors = [
            libc::pollfd {
                fd: self.notify.as_raw_fd(),
                events: libc::POLLIN,
                revents: 0,
            },
            libc::pollfd {
                fd: self.device.as_raw_fd(),
                events,
                revents: 0,
            },
        ];
        if unsafe {
            libc::poll(
                descriptors.as_mut_ptr(),
                descriptors.len() as libc::nfds_t,
                -1,
            )
        } == -1
        {
            return Err(io::Error::last_os_error());
        }
        Ok(descriptors[0].revents == 0)
    }
}

struct Stream<T> {
    inner: T,
    readiness: Arc<Readiness>,
}

impl Read for Stream<Reader> {
    fn read(&mut self, bytes: &mut [u8]) -> io::Result<usize> {
        if bytes.is_empty() {
            return Ok(0);
        }
        while self.readiness.wait(libc::POLLIN)? {
            match self.inner.read(bytes) {
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => continue,
                result => return result,
            }
        }
        Ok(0)
    }
}

impl Write for Stream<Writer> {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if bytes.is_empty() {
            return Ok(0);
        }
        while self.readiness.wait(libc::POLLOUT)? {
            match self.inner.write(bytes) {
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => continue,
                result => return result,
            }
        }
        Err(io::ErrorKind::BrokenPipe.into())
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

pub(super) struct Process {
    child: Option<Box<dyn Child + Send + Sync>>,
    sid: libc::pid_t,
    exit: Option<i32>,
    stop: Option<UnixStream>,
}

impl Process {
    pub(super) fn new(child: Box<dyn Child + Send + Sync>, launch: Launch) -> Self {
        let sid = child.process_id().expect("Unix PTY child has a PID") as libc::pid_t;
        Self {
            child: Some(child),
            sid,
            exit: None,
            stop: Some(launch.stop),
        }
    }

    pub(super) fn poll_exit(&mut self) -> io::Result<Option<i32>> {
        if self.child.is_none() || self.exit.is_some() {
            return Ok(self.exit);
        }

        loop {
            let mut info: libc::siginfo_t = unsafe { mem::zeroed() };
            // Retain the waitable child and its session ID until closing finishes.
            let result = unsafe {
                libc::waitid(
                    libc::P_PID,
                    self.sid as libc::id_t,
                    &mut info,
                    libc::WEXITED | libc::WNOHANG | libc::WNOWAIT,
                )
            };
            if result == 0 {
                if unsafe { info.si_pid() } == self.sid {
                    let status = unsafe { info.si_status() };
                    self.exit = Some(if info.si_code == libc::CLD_EXITED {
                        status
                    } else {
                        128 + status
                    });
                }
                return Ok(self.exit);
            }
            let error = io::Error::last_os_error();
            if error.kind() != io::ErrorKind::Interrupted {
                return Err(error);
            }
        }
    }

    pub(super) fn close(&mut self, master: Option<Box<dyn MasterPty + Send>>) -> io::Result<()> {
        // The output worker still holds a clone of the master. Signal the whole session
        // explicitly rather than waiting for the last descriptor to trigger a hangup.
        drop(master);
        if self.child.is_none() {
            return Ok(());
        }
        let result = close_session(self.sid);
        if let Some(stop) = self.stop.take() {
            let _ = stop.shutdown(Shutdown::Both);
        }
        // Keep the shell waitable while session membership is unconfirmed.
        result?;
        self.reap_shell()
    }

    fn reap_shell(&mut self) -> io::Result<()> {
        // The shell can leave libproc's live process list before waitid observes the exit.
        let deadline = Instant::now() + REAP_TIMEOUT;
        while self.poll_exit()?.is_none() {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(io::Error::other("未能确认终端 Shell 已退出"));
            }
            thread::sleep(CHECK_INTERVAL.min(remaining));
        }
        if let Some(child) = self.child.as_mut() {
            child.wait()?;
        }
        self.child = None;
        Ok(())
    }
}

fn close_session(sid: libc::pid_t) -> io::Result<()> {
    for (signal, grace) in [(libc::SIGHUP, 200), (libc::SIGTERM, 350)] {
        let members = session_members(sid)?;
        if members.is_empty() {
            return Ok(());
        }
        for member in members {
            member.signal(sid, signal);
        }
        if wait_for_empty(sid, Duration::from_millis(grace))? {
            return Ok(());
        }
    }

    let deadline = Instant::now() + Duration::from_millis(1500);
    loop {
        let members = session_members(sid)?;
        if members.is_empty() {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(io::Error::other("未能确认所有终端会话进程已退出"));
        }
        for member in members {
            member.signal(sid, libc::SIGKILL);
        }
        thread::sleep(CHECK_INTERVAL);
    }
}

fn wait_for_empty(sid: libc::pid_t, timeout: Duration) -> io::Result<bool> {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if session_members(sid)?.is_empty() {
            return Ok(true);
        }
        thread::sleep(CHECK_INTERVAL);
    }
    Ok(false)
}

struct Member {
    pid: libc::pid_t,
    // A missing identity is retained as an unverified member, never as a signal target.
    start: Option<(u64, u64)>,
}

impl Member {
    fn signal(self, sid: libc::pid_t, signal: libc::c_int) {
        let Some(start) = self.start else { return };
        let Some(current) = process_info(self.pid) else {
            return;
        };
        if start == (current.pbi_start_tvsec, current.pbi_start_tvusec)
            && unsafe { libc::getsid(self.pid) } == sid
        {
            unsafe { libc::kill(self.pid, signal) };
        }
    }
}

fn session_members(sid: libc::pid_t) -> io::Result<Vec<Member>> {
    let mut capacity = 1024;
    loop {
        let mut pids = vec![0; capacity];
        let count = unsafe {
            libc::proc_listallpids(
                pids.as_mut_ptr().cast(),
                (capacity * mem::size_of::<libc::pid_t>()) as libc::c_int,
            )
        };
        if count <= 0 {
            return Err(io::Error::last_os_error());
        }
        if count as usize >= capacity {
            if capacity >= 131_072 {
                return Err(io::Error::other("终端会话进程列表超过容量"));
            }
            capacity *= 2;
            continue;
        }
        pids.truncate(count as usize);
        return Ok(pids
            .into_iter()
            .filter_map(|pid| {
                if pid <= 0 || unsafe { libc::getsid(pid) } != sid {
                    return None;
                }
                let info = process_info(pid);
                if info
                    .as_ref()
                    .is_some_and(|info| info.pbi_status == libc::SZOMB)
                {
                    return None;
                }
                Some(Member {
                    pid,
                    start: info.map(|info| (info.pbi_start_tvsec, info.pbi_start_tvusec)),
                })
            })
            .collect());
    }
}

fn process_info(pid: libc::pid_t) -> Option<libc::proc_bsdinfo> {
    let mut info = MaybeUninit::<libc::proc_bsdinfo>::uninit();
    let size = mem::size_of::<libc::proc_bsdinfo>() as libc::c_int;
    let read = unsafe {
        libc::proc_pidinfo(
            pid,
            libc::PROC_PIDTBSDINFO,
            0,
            info.as_mut_ptr().cast(),
            size,
        )
    };
    (read == size).then(|| unsafe { info.assume_init() })
}
