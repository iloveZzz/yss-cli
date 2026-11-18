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

pub(super) struct Session {
    child: Child,
    tree: process::ProcessTree,
    input: ChildStdin,
    messages: Receiver<Result<Value>>,
    active: bool,
}

fn protocol(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsupportedFormat, message)
}

impl Session {
    pub fn open(mut command: Command, cancel: &Cancellation) -> Result<Self> {
        cancel.check()?;
        command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let scope = process::isolate(&mut command)?;
        let mut child = command
            .spawn()
            .map_err(|_| YssError::new(ErrorCode::NotReady, "无法启动本机 Pi RPC"))?;
        let mut tree = process::ProcessTree::attach(&mut child, scope)?;
        let input = child.stdin.take().expect("piped stdin");
        let fd = input.as_raw_fd();
        // A stuck Pi process must not hold cancellation behind a full stdin pipe.
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags == -1 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } == -1
        {
            process::stop(&mut child, &mut tree);
            return Err(protocol("无法设置 Pi RPC 写入超时"));
        }
        let output = child.stdout.take().expect("piped stdout");
        let (send, messages) = mpsc::sync_channel(8);
        thread::spawn(move || {
            let mut output = BufReader::new(output);
            loop {
                let mut line = Vec::new();
                let value = match (&mut output)
                    .take(FRAME_LIMIT + 1)
                    .read_until(b'\n', &mut line)
                {
                    Ok(0) => break,
                    Ok(size) if size as u64 <= FRAME_LIMIT && line.last() == Some(&b'\n') => {
                        serde_json::from_slice(&line).map_err(|_| protocol("Pi RPC 返回无效 JSONL"))
                    }
                    Ok(_) => Err(protocol("Pi RPC 消息不完整或超过 8 MiB")),
                    Err(_) => Err(protocol("Pi RPC 输出读取失败")),
                };
                let failed = value.is_err();
                if send.send(value).is_err() || failed {
                    break;
                }
            }
        });
        if let Some(mut stderr) = child.stderr.take() {
            // Startup and provider diagnostics can contain credentials.
            thread::spawn(move || {
                let _ = std::io::copy(&mut stderr, &mut std::io::sink());
            });
        }
        Ok(Self {
            child,
            tree,
            input,
            messages,
            active: false,
        })
    }

    fn send(&mut self, command: Value, cancel: &Cancellation, timeout: Duration) -> Result<()> {
        let mut line = serde_json::to_vec(&command).map_err(|_| protocol("Pi RPC 请求编码失败"))?;
        line.push(b'\n');
        if line.len() as u64 > FRAME_LIMIT {
            return Err(protocol("Pi RPC 请求超过 8 MiB"));
        }
        let deadline = Instant::now() + timeout;
        let mut bytes = line.as_slice();
        while !bytes.is_empty() {
            cancel.check()?;
            if Instant::now() >= deadline {
                return Err(protocol("Pi RPC 请求写入超时"));
            }
            match self.input.write(bytes) {
                Ok(0) => return Err(protocol("Pi RPC 请求通道已关闭")),
                Ok(size) => bytes = &bytes[size..],
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(20));
                }
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(_) => return Err(protocol("Pi RPC 请求写入失败")),
            }
        }
        Ok(())
    }

    fn receive(&mut self, cancel: &Cancellation, deadline: Option<Instant>) -> Result<Value> {
        loop {
            cancel.check()?;
            if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                return Err(protocol("Pi RPC 响应超时"));
            }
            match self.messages.recv_timeout(Duration::from_millis(100)) {
                Ok(value) => return value,
                Err(RecvTimeoutError::Timeout) if self.child.try_wait()?.is_some() => {
                    return Err(protocol("Pi 进程提前退出"));
                }
                Err(RecvTimeoutError::Timeout) => {}
                Err(RecvTimeoutError::Disconnected) => {
                    return Err(protocol("Pi 进程在当前请求完成前关闭了 RPC 通道"));
                }
            }
        }
    }

    pub fn state(&mut self, cancel: &Cancellation) -> Result<Value> {
        self.send(
            json!({ "type": "get_state", "id": "state" }),
            cancel,
            Duration::from_secs(5),
        )?;
        let deadline = Some(Instant::now() + Duration::from_secs(20));
        loop {
            let value = self.receive(cancel, deadline)?;
            if value["type"] == "response" && value["id"] == "state" {
                if value["command"] == "get_state"
                    && value["success"] == true
                    && value["data"].is_object()
                {
                    return Ok(value["data"].clone());
                }
                return Err(protocol("Pi get_state 响应不兼容"));
            }
        }
    }

    pub fn prompt(&mut self, prompt: &str, context: &JobContext) -> Result<()> {
        let cancel = &context.cancel;
        self.active = true;
        self.send(
            json!({ "type": "prompt", "id": "prompt", "message": prompt }),
            cancel,
            Duration::from_secs(5),
        )?;
        let mut accepted = false;
        let mut settled = None;
        let mut messages = Messages::default();
        let deadline = Instant::now() + Duration::from_secs(30);
        while !accepted || settled.is_none() {
            let event = self.receive(cancel, (!accepted).then_some(deadline))?;
            match event["type"].as_str() {
                Some("response") if event["id"] == "prompt" => {
                    if event["command"] != "prompt" || event["success"] != true {
                        return Err(provider_error(event["error"].as_str()));
                    }
                    if event["data"]["disposition"] != "started" {
                        return Err(protocol("Pi 未启动本次模型执行"));
                    }
                    accepted = true;
                }
                Some("agent_settled") => {
                    settled = Some(
                        event["aborted"]
                            .as_bool()
                            .ok_or_else(|| protocol("Pi 完成事件格式无效"))?,
                    );
                    self.active = false;
                }
                Some("message_start" | "message_update" | "message_end") => {
                    messages.record(&event, context)?;
                }
                Some("auto_retry_start") => {
                    context.emit("piStatus", json!({ "message": "Pi 正在重试模型请求" }))?;
                }
                _ => {}
            }
        }
        if settled == Some(true) || messages.reason.as_deref() == Some("aborted") {
            return Err(YssError::new(ErrorCode::Cancelled, "Pi 本轮已停止"));
        }
        match messages.reason.as_deref() {
            Some("stop") => cancel.check(),
            Some("error") => Err(messages.error.unwrap_or_else(|| provider_error(None))),
            Some("length") => Err(protocol("Pi 达到模型输出上限，本轮未完成")),
            _ => Err(protocol("Pi 本轮缺少完整的最终答复")),
        }
    }
}

