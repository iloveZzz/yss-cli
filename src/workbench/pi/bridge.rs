#[cfg(windows)]
use super::super::windows_pipe::Pipe;
use super::{
    evidence::Evidence,
    sandbox::Sandbox,
    workspace::{Location as ProjectLocation, Projects},
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::unique_id,
    model::Sha256,
    workbench::{
        agent::{CommandExecution, ProjectMode},
        jobs::JobContext,
    },
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
#[cfg(unix)]
use std::io::{BufRead, BufReader, Read};
#[cfg(unix)]
use std::os::unix::{
    net::{UnixListener, UnixStream},
    process::ExitStatusExt,
};
use std::{
    collections::BTreeSet,
    path::{Path, PathBuf},
    process::ExitStatus,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

const READ_TOOLS: &[&str] = &["read", "ls", "grep"];
#[cfg(unix)]
const MAX_REQUEST: u64 = 4 * 1024 * 1024;
const COMMAND_OUTPUT_LIMIT: usize = 256 * 1024;
#[cfg(unix)]
const COMMAND_STDERR_LIMIT: usize = 64 * 1024;

#[derive(Clone, Copy, PartialEq, Eq)]
pub(super) enum Access {
    ReadWrite,
    ReadOnly,
    NativeFork,
}

pub(super) enum Commands {
    Disabled,
    Sandbox(Box<Sandbox>),
    #[cfg(unix)]
    Host,
}

impl Commands {
    fn execution(&self) -> Option<CommandExecution> {
        match self {
            Self::Disabled => None,
            Self::Sandbox(_) => Some(CommandExecution::Sandbox),
            #[cfg(unix)]
            Self::Host => Some(CommandExecution::Host),
        }
    }
}

fn execution_name(execution: CommandExecution) -> &'static str {
    match execution {
        CommandExecution::Host => "host",
        CommandExecution::Sandbox => "sandbox",
    }
}

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

#[derive(Serialize)]
struct CommandDiagnostic {
    code: &'static str,
    message: &'static str,
    hint: &'static str,
}

fn dyld_message(line: &str) -> Option<&str> {
    let rest = line.strip_prefix("dyld")?;
    if let Some(message) = rest.strip_prefix(':') {
        return Some(message.trim_start());
    }
    let (pid, message) = rest.strip_prefix('[')?.split_once("]:")?;
    (!pid.is_empty() && pid.bytes().all(|byte| byte.is_ascii_digit())).then(|| message.trim_start())
}

fn dyld_library_sandbox_denied(stderr: &str) -> bool {
    let mut loading_library = false;
    for line in stderr.lines() {
        let content = line.trim_start();
        if let Some(message) = dyld_message(content) {
            loading_library = message.starts_with("Library not loaded:");
        } else if content.is_empty()
            || (line == content
                && !content.starts_with("Referenced from:")
                && !content.starts_with("Reason:"))
        {
            loading_library = false;
        }
        if loading_library && content.contains("blocked by sandbox") {
            return true;
        }
    }
    false
}

fn bash_command_not_found(line: &str) -> bool {
    let Some((source, command)) = line
        .strip_suffix(": command not found")
        .and_then(|line| line.split_once(": "))
    else {
        return false;
    };
    if matches!(source.rsplit('/').next(), Some("bash" | "-bash")) {
        return true;
    }
    command
        .strip_prefix("line ")
        .and_then(|line| line.split_once(": "))
        .is_some_and(|(number, _)| {
            !source.is_empty()
                && !number.is_empty()
                && number.bytes().all(|byte| byte.is_ascii_digit())
        })
}

#[cfg(target_os = "macos")]
fn apple_developer_tools_unavailable(line: &str) -> bool {
    line.starts_with(
        "xcode-select: error: No developer tools were found and no install could be requested",
    ) || line
        .strip_prefix("xcrun: error: missing DEVELOPER_DIR path: ")
        .is_some_and(|path| !path.is_empty())
}

#[cfg(target_os = "macos")]
fn git_metadata_access_denied(line: &str) -> bool {
    let Some(details) = line.strip_prefix("fatal: error reading ") else {
        return false;
    };
    if let Some(quoted) = details.strip_prefix('\'') {
        let Some((path, suffix)) = quoted.rsplit_once('\'') else {
            return false;
        };
        // Apple Git can name the denied .git directory without including errno text.
        if suffix.is_empty() {
            return path == ".git" || path.ends_with("/.git");
        }
        return matches!(suffix, ": Operation not permitted" | ": Permission denied")
            && path.split('/').any(|part| part == ".git");
    }
    details.rsplit_once(": ").is_some_and(|(path, reason)| {
        matches!(reason, "Operation not permitted" | "Permission denied")
            && path.split('/').any(|part| part == ".git")
    })
}

#[cfg(target_os = "macos")]
fn pnpm_wrapper_permission_denied(line: &str) -> bool {
    let Some((prefix, reason)) = line.rsplit_once(": cannot execute: ") else {
        return false;
    };
    let path = prefix.rsplit_once(": ").map_or(prefix, |(_, path)| path);
    matches!(reason, "Operation not permitted" | "Permission denied")
        && Path::new(path)
            .file_name()
            .is_some_and(|name| name == "pnpm")
}

fn command_diagnostics(stderr: &str, execution: CommandExecution) -> Vec<CommandDiagnostic> {
    let mut diagnostics = Vec::new();
    if execution == CommandExecution::Sandbox && dyld_library_sandbox_denied(stderr) {
        diagnostics.push(CommandDiagnostic {
            code: "dyld_library_sandbox_denied",
            message: "动态库加载被沙箱阻止。",
            hint: "优先使用本轮已有的工程和版本信息。需要验证这个工具时，请用户在自己的环境中运行。",
        });
    }
    if stderr.lines().any(bash_command_not_found) {
        diagnostics.push(CommandDiagnostic {
            code: "command_not_found",
            message: "Bash 未找到要执行的命令。",
            hint: if execution == CommandExecution::Host {
                "核对本机 PATH、工具安装和工程约定的命令。"
            } else {
                "核对工程约定的命令和离线环境可用的工具，按需单独验证。"
            },
        });
    }
    #[cfg(target_os = "macos")]
    if execution == CommandExecution::Sandbox {
        if stderr.lines().any(apple_developer_tools_unavailable) {
            diagnostics.push(CommandDiagnostic {
                code: "apple_developer_tools_unavailable",
                message: "macOS 系统工具启动器在离线环境中找不到开发工具。",
                hint: "Git 分支和 HEAD 使用工作台本轮已核验的信息，文件变化查看「执行成果」。构建或测试可使用工程已准备的离线入口。",
            });
        }
        if stderr.lines().any(git_metadata_access_denied) {
            diagnostics.push(CommandDiagnostic {
                code: "git_metadata_sandbox_denied",
                message: "离线沙箱保护 Git 仓库元数据。",
                hint: "本轮 Git 分支和 HEAD 由工作台核验，文件变化可在「执行成果」中查看。",
            });
        }
        if stderr.lines().any(pnpm_wrapper_permission_denied) {
            diagnostics.push(CommandDiagnostic {
                code: "pnpm_wrapper_permission_denied",
                message: "离线环境无法执行当前的 pnpm 启动入口。",
                hint: "核对命令工作目录与 package.json 声明的 pnpm 版本，使用该工程已准备的离线入口。前端脚本也可在本轮结束后通过「本机测试与构建」运行。",
            });
        }
        if stderr
            .lines()
            .any(|line| line == "yss-pi-pnpm: PNPM_INTEGRITY_MISMATCH")
        {
            diagnostics.push(CommandDiagnostic {
                code: "pnpm_offline_integrity_mismatch",
                message: "无法从离线缓存记录核对工程声明的 pnpm 校验值。",
                hint: "在本机按 package.json 声明的精确版本和校验值准备 Corepack 缓存后重试。",
            });
        }
        if stderr
            .lines()
            .any(|line| line == "yss-pi-pnpm: PNPM_PIN_UNAVAILABLE")
        {
            diagnostics.push(CommandDiagnostic {
                code: "pnpm_offline_runtime_unavailable",
                message: "工程锁定的 pnpm 在离线环境中无法启动。",
                hint: "检查本机 Node 和对应版本的 Corepack 缓存后重试。",
            });
        }
    }
    diagnostics
}

fn command_result(
    item: &mut Value,
    status: ExitStatus,
    stderr: &[u8],
    output: &str,
    execution: CommandExecution,
) -> Value {
    let code = status.code().unwrap_or(-1);
    item["exitCode"] = json!(code);
    item["status"] = json!(if code == 0 { "completed" } else { "failed" });
    item["aggregatedOutput"] = json!(output);
    let mut reply = json!({ "exitCode": code, "commandExecution": execution });
    #[cfg(unix)]
    let signal = status.signal();
    #[cfg(windows)]
    let signal: Option<i32> = None;
    let mut text = if let Some(number) = signal {
        #[cfg(unix)]
        let name = match number {
            libc::SIGKILL => Some("SIGKILL"),
            libc::SIGABRT => Some("SIGABRT"),
            libc::SIGTERM => Some("SIGTERM"),
            _ => None,
        };
        #[cfg(windows)]
        let name: Option<&str> = None;
        let mut signal = json!({ "number": number });
        let message = if let Some(name) = name {
            signal["name"] = json!(name);
            format!("收到 {name}（{number}）")
        } else {
            format!("收到信号 {number}")
        };
        item["terminationSignal"] = signal.clone();
        reply["terminationSignal"] = signal;
        message
    } else {
        format!("退出码 {code}")
    };
    if !status.success() {
        let diagnostics = command_diagnostics(&String::from_utf8_lossy(stderr), execution);
        if !diagnostics.is_empty() {
            for diagnostic in &diagnostics {
                text.push('\n');
                text.push_str(diagnostic.message);
                text.push('\n');
                text.push_str(diagnostic.hint);
            }
            let diagnostics = json!(diagnostics);
            item["commandDiagnostics"] = diagnostics.clone();
            reply["commandDiagnostics"] = diagnostics;
        }
    }
    text.push('\n');
    text.push_str(output);
    reply["text"] = json!(text);
    reply
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
    execution: Option<CommandExecution>,
    command_failure: Arc<Mutex<Option<YssError>>>,
}

struct ToolReply {
    body: Value,
    ready: bool,
}

impl Bridge {
    pub fn start(
        private: &Path,
        context: &JobContext,
        projects: Projects,
        commands: Commands,
        access: Access,
        ask: impl Fn(&JobContext, &str) -> Result<String> + Send + 'static,
        evidence: Evidence,
    ) -> Result<Self> {
        let command_failure = Arc::new(Mutex::new(None));
        let execution = commands.execution();
        Self::start_transport(
            private,
            &context.cancel,
            execution,
            command_failure.clone(),
            |token, ready, cancel| {
                let mut context = context.clone();
                context.cancel = cancel;
                let broker = Broker {
                    context,
                    projects,
                    commands,
                    access,
                    evidence,
                    token,
                    ready,
                    command_failure,
                };
                move |request| broker.response(request, &ask)
            },
        )
    }

    pub fn start_probe(private: &Path, cancel: &Cancellation) -> Result<Self> {
        Self::start_transport(
            private,
            cancel,
            None,
            Arc::new(Mutex::new(None)),
            |token, _, _| {
                move |request: Result<Request>| {
                    let result = request.and_then(|request| match request.operation {
                        Operation::Ready { tools } if request.token == token => {
                            ready_response(&tools, Access::ReadOnly, false)
                        }
                        _ => Err(invalid("Pi 工具自检请求无效")),
                    });
                    match result {
                        Ok(body) => ToolReply { body, ready: true },
                        Err(error) => ToolReply {
                            body: json!({"error": error.message}),
                            ready: false,
                        },
                    }
                }
            },
        )
    }

    fn start_transport<F>(
        private: &Path,
        parent_cancel: &Cancellation,
        execution: Option<CommandExecution>,
        failure: Arc<Mutex<Option<YssError>>>,
        response: impl FnOnce(String, Arc<AtomicBool>, Cancellation) -> F,
    ) -> Result<Self>
    where
        F: Fn(Result<Request>) -> ToolReply + Send + 'static,
    {
        #[cfg(unix)]
        let path = private.join("tools.sock");
        #[cfg(unix)]
        let listener = UnixListener::bind(&path)?;
        #[cfg(unix)]
        listener.set_nonblocking(true)?;
        #[cfg(windows)]
        let mut listener = Pipe::bind()?;
        #[cfg(windows)]
        let path = {
            let _ = private;
            PathBuf::from(&listener.name)
        };
        let token = unique_id()?;
        let ready = Arc::new(AtomicBool::new(false));
        let cancel = parent_cancel.child();
        let response = response(token.clone(), ready.clone(), cancel.clone());
        let worker_cancel = cancel.clone();
        let worker_ready = ready.clone();
        #[cfg(unix)]
        let worker_failure = failure.clone();
        let worker = thread::Builder::new()
            .name("pi-tools".into())
            .spawn(move || {
                while !worker_cancel.is_cancelled() {
                    #[cfg(unix)]
                    match listener.accept() {
                        Ok((stream, _)) => {
                            if serve_request(stream, &response) {
                                worker_ready.store(true, Ordering::Release);
                            }
                            if command_failure(&worker_failure).is_err() {
                                worker_cancel.cancel();
                            }
                        }
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                            thread::sleep(Duration::from_millis(20));
                        }
                        Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                        Err(_) => break,
                    }
                    #[cfg(windows)]
                    match listener.receive(&worker_cancel) {
                        Ok(Some(bytes)) => {
                            let reply = response(
                                serde_json::from_slice(&bytes)
                                    .map_err(|_| invalid("Pi 工具请求格式无效")),
                            );
                            if let Ok(bytes) = serde_json::to_vec(&reply.body)
                                && listener.reply(&bytes, &worker_cancel).is_ok()
                                && reply.ready
                            {
                                worker_ready.store(true, Ordering::Release);
                            }
                        }
                        Ok(None) => {}
                        Err(_) if worker_cancel.is_cancelled() => break,
                        Err(_) => continue,
                    }
                }
            })?;
        Ok(Self {
            path,
            token,
            ready,
            cancel,
            worker: Some(worker),
            execution,
            command_failure: failure,
        })
    }

    pub fn configure(&self, command: &mut std::process::Command) {
        command
            .env("YSS_PI_SOCKET", &self.path)
            .env("YSS_PI_SOCKET_TOKEN", &self.token)
            .env("YSS_PI_NATIVE_FORK_READ_ONLY", "0")
            .env(
                "YSS_PI_ALLOW_COMMANDS",
                if self.execution.is_some() { "1" } else { "0" },
            )
            .env(
                "YSS_PI_COMMAND_EXECUTION",
                self.execution.map_or("", execution_name),
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

    pub fn prompt_cancellation(&self) -> Cancellation {
        self.cancel.clone()
    }

    pub fn verify_command_failure(&self) -> Result<()> {
        command_failure(&self.command_failure)
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
    commands: Commands,
    access: Access,
    evidence: Evidence,
    token: String,
    ready: Arc<AtomicBool>,
    command_failure: Arc<Mutex<Option<YssError>>>,
}

fn command_failure(cell: &Mutex<Option<YssError>>) -> Result<()> {
    cell.lock()
        .map_err(|_| YssError::new(ErrorCode::RecoveryRequired, "无法核对命令恢复状态"))?
        .clone()
        .map_or(Ok(()), Err)
}

#[cfg(unix)]
fn host_baseline_failure(source: YssError, finished: bool) -> YssError {
    let message = if finished {
        "本机命令结束后 Git 基线已变化或无法核对。本轮已停止，命令结果和原工程变更已保留。"
    } else {
        "命令执行前 Git 基线已变化或无法核对。本轮已停止。"
    };
    YssError::new(ErrorCode::ConcurrentChange, message)
        .context("piErrorCode", "host_baseline_changed")
        .context("piCommandExecution", execution_name(CommandExecution::Host))
        .context("cause", source.message)
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
            ErrorCode::RecoveryRequired => "recovery_required",
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
        "host_baseline_changed" => "请先核对当前 Git 分支、HEAD、暂存区和文件变更，再发起下一轮。",
        "recovery_required" => "命令现场已保留。请先处理工作台的命令恢复，再继续本轮任务。",
        _ => "查看本次工具错误，其他核对可以继续。",
    };
    (code, hint)
}

#[cfg(unix)]
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

#[cfg(unix)]
fn serve_request(mut stream: UnixStream, response: &impl Fn(Result<Request>) -> ToolReply) -> bool {
    // BSD can inherit nonblocking mode from the listener and bypass the read timeout.
    if stream.set_nonblocking(false).is_err()
        || stream
            .set_read_timeout(Some(Duration::from_secs(1)))
            .is_err()
        || stream
            .set_write_timeout(Some(Duration::from_secs(1)))
            .is_err()
    {
        return false;
    }
    let reply = response(read_request(&mut stream));
    serde_json::to_writer(&mut stream, &reply.body).is_ok() && reply.ready
}

fn ready_response(tools: &[String], access: Access, commands: bool) -> Result<Value> {
    let mut expected: BTreeSet<&str> = READ_TOOLS.iter().copied().collect();
    if access != Access::NativeFork {
        expected.extend(["ask_spec_question", "update_plan"]);
        if access == Access::ReadWrite {
            expected.extend(["write", "edit"]);
        }
        if commands {
            expected.insert("bash");
        }
    }
    if tools.len() != expected.len()
        || tools.iter().map(String::as_str).collect::<BTreeSet<_>>() != expected
    {
        return Err(invalid("Pi 启用的工具超出工作台授权"));
    }
    Ok(json!({"text": "工作台工具已就绪"}))
}

impl Broker {
    fn verify_command_failure(&self) -> Result<()> {
        command_failure(&self.command_failure)
    }

    fn stop_commands_after(&self, error: YssError) -> YssError {
        match self.command_failure.lock() {
            Ok(mut stored) => stored.get_or_insert(error).clone(),
            Err(_) => YssError::new(ErrorCode::RecoveryRequired, "无法记录命令恢复状态"),
        }
    }

    fn response(
        &self,
        request: Result<Request>,
        ask: &impl Fn(&JobContext, &str) -> Result<String>,
    ) -> ToolReply {
        let ready = request
            .as_ref()
            .is_ok_and(|request| matches!(&request.operation, Operation::Ready { .. }));
        match request.and_then(|request| self.execute(request, ask)) {
            Ok(body) => ToolReply { body, ready },
            Err(error) => {
                let (code, hint) = failure_fields(&error);
                let mut reply = json!({ "error": error.message, "errorCode": code,
                    "displayPath": error.context.get("piDisplayPath"), "hint": hint });
                if let Some(mode) = error.context.get("piCommandExecution") {
                    reply["commandExecution"] = json!(mode);
                    if let Some(code) = error
                        .context
                        .get("exitCode")
                        .and_then(|code| code.parse::<i32>().ok())
                    {
                        reply["exitCode"] = json!(code);
                    }
                }
                ToolReply {
                    body: reply,
                    ready: false,
                }
            }
        }
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
        self.verify_command_failure()?;
        if let Operation::Ready { tools } = &request.operation {
            return ready_response(tools, self.access, self.commands.execution().is_some());
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
        if self.access == Access::NativeFork
            && !matches!(
                &request.operation,
                Operation::Read { .. } | Operation::Ls { .. } | Operation::Grep { .. }
            )
        {
            return Err(invalid("Pi 原生分叉首轮只允许读取、列出和搜索"));
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
            } => {
                let (read, identity) =
                    self.file_call(id, "read", "读取", &location, |target| {
                        let read = target.read(offset, limit)?;
                        Ok(((read, self.projects.review_location(target)), None))
                    })?;
                let source = if read.complete_lines > 0
                    && let (Some((project_id, path)), Some(digest)) = (identity, read.digest)
                    && let Some(source_id) = self.evidence.register_file(
                        &project_id,
                        &path,
                        digest.as_str(),
                        read.first_line,
                        read.text[..read.complete_bytes].to_owned(),
                    ) {
                    let last_line = read.first_line + read.complete_lines - 1;
                    Some(format!(
                        "工作台文件来源 sourceId={source_id}，已返回完整行 {}-{last_line}。",
                        read.first_line
                    ))
                } else {
                    None
                };
                return Ok(json!({ "text": read.text, "sourceHint": source }));
            }
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
                    let change = target.write(content, cancel)?;
                    Ok(("已写入原工程文件".into(), Some(change)))
                })?
            }
            Operation::Edit {
                location,
                old_text,
                new_text,
            } => self.file_call(id, "edit", "编辑", &location, |target| {
                let change = target.edit(&old_text, &new_text, cancel)?;
                Ok(("已编辑原工程文件".into(), Some(change)))
            })?,
            Operation::Bash { location, command } => {
                let label = if self.commands.execution() == Some(CommandExecution::Host) {
                    "执行本机命令"
                } else {
                    "执行离线命令"
                };
                let target = self.prepare_command(&location, &command).map_err(|error| {
                    self.record_failure(id, "bash", label, &location, None, error)
                })?;
                let cwd = target.directory().map_err(|error| {
                    self.record_failure(id, "bash", label, &location, None, error)
                })?;
                return self.run_command(id, &target, &cwd, &command);
            }
            Operation::AskSpecQuestion { question_id } => ask(&self.context, &question_id)?,
        };
        Ok(json!({ "text": result }))
    }

    fn file_call<T>(
        &self,
        id: &str,
        operation: &str,
        label: &str,
        location: &Location,
        call: impl FnOnce(&ProjectLocation<'_>) -> Result<(T, Option<(&'static str, Sha256)>)>,
    ) -> Result<T> {
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
        mut error: YssError,
    ) -> YssError {
        let display_path = self
            .projects
            .error_display_path(location.project_id.as_deref(), &location.path);
        let (code, hint) = failure_fields(&error);
        let mut event = json!({
            "id": id, "operation": operation, "path": null, "status": "failed", "message": label,
            "error": error.message, "errorCode": code, "displayPath": display_path, "hint": hint,
            "projectId": review.as_ref().map(|(id, _)| id), "reviewPath": review.as_ref().map(|(_, path)| path)
        });
        if operation == "bash"
            && let Some(execution) = self.commands.execution()
        {
            event["commandExecution"] = json!(execution);
            error = error.context("piCommandExecution", execution_name(execution));
        }
        let _ = self.context.emit("piTool", event);
        error.context("piDisplayPath", display_path)
    }

    fn prepare_command(&self, location: &Location, command: &str) -> Result<ProjectLocation<'_>> {
        if self.commands.execution().is_none() {
            return Err(invalid("本轮未授权命令").context("piErrorCode", "permission_denied"));
        }
        if command.trim().is_empty() || command.len() > 64 * 1024 || command.contains('\0') {
            return Err(invalid("命令须为 64 KiB 以内的有效文本"));
        }
        let target = self
            .projects
            .locate(location.project_id.as_deref(), &location.path)?;
        if target.mode() != ProjectMode::Execute {
            return Err(invalid("此工程未授权命令").context("piErrorCode", "permission_denied"));
        }
        self.projects
            .check_baselines(&self.context.cancel)
            .map_err(|error| {
                #[cfg(unix)]
                if matches!(self.commands, Commands::Host) && error.code != ErrorCode::Cancelled {
                    return self.stop_commands_after(host_baseline_failure(error, false));
                }
                error
            })?;
        Ok(target)
    }

    fn run_command(
        &self,
        id: &str,
        target: &ProjectLocation<'_>,
        cwd: &Path,
        command: &str,
    ) -> Result<Value> {
        let execution = self
            .commands
            .execution()
            .ok_or_else(|| invalid("本轮未授权命令"))?;
        let base = json!({ "id": id, "type": "commandExecution", "command": command, "cwd": cwd,
            "projectId": target.project_id, "commandExecution": execution });
        let activity_id = self
            .context
            .emit_with_id("item/started", json!({ "item": base.clone() }))?;
        let mut item = base;
        let mut output = String::new();
        let mut fragments = [Vec::new(), Vec::new()];
        #[cfg(unix)]
        let mut host_stderr = Vec::new();
        let mut published = 0;
        let mut last_publish: Option<Instant> = None;
        let publish = |chunk: Option<(bool, &[u8])>| {
            if let Some((stdout, bytes)) = chunk {
                #[cfg(unix)]
                if execution == CommandExecution::Host && !stdout {
                    let remaining = COMMAND_STDERR_LIMIT - host_stderr.len();
                    host_stderr.extend_from_slice(&bytes[..bytes.len().min(remaining)]);
                }
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
                    json!({"itemId":id,"delta":&output[published..],"commandExecution":execution}),
                )?;
                published = output.len();
                last_publish = Some(Instant::now());
            }
            Ok(())
        };
        let result = match &self.commands {
            Commands::Disabled => unreachable!("command execution checked before recording"),
            #[cfg(unix)]
            Commands::Host => target
                .host_directory()
                .and_then(|directory| {
                    super::host_command::run(cwd, directory, command, &self.context.cancel, publish)
                })
                .map(|status| (status, host_stderr)),
            #[cfg(unix)]
            Commands::Sandbox(sandbox) => sandbox
                .run(target.root(), cwd, command, &self.context.cancel, publish)
                .and_then(|(status, _, stderr)| {
                    self.projects.check_baselines(&self.context.cancel)?;
                    Ok((status, stderr))
                }),
            #[cfg(windows)]
            Commands::Sandbox(sandbox) => super::windows_command::run(
                sandbox,
                &self.projects,
                target,
                &self.context,
                command,
                publish,
            )
            .map(|(status, _, stderr)| (status, stderr)),
        };
        for fragment in fragments {
            append_output(&mut output, &String::from_utf8_lossy(&fragment));
        }
        let result = match result {
            Ok((status, stderr)) => Ok(command_result(
                &mut item, status, &stderr, &output, execution,
            )),
            Err(error) => {
                #[cfg(windows)]
                if error.code == ErrorCode::RecoveryRequired {
                    self.stop_commands_after(error.clone());
                }
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
                Err(error.context("piCommandExecution", execution_name(execution)))
            }
        };
        let recorded = self.context.emit("item/completed", json!({ "item": item }));
        // Check the repository even if the command exited unsuccessfully or was canceled.
        #[cfg(unix)]
        if execution == CommandExecution::Host
            && let Err(cause) = self.projects.check_baselines(&Cancellation::new())
        {
            let mut drift = host_baseline_failure(cause, true);
            if let Err(error) = recorded {
                drift.message = "本机命令结束后 Git 基线已变化或无法核对。本轮已停止，原工程变更已保留，命令日志写入失败。".into();
                drift = drift.context("commandLogError", error.message);
            }
            if let Ok(reply) = &result
                && let Some(code) = reply["exitCode"].as_i64()
            {
                drift = drift.context("exitCode", code);
            }
            return Err(self.stop_commands_after(drift));
        }
        recorded?;
        let mut reply = result?;
        if let Some(source_id) =
            self.evidence
                .register_command(activity_id, command, reply["exitCode"].as_i64())
        {
            reply["sourceHint"] = json!(format!("工作台命令来源 sourceId={source_id}。"));
        }
        Ok(reply)
    }
}
