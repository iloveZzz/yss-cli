use super::{
    Runtime, Session,
    acp::{self, Acp, protocol},
    launch::{self, Installation, Scratch},
    model_changed, not_ready, source,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::Root,
    model::Sha256,
    workbench::{
        agent_protocol::{PiModel, ThinkingLevel},
        assistant_ui::REPLY_GUIDANCE,
        jobs::JobContext,
    },
};
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

pub(super) const VERSION: &str = launch::VERSION;

pub(super) struct Entry {
    pub model: PiModel,
    pub effort: Option<ThinkingLevel>,
    pub efforts: Vec<ThinkingLevel>,
}

pub(super) struct Catalog {
    pub entries: Vec<Entry>,
    current: String,
}

fn effort(value: &Value) -> Option<ThinkingLevel> {
    serde_json::from_value(value.clone()).ok()
}

impl Catalog {
    fn read(state: &Value) -> Result<Self> {
        let state = state
            .get("models")
            .or_else(|| state["_meta"].get("modelState"))
            .ok_or_else(|| not_ready("catalog", "Grok Build 未返回本会话的模型目录"))?;
        let current = state["currentModelId"]
            .as_str()
            .ok_or_else(|| not_ready("catalog", "无法确认 Grok Build 当前模型"))?;
        let available = state["availableModels"]
            .as_array()
            .ok_or_else(|| not_ready("catalog", "Grok Build 模型目录格式无法核对"))?;
        let mut entries = Vec::new();
        for item in available {
            let Some(model) = item["modelId"]
                .as_str()
                .and_then(|id| PiModel::new("xai", id))
            else {
                continue;
            };
            let meta = &item["_meta"];
            let mut efforts = Vec::new();
            for option in meta["reasoningEfforts"].as_array().into_iter().flatten() {
                let level = option
                    .get("value")
                    .or_else(|| option.get("id"))
                    .and_then(effort);
                if let Some(level) = level
                    && !efforts.contains(&level)
                {
                    efforts.push(level);
                }
            }
            let level = effort(&meta["reasoningEffort"]);
            let effort = if efforts.is_empty() {
                None
            } else {
                level.filter(|level| efforts.contains(level))
            };
            entries.push(Entry {
                model,
                efforts,
                effort,
            });
        }
        if !entries.iter().any(|entry| entry.model.id == current) {
            return Err(not_ready("catalog", "Grok Build 当前模型不在本会话目录内"));
        }
        Ok(Self {
            entries,
            current: current.into(),
        })
    }

    pub(super) fn select(&self, selected: Option<&PiModel>) -> Result<&Entry> {
        self.entries
            .iter()
            .find(|entry| {
                selected.map_or(entry.model.id == self.current, |model| {
                    model == &entry.model
                })
            })
            .ok_or_else(|| {
                not_ready(
                    "modelUnavailable",
                    "所选 Grok Build 模型已不可用，请重新选择",
                )
            })
    }
}

fn initialize(acp: &mut Acp, installation: &Installation, cancel: &Cancellation) -> Result<String> {
    let response = acp.request("initialize", json!({
        "protocolVersion":1,
        "clientInfo":{"name":"yss-workbench","version":env!("CARGO_PKG_VERSION")},
        "clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false},
        "_meta":{"clientType":"extension"}
    }), cancel)?;
    let meta = &response["_meta"];
    if response["protocolVersion"] != 1
        || meta["agentVersion"] != VERSION
        || response["agentCapabilities"]["sessionCapabilities"]
            .get("resume")
            .is_none()
        || response["agentCapabilities"]["promptCapabilities"]["embeddedContext"] != true
        || meta["mcpServers"]
            .as_array()
            .is_none_or(|servers| !servers.is_empty())
    {
        return Err(protocol("Grok Build ACP 版本或受控会话能力无法核对"));
    }
    if !response["authMethods"]
        .as_array()
        .is_some_and(|methods| methods.iter().any(|method| method["id"] == "cached_token"))
    {
        return Err(launch::credentials());
    }
    acp.request(
        "authenticate",
        json!({
            "methodId":"cached_token", "_meta":{"headless":true}
        }),
        cancel,
    )
    .map_err(|error| {
        if error
            .context
            .get("grokIssue")
            .is_some_and(|issue| issue == "credentials")
        {
            launch::credentials()
        } else {
            error
        }
    })?;
    let account = acp.request("_x.ai/auth/info", json!({}), cancel)?;
    let identity = |key: &str| {
        account[key].as_str().filter(|value| {
            !value.trim().is_empty() && value.len() <= 512 && !value.chars().any(char::is_control)
        })
    };
    let (Some(principal), Some(kind)) = (identity("principalId"), identity("principalType")) else {
        return Err(not_ready(
            "credentials",
            "Grok Build 未返回可核验的登录身份",
        ));
    };
    if !account["userBlockedReason"].is_null()
        || account["teamBlockedReasons"]
            .as_array()
            .is_some_and(|reasons| !reasons.is_empty())
    {
        return Err(not_ready(
            "modelAccess",
            "当前 Grok Build 登录身份无法使用模型服务",
        ));
    }
    // Persist a one-way identifier scoped to the CLI and auth profile. Account
    // labels and credential material never enter workbench records.
    let fields = json!([
        installation.identity(),
        kind,
        principal,
        identity("teamId"),
        identity("organizationId")
    ]);
    Ok(Sha256::of(fields.to_string().as_bytes()).as_str().into())
}

