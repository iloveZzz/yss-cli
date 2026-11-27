//! Restricted local transport for a future Windows Pi tool broker.
//!
//! The broker must validate its random request token before executing any operation.
//! Binding this transport does not make the Windows Pi runtime available.
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs,
};
use std::{
    io::{self, ErrorKind, Read, Write},
    mem, thread,
    time::{Duration, Instant},
};
use tokio::{
    net::windows::named_pipe::{NamedPipeServer, ServerOptions},
    runtime::Runtime,
};
use windows_sys::Win32::{
    Foundation::{ERROR_BROKEN_PIPE, ERROR_NO_DATA, ERROR_PIPE_NOT_CONNECTED},
    Security::SECURITY_ATTRIBUTES,
};

const MAX_REQUEST: usize = 4 * 1024 * 1024;
const IO_TIMEOUT: Duration = Duration::from_secs(1);
const POLL: Duration = Duration::from_millis(20);

pub(super) struct Pipe {
    pub name: String,
    runtime: Runtime,
    server: NamedPipeServer,
    connected: bool,
}

/// Exercise the actual local pipe framing and cancellation before using it for Pi.
/// This does not substitute for testing Windows access from another user or host.
pub(super) fn probe(cancel: &Cancellation) -> Result<()> {
    const EXCHANGES: [(&[u8], &[u8]); 2] = [(b"probe\n", b"ready"), (b"reuse\n", b"again")];
    cancel.check()?;
    let mut pipe = Pipe::bind()?;
    // Connect and close before the server calls ConnectNamedPipe. This exercises
    // reusing a pipe instance after a client leaves before its request arrives.
    let mut client_options = std::fs::OpenOptions::new();
    client_options.read(true).write(true);
    drop(client_options.open(&pipe.name)?);
    if pipe.receive(cancel)?.is_some() {
        return Err(YssError::new(
            ErrorCode::NotReady,
            "Windows Pi 管道断开自检失败",
        ));
    }
    let name = pipe.name.clone();
    let client =
        thread::Builder::new()
            .name("pi-pipe-probe".into())
            .spawn(move || -> io::Result<()> {
                for (request, response) in EXCHANGES {
                    let deadline = Instant::now() + Duration::from_secs(2);
                    let mut stream = loop {
                        match client_options.open(&name) {
                            Ok(stream) => break stream,
                            Err(_) if Instant::now() < deadline => thread::sleep(POLL),
                            Err(error) => return Err(error),
                        }
                    };
                    stream.write_all(request)?;
                    let mut reply = Vec::new();
                    loop {
                        let mut byte = [0u8; 1];
                        if stream.read(&mut byte)? != 1 {
                            return Err(io::ErrorKind::UnexpectedEof.into());
                        }
                        reply.push(byte[0]);
                        if byte[0] == b'\n' {
                            break;
                        }
                        if reply.len() > 32 {
                            return Err(io::ErrorKind::InvalidData.into());
                        }
                    }
                    if reply.strip_suffix(b"\n") != Some(response) {
                        return Err(io::ErrorKind::InvalidData.into());
                    }
                    stream.write_all(b"\n")?;
                }
                Ok(())
            })?;
    let result = (|| -> Result<()> {
        for (expected, response) in EXCHANGES {
            let deadline = Instant::now() + Duration::from_secs(2);
            loop {
                cancel.check()?;
                if Instant::now() >= deadline {
                    return Err(YssError::new(
                        ErrorCode::NotReady,
                        "Windows Pi 管道连接自检超时",
                    ));
                }
                match pipe.receive(cancel)? {
                    None => continue,
                    Some(request) if request == expected => break,
                    Some(_) => {
                        return Err(YssError::new(
                            ErrorCode::NotReady,
                            "Windows Pi 管道请求自检失败",
                        ));
                    }
                }
            }
            pipe.reply(response, cancel)?;
        }
        Ok(())
    })();
    drop(pipe); // Close the handle before joining a client waiting on a failed server.
    let client_result = client
        .join()
        .map_err(|_| YssError::new(ErrorCode::NotReady, "Windows Pi 管道自检线程失败"))?;
    result?;
    client_result
        .map_err(|_| YssError::new(ErrorCode::NotReady, "Windows Pi 管道收发或确认自检失败"))?;
    let cancelled = cancel.child();
    let trigger = cancelled.clone();
    let timer = thread::Builder::new()
        .name("pi-pipe-cancel-probe".into())
        .spawn(move || {
            thread::sleep(Duration::from_millis(60));
            trigger.cancel();
        })?;
    let mut pipe = Pipe::bind()?;
    let deadline = Instant::now() + Duration::from_secs(2);
    let cancelled_cleanly = loop {
        if Instant::now() >= deadline {
            break false;
        }
        match pipe.receive(&cancelled) {
            Ok(None) => continue,
            Err(error) if error.code == ErrorCode::Cancelled => break true,
            _ => break false,
        }
    };
    let _ = timer.join();
    cancel.check()?;
    if !cancelled_cleanly {
        return Err(YssError::new(
            ErrorCode::NotReady,
            "Windows Pi 管道取消自检失败",
        ));
    }
    Ok(())
}

