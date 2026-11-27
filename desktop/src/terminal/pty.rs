use portable_pty::{CommandBuilder, MasterPty, PtySize, native_pty_system};
use std::{
    io::{Read, Write},
    path::Path,
    sync::{Arc, Mutex},
};

#[cfg(target_os = "macos")]
#[path = "pty/macos.rs"]
mod native;
#[cfg(windows)]
#[path = "pty/windows.rs"]
mod native;

#[cfg(windows)]
#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct WindowsPty {
    backend: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    build_number: Option<u32>,
}

#[cfg(windows)]
pub(super) fn windows_pty() -> WindowsPty {
    WindowsPty {
        backend: "conpty",
        build_number: native::build_number(),
    }
}

pub(super) struct Pty {
    pub reader: Box<dyn Read + Send>,
    pub writer: Box<dyn Write + Send>,
    pub control: Arc<dyn Control>,
}

pub(super) trait Control: Send + Sync {
    fn resize(&self, cols: u16, rows: u16) -> Result<(), String>;
    fn poll_exit(&self) -> Result<Option<i32>, String>;
    fn close(&self) -> Result<(), String>;
}

struct TerminalControl {
    state: Mutex<NativeState>,
}

struct NativeState {
    master: Option<Box<dyn MasterPty + Send>>,
    process: native::Process,
}

impl Control for TerminalControl {
    fn resize(&self, cols: u16, rows: u16) -> Result<(), String> {
        let state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let master = state.master.as_ref().ok_or("终端已关闭")?;
        master
            .resize(pty_size(cols, rows)?)
            .map_err(|error| format!("调整终端尺寸失败：{error}"))
    }

    fn poll_exit(&self) -> Result<Option<i32>, String> {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        state.process.poll_exit().map_err(|error| error.to_string())
    }

    fn close(&self) -> Result<(), String> {
        let mut state = self.state.lock().unwrap_or_else(|error| error.into_inner());
        let master = state.master.take();
        state
            .process
            .close(master)
            .map_err(|error| error.to_string())
    }
}

pub(super) fn spawn(cwd: &Path, cols: u16, rows: u16) -> Result<Pty, String> {
    if !cwd.is_absolute() || !cwd.is_dir() {
        return Err("终端启动目录不可用".into());
    }
    let size = pty_size(cols, rows)?;
    let (mut command, launch) = native::prepare().map_err(|error| error.to_string())?;
    configure_shell(&mut command, cwd);

    let pair = native_pty_system()
        .openpty(size)
        .map_err(|error| format!("打开伪终端失败：{error}"))?;
    let reader = pair
        .master
        .try_clone_reader()
        .map_err(|error| format!("打开终端输出失败：{error}"))?;
    let writer = pair
        .master
        .take_writer()
        .map_err(|error| format!("打开终端输入失败：{error}"))?;
    #[cfg(target_os = "macos")]
    let (reader, writer) = launch
        .streams(&*pair.master, reader, writer)
        .map_err(|error| format!("初始化终端输入输出失败：{error}"))?;
    let child = pair
        .slave
        .spawn_command(command)
        .map_err(|error| format!("启动系统 Shell 失败：{error}"))?;
    drop(pair.slave);
    let state = NativeState {
        master: Some(pair.master),
        process: native::Process::new(child, launch),
    };

    Ok(Pty {
        reader,
        writer,
        control: Arc::new(TerminalControl {
            state: Mutex::new(state),
        }),
    })
}

fn configure_shell(command: &mut CommandBuilder, cwd: &Path) {
    command.cwd(cwd.as_os_str());
    command.env("TERM", "xterm-256color");
    command.env("COLORTERM", "truecolor");
}

fn pty_size(cols: u16, rows: u16) -> Result<PtySize, String> {
    if cols == 0 || rows == 0 || cols > i16::MAX as u16 || rows > i16::MAX as u16 {
        return Err("终端行列数超出支持范围".into());
    }
    Ok(PtySize {
        rows,
        cols,
        pixel_width: 0,
        pixel_height: 0,
    })
}