fn new_session(acp: &mut Acp, cwd: &Path, cancel: &Cancellation) -> Result<Value> {
    acp.request("session/new", json!({"cwd":cwd,"mcpServers":[]}), cancel)
}

fn session_id(value: &Value) -> Result<&str> {
    value
        .as_str()
        .filter(|value| {
            (1..=128).contains(&value.len())
                && value
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
        })
        .ok_or_else(|| protocol("Grok Build 原生会话标识无法核对"))
}

fn close_session(acp: &mut Acp, id: &str, cancel: &Cancellation) {
    let _ = acp.request("session/close", json!({"sessionId":id}), cancel);
}

pub(super) fn inspect(
    cancel: &Cancellation,
    protected: &[PathBuf],
) -> Result<(Installation, Catalog, String)> {
    let installation = Installation::discover(protected, cancel)?;
    let (catalog, identity) = (|| -> Result<(Catalog, String)> {
        let scratch = Scratch::new()?;
        let sessions = launch::storage(scratch.path(), "catalog-sessions")?;
        let cwd = launch::storage(scratch.path(), "catalog-cwd")?;
        let mut acp = installation.start(&scratch, &cwd, &sessions, None, None, cancel)?;
        let identity = initialize(&mut acp, &installation, cancel)?;
        let session = new_session(&mut acp, &cwd, cancel)?;
        let id = session_id(&session["sessionId"])?;
        let catalog = Catalog::read(&session)?;
        close_session(&mut acp, id, cancel);
        acp.stop()?;
        installation.revalidate(cancel)?;
        Ok((catalog, identity))
    })()
    .map_err(|error| {
        error
            .context("executable", installation.binary.display())
            .context("version", VERSION)
    })?;
    Ok((installation, catalog, identity))
}

fn activate_session(
    acp: &mut Acp,
    cwd: &Path,
    saved: Option<&Session>,
    cancel: &Cancellation,
) -> Result<(String, Value)> {
    if let Some(saved) = saved {
        let id = session_id(&json!(saved.id))?.to_owned();
        let listed = acp
            .request("session/list", json!({"cwd":cwd}), cancel)
            .map_err(|error| error.context("grokResume", "unverified"))?;
        let exists = listed["sessions"].as_array().is_some_and(|items| {
            items.iter().any(|item| {
                item["sessionId"] == id
                    && item["cwd"]
                        .as_str()
                        .is_some_and(|path| Path::new(path) == cwd)
            })
        });
        if !exists {
            return Err(not_ready(
                "resumeUnavailable",
                "无法继续原 Grok Build 会话，请保留草稿或明确新建",
            ));
        }
        let response = acp
            .request(
                "session/resume",
                json!({
                    "sessionId":id,"cwd":cwd,"mcpServers":[]
                }),
                cancel,
            )
            .map_err(|error| error.context("grokResume", "unverified"))?;
        if response
            .get("sessionId")
            .is_some_and(|actual| actual != &id)
        {
            return Err(not_ready(
                "resumeUnavailable",
                "Grok Build 实际打开的会话与原会话不符",
            ));
        }
        return Ok((id, response));
    }
    let response = new_session(acp, cwd, cancel)?;
    let id = session_id(&response["sessionId"])?.to_owned();
    Ok((id, response))
}

