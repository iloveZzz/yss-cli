use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
};
use serde_json::{Value, json};
use std::{
    io::{BufRead, BufReader, Read, Write},
    path::Path,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver, RecvTimeoutError},
    thread,
    time::{Duration, Instant},
};

pub struct RunRequest<'a> {
    pub cwd: &'a str,
    pub roots: &'a [String],
    pub prompt: &'a str,
    pub resume: Option<&'a str>,
}

pub struct Session {
    child: Child,
    tree: super::process::ProcessTree,
    input: ChildStdin,
    messages: Receiver<std::result::Result<Value, String>>,
    next_id: u64,
    thread_id: Option<String>,
    turn_id: Option<String>,
    completed: Option<Value>,
}
fn protocol(message: impl Into<String>) -> YssError {
    YssError::new(ErrorCode::UnsupportedFormat, message)
}
impl Session {
    pub fn open(executable: &str, cwd: &Path, cancel: &Cancellation) -> Result<Self> {
        let version = super::process::run(
            executable,
            &["--version".into()],
            cancel,
            Duration::from_secs(10),
        )
        .map_err(|mut error| {
            if error.code != ErrorCode::Cancelled {
                error.message = format!("Codex CLI 版本探测失败：{}", error.message);
            }
            error
        })?;
        if !String::from_utf8_lossy(&version).starts_with("codex-cli ") {
            return Err(protocol(
                "无法识别 Codex CLI 版本，请检查所配置的 Codex 可执行文件",
            ));
        }
        cancel.check()?;
        let mut command = Command::new(executable);
        command
            .args(["app-server", "--stdio"])
            .current_dir(cwd)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let scope = super::process::isolate(&mut command)?;
        let mut child = command.spawn().map_err(|e| {
            YssError::new(ErrorCode::NotReady, "Codex app-server stdio 启动失败")
                .context("detail", e)
        })?;
        let tree = super::process::ProcessTree::attach(&mut child, scope)?;
        let input = child
            .stdin
            .take()
            .ok_or_else(|| protocol("Codex stdin 不可用"))?;
        let output = child
            .stdout
            .take()
            .ok_or_else(|| protocol("Codex stdout 不可用"))?;
        let (tx, messages) = mpsc::sync_channel(256);
        thread::spawn(move || {
            let mut reader = BufReader::new(output);
            loop {
                let mut line = Vec::new();
                let read = (&mut reader)
                    .take(8 * 1024 * 1024 + 1)
                    .read_until(b'\n', &mut line);
                match read {
                    Ok(0) => break,
                    Ok(_) if line.len() > 8 * 1024 * 1024 => {
                        let _ = tx.send(Err("Codex 消息超过 8 MiB 限制".into()));
                        break;
                    }
                    Ok(_) => {
                        let parsed = serde_json::from_slice(&line)
                            .map_err(|e| format!("Codex 返回无效 JSONL：{e}"));
                        if tx.send(parsed).is_err() {
                            break;
                        }
                    }
                    Err(e) => {
                        let _ = tx.send(Err(format!("读取 Codex 输出失败：{e}")));
                        break;
                    }
                }
            }
        });
        if let Some(stderr) = child.stderr.take() {
            thread::spawn(move || {
                let _ = std::io::copy(&mut BufReader::new(stderr), &mut std::io::sink());
            });
        }
        Ok(Self {
            child,
            tree,
            input,
            messages,
            next_id: 1,
            thread_id: None,
            turn_id: None,
            completed: None,
        })
    }
    fn send(&mut self, value: Value) -> Result<()> {
        serde_json::to_writer(&mut self.input, &value)
            .map_err(|e| protocol("Codex 消息编码失败").context("detail", e))?;
        self.input.write_all(b"\n")?;
        self.input.flush()?;
        Ok(())
    }
    fn send_request(&mut self, method: &str, params: Value) -> Result<u64> {
        let id = self.next_id;
        self.next_id += 1;
        self.send(json!({"id":id,"method":method,"params":params}))?;
        Ok(id)
    }
    fn receive(&mut self, cancel: &Cancellation, deadline: Option<Instant>) -> Result<Value> {
        loop {
            if cancel.is_cancelled() {
                self.interrupt();
                return cancel.check().map(|_| Value::Null);
            }
            if deadline.is_some_and(|d| Instant::now() > d) {
                return Err(protocol(
                    "Codex app-server 握手或请求超时，请检查本机 CLI 兼容性",
                ));
            }
            match self.messages.recv_timeout(Duration::from_millis(100)) {
                Ok(Ok(value)) => return Ok(value),
                Ok(Err(error)) => return Err(protocol(error)),
                Err(RecvTimeoutError::Timeout) => {
                    if let Some(status) = self.child.try_wait()? {
                        return Err(protocol("Codex app-server 提前退出").context("status", status));
                    }
                }
                Err(RecvTimeoutError::Disconnected) => {
                    return Err(protocol(
                        "Codex app-server 已关闭输出，运行尚未收到完成事件",
                    ));
                }
            }
        }
    }
    fn dispatch(
        &mut self,
        message: Value,
        event: &mut impl FnMut(&str, Value) -> Result<()>,
        input: &mut impl FnMut(&str, Value) -> Result<Value>,
    ) -> Result<()> {
        if let Some(method) = message.get("method").and_then(Value::as_str) {
            let params = message.get("params").cloned().unwrap_or(Value::Null);
            if method == "turn/completed" {
                self.completed = Some(params.clone());
            }
            if let Some(id) = message.get("id") {
                if matches!(
                    method,
                    "item/commandExecution/requestApproval"
                        | "item/fileChange/requestApproval"
                        | "item/permissions/requestApproval"
                        | "item/tool/requestUserInput"
                        | "tool/requestUserInput"
                        | "mcpServer/elicitation/request"
                        | "execCommandApproval"
                        | "applyPatchApproval"
                ) {
                    let key = serde_json::to_string(id).map_err(|e| protocol(e.to_string()))?;
                    let answer = input(
                        &key,
                        json!({"protocolMethod":method,"params":params,"protocolRequestId":id}),
                    )?;
                    self.send(json!({"id":id,"result":answer}))?;
                } else {
                    self.send(json!({"id":id,"error":{"code":-32601,"message":format!("YSS Workbench 不支持服务端请求 {method}")}}))?;
                    event(
                        "unsupportedRequest",
                        json!({"method":method,"params":params}),
                    )?;
                }
            } else {
                event(method, params)?;
            }
        }
        Ok(())
    }
    fn request(
        &mut self,
        method: &str,
        params: Value,
        cancel: &Cancellation,
        event: &mut impl FnMut(&str, Value) -> Result<()>,
        input: &mut impl FnMut(&str, Value) -> Result<Value>,
    ) -> Result<Value> {
        let id = self.send_request(method, params)?;
        let deadline = Some(Instant::now() + Duration::from_secs(30));
        loop {
            let value = self.receive(cancel, deadline)?;
            if value.get("method").is_none() && value.get("id") == Some(&json!(id)) {
                if let Some(error) = value.get("error") {
                    return Err(
                        protocol(format!("Codex {method} 请求失败")).context("detail", error)
                    );
                }
                return value
                    .get("result")
                    .cloned()
                    .ok_or_else(|| protocol("Codex 响应缺少 result"));
            }
            self.dispatch(value, event, input)?;
        }
    }
    pub fn run(
        &mut self,
        request: RunRequest<'_>,
        cancel: &Cancellation,
        mut event: impl FnMut(&str, Value) -> Result<()>,
        mut input: impl FnMut(&str, Value) -> Result<Value>,
    ) -> Result<Value> {
        let RunRequest {
            cwd,
            roots,
            prompt,
            resume,
        } = request;
        self.request("initialize",json!({"clientInfo":{"name":"yss_workbench","title":"YSS Workbench","version":env!("CARGO_PKG_VERSION")}}),cancel,&mut event,&mut input)?;
        self.send(json!({"method":"initialized"}))?;
        let mut params = json!({"cwd":cwd,"sandbox":"workspace-write"});
        let method = if let Some(id) = resume {
            params["threadId"] = json!(id);
            "thread/resume"
        } else {
            "thread/start"
        };
        let settings = self.request(method, params, cancel, &mut event, &mut input)?;
        let thread_id = settings
            .pointer("/thread/id")
            .and_then(Value::as_str)
            .ok_or_else(|| protocol("Codex thread 响应缺少 thread.id，宿主协议不兼容"))?
            .to_owned();
        self.thread_id = Some(thread_id.clone());
        event("hostThread", settings.clone())?;
        let mut turn =
            json!({"threadId":thread_id,"cwd":cwd,"input":[{"type":"text","text":prompt}]});
        if let Some(sandbox) = settings.get("sandbox") {
            let mut sandbox = sandbox.clone();
            if sandbox.get("type").and_then(Value::as_str) == Some("workspaceWrite") {
                sandbox["writableRoots"] = json!(roots);
            }
            turn["sandboxPolicy"] = sandbox;
        }
        self.completed = None;
        let started = self.request("turn/start", turn, cancel, &mut event, &mut input)?;
        self.turn_id = started
            .pointer("/turn/id")
            .and_then(Value::as_str)
            .map(str::to_owned);
        if self.turn_id.is_none() {
            return Err(protocol("Codex turn 响应缺少 turn.id，宿主协议不兼容"));
        }
        event("hostTurn", started)?;
        loop {
            if let Some(completed) = self.completed.take()
                && completed.get("threadId").and_then(Value::as_str) == self.thread_id.as_deref()
                && completed.pointer("/turn/id").and_then(Value::as_str) == self.turn_id.as_deref()
            {
                self.turn_id = None;
                return Ok(completed);
            }
            let value = self.receive(cancel, None)?;
            self.dispatch(value, &mut event, &mut input)?;
        }
    }

    fn interrupt(&mut self) {
        if let (Some(thread_id), Some(turn_id)) = (self.thread_id.clone(), self.turn_id.clone()) {
            let _ = self.send_request(
                "turn/interrupt",
                json!({"threadId":thread_id,"turnId":turn_id}),
            );
        }
    }
}
impl Drop for Session {
    fn drop(&mut self) {
        let active = self.turn_id.is_some();
        self.interrupt();
        if active {
            let deadline = Instant::now() + Duration::from_secs(2);
            while Instant::now() < deadline {
                match self.messages.recv_timeout(Duration::from_millis(100)) {
                    Ok(Ok(value))
                        if value.get("method").and_then(Value::as_str)
                            == Some("turn/completed") =>
                    {
                        break;
                    }
                    Err(RecvTimeoutError::Disconnected) => break,
                    _ => {}
                }
            }
        }
        super::process::stop(&mut self.child, &mut self.tree);
    }
}
