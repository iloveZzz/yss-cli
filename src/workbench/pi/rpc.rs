use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{
        agent_protocol::{PiModel, ThinkingLevel},
        jobs::JobContext,
        process,
    },
};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, btree_map::Entry},
    io::{BufRead, BufReader, Read, Write},
    os::fd::AsRawFd,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver, RecvTimeoutError},
    thread,
    time::{Duration, Instant},
};

const FRAME_LIMIT: u64 = 8 * 1024 * 1024;
const REASONING_LIMIT: usize = 6_000;

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
        self.query("get_state", cancel)
    }

    pub fn available_models(&mut self, cancel: &Cancellation) -> Result<Value> {
        self.query("get_available_models", cancel)
    }

    pub fn thinking_levels(&mut self, cancel: &Cancellation) -> Result<Vec<ThinkingLevel>> {
        let data = self.query("get_available_thinking_levels", cancel)?;
        let levels: Vec<ThinkingLevel> = serde_json::from_value(data["levels"].clone())
            .map_err(|_| protocol("Pi 推理级别列表格式无效"))?;
        if levels.is_empty() || levels.len() > 7 {
            return Err(protocol("Pi 推理级别列表格式无效"));
        }
        Ok(levels)
    }

    pub fn set_thinking_level(
        &mut self,
        level: ThinkingLevel,
        cancel: &Cancellation,
    ) -> Result<()> {
        self.request(
            "set_thinking_level",
            json!({"type":"set_thinking_level", "id":"set_thinking_level", "level":level}),
            cancel,
        )?;
        Ok(())
    }

    pub fn set_model(&mut self, model: &PiModel, cancel: &Cancellation) -> Result<()> {
        self.request(
            "set_model",
            json!({"type":"set_model", "id":"set_model", "provider":model.provider, "modelId":model.id}),
            cancel,
        )?;
        Ok(())
    }

    fn query(&mut self, command: &str, cancel: &Cancellation) -> Result<Value> {
        let data = self.request(command, json!({ "type": command, "id": command }), cancel)?;
        if !data.is_object() {
            return Err(protocol("Pi 状态或模型查询响应不兼容"));
        }
        Ok(data)
    }

    fn request(&mut self, command: &str, payload: Value, cancel: &Cancellation) -> Result<Value> {
        self.send(payload, cancel, Duration::from_secs(5))?;
        let deadline = Some(Instant::now() + Duration::from_secs(20));
        loop {
            let value = self.receive(cancel, deadline)?;
            if value["type"] == "response" && value["id"] == command {
                if value["command"] == command && value["success"] == true {
                    return Ok(value["data"].clone());
                }
                if command == "set_model" {
                    return Err(YssError::new(
                        ErrorCode::NotReady,
                        "Pi 未能切换到所选模型，请核对配置",
                    )
                    .context("piIssue", "modelUnavailable"));
                }
                return Err(protocol("Pi 状态或模型查询响应不兼容"));
            }
        }
    }

    pub fn prompt(&mut self, prompt: &str, context: &JobContext) -> Result<()> {
        self.complete_prompt(prompt, &context.cancel, Some(context), None)
    }

    pub fn test_connection(&mut self, cancel: &Cancellation) -> Result<()> {
        self.complete_prompt(
            "Reply with OK.",
            cancel,
            None,
            Some(Instant::now() + Duration::from_secs(20)),
        )
    }

    fn complete_prompt(
        &mut self,
        prompt: &str,
        cancel: &Cancellation,
        context: Option<&JobContext>,
        finish_deadline: Option<Instant>,
    ) -> Result<()> {
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
            let event =
                match self.receive(cancel, finish_deadline.or((!accepted).then_some(deadline))) {
                    Ok(event) => event,
                    Err(error) => {
                        if let Some(context) = context {
                            let status = if error.code == ErrorCode::Cancelled {
                                "canceled"
                            } else {
                                "failed"
                            };
                            let _ = messages.finish_reasoning(context, &Value::Null, status);
                        }
                        return Err(error);
                    }
                };
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
                    if let Some(context) = context {
                        context.emit("piStatus", json!({ "message": "Pi 正在重试模型请求" }))?;
                    } else {
                        return Err(messages.error.unwrap_or_else(|| provider_error(None)));
                    }
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
    let status_is = |code: &str| {
        raw.starts_with(&format!("{code} "))
            || raw.contains(&format!("status {code}"))
            || raw.contains(&format!("http {code}"))
    };
    let (code, issue, message) = if raw.contains("invalidsubscription") {
        return YssError::new(
            ErrorCode::NotReady,
            "方舟套餐不可用，请核对套餐状态和专属 API Key，然后重新检测",
        )
        .context("piIssue", "subscription")
        .context("providerCode", "InvalidSubscription");
    } else if raw.contains("not supported when using codex with a chatgpt account") {
        (
            ErrorCode::NotReady,
            "modelAccess",
            "当前 ChatGPT 登录不支持此模型，请选择账号支持的模型后发起新运行",
        )
    } else if raw.contains("model_not_found")
        || (raw.contains("model") && (raw.contains("not found") || raw.contains("no model")))
    {
        (
            ErrorCode::NotReady,
            "modelUnavailable",
            "Pi 未找到可用模型，请核对本机 Pi 配置",
        )
    } else if status_is("401")
        || [
            "credential",
            "authentication",
            "no api key",
            "invalidapikey",
            "unauthorized",
            "oauth",
            "not logged",
        ]
        .iter()
        .any(|term| raw.contains(term))
    {
        (
            ErrorCode::NotReady,
            "credentials",
            "Pi 模型凭证不可用，请在本机 Pi 核对登录和 API Key",
        )
    } else if status_is("403") || raw.contains("permission denied") || raw.contains("accessdenied")
    {
        (
            ErrorCode::NotReady,
            "modelAccess",
            "供应商拒绝模型访问，请核对当前账号的模型权限",
        )
    } else if status_is("429") || raw.contains("rate limit") {
        (
            ErrorCode::Network,
            "rateLimit",
            "供应商暂时限制模型请求，请核对配额或稍后重试",
        )
    } else {
        (
            ErrorCode::Network,
            "requestFailed",
            "Pi 模型请求失败，请在本机 Pi 检查模型与服务连接",
        )
    };
    YssError::new(code, message).context("piIssue", issue)
}

