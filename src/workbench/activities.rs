//! Read-only activity projection of the persisted job event stream.
use super::{
    jobs::{JobEvent, JobManager, lock_store},
    sources::invalid,
};
use crate::error::Result;
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, HashMap, VecDeque},
    sync::{Arc, Mutex},
};

const PAGE_SIZE: usize = 50;
const SUMMARY_CHARS: usize = 240;
const CONVERSATION_PAGE_TEXT_BYTES: usize = 2 * 1024 * 1024;

#[derive(Clone, Copy, PartialEq, Eq, Hash, Serialize)]
#[serde(rename_all = "camelCase")]
enum Kind {
    Message,
    Reasoning,
    User,
    Info,
    Command,
    Files,
    Tool,
    Question,
    Status,
    Error,
}

#[derive(Clone, Copy, PartialEq)]
enum ActivityView {
    Summary,
    Conversation,
    Detail,
}

#[derive(PartialEq, Eq, Hash)]
struct ItemKey {
    turn: String,
    item: String,
    kind: Kind,
}

fn item_payload(event: &JobEvent) -> &Value {
    let payload = &event.payload;
    if payload.get("item").is_none()
        && payload.get("itemId").is_none()
        && payload["params"].is_object()
    {
        &payload["params"]
    } else {
        payload
    }
}

fn event_text(payload: &Value) -> Option<&str> {
    [
        &payload["text"],
        &payload["delta"],
        &payload["output"],
        &payload["message"],
        &payload["params"]["text"],
        &payload["params"]["delta"],
        &payload["delta"]["text"],
    ]
    .into_iter()
    .find_map(Value::as_str)
}

fn key(event: &JobEvent, kind: Kind) -> Option<ItemKey> {
    let payload = item_payload(event);
    let item = if kind == Kind::Question {
        payload["requestId"].as_str()
    } else {
        payload["itemId"]
            .as_str()
            .or_else(|| payload["item"]["id"].as_str())
    }?;
    Some(ItemKey {
        turn: payload["turnId"].as_str().unwrap_or_default().into(),
        item: item.into(),
        kind,
    })
}