fn check_session_model(
    acp: &mut Acp,
    session_id: &str,
    state: &Value,
    model: &PiModel,
    select_default: bool,
    selected_effort: Option<ThinkingLevel>,
    cancel: &Cancellation,
) -> Result<Option<ThinkingLevel>> {
    let catalog = Catalog::read(state)?;
    let same_model = catalog.current == model.id;
    if select_default && !same_model {
        return Err(model_changed());
    }
    let entry = catalog.select(Some(model))?;
    if selected_effort.is_some_and(|level| !entry.efforts.contains(&level)) {
        return Err(not_ready(
            "thinkingUnavailable",
            "所选 Grok Build 推理档位已不可用",
        ));
    }
    let config = |state: &Value, id: &str| {
        state["configOptions"]
            .as_array()
            .and_then(|items| items.iter().find(|item| item["id"] == id))
            .and_then(|item| item.get("currentValue"))
            .cloned()
    };
    let mut set = |id: &str, value: Value| {
        acp.request(
            "session/set_config_option",
            json!({"sessionId":session_id,"configId":id,"value":value}),
            cancel,
        )
    };
    let mut state = state.clone();
    if !same_model {
        state = set("model", json!(model.id))?;
    }
    let mut actual = config(&state, "reasoning_effort")
        .as_ref()
        .and_then(effort)
        .or_else(|| same_model.then_some(entry.effort).flatten());
    if let Some(level) = selected_effort
        && actual != Some(level)
    {
        state = set("reasoning_effort", json!(level))?;
        actual = config(&state, "reasoning_effort").as_ref().and_then(effort);
    }
    if config(&state, "model").map_or(!same_model, |value| value != model.id)
        || selected_effort.is_some_and(|level| actual != Some(level))
    {
        return Err(not_ready(
            "modelUnavailable",
            "无法确认 Grok Build 本轮实际模型和推理档位",
        ));
    }
    Ok(actual)
}

struct Output<'a> {
    context: &'a JobContext,
    flush_at: Option<Instant>,
}

impl Output<'_> {
    fn apply(&mut self, update: &Value) -> Result<()> {
        match update["sessionUpdate"].as_str() {
            Some("agent_message_chunk") if update["content"]["type"] == "text" => {
                if let Some(text) = update["content"]["text"].as_str()
                    && !text.is_empty()
                {
                    self.flush_at = self.context.emit_pi_delta(
                        "item/agentMessage/delta",
                        "grok-response",
                        text,
                    )?;
                }
            }
            Some("tool_call" | "tool_call_update") => {
                let status = match update["status"].as_str() {
                    Some("completed") => "completed",
                    Some("failed") => "failed",
                    Some("in_progress") => "running",
                    _ => "pending",
                };
                if let Ok(id) = session_id(&update["toolCallId"]) {
                    self.flush()?;
                    self.context.emit(
                        "piTool",
                        json!({
                            "id":id, "toolCallId":id, "name":"Grok Build", "status":status,
                            "message":"Grok Build 工具活动"
                        }),
                    )?;
                }
            }
            _ => {}
        }
        Ok(())
    }

    fn flush(&mut self) -> Result<()> {
        if self.flush_at.take().is_some() {
            self.context.flush_pi_delta()?;
        }
        Ok(())
    }
}

fn terminal(reason: &str) -> Option<Result<()>> {
    match reason {
        "end_turn" => Some(Ok(())),
        "cancelled" => Some(Err(YssError::new(
            ErrorCode::Cancelled,
            "Grok Build 已确认停止",
        ))),
        "max_tokens" | "max_turn_requests" => Some(Err(not_ready(
            "responseLimit",
            "Grok Build 已达到轮次或上下文限制",
        ))),
        "refusal" => Some(Err(not_ready("responseRefused", "Grok Build 拒绝完成任务"))),
        _ => None,
    }
}

fn notification_terminal(frame: &Value, prompt_id: &str) -> Option<Result<()>> {
    let params = &frame["params"];
    let update = &params["update"];
    let reason = match frame["method"].as_str()? {
        "x.ai/session/prompt_complete" | "_x.ai/session/prompt_complete"
            if params["promptId"] == prompt_id =>
        {
            params["stopReason"].as_str()
        }
        "x.ai/session_notification" | "_x.ai/session_notification" | "_x.ai/session/update"
            if update["sessionUpdate"] == "turn_completed"
                && (update["prompt_id"] == prompt_id || update["promptId"] == prompt_id) =>
        {
            update["stop_reason"]
                .as_str()
                .or_else(|| update["stopReason"].as_str())
        }
        _ => None,
    }?;
    match reason {
        "rate_limit" => Some(Err(acp::rate_limited())),
        "error" => Some(Err(not_ready(
            "responseError",
            "Grok Build 已结束本轮并报告模型请求失败",
        ))),
        _ => terminal(reason),
    }
}

