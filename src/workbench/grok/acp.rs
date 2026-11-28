use super::not_ready;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{jobs::JobContext, process},
};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Read, Write},
    os::fd::AsRawFd,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver, RecvTimeoutError},
    thread,
    time::{Duration, Instant},
};

const FRAME_LIMIT: u64 = 8 * 1024 * 1024;
const POLL: Duration = Duration::from_millis(20);
pub(super) const SETUP_TIMEOUT: Duration = Duration::from_secs(12);

pub(super) struct Acp {
    child: Child,
    tree: process::ProcessTree,
    stdin: ChildStdin,
    frames: Receiver<Result<Value>>,
    next_request: u64,
    context: Option<JobContext>,
    stopped: bool,
}

pub(super) fn protocol(message: &str) -> YssError {
    not_ready("protocol", message)
}

pub(super) fn rate_limited() -> YssError {
    YssError::new(ErrorCode::Network, "Grok Build 暂时限制了模型请求")
        .context("grokIssue", "rateLimit")
}

pub(super) fn remote_error(error: &Value) -> YssError {
    let code = error["code"].as_i64();
    let message = error["message"].as_str().unwrap_or("").to_ascii_lowercase();
    if code == Some(-32000)
        || message.contains("authenticat")
        || message.contains("not logged")
        || message.contains("sign in")
        || message.contains("unauthorized")
    {
        return not_ready("credentials", "Grok Build 需要在专用配置中登录或重新登录");
    }
    if code == Some(-32003) {
        return rate_limited();
    }
    protocol("Grok Build 拒绝了 ACP 请求")
}