fn item_kind(event: &JobEvent) -> Option<Kind> {
    match item_payload(event)["item"]["type"]
        .as_str()
        .unwrap_or(&event.kind)
    {
        "agentMessage" | "item/agentMessage/delta" => Some(Kind::Message),
        "piReasoning" | "item/piReasoning/delta" => Some(Kind::Reasoning),
        "piCompaction" => Some(Kind::Tool),
        "userMessage" => Some(Kind::User),
        "commandExecution" | "item/commandExecution/outputDelta" => Some(Kind::Command),
        "fileChange" => Some(Kind::Files),
        "inputRequested" if event.payload["request"]["protocolMethod"] == "pi/askSpecQuestion" => {
            Some(Kind::Question)
        }
        "piQuestionResult" => Some(Kind::Question),
        _ => None,
    }
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct Question {
    request_id: String,
    question_id: String,
    reason: String,
    options: Vec<String>,
    answer: Option<String>,
    response_id: Option<String>,
    error: Option<String>,
}

fn text_field(value: &Value) -> Option<String> {
    value.as_str().map(str::to_owned)
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct Activity {
    id: u64,
    updated_event_id: u64,
    created_at: String,
    kind: Kind,
    text: String,
    status: String,
    exit_code: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    termination_signal: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    command_diagnostics: Option<Value>,
    #[serde(skip_serializing_if = "String::is_empty")]
    output: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    item_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    operation: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    project_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error_code: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    display_path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    hint: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    duration_ms: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    question: Option<Box<Question>>,
}

impl Activity {
    fn new(event: &JobEvent, kind: Kind) -> Self {
        Self {
            id: event.id,
            updated_event_id: event.id,
            created_at: event.created_at.clone(),
            kind,
            text: String::new(),
            status: String::new(),
            exit_code: None,
            termination_signal: None,
            command_diagnostics: None,
            output: String::new(),
            item_id: key(event, kind).map(|key| key.item),
            operation: None,
            path: None,
            project_id: None,
            error: None,
            error_code: None,
            display_path: None,
            hint: None,
            duration_ms: None,
            question: None,
        }
    }

    // The index retains a short prefix. Expansion replays the same events without this limit.
    fn append(&mut self, value: &str, full: bool) {
        if full {
            self.text.push_str(value);
        } else {
            let remaining = SUMMARY_CHARS.saturating_sub(self.text.chars().count());
            self.text.extend(value.chars().take(remaining));
        }
    }

    fn replace(&mut self, value: &str, full: bool) {
        self.text.clear();
        self.append(value, full);
    }

    fn update_item(&mut self, event: &JobEvent, view: ActivityView) {
        let full = view != ActivityView::Summary;
        let payload = item_payload(event);
        let item = &payload["item"];
        self.updated_event_id = event.id;
        if let Some(status) = item["status"].as_str() {
            self.status = status.into();
        } else if event.kind == "item/completed" {
            self.status = "completed".into();
        } else if event.kind == "item/started" {
            self.status = "inProgress".into();
        }
        match self.kind {
            Kind::Reasoning => {
                if event.kind == "item/piReasoning/delta" {
                    if let Some(delta) = event_text(payload) {
                        self.append(delta, full);
                    }
                    if self.status.is_empty() {
                        self.status = "inProgress".into();
                    }
                } else {
                    self.duration_ms = item["durationMs"].as_u64();
                    if let Some(text) = item["text"].as_str() {
                        self.replace(text, full);
                    }
                }
            }
            Kind::Message | Kind::User => {
                if let Some(text) = item["text"].as_str().filter(|text| !text.is_empty()) {
                    self.replace(text, full);
                } else if let Some(parts) = item["content"].as_array().filter(|v| !v.is_empty()) {
                    self.replace(
                        &parts
                            .iter()
                            .filter_map(|p| p["text"].as_str())
                            .collect::<Vec<_>>()
                            .join("\n"),
                        full,
                    );
                } else if event.kind.ends_with("/delta")
                    && let Some(delta) = event_text(payload)
                {
                    self.append(delta, full);
                }
            }
            Kind::Command => {
                if let Some(command) = item["command"].as_str() {
                    self.replace(command, full);
                } else if let Some(parts) = item["command"].as_array() {
                    self.replace(
                        &parts
                            .iter()
                            .filter_map(Value::as_str)
                            .collect::<Vec<_>>()
                            .join(" "),
                        full,
                    );
                }
                if view == ActivityView::Detail {
                    if let Some(output) =
                        item["aggregatedOutput"].as_str().filter(|s| !s.is_empty())
                    {
                        self.output = output.into();
                    } else if event.kind.ends_with("/outputDelta")
                        && let Some(delta) = event_text(payload)
                    {
                        self.output.push_str(delta);
                    }
                }
                if let Some(code) = item["exitCode"].as_i64() {
                    self.exit_code = Some(code);
                }
                if item["terminationSignal"].is_object() {
                    self.termination_signal = Some(item["terminationSignal"].clone());
                }
                if item["commandDiagnostics"]
                    .as_array()
                    .is_some_and(|items| !items.is_empty())
                {
                    self.command_diagnostics = Some(item["commandDiagnostics"].clone());
                }
            }
            Kind::Files => {
                self.operation = text_field(&item["operation"]).or(self.operation.take());
                self.project_id = text_field(&item["projectId"]).or(self.project_id.take());
                self.path = text_field(&item["reviewPath"]).or(self.path.take());
                if let Some(changes) = item["changes"].as_array() {
                    self.replace(
                        &changes
                            .iter()
                            .filter_map(|f| f["path"].as_str())
                            .collect::<Vec<_>>()
                            .join("\n"),
                        full,
                    );
                }
            }
            Kind::Question => self.update_question(event, full),
            Kind::Tool => {
                self.operation = text_field(&item["operation"]);
            }
            _ => {}
        }
    }

    fn update_question(&mut self, event: &JobEvent, full: bool) {
        let p = &event.payload;
        if event.kind == "inputRequested" {
            let params = &p["request"]["params"];
            let spec = &params["questions"][0];
            let (Some(request_id), Some(question_id), Some(title)) = (
                p["requestId"].as_str(),
                spec["id"].as_str(),
                spec["question"].as_str(),
            ) else {
                return;
            };
            self.question = Some(Box::new(Question {
                request_id: request_id.into(),
                question_id: question_id.into(),
                reason: text_field(&params["reason"]).unwrap_or_default(),
                options: spec["options"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .filter_map(|option| text_field(&option["label"]))
                    .collect(),
                answer: None,
                response_id: None,
                error: None,
            }));
            self.replace(title, full);
            self.status = "pending".into();
        } else if let Some(question) = &mut self.question {
            if event.kind == "inputResolved" {
                self.status = "saving".into();
            } else if event.kind == "piQuestionResult" {
                question.response_id = text_field(&p["responseId"]);
                question.answer = text_field(&p["answer"]);
                question.error = text_field(&p["error"]);
                self.status = if question.response_id.is_some() && question.answer.is_some() {
                    "saved"
                } else {
                    "failed"
                }
                .into();
            }
        }
    }

    fn in_conversation(&self) -> bool {
        matches!(
            self.kind,
            Kind::Message
                | Kind::User
                | Kind::Command
                | Kind::Files
                | Kind::Tool
                | Kind::Error
                | Kind::Reasoning
        ) || self.question.is_some()
    }

    fn needs_body(&self) -> bool {
        matches!(
            self.kind,
            Kind::Message
                | Kind::User
                | Kind::Question
                | Kind::Command
                | Kind::Files
                | Kind::Tool
                | Kind::Reasoning
        )
    }
}

fn standalone(event: &JobEvent, full: bool) -> Option<Activity> {
    let p = &event.payload;
    let status = p["status"].as_str().unwrap_or_default();
    let message = event_text(p)
        .or_else(|| p["error"]["message"].as_str())
        .unwrap_or_default();
    let (kind, text) = match event.kind.as_str() {
        "run" if p["resumed"] == true => (Kind::Status, "正在续接原运行"),
        "run" => (Kind::Status, "准备隔离工作区"),
        "hostThread" => (Kind::Status, "已连接 Codex 会话"),
        "hostTurn" => (Kind::Status, "Codex 开始处理"),
        "piStatus" => (Kind::Status, message),
        "piTool" => (Kind::Tool, message),
        "inputRequested" => (Kind::Status, "等待你的输入或授权"),
        "inputResolved" => (Kind::Status, "已提交你的答复"),
        "plan" => (Kind::Status, "已核对操作内容"),
        "query" => (Kind::Status, "读取外部关联"),
        "status" | "stateChanged" | "finished" if !message.is_empty() => (Kind::Error, message),
        "finished" => (Kind::Status, "本轮执行结束"),
        "status" | "stateChanged" if !status.is_empty() => (Kind::Status, "更新任务状态"),
        "error" | "failed" | "progressError" | "unsupportedRequest" => (
            Kind::Error,
            if message.is_empty() {
                "运行遇到问题"
            } else {
                message
            },
        ),
        _ if !message.is_empty() && !event.kind.contains("reasoning") => (Kind::Info, message),
        _ => return None,
    };
    let mut row = Activity::new(event, kind);
    row.status = status.into();
    row.append(text, full);
    if row.kind == Kind::Tool {
        row.item_id = text_field(&p["id"]);
        row.operation = text_field(&p["operation"]);
        row.project_id = text_field(&p["projectId"]);
        row.path = text_field(&p["reviewPath"]).or_else(|| text_field(&p["path"]));
        row.error = text_field(&p["error"]);
        row.error_code = text_field(&p["errorCode"]);
        row.display_path = text_field(&p["displayPath"]);
        row.hint = text_field(&p["hint"]);
        if row.status.is_empty() {
            row.status = "completed".into();
        }
    }
    if event.kind == "query" {
        for value in [&p["host"], &p["project"]]
            .into_iter()
            .filter_map(Value::as_str)
        {
            row.append(" · ", full);
            row.append(value, full);
        }
    }
    Some(row)
}

#[derive(Default)]
struct Index {
    cursor: u64,
    rows: Vec<Activity>,
    items: HashMap<ItemKey, usize>,
    last_message: Option<usize>,
    run_id: Option<String>,
    host: Option<String>,
    prompt: Option<String>,
    previous_job_id: Option<String>,
    citations: Vec<Value>,
    allow_commands: Option<bool>,
    pi_settings: Option<Value>,
    pi_context: Option<Value>,
    pi_usage: Option<Value>,
    pi_plan: Option<Value>,
    pending: BTreeMap<String, JobEvent>,
    last_status: String,
}

impl Index {
    fn ingest(&mut self, event: JobEvent) {
        self.cursor = event.id;
        let p = &event.payload;
        match event.kind.as_str() {
            "run" => {
                self.run_id = text_field(&p["runId"]);
                self.host = text_field(&p["host"]);
                self.prompt = text_field(&p["prompt"]);
                self.previous_job_id = text_field(&p["previousJobId"]);
                self.citations = p["citations"].as_array().cloned().unwrap_or_default();
                self.allow_commands = p["allowCommands"].as_bool();
            }
            "piStatus" if p["model"].is_object() => {
                self.pi_settings = Some(
                    json!({"model":p["model"],"thinkingLevel":p["thinkingLevel"],"contextWindow":p["contextWindow"],"readOnly":p["readOnly"]}),
                );
            }
            "piContext" => self.pi_context = Some(p.clone()),
            "piUsage" => self.pi_usage = Some(p.clone()),
            "piPlan" => self.pi_plan = Some(p["steps"].clone()),
            "inputRequested" => {
                if let Some(id) = p["requestId"].as_str() {
                    self.pending.insert(id.into(), event.clone());
                }
            }
            "inputResolved" => {
                if let Some(id) = p["requestId"].as_str() {
                    self.pending.remove(id);
                }
                if let Some(pos) = key(&event, Kind::Question).and_then(|k| self.items.get(&k)) {
                    self.rows[*pos].update_item(&event, ActivityView::Summary);
                }
            }
            "status" | "stateChanged" => {
                let status = p["status"].as_str().unwrap_or_default();
                if status == self.last_status && p["error"].is_null() {
                    return;
                }
                self.last_status = status.into();
            }
            _ => {}
        }
        if let Some(kind) = item_kind(&event) {
            let next = self.rows.len();
            if event.kind == "piQuestionResult" {
                if let Some(pos) = key(&event, kind).and_then(|k| self.items.get(&k)) {
                    self.rows[*pos].update_item(&event, ActivityView::Summary);
                }
                return;
            }
            let pos = key(&event, kind)
                .map(|k| *self.items.entry(k).or_insert(next))
                .unwrap_or(next);
            if pos == next {
                self.rows.push(Activity::new(&event, kind));
            }
            self.rows[pos].update_item(&event, ActivityView::Summary);
            if kind == Kind::Message && !self.rows[pos].text.is_empty() {
                self.last_message =
                    Some(self.last_message.map_or(pos, |previous| previous.max(pos)));
            }
        } else if let Some(row) = standalone(&event, false) {
            self.rows.push(row);
        }
    }
}

// A few open task tabs share incremental summaries. Raw output stays in SQLite.
#[derive(Default)]
pub(crate) struct ActivityCache(VecDeque<(String, Arc<Mutex<Index>>)>);

impl ActivityCache {
    fn index(&mut self, id: &str) -> Arc<Mutex<Index>> {
        let entry = self
            .0
            .iter()
            .position(|(key, _)| key == id)
            .and_then(|pos| self.0.remove(pos))
            .unwrap_or_else(|| (id.into(), Arc::default()));
        let index = entry.1.clone();
        self.0.push_front(entry);
        self.0.truncate(4);
        index
    }
}

fn visit_events(
    jobs: &JobManager,
    id: &str,
    mut after: u64,
    through: u64,
    mut apply: impl FnMut(JobEvent),
) -> Result<()> {
    while after < through {
        let batch = lock_store(&jobs.store)?.events_through(id, after, through)?;
        if batch.is_empty() {
            break;
        }
        for event in batch {
            after = event.id;
            apply(event);
        }
    }
    Ok(())
}

fn with_index<T>(
    jobs: &JobManager,
    id: &str,
    read: impl FnOnce(&Index, bool) -> Result<T>,
) -> Result<T> {
    let cached = jobs
        .activity_cache
        .lock()
        .map_err(|_| invalid("活动索引不可用"))?
        .index(id);
    let mut index = cached.lock().map_err(|_| invalid("活动索引不可用"))?;
    let (terminal, end) = {
        let mut store = lock_store(&jobs.store)?;
        let job = store
            .record::<super::jobs::JobRecord>("job", id)?
            .ok_or_else(|| invalid("执行记录不存在").context("httpStatus", 404))?;
        (job.terminal(), store.last_event_id(id)?)
    };
    visit_events(jobs, id, index.cursor, end, |event| index.ingest(event))?;
    read(&index, terminal)
}

pub(crate) fn page(jobs: &JobManager, id: &str, before: Option<u64>) -> Result<Value> {
    with_index(jobs, id, |index, terminal| {
        let stop = before.map_or(index.rows.len(), |bound| {
            index.rows.partition_point(|row| row.id < bound)
        });
        let start = stop.saturating_sub(PAGE_SIZE);
        Ok(json!({
            "items": &index.rows[start..stop], "hasOlder": start > 0, "total": index.rows.len(),
            "cursor": index.cursor, "terminal": terminal, "runId": index.run_id, "host": index.host,
            "latestMessage": index.last_message.map(|pos| &index.rows[pos].text),
            "piSettings": index.pi_settings, "piContext": index.pi_context, "piUsage": index.pi_usage,
            "piPlan": index.pi_plan,
            "pendingInputs": if terminal { Vec::new() } else { index.pending.values().collect() }
        }))
    })
}

pub(crate) fn conversation(jobs: &JobManager, id: &str, before: Option<u64>) -> Result<Value> {
    with_index(jobs, id, |index, terminal| {
        let stop = before.map_or(index.rows.len(), |bound| {
            index.rows.partition_point(|row| row.id < bound)
        });
        let mut matching = index.rows[..stop]
            .iter()
            .rev()
            .filter(|row| row.in_conversation());
        let mut items = Vec::new();
        let mut text_bytes = 0;
        let mut has_older = false;
        for _ in 0..PAGE_SIZE {
            let Some(row) = matching.next() else { break };
            let row = if row.needs_body() {
                replay_activity(
                    jobs,
                    id,
                    row.id,
                    row.updated_event_id,
                    ActivityView::Conversation,
                )?
            } else {
                row.clone()
            };
            text_bytes += row.text.len();
            if !items.is_empty() && text_bytes > CONVERSATION_PAGE_TEXT_BYTES {
                has_older = true;
                break;
            }
            items.push(row);
        }
        items.reverse();
        Ok(json!({
            "items": items, "hasOlder": has_older || matching.next().is_some(), "cursor": index.cursor,
            "terminal": terminal, "runId": index.run_id, "host": index.host,
            "prompt": index.prompt, "previousJobId": index.previous_job_id,
            "citations": index.citations, "allowCommands": index.allow_commands
        }))
    })
}

pub(crate) fn agent_attention(jobs: &JobManager, id: &str) -> Result<Vec<Value>> {
    with_index(jobs, id, |index, terminal| {
        Ok(index.agent_attention(terminal))
    })
}

pub(super) fn agent_question_attention(
    store: &super::store::Store,
    job: &super::jobs::JobRecord,
) -> Result<Vec<Value>> {
    let mut index = Index::default();
    for event in store.agent_question_events(&job.id)? {
        index.ingest(event);
    }
    Ok(index.agent_attention(job.terminal()))
}

impl Index {
    fn agent_attention(&self, terminal: bool) -> Vec<Value> {
        let questions: Vec<_> = self
            .rows
            .iter()
            .rev()
            .filter(|row| row.question.is_some())
            .collect();
        let mut items: Vec<_> = questions
            .iter()
            .filter_map(|row| {
                let kind = match row.status.as_str() {
                    "pending" | "saving" if !terminal => "question",
                    "failed"
                        if !questions.iter().any(|next| {
                            next.id > row.id
                                && next.question.as_ref().map(|q| &q.question_id)
                                    == row.question.as_ref().map(|q| &q.question_id)
                        }) =>
                    {
                        "answerFailed"
                    }
                    _ => return None,
                };
                Some(json!({"kind":kind,"activityId":row.id,"status":row.status}))
            })
            .collect();
        items.sort_by_key(|item| item["kind"] != "question");
        items
    }
}

pub(crate) fn detail(jobs: &JobManager, id: &str, first: u64) -> Result<Value> {
    let end = with_index(jobs, id, |index, _| {
        let pos = index
            .rows
            .binary_search_by_key(&first, |row| row.id)
            .map_err(|_| invalid("执行活动不存在").context("httpStatus", 404))?;
        Ok(index.rows[pos].updated_event_id)
    })?;
    Ok(json!(replay_activity(
        jobs,
        id,
        first,
        end,
        ActivityView::Detail
    )?))
}

fn replay_activity(
    jobs: &JobManager,
    id: &str,
    first: u64,
    end: u64,
    view: ActivityView,
) -> Result<Activity> {
    let mut result: Option<Activity> = None;
    let mut group = None;
    visit_events(jobs, id, first - 1, end, |event| {
        if event.id == first {
            if let Some(kind) = item_kind(&event) {
                group = key(&event, kind);
                let mut row = Activity::new(&event, kind);
                row.update_item(&event, view);
                result = Some(row);
            } else {
                result = standalone(&event, true);
            }
        } else if let (Some(row), Some(wanted)) = (&mut result, &group)
            && (item_kind(&event) == Some(row.kind)
                || (row.kind == Kind::Question && event.kind == "inputResolved"))
            && key(&event, row.kind).as_ref() == Some(wanted)
        {
            row.update_item(&event, view);
        }
    })?;
    result.ok_or_else(|| invalid("执行活动不存在"))
}