#[derive(Default)]
struct Messages {
    seq: u64,
    current: bool,
    reason: Option<String>,
    error: Option<YssError>,
    reasoning: BTreeMap<u64, ReasoningBlock>,
    reasoning_seq: u64,
}

fn reasoning_excerpt(text: &str, limit: usize) -> String {
    let mut chars = text.chars();
    let mut excerpt = chars.by_ref().take(limit).collect::<String>();
    if chars.next().is_some() {
        excerpt.push('…');
    }
    excerpt
}

struct ReasoningBlock {
    id: String,
    started_at: Instant,
    published_chars: usize,
    finished: bool,
}

impl ReasoningBlock {
    fn append(&mut self, context: &JobContext, delta: &str) -> Result<()> {
        if self.finished || delta.is_empty() || self.published_chars > REASONING_LIMIT {
            return Ok(());
        }
        let delta = reasoning_excerpt(delta, REASONING_LIMIT - self.published_chars);
        self.published_chars += delta.chars().count();
        context.emit(
            "item/piReasoning/delta",
            json!({"itemId":self.id,"delta":delta}),
        )
    }

    fn finish(&mut self, context: &JobContext, text: Option<&str>, status: &str) -> Result<()> {
        if self.finished {
            return Ok(());
        }
        self.finished = true;
        let text = text.map(|value| reasoning_excerpt(value, REASONING_LIMIT));
        context.emit(
            "item/completed",
            json!({"item":{
                "id":self.id, "type":"piReasoning", "text":text, "status":status,
                "durationMs":self.started_at.elapsed().as_millis() as u64
            }}),
        )
    }
}

impl Messages {
    fn reasoning_block(&mut self, index: u64, context: &JobContext) -> Result<&mut ReasoningBlock> {
        Ok(match self.reasoning.entry(index) {
            Entry::Occupied(block) => block.into_mut(),
            Entry::Vacant(entry) => {
                self.reasoning_seq += 1;
                let id = format!("pi-reasoning-{}", self.reasoning_seq);
                context.emit(
                    "item/started",
                    json!({"item":{"id":id,"type":"piReasoning"}}),
                )?;
                entry.insert(ReasoningBlock {
                    id,
                    started_at: Instant::now(),
                    published_chars: 0,
                    finished: false,
                })
            }
        })
    }

    fn finish_reasoning(
        &mut self,
        context: &JobContext,
        message: &Value,
        status: &str,
    ) -> Result<()> {
        if let Some(parts) = message["content"].as_array() {
            for (index, part) in parts.iter().enumerate() {
                if part["type"] == "thinking"
                    && part["redacted"] != true
                    && let Some(text) = part["thinking"]
                        .as_str()
                        .filter(|text| !text.trim().is_empty())
                {
                    self.reasoning_block(index as u64, context)?.finish(
                        context,
                        Some(text),
                        status,
                    )?;
                }
            }
        }
        for block in self.reasoning.values_mut() {
            block.finish(context, None, status)?;
        }
        self.reasoning.clear();
        Ok(())
    }

    fn thinking(&mut self, event: &Value, context: &JobContext) -> Result<()> {
        let Some(index) = event["contentIndex"].as_u64() else {
            return Ok(());
        };
        let block = self.reasoning_block(index, context)?;
        match event["type"].as_str() {
            Some("thinking_delta") => {
                block.append(context, event["delta"].as_str().unwrap_or_default())
            }
            Some("thinking_end") => block.finish(context, event["content"].as_str(), "completed"),
            _ => Ok(()),
        }
    }

    fn record(&mut self, event: &Value, context: Option<&JobContext>) -> Result<()> {
        let message = &event["message"];
        let kind = event["type"].as_str().unwrap_or_default();
        let update = &event["assistantMessageEvent"];
        if let Some(context) = context {
            if kind == "message_update"
                && matches!(
                    update["type"].as_str(),
                    Some("thinking_start" | "thinking_delta" | "thinking_end")
                )
            {
                return self.thinking(update, context);
            }
            if kind == "message_end" && message["role"] == "assistant" {
                let status = match message["stopReason"].as_str() {
                    Some("aborted") => "canceled",
                    Some("error") => "failed",
                    _ => "completed",
                };
                self.finish_reasoning(context, message, status)?;
            }
        }
        if kind == "message_start" && message["role"] == "assistant" {
            self.seq += 1;
            self.current = true;
        }
        let id = format!("pi-message-{}", self.seq);
        let event = match kind {
            "message_update"
                if context.is_some()
                    && self.current
                    && event["assistantMessageEvent"]["type"] == "text_delta" =>
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
                if context.is_none() {
                    return Ok(());
                }
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
        if let (Some(context), Some((kind, payload))) = (context, event) {
            context.emit(kind, payload)?;
        }
        Ok(())
    }
}