impl Pipe {
    pub fn bind() -> Result<Self> {
        let name = format!(r"\\.\pipe\yss-pi-{}", fs::unique_id()?);
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_io()
            .enable_time()
            .build()?;
        let _guard = runtime.enter();
        let server = fs::platform::with_private_descriptor(|descriptor| {
            let mut security = SECURITY_ATTRIBUTES {
                nLength: mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
                lpSecurityDescriptor: descriptor,
                bInheritHandle: 0,
            };
            // The descriptor remains valid throughout CreateNamedPipeW.
            unsafe {
                ServerOptions::new()
                    .first_pipe_instance(true)
                    .reject_remote_clients(true)
                    .max_instances(1)
                    .create_with_security_attributes_raw(
                        &name,
                        (&mut security as *mut SECURITY_ATTRIBUTES).cast(),
                    )
            }
        })?;
        drop(_guard);
        Ok(Self {
            name,
            runtime,
            server,
            connected: false,
        })
    }

    pub fn receive(&mut self, cancel: &Cancellation) -> Result<Option<Vec<u8>>> {
        cancel.check()?;
        let connected = self
            .runtime
            .block_on(async { tokio::time::timeout(POLL, self.server.connect()).await });
        match connected {
            Err(_) => return Ok(None),
            Ok(Err(error)) if error.raw_os_error() == Some(ERROR_NO_DATA as i32) => {
                // A client may close before ConnectNamedPipe succeeds. The
                // instance still needs to be disconnected before it can listen.
                self.server.disconnect()?;
                return Ok(None);
            }
            Ok(Err(error)) => return Err(error.into()),
            Ok(Ok(())) => self.connected = true,
        }
        let deadline = Instant::now() + IO_TIMEOUT;
        let mut bytes = Vec::new();
        loop {
            if let Err(error) = cancel.check() {
                self.disconnect();
                return Err(error);
            }
            if Instant::now() >= deadline {
                self.disconnect();
                return Err(YssError::new(ErrorCode::NotReady, "Pi 工具请求读取超时"));
            }
            let mut buffer = [0; 8192];
            match self.server.try_read(&mut buffer) {
                Ok(0) => {
                    self.disconnect();
                    return if bytes.is_empty() {
                        Ok(None)
                    } else {
                        Err(io::Error::from(ErrorKind::UnexpectedEof).into())
                    };
                }
                Ok(n) => {
                    bytes.extend_from_slice(&buffer[..n]);
                    if bytes.len() > MAX_REQUEST {
                        self.disconnect();
                        return Err(YssError::new(
                            ErrorCode::InvalidInput,
                            "Pi 工具请求超过长度限制",
                        ));
                    }
                    if let Some(pos) = bytes.iter().position(|byte| *byte == b'\n') {
                        if pos + 1 != bytes.len() {
                            self.disconnect();
                            return Err(YssError::new(
                                ErrorCode::InvalidInput,
                                "Pi 工具请求包含多条消息",
                            ));
                        }
                        return Ok(Some(bytes));
                    }
                }
                Err(error) if error.kind() == ErrorKind::WouldBlock => {
                    let _ = self.runtime.block_on(async {
                        tokio::time::timeout(POLL, self.server.readable()).await
                    });
                }
                Err(error) => {
                    self.disconnect();
                    if bytes.is_empty()
                        && matches!(
                            error.raw_os_error().map(|code| code as u32),
                            Some(ERROR_BROKEN_PIPE | ERROR_NO_DATA | ERROR_PIPE_NOT_CONNECTED)
                        )
                    {
                        return Ok(None);
                    }
                    return Err(error.into());
                }
            }
        }
    }

    pub fn reply(&mut self, bytes: &[u8], cancel: &Cancellation) -> Result<()> {
        if !self.connected {
            return Err(YssError::new(ErrorCode::NotReady, "Pi 工具连接已关闭"));
        }
        let deadline = Instant::now() + IO_TIMEOUT;
        // A newline frames the response. The client acknowledges it before the
        // server disconnects; DisconnectNamedPipe otherwise discards unread data.
        let mut frame = Vec::with_capacity(bytes.len() + 1);
        frame.extend_from_slice(bytes);
        frame.push(b'\n');
        let mut remaining = frame.as_slice();
        let result = loop {
            if let Err(error) = cancel.check() {
                break Err(error);
            }
            if Instant::now() >= deadline {
                break Err(YssError::new(ErrorCode::NotReady, "Pi 工具响应写入超时"));
            }
            if remaining.is_empty() {
                break self.await_ack(cancel, deadline);
            }
            match self.server.try_write(remaining) {
                Ok(0) => break Err(io::Error::from(ErrorKind::WriteZero).into()),
                Ok(n) => remaining = &remaining[n..],
                Err(error) if error.kind() == ErrorKind::WouldBlock => {
                    let _ = self.runtime.block_on(async {
                        tokio::time::timeout(POLL, self.server.writable()).await
                    });
                }
                Err(error) => break Err(error.into()),
            }
        };
        self.disconnect();
        result
    }

    fn await_ack(&self, cancel: &Cancellation, deadline: Instant) -> Result<()> {
        loop {
            cancel.check()?;
            if Instant::now() >= deadline {
                return Err(YssError::new(ErrorCode::NotReady, "Pi 工具响应未确认"));
            }
            let mut ack = [0u8; 1];
            match self.server.try_read(&mut ack) {
                Ok(1) if ack[0] == b'\n' => return Ok(()),
                Ok(_) => return Err(YssError::new(ErrorCode::NotReady, "Pi 工具响应确认无效")),
                Err(error) if error.kind() == ErrorKind::WouldBlock => {
                    let _ = self.runtime.block_on(async {
                        tokio::time::timeout(POLL, self.server.readable()).await
                    });
                }
                Err(error) => return Err(error.into()),
            }
        }
    }

    fn disconnect(&mut self) {
        if self.connected {
            let _ = self.server.disconnect();
            self.connected = false;
        }
    }
}