#[allow(clippy::too_many_arguments)]
fn prompt(
    acp: &mut Acp,
    id: &str,
    request: &str,
    source: Option<Value>,
    handoff: Option<&str>,
    context: &JobContext,
    before_prompt: impl FnOnce() -> Result<()>,
) -> Result<()> {
    let request_id = acp.id();
    let prompt_id = format!("yss-{}", context.id);
    let mut content = vec![
        json!({"type":"text","text":request}),
        json!({"type":"text","text":REPLY_GUIDANCE}),
    ];
    content.extend(source);
    let request = Acp::prepare_frame(&json!({
        "jsonrpc":"2.0","id":request_id,"method":"session/prompt",
        "params":{"sessionId":id,"prompt":content,"_meta":{"promptId":prompt_id,"requestId":prompt_id}}
    }))?;
    before_prompt()?;
    context.cancel.check()?;
    context.mark_agent_dispatching()?;
    let uncertain = |error: YssError| error.context("agentUncertain", "true");
    acp.send_prepared(&request, &context.cancel)
        .map_err(uncertain)?;
    context.trace("grokPromptSent");
    let mut submitted = false;
    let mut confirm_submission = || -> Result<()> {
        if !submitted {
            context.mark_agent_dispatched(handoff).map_err(uncertain)?;
            submitted = true;
        }
        Ok(())
    };
    let stop_signal = Cancellation::new();
    let mut stop_deadline = None;
    let mut response_completion = None;
    let mut notification_completion = None;
    let mut drain_until = None;
    let mut output = Output {
        context,
        flush_at: None,
    };
    let deadline = Instant::now() + Duration::from_secs(30 * 60);
    loop {
        let now = Instant::now();
        if context.cancel.is_cancelled() && stop_deadline.is_none() && drain_until.is_none() {
            // A completion can already be waiting on stdout while the cancel
            // notification fails to write to a closing pipe.
            let _ = acp.send(
                json!({
                    "jsonrpc":"2.0","method":"session/cancel",
                    "params":{"sessionId":id,"_meta":{"cancelTrigger":"ctrl_c"}}
                }),
                &stop_signal,
            );
            stop_deadline = Some(now + Duration::from_secs(2));
        }
        let limit = drain_until.or(stop_deadline).unwrap_or(deadline);
        if now >= limit {
            break;
        }
        let poll = limit.min(now + Duration::from_millis(100));
        let frame = match acp.receive(&stop_signal, poll) {
            Ok(frame) => frame,
            Err(_) if drain_until.is_some() => break,
            Err(error) => return Err(uncertain(error)),
        };
        if output.flush_at.is_some_and(|at| Instant::now() >= at) {
            output.flush().map_err(uncertain)?;
        }
        let Some(frame) = frame else {
            continue;
        };
        if frame.get("method").is_none()
            && frame["id"] == request_id
            && response_completion.is_none()
        {
            let result = if let Some(error) = frame.get("error") {
                Err(acp::remote_error(error))
            } else {
                confirm_submission()?;
                frame["result"]["stopReason"]
                    .as_str()
                    .and_then(terminal)
                    .unwrap_or_else(|| {
                        Err(uncertain(protocol("Grok Build 返回的停止原因无法核对")))
                    })
            };
            response_completion = Some(result);
            drain_until.get_or_insert_with(|| Instant::now() + Duration::from_millis(350));
            continue;
        }
        if frame.get("id").is_some() {
            acp.block_agent_request(&frame, &stop_signal)
                .map_err(uncertain)?;
            continue;
        }
        let params = &frame["params"];
        if params["sessionId"] != id {
            continue;
        }
        let update = &params["update"];
        if frame["method"] == "session/update"
            && params["_meta"]["promptId"] == prompt_id
            && matches!(
                update["sessionUpdate"].as_str(),
                Some("agent_message_chunk" | "tool_call" | "tool_call_update")
            )
        {
            confirm_submission()?;
            output.apply(update).map_err(uncertain)?;
        } else if notification_completion.is_none()
            && let Some(result) = notification_terminal(&frame, &prompt_id)
        {
            confirm_submission()?;
            notification_completion = Some(result);
            drain_until.get_or_insert_with(|| Instant::now() + Duration::from_millis(350));
        }
    }
    output.flush().map_err(uncertain)?;
    // The original ACP response is authoritative when valid. A correlated Grok
    // completion can settle the turn if that response was missing or unreadable.
    let result = match response_completion {
        Some(Err(error)) if error.context.contains_key("agentUncertain") => {
            notification_completion.unwrap_or(Err(error))
        }
        Some(result) => result,
        None => notification_completion.unwrap_or_else(|| {
            Err(uncertain(protocol(
                "无法确认 Grok Build 本轮结果，请核对原执行记录",
            )))
        }),
    };
    if !result
        .as_ref()
        .is_err_and(|error| error.context.contains_key("agentUncertain"))
    {
        context.mark_agent_settled().map_err(uncertain)?;
    }
    result
}

