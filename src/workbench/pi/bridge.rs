use super::{
    sandbox::Sandbox,
    workspace::{Location as ProjectLocation, Projects},
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::unique_id,
    model::Sha256,
    workbench::jobs::JobContext,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeSet,
    io::{BufRead, BufReader, Read},
    os::unix::net::{UnixListener, UnixStream},
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

const TOOLS: &[&str] = &["read", "ls", "grep", "ask_spec_question", "update_plan"];
const MAX_REQUEST: u64 = 4 * 1024 * 1024;
const COMMAND_OUTPUT_LIMIT: usize = 256 * 1024;

fn append_output(output: &mut String, text: &str) {
    if output.len() > COMMAND_OUTPUT_LIMIT {
        return;
    }
    let take = text.floor_char_boundary(text.len().min(COMMAND_OUTPUT_LIMIT - output.len()));
    output.push_str(&text[..take]);
    if take < text.len() {
        output.push_str("\n[达到命令输出长度上限]");
    }
}

fn complete_utf8_prefix(bytes: &[u8]) -> usize {
    (bytes.len().saturating_sub(3)..bytes.len())
        .find(|&start| {
            std::str::from_utf8(&bytes[start..])
                .is_err_and(|error| error.valid_up_to() == 0 && error.error_len().is_none())
        })
        .unwrap_or(bytes.len())
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Location {
    #[serde(default)]
    path: String,
    project_id: Option<String>,
}

#[derive(Deserialize)]
struct Request {
    token: String,
    id: Option<String>,
    #[serde(flatten)]
    operation: Operation,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct PlanStep {
    title: String,
    status: PlanStatus,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
enum PlanStatus {
    Pending,
    InProgress,
    Completed,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
enum CompactionStatus {
    InProgress,
    Completed,
    Failed,
}

#[derive(Deserialize)]
#[serde(
    tag = "operation",
    rename_all = "snake_case",
    rename_all_fields = "camelCase"
)]
enum Operation {
    Ready {
        tools: Vec<String>,
    },
    Read {
        #[serde(flatten)]
        location: Location,
        offset: Option<usize>,
        limit: Option<usize>,
    },
    Ls {
        #[serde(flatten)]
        location: Location,
    },
    Grep {
        #[serde(flatten)]
        location: Location,
        query: String,
    },
    Write {
        #[serde(flatten)]
        location: Location,
        content: String,
    },
    Edit {
        #[serde(flatten)]
        location: Location,
        old_text: String,
        new_text: String,
    },
    Bash {
        #[serde(flatten)]
        location: Location,
        command: String,
    },
    AskSpecQuestion {
        question_id: String,
    },
    UpdatePlan {
        steps: Vec<PlanStep>,
    },
    ReportContext {
        used_tokens: Option<u64>,
        context_window: u64,
    },
    ReportCompaction {
        sequence: u32,
        status: CompactionStatus,
    },
}

pub(super) struct Bridge {
    path: PathBuf,
    token: String,
    ready: Arc<AtomicBool>,
    cancel: Cancellation,
    worker: Option<JoinHandle<()>>,
}

impl Bridge {
    pub fn start(
        private: &Path,
        context: &JobContext,
        projects: Projects,
        sandbox: Option<Sandbox>,
        read_only: bool,
        ask: impl Fn(&JobContext, &str) -> Result<String> + Send + 'static,
    ) -> Result<Self> {
        let path = private.join("tools.sock");
        let listener = UnixListener::bind(&path)?;
        listener.set_nonblocking(true)?;
        let token = unique_id()?;
        let ready = Arc::new(AtomicBool::new(false));
        let cancel = context.cancel.child();
        let mut context = context.clone();
        context.cancel = cancel.clone();
        let mut broker = Broker {
            context,
            projects,
            sandbox,
            read_only,
            token: token.clone(),
            ready: ready.clone(),
        };
        let worker = thread::Builder::new()
            .name("pi-tools".into())
            .spawn(move || {
                while !broker.context.cancel.is_cancelled() {
                    match listener.accept() {
                        Ok((stream, _)) => broker.serve(stream, &ask),
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                            thread::sleep(Duration::from_millis(20));
                        }
                        Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                        Err(_) => break,
                    }
                }
            })?;
        Ok(Self {
            path,
            token,
            ready,
            cancel,
            worker: Some(worker),
        })
    }

    pub fn configure(&self, command: &mut std::process::Command, allow_commands: bool) {
        command
            .env("YSS_PI_SOCKET", &self.path)
            .env("YSS_PI_SOCKET_TOKEN", &self.token)
            .env(
                "YSS_PI_ALLOW_COMMANDS",
                if allow_commands { "1" } else { "0" },
            );
    }

    pub fn await_ready(&self) -> Result<()> {
        let deadline = Instant::now() + Duration::from_secs(3);
        while !self.ready.load(Ordering::Acquire) {
            self.cancel.check()?;
            if Instant::now() >= deadline {
                return Err(YssError::new(
                    ErrorCode::NotReady,
                    "Pi 未加载工作台的受控工具",
                ));
            }
            thread::sleep(Duration::from_millis(20));
        }
        Ok(())
    }
}

impl Drop for Bridge {
    fn drop(&mut self) {
        self.cancel.cancel();
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

struct Broker {
    context: JobContext,
    projects: Projects,
    sandbox: Option<Sandbox>,
    read_only: bool,
    token: String,
    ready: Arc<AtomicBool>,
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}

fn failure_fields(error: &YssError) -> (&str, &'static str) {
    let code = error
        .context
        .get("piErrorCode")
        .map(String::as_str)
        .unwrap_or_else(|| match error.code {
            ErrorCode::UnsafePath => "protected_path",
            ErrorCode::ConcurrentChange => "concurrent_change",
            ErrorCode::InvalidInput | ErrorCode::Argument => "invalid_input",
            ErrorCode::Io
                if error
                    .context
                    .get("kind")
                    .is_some_and(|kind| kind == "NotFound") =>
            {
                "missing_path"
            }
            _ => "operation_failed",
        });
    let hint = match code {
        "missing_path" => "核对工程内的路径和当前材料状态，可以改查可读源码。",
        "path_outside_scope" => {
            "用已选工程的 projectId 和工程内相对路径。如需其他工程，从 Spec 发起新会话并选择它。"
        }
        "protected_path" => "选择本轮允许访问的普通工程文件或目录。",
        "path_not_reviewable" => {
            "可读取该文件。需要更新产物时，请修改源文件并通过授权的离线构建生成。"
        }
        "permission_denied" => "仍可继续只读核对。需要编辑或运行命令时，请核对下一轮的运行设置。",
        "invalid_path" | "invalid_input" => "检查参数格式，文件位置使用工程内相对路径。",
        "concurrent_change" => {
            "重新读取当前文件。若 Git 分支、HEAD 或暂存区已改变，请结束本轮并重新发起。"
        }
        _ => "查看本次工具错误，其他核对可以继续。",
    };
    (code, hint)
}

fn read_request(stream: &mut UnixStream) -> Result<Request> {
    let mut line = Vec::new();
    let read = BufReader::new(stream)
        .take(MAX_REQUEST + 1)
        .read_until(b'\n', &mut line)? as u64;
    if read == 0 || read > MAX_REQUEST || line.last() != Some(&b'\n') {
        return Err(invalid("Pi 工具请求不完整或超过长度限制"));
    }
    serde_json::from_slice(&line).map_err(|_| invalid("Pi 工具请求格式无效"))
}

impl Broker {
    fn serve(
        &mut self,
        mut stream: UnixStream,
        ask: &impl Fn(&JobContext, &str) -> Result<String>,
    ) {
        // BSD can inherit nonblocking mode from the listener and bypass the read timeout.
        if stream.set_nonblocking(false).is_err()
            || stream
                .set_read_timeout(Some(Duration::from_secs(1)))
                .is_err()
            || stream
                .set_write_timeout(Some(Duration::from_secs(1)))
                .is_err()
        {
            return;
        }
        let value = read_request(&mut stream)
            .and_then(|request| self.execute(request, ask))
            .unwrap_or_else(|error| {
                let (code, hint) = failure_fields(&error);
                json!({ "error": error.message, "errorCode": code,
                    "displayPath": error.context.get("piDisplayPath"), "hint": hint })
            });
        let _ = serde_json::to_writer(&mut stream, &value);
    }

    fn execute(
        &self,
        request: Request,
        ask: &impl Fn(&JobContext, &str) -> Result<String>,
    ) -> Result<Value> {
        self.context.cancel.check()?;
        if request.token != self.token {
            return Err(invalid("Pi 工具连接无效"));
        }
        if let Operation::Ready { tools } = &request.operation {
            let mut expected: BTreeSet<&str> = TOOLS.iter().copied().collect();
            if !self.read_only {
                expected.extend(["write", "edit"]);
            }
            if self.sandbox.is_some() {
                expected.insert("bash");
            }
            if tools.len() != expected.len()
                || tools.iter().map(String::as_str).collect::<BTreeSet<_>>() != expected
            {
                return Err(invalid("Pi 启用的工具超出工作台授权"));
            }
            self.ready.store(true, Ordering::Release);
            return Ok(json!({ "text": "工作台工具已就绪" }));
        }
        if !self.ready.load(Ordering::Acquire) {
            return Err(invalid("Pi 工具尚未就绪"));
        }
        match &request.operation {
            Operation::ReportContext {
                used_tokens,
                context_window,
            } => {
                if *context_window == 0
                    || *context_window > 1_000_000_000
                    || used_tokens.is_some_and(|used| used > 1_000_000_000)
                {
                    return Err(invalid("Pi 上下文用量无效"));
                }
                self.context.emit(
                    "piContext",
                    json!({"usedTokens":used_tokens,"contextWindow":context_window}),
                )?;
                return Ok(json!({"text":"已更新上下文用量"}));
            }
            Operation::ReportCompaction { sequence, status } => {
                let kind = if matches!(status, CompactionStatus::InProgress) {
                    "item/started"
                } else {
                    "item/completed"
                };
                self.context.emit(
                    kind,
                    json!({"item":{"id":format!("pi-compact-{sequence}"),
                    "type":"piCompaction","operation":"compact","status":status}}),
                )?;
                return Ok(json!({"text":"已更新上下文压缩状态"}));
            }
            _ => {}
        }
        let id = request
            .id
            .as_deref()
            .filter(|id| !id.is_empty() && id.len() <= 256)
            .ok_or_else(|| invalid("Pi 工具调用缺少有效编号"))?;
        let cancel = &self.context.cancel;
        let result = match request.operation {
            Operation::Ready { .. }
            | Operation::ReportContext { .. }
            | Operation::ReportCompaction { .. } => unreachable!(),
            Operation::UpdatePlan { mut steps } => {
                for step in &mut steps {
                    step.title = step.title.trim().into();
                }
                if steps.is_empty()
                    || steps.len() > 20
                    || steps.iter().any(|step| {
                        step.title.is_empty()
                            || step.title.len() > 1024
                            || step.title.chars().any(char::is_control)
                    })
                {
                    return Err(invalid("请提供 1 至 20 个单行计划步骤"));
                }
                self.context.emit("piPlan", json!({"steps":steps}))?;
                "已更新本轮执行计划".into()
            }
            Operation::Read {
                location,
                offset,
                limit,
            } => self.file_call(id, "read", "读取", &location, |target| {
                Ok((target.read(offset, limit)?, None))
            })?,
            Operation::Ls { location } => {
                self.file_call(id, "ls", "列出", &location, |target| {
                    Ok((target.list(cancel)?, None))
                })?
            }
            Operation::Grep { location, query } => {
                self.file_call(id, "grep", "搜索", &location, |target| {
                    Ok((target.grep(&query, cancel)?, None))
                })?
            }
            Operation::Write { location, content } => {
                self.file_call(id, "write", "写入", &location, |target| {
                    self.require_edit()?;
                    let change = target.write(content, cancel)?;
                    Ok(("已写入原工程文件".into(), Some(change)))
                })?
            }
            Operation::Edit {
                location,
                old_text,
                new_text,
            } => self.file_call(id, "edit", "编辑", &location, |target| {
                self.require_edit()?;
                let change = target.edit(&old_text, &new_text, cancel)?;
                Ok(("已编辑原工程文件".into(), Some(change)))
            })?,
            Operation::Bash { location, command } => {
                let target = self.prepare_command(&location, &command).map_err(|error| {
                    self.record_failure(id, "bash", "执行离线命令", &location, None, error)
                })?;
                let cwd = target.directory().map_err(|error| {
                    self.record_failure(id, "bash", "执行离线命令", &location, None, error)
                })?;
                return self.run_command(id, &cwd, &command);
            }
            Operation::AskSpecQuestion { question_id } => ask(&self.context, &question_id)?,
        };
        Ok(json!({ "text": result }))
    }

    fn file_call(
        &self,
        id: &str,
        operation: &str,
        label: &str,
        location: &Location,
        call: impl FnOnce(&ProjectLocation<'_>) -> Result<(String, Option<(&'static str, Sha256)>)>,
    ) -> Result<String> {
        let mut path = None;
        let mut review = None;
        let result = self
            .projects
            .locate(location.project_id.as_deref(), &location.path)
            .and_then(|target| {
                path = Some(target.display());
                review = self.projects.review_location(&target);
                call(&target)
            });
        let message = path.as_ref().map_or_else(
            || label.into(),
            |path| format!("{label} {}", path.display()),
        );
        match result {
            Ok((text, change)) => {
                if let Some((action, digest)) = change {
                    self.context.emit(
                        "item/completed",
                        json!({
                            "item": { "id": id, "type": "fileChange", "operation": operation,
                                "projectId": review.as_ref().map(|(id, _)| id),
                                "reviewPath": review.as_ref().map(|(_, path)| path),
                                "afterDigest": digest,
                                "changes": [{ "path": path, "kind": action }] }
                        }),
                    )?;
                } else {
                    self.context.emit("piTool", json!({
                        "id": id, "operation": operation, "path": path, "status": "completed", "message": message,
                        "displayPath": self.projects.error_display_path(location.project_id.as_deref(), &location.path),
                        "projectId": review.as_ref().map(|(id, _)| id), "reviewPath": review.as_ref().map(|(_, path)| path)
                    }))?;
                }
                Ok(text)
            }
            Err(error) => Err(self.record_failure(id, operation, label, location, review, error)),
        }
    }

    fn record_failure(
        &self,
        id: &str,
        operation: &str,
        label: &str,
        location: &Location,
        review: Option<(String, String)>,
        error: YssError,
    ) -> YssError {
        let display_path = self
            .projects
            .error_display_path(location.project_id.as_deref(), &location.path);
        let (code, hint) = failure_fields(&error);
        let _ = self.context.emit("piTool", json!({
            "id": id, "operation": operation, "path": null, "status": "failed", "message": label,
            "error": error.message, "errorCode": code, "displayPath": display_path, "hint": hint,
            "projectId": review.as_ref().map(|(id, _)| id), "reviewPath": review.as_ref().map(|(_, path)| path)
        }));
        error.context("piDisplayPath", display_path)
    }

    fn require_edit(&self) -> Result<()> {
        if self.read_only {
            return Err(invalid("只读模式未授权编辑文件或执行命令")
                .context("piErrorCode", "permission_denied"));
        }
        Ok(())
    }

    fn prepare_command(&self, location: &Location, command: &str) -> Result<ProjectLocation<'_>> {
        self.require_edit()?;
        if self.sandbox.is_none() {
            return Err(invalid("本轮未授权离线命令").context("piErrorCode", "permission_denied"));
        }
        if command.trim().is_empty() || command.len() > 64 * 1024 || command.contains('\0') {
            return Err(invalid("命令须为 64 KiB 以内的有效文本"));
        }
        let target = self
            .projects
            .locate(location.project_id.as_deref(), &location.path)?;
        self.projects.check_baselines(&self.context.cancel)?;
        Ok(target)
    }

    fn run_command(&self, id: &str, cwd: &Path, command: &str) -> Result<Value> {
        let sandbox = self
            .sandbox
            .as_ref()
            .ok_or_else(|| invalid("本轮未授权离线命令"))?;
        let base = json!({ "id": id, "type": "commandExecution", "command": command, "cwd": cwd });
        self.context
            .emit("item/started", json!({ "item": base.clone() }))?;
        let mut item = base;
        let mut output = String::new();
        let mut fragments = [Vec::new(), Vec::new()];
        let mut published = 0;
        let mut last_publish: Option<Instant> = None;
        let result = sandbox.run(cwd, command, &self.context.cancel, |chunk| {
            if let Some((stdout, bytes)) = chunk {
                let pending = &mut fragments[usize::from(!stdout)];
                pending.extend_from_slice(bytes);
                let prefix = complete_utf8_prefix(pending);
                append_output(&mut output, &String::from_utf8_lossy(&pending[..prefix]));
                pending.drain(..prefix);
            }
            if output.len() > published
                && last_publish.is_none_or(|at| at.elapsed() >= Duration::from_millis(100))
            {
                self.context.emit(
                    "item/commandExecution/outputDelta",
                    json!({"itemId":id,"delta":&output[published..]}),
                )?;
                published = output.len();
                last_publish = Some(Instant::now());
            }
            Ok(())
        });
        let result = result.and_then(|result| {
            self.projects.check_baselines(&self.context.cancel)?;
            Ok(result)
        });
        for fragment in fragments {
            append_output(&mut output, &String::from_utf8_lossy(&fragment));
        }
        let code = match result {
            Ok((status, _, _)) => status.code().unwrap_or(-1),
            Err(error) => {
                item["status"] = json!(if error.code == ErrorCode::Cancelled {
                    "canceled"
                } else {
                    "failed"
                });
                if !output.is_empty() && !output.ends_with('\n') {
                    output.push('\n');
                }
                output.push_str(&error.message);
                item["aggregatedOutput"] = json!(output);
                self.context
                    .emit("item/completed", json!({ "item": item }))?;
                return Err(error);
            }
        };
        item["exitCode"] = json!(code);
        item["status"] = json!(if code == 0 { "completed" } else { "failed" });
        item["aggregatedOutput"] = json!(output);
        self.context
            .emit("item/completed", json!({ "item": item }))?;
        Ok(json!({ "text": format!("退出码 {code}\n{output}"), "exitCode": code }))
    }
}