impl Drop for Session {
    fn drop(&mut self) {
        if self.active {
            let cancel = Cancellation::new();
            let _ = self.send(
                json!({ "type": "abort", "id": "abort" }),
                &cancel,
                Duration::from_millis(250),
            );
            let deadline = Some(Instant::now() + Duration::from_secs(2));
            while let Ok(value) = self.receive(&cancel, deadline) {
                if value["type"] == "agent_settled"
                    || (value["type"] == "response" && value["id"] == "abort")
                {
                    break;
                }
            }
        }
        process::stop(&mut self.child, &mut self.tree);
    }
}

fn provider_error(raw: Option<&str>) -> YssError {
    let raw = raw.unwrap_or_default().to_ascii_lowercase();
    if [
        "401",
        "403",
        "credential",
        "authentication",
        "no api key",
        "invalidsubscription",
        "unauthorized",
        "oauth",
        "not logged",
    ]
    .iter()
    .any(|term| raw.contains(term))
    {
        YssError::new(
            ErrorCode::NotReady,
            "Pi 模型认证失败，请检查本机 Pi 的供应商、登录和订阅",
        )
    } else if raw.contains("model") && (raw.contains("not found") || raw.contains("no model")) {
        YssError::new(ErrorCode::NotReady, "Pi 未找到可用模型，请检查本机 Pi 配置")
    } else {
        YssError::new(
            ErrorCode::Network,
            "Pi 模型请求失败，请在本机 Pi 检查模型与服务连接",
        )
    }
}

#[derive(Default)]
struct Messages {
    seq: u64,
    current: bool,
    reason: Option<String>,
    error: Option<YssError>,
}

impl Messages {
    fn record(&mut self, event: &Value, context: &JobContext) -> Result<()> {
        let message = &event["message"];
        let kind = event["type"].as_str().unwrap_or_default();
        if kind == "message_start" && message["role"] == "assistant" {
            self.seq += 1;
            self.current = true;
        }
        let id = format!("pi-message-{}", self.seq);
        let event = match kind {
            "message_update"
                if self.current && event["assistantMessageEvent"]["type"] == "text_delta" =>
            {
                let delta = event["assistantMessageEvent"]["delta"]
                    .as_str()
                    .unwrap_or_default();
                Some((
                    "item/agentMessage/delta",
                    json!({ "itemId": id, "delta": delta }),
                ))
            }
            "message_end" if message["role"] == "assistant" => {
                if !self.current {
                    self.seq += 1;
                }
                self.current = false;
                let reason = message["stopReason"].as_str();
                self.reason = reason.map(str::to_owned);
                self.error = (reason == Some("error"))
                    .then(|| provider_error(message["errorMessage"].as_str()));
                let content = message["content"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .filter(|chunk| chunk["type"] == "text")
                    .filter_map(|chunk| chunk["text"].as_str())
                    .collect::<Vec<_>>()
                    .join("\n");
                if content.is_empty() {
                    None
                } else {
                    Some((
                        "item/completed",
                        json!({
                            "item": { "id": format!("pi-message-{}", self.seq), "type": "agentMessage", "text": content }
                        }),
                    ))
                }
            }
            _ => None,
        };
        if let Some((kind, payload)) = event {
            context.emit(kind, payload)?;
        }
        Ok(())
    }
}