impl Acp {
    pub(super) fn open(
        mut command: Command,
        cancel: &Cancellation,
        context: Option<&JobContext>,
    ) -> Result<Self> {
        cancel.check()?;
        command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        let scope = process::isolate(&mut command)?;
        if let Some(context) = context {
            context.begin_grok_cleanup_guard(&scope)?;
        }
        let mut child = match command.spawn() {
            Ok(child) => child,
            Err(_) => {
                if let Some(context) = context {
                    context.abandon_grok_cleanup_guard()?;
                }
                return Err(not_ready("sandbox", "无法启动受控的 Grok Build 进程"));
            }
        };
        let mut tree = process::ProcessTree::attach(&mut child, scope)?;
        if let Some(context) = context {
            let identity = process::process_group_identity(child.id());
            if let Err(error) = context.record_grok_child(identity) {
                process::stop(&mut child, &mut tree);
                if let Some(identity) = identity {
                    context.clear_unregistered_grok_child(&identity)?;
                }
                return Err(error);
            }
        }
        let stdin = child.stdin.take().expect("piped Grok stdin");
        let fd = stdin.as_raw_fd();
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        let output = child.stdout.take().expect("piped Grok stdout");
        let (send, frames) = mpsc::sync_channel(8);
        let mut acp = Self {
            child,
            tree,
            stdin,
            frames,
            next_request: 0,
            context: context.cloned(),
            stopped: false,
        };
        if flags == -1 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } == -1
        {
            acp.stop()?;
            return Err(protocol("无法设置 Grok Build 请求的写入超时"));
        }
        thread::spawn(move || {
            let mut output = BufReader::new(output);
            loop {
                let mut line = Vec::new();
                let result = match (&mut output)
                    .take(FRAME_LIMIT + 1)
                    .read_until(b'\n', &mut line)
                {
                    Ok(0) => break,
                    Ok(size) if size as u64 <= FRAME_LIMIT && line.ends_with(b"\n") => {
                        serde_json::from_slice(&line)
                            .map_err(|_| protocol("Grok Build 返回的 ACP 消息不是 JSON"))
                    }
                    _ => Err(protocol(
                        "Grok Build 的 ACP 消息读取失败、不完整或超过 8 MiB",
                    )),
                };
                let failed = result.is_err();
                if send.send(result).is_err() || failed {
                    break;
                }
            }
        });
        Ok(acp)
    }

    pub(super) fn stop(&mut self) -> Result<()> {
        if self.stopped {
            return Ok(());
        }
        process::stop(&mut self.child, &mut self.tree);
        self.child.wait().map_err(|_| {
            YssError::new(
                ErrorCode::RecoveryRequired,
                "无法确认 Grok Build 进程已退出",
            )
        })?;
        if let Some(context) = self.context.as_ref() {
            context.clear_grok_cleanup_guard()?;
        }
        self.stopped = true;
        Ok(())
    }

    pub(super) fn id(&mut self) -> u64 {
        self.next_request += 1;
        self.next_request
    }

    pub(super) fn send(&mut self, frame: Value, cancel: &Cancellation) -> Result<()> {
        self.send_prepared(&Self::prepare_frame(&frame)?, cancel)
    }

    pub(super) fn prepare_frame(frame: &Value) -> Result<Vec<u8>> {
        let mut line =
            serde_json::to_vec(frame).map_err(|_| protocol("无法编码 Grok Build ACP 请求"))?;
        line.push(b'\n');
        if line.len() as u64 > FRAME_LIMIT {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "Grok Build 请求超过 8 MiB",
            ));
        }
        Ok(line)
    }

    pub(super) fn send_prepared(&mut self, mut rest: &[u8], cancel: &Cancellation) -> Result<()> {
        let deadline = Instant::now() + SETUP_TIMEOUT;
        while !rest.is_empty() {
            cancel.check()?;
            if Instant::now() >= deadline {
                return Err(protocol("Grok Build 请求写入超时"));
            }
            match self.stdin.write(rest) {
                Ok(n) if n > 0 => rest = &rest[n..],
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => {}
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => thread::sleep(POLL),
                _ => return Err(protocol("Grok Build 请求写入失败")),
            }
        }
        Ok(())
    }

    pub(super) fn receive(
        &mut self,
        cancel: &Cancellation,
        deadline: Instant,
    ) -> Result<Option<Value>> {
        loop {
            cancel.check()?;
            let left = deadline.saturating_duration_since(Instant::now());
            if left.is_zero() {
                return Ok(None);
            }
            match self.frames.recv_timeout(POLL.min(left)) {
                Ok(result) => {
                    let frame = result?;
                    if frame["jsonrpc"] != "2.0" {
                        return Err(protocol("Grok Build 返回了无效的 ACP 消息"));
                    }
                    return Ok(Some(frame));
                }
                Err(RecvTimeoutError::Timeout) => {}
                Err(RecvTimeoutError::Disconnected) => {
                    return Err(protocol("Grok Build ACP 进程已断开"));
                }
            }
        }
    }

    pub(super) fn block_agent_request(
        &mut self,
        frame: &Value,
        cancel: &Cancellation,
    ) -> Result<()> {
        let (Some(method), Some(id)) = (frame["method"].as_str(), frame.get("id")) else {
            return Ok(());
        };
        let response = if method == "session/request_permission" {
            json!({"jsonrpc":"2.0","id":id,"result":{"outcome":{"outcome":"cancelled"}}})
        } else {
            json!({"jsonrpc":"2.0","id":id,"error":{"code":-32601,"message":"Capability unavailable"}})
        };
        self.send(response, cancel)
    }

    pub(super) fn request(
        &mut self,
        method: &str,
        params: Value,
        cancel: &Cancellation,
    ) -> Result<Value> {
        let id = self.id();
        self.send(
            json!({"jsonrpc":"2.0","id":id,"method":method,"params":params}),
            cancel,
        )?;
        let deadline = Instant::now() + SETUP_TIMEOUT;
        while let Some(frame) = self.receive(cancel, deadline)? {
            if frame.get("method").is_some() {
                self.block_agent_request(&frame, cancel)?;
            } else if frame["id"] == id {
                return reply(&frame);
            }
        }
        Err(protocol("Grok Build ACP 请求超时"))
    }
}

impl Drop for Acp {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}

pub(super) fn reply(frame: &Value) -> Result<Value> {
    if let Some(error) = frame.get("error") {
        return Err(remote_error(error));
    }
    frame
        .get("result")
        .cloned()
        .ok_or_else(|| protocol("Grok Build ACP 响应缺少结果"))
}
