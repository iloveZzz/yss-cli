use std::{
    collections::{BTreeMap, VecDeque},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::Duration,
};

#[derive(Clone, Copy, PartialEq, Eq)]
struct Identity {
    pid: u32,
    start: (u64, u64),
}
#[derive(Clone, Copy)]
struct Tracked {
    identity: Identity,
    depth: usize,
}
#[cfg(target_os = "macos")]
fn bsd_info(pid: u32) -> Option<libc::proc_bsdinfo> {
    let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
    let size = std::mem::size_of_val(&info);
    let read = unsafe {
        libc::proc_pidinfo(
            pid as i32,
            libc::PROC_PIDTBSDINFO,
            0,
            &mut info as *mut _ as *mut libc::c_void,
            size as i32,
        )
    };
    (read == size as i32).then_some(info)
}
#[cfg(target_os = "macos")]
fn identity(pid: u32) -> Option<Identity> {
    let info = bsd_info(pid)?;
    Some(Identity {
        pid,
        start: (info.pbi_start_tvsec, info.pbi_start_tvusec),
    })
}
#[cfg(target_os = "macos")]
fn scoped(scope: &str) -> Vec<Tracked> {
    let mut pids = vec![0i32; 65536];
    let count = unsafe {
        libc::proc_listallpids(
            pids.as_mut_ptr() as *mut libc::c_void,
            (pids.len() * std::mem::size_of::<i32>()) as i32,
        )
    };
    if count <= 0 {
        return Vec::new();
    }
    let needle = format!("YSS_PROCESS_SCOPE={scope}");
    let mut argument_limit: libc::c_int = 0;
    let mut limit_size = std::mem::size_of_val(&argument_limit);
    let mut limit_name = [libc::CTL_KERN, libc::KERN_ARGMAX];
    let queried = unsafe {
        libc::sysctl(
            limit_name.as_mut_ptr(),
            limit_name.len() as u32,
            &mut argument_limit as *mut _ as *mut libc::c_void,
            &mut limit_size,
            std::ptr::null_mut(),
            0,
        )
    };
    if queried != 0 || !(1..=8 * 1024 * 1024).contains(&argument_limit) {
        return Vec::new();
    }
    let mut environment = vec![0u8; argument_limit as usize];
    let mut found = Vec::new();
    for pid in pids.into_iter().filter(|pid| *pid > 0) {
        let Some(info) = bsd_info(pid as u32) else {
            continue;
        };
        if info.pbi_uid != unsafe { libc::geteuid() } {
            continue;
        }
        let before = Identity {
            pid: pid as u32,
            start: (info.pbi_start_tvsec, info.pbi_start_tvusec),
        };
        let mut name = [libc::CTL_KERN, libc::KERN_PROCARGS2, pid];
        let mut length = environment.len();
        let result = unsafe {
            libc::sysctl(
                name.as_mut_ptr(),
                name.len() as u32,
                environment.as_mut_ptr() as *mut libc::c_void,
                &mut length,
                std::ptr::null_mut(),
                0,
            )
        };
        if result == 0
            && environment[..length]
                .split(|byte| *byte == 0)
                .any(|entry| entry == needle.as_bytes())
            && identity(pid as u32) == Some(before)
        {
            found.push(Tracked {
                identity: before,
                depth: 1,
            });
        }
    }
    found
}
#[cfg(not(target_os = "macos"))]
fn scoped(scope: &str) -> Vec<Tracked> {
    use std::{io::Read, os::unix::fs::MetadataExt};
    let Ok(entries) = std::fs::read_dir("/proc") else {
        return Vec::new();
    };
    let needle = format!("YSS_PROCESS_SCOPE={scope}");
    entries
        .filter_map(|entry| {
            let pid = entry.ok()?.file_name().to_str()?.parse::<u32>().ok()?;
            let before = identity(pid)?;
            let path = format!("/proc/{pid}/environ");
            if std::fs::metadata(&path).ok()?.uid() != unsafe { libc::geteuid() } {
                return None;
            }
            let file = std::fs::File::open(path).ok()?;
            let mut bytes = Vec::new();
            file.take(8 * 1024 * 1024).read_to_end(&mut bytes).ok()?;
            (bytes
                .split(|byte| *byte == 0)
                .any(|entry| entry == needle.as_bytes())
                && identity(pid) == Some(before))
            .then_some(Tracked {
                identity: before,
                depth: 1,
            })
        })
        .collect()
}
#[cfg(not(target_os = "macos"))]
fn linux_stat(pid: u32) -> Option<(Identity, u32)> {
    let stat = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    let fields: Vec<_> = stat.rsplit_once(')')?.1.split_whitespace().collect();
    Some((
        Identity {
            pid,
            start: (fields.get(19)?.parse().ok()?, 0),
        },
        fields.get(1)?.parse().ok()?,
    ))
}
#[cfg(not(target_os = "macos"))]
fn identity(pid: u32) -> Option<Identity> {
    linux_stat(pid).map(|(identity, _)| identity)
}
#[cfg(target_os = "macos")]
fn children(pid: u32) -> Vec<u32> {
    let mut buffer = vec![0i32; 4096];
    let bytes = (buffer.len() * std::mem::size_of::<i32>()) as i32;
    let result = unsafe {
        libc::proc_listchildpids(pid as i32, buffer.as_mut_ptr() as *mut libc::c_void, bytes)
    };
    if result <= 0 {
        return Vec::new();
    }
    buffer
        .into_iter()
        .filter_map(|pid| (pid > 0).then_some(pid as u32))
        .collect()
}
#[cfg(not(target_os = "macos"))]
fn children(parent: u32) -> Vec<u32> {
    let Ok(entries) = std::fs::read_dir("/proc") else {
        return Vec::new();
    };
    entries
        .filter_map(|entry| entry.ok()?.file_name().to_str()?.parse::<u32>().ok())
        .filter(|pid| linux_stat(*pid).is_some_and(|(_, ppid)| ppid == parent))
        .collect()
}
fn observe(known: &Mutex<BTreeMap<u32, Tracked>>) {
    let Ok(mut known) = known.lock() else {
        return;
    };
    let mut queue: VecDeque<_> = known.values().copied().collect();
    let mut scanned = std::collections::BTreeSet::new();
    while let Some(parent) = queue.pop_front() {
        if !scanned.insert(parent.identity.pid)
            || identity(parent.identity.pid) != Some(parent.identity)
        {
            continue;
        }
        for pid in children(parent.identity.pid) {
            if let Some(identity) = identity(pid) {
                let child = Tracked {
                    identity,
                    depth: parent.depth + 1,
                };
                known.insert(pid, child);
                queue.push_back(child);
            }
        }
    }
}
fn signal(process: Tracked, signal: i32) {
    if identity(process.identity.pid) == Some(process.identity) {
        unsafe {
            libc::kill(process.identity.pid as i32, signal);
        }
    }
}
pub(super) struct UnixTree {
    pid: u32,
    scope: String,
    root: Option<Identity>,
    known: Arc<Mutex<BTreeMap<u32, Tracked>>>,
    stopping: Arc<AtomicBool>,
    monitor: Option<JoinHandle<()>>,
}
impl UnixTree {
    pub(super) fn new(pid: u32, scope: String) -> Self {
        let root = identity(pid);
        let mut initial = BTreeMap::new();
        if let Some(identity) = root {
            initial.insert(pid, Tracked { identity, depth: 0 });
        }
        let known = Arc::new(Mutex::new(initial));
        observe(&known);
        let stopping = Arc::new(AtomicBool::new(false));
        let scan = known.clone();
        let stop = stopping.clone();
        let monitor = thread::spawn(move || {
            while !stop.load(Ordering::Acquire) {
                observe(&scan);
                thread::sleep(Duration::from_millis(20));
            }
        });
        Self {
            pid,
            scope,
            root,
            known,
            stopping,
            monitor: Some(monitor),
        }
    }
    pub(super) fn terminate(&mut self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(monitor) = self.monitor.take() {
            let _ = monitor.join();
        }
        observe(&self.known);
        if let Ok(mut known) = self.known.lock() {
            for process in scoped(&self.scope) {
                known.entry(process.identity.pid).or_insert(process);
            }
        }
        let mut processes = self
            .known
            .lock()
            .map(|known| known.values().copied().collect::<Vec<_>>())
            .unwrap_or_default();
        processes.sort_by_key(|process| process.depth);
        for process in &processes {
            signal(*process, libc::SIGSTOP);
        }
        // Stopped parents cannot create further descendants while the final tree is collected.
        observe(&self.known);
        processes = self
            .known
            .lock()
            .map(|known| known.values().copied().collect())
            .unwrap_or_default();
        processes.sort_by_key(|process| std::cmp::Reverse(process.depth));
        for process in processes {
            signal(process, libc::SIGKILL);
        }
        if identity(self.pid).is_none() || identity(self.pid) == self.root {
            unsafe {
                libc::kill(-(self.pid as i32), libc::SIGKILL);
            }
        }
    }
}