#[allow(clippy::too_many_arguments)]
pub(super) fn run(
    mut runtime: Runtime,
    data_dir: &Path,
    run_id: &str,
    project_root: &Path,
    excluded: &[PathBuf],
    scope_identity: &str,
    request: &str,
    source_task: &str,
    spec_path: &str,
    handoff: Option<&str>,
    resume: Option<&Session>,
    effort: Option<ThinkingLevel>,
    context: &JobContext,
    mut persist: impl FnMut(Session, Value) -> Result<()>,
    before_prompt: impl FnOnce() -> Result<()>,
) -> Result<()> {
    let cancel = &context.cancel;
    if scope_identity.len() != 64 || !scope_identity.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(not_ready("scope", "Grok Build 工程范围标识无法核对"));
    }
    if resume.is_some_and(|saved| saved.identity != runtime.expected_identity) {
        return Err(not_ready(
            "resumeIdentityChanged",
            "原 Grok Build 会话的登录或安装身份已变化，请恢复原配置并重新核对",
        ));
    }
    if resume.is_some_and(|saved| {
        saved.scope_identity != scope_identity
            || !launch::session_available(data_dir, run_id, &saved.id)
    }) {
        return Err(not_ready(
            "resumeUnavailable",
            "原 Grok Build 会话的工程范围或原生记录已变化，请明确新建",
        ));
    }
    let source_root = Root::open(project_root)?;
    let git_scope_identity = Sha256::of(
        &serde_json::to_vec(&source_root.git_boundary().blocked)
            .map_err(|_| not_ready("scope", "无法记录 Grok Build 主工程的 Git 边界"))?,
    );
    if resume.is_some_and(|saved| {
        saved.git_scope_identity.as_deref() != Some(git_scope_identity.as_str())
    }) {
        return Err(not_ready(
            "resumeUnavailable",
            "无法确认原 Grok Build 会话的 Git 边界仍一致，请从原 Spec 新建会话",
        ));
    }
    runtime.installation.refresh_auth_stamp(cancel)?;
    let store = launch::storage(data_dir, "grok")?;
    let store = launch::storage(&store, run_id)?;
    let cwd = launch::storage(&store, "workspace")?;
    if std::fs::read_dir(&cwd)?.next().is_some() {
        return Err(not_ready(
            "configuration",
            "Grok Build 专用工作目录包含未受信文件",
        ));
    }
    let sessions = launch::storage(&store, "sessions")?;
    let scratch = Scratch::new()?;
    let mut acp = runtime.installation.start(
        &scratch,
        &cwd,
        &sessions,
        (!runtime.select_default).then_some(&runtime.expected_model),
        Some(context),
        cancel,
    )?;
    let execution = (|| {
        let identity = initialize(&mut acp, &runtime.installation, cancel)?;
        if identity != runtime.expected_identity {
            return Err(not_ready(
                if resume.is_some() {
                    "resumeIdentityChanged"
                } else {
                    "identityChanged"
                },
                "Grok Build 的实际登录账号已变化，请重新核对",
            ));
        }
        let (id, state) = activate_session(&mut acp, &cwd, resume, cancel)?;
        let effort = check_session_model(
            &mut acp,
            &id,
            &state,
            &runtime.expected_model,
            runtime.select_default,
            effort,
            cancel,
        )?;
        context.trace("grokReady");
        let source = source::content(&source_root, excluded, source_task, spec_path, cancel)?;
        persist(
            Session {
                id: id.clone(),
                identity,
                scope_identity: scope_identity.into(),
                git_scope_identity: Some(git_scope_identity.as_str().into()),
            },
            json!({
                "host":"grok", "provider":"xai", "model":runtime.expected_model.id,
                "version":VERSION, "thinkingLevel":effort, "readOnly":true
            }),
        )?;
        context.mark_running()?;
        let result = prompt(&mut acp, &id, request, source, handoff, context, || {
            before_prompt()?;
            runtime.installation.revalidate(cancel)?;
            source_root.revalidate()
        });
        if !cancel.is_cancelled()
            && !result
                .as_ref()
                .is_err_and(|error| error.context.contains_key("agentUncertain"))
        {
            close_session(&mut acp, &id, cancel);
        }
        result
    })();
    acp.stop()?;
    execution
}
