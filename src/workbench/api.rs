use super::{
    agent, agent_protocol, agent_scope, artifacts, backup, ci_job, drafts, engineering, grok,
    integrations,
    jobs::{self, JobManager},
    merge_requests,
    model::*,
    read_model, sources,
};
use crate::{
    cancel::Cancellation,
    codec::{self, Limits},
    error::Result,
    fs::Root,
    model::{BuildInfo, RelPath, Sha256},
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::{Arc, Mutex, OnceLock},
    time::{Duration, Instant, UNIX_EPOCH},
};

type DesktopFlushReceipt = (String, bool, Instant);
static DESKTOP_FLUSH: OnceLock<Mutex<BTreeMap<String, DesktopFlushReceipt>>> = OnceLock::new();

fn desktop_identifier(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

#[derive(Clone)]
pub struct PlatformApi {
    pub jobs: Arc<JobManager>,
    pub bundle: Arc<[u8]>,
    pub cancel: Cancellation,
    pub origin: String,
    pub(super) reads: Arc<read_model::ReadModel>,
}

fn encode(value: impl serde::Serialize) -> Result<Value> {
    serde_json::to_value(value).map_err(|_| sources::invalid("响应编码失败"))
}
fn decode<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T> {
    codec::decode_json(
        bytes,
        Limits {
            bytes: 1024 * 1024,
            ..Limits::manifest()
        },
    )
    .map_err(|error| sources::invalid("请求格式或字段无效").context("detail", error.message))
}
pub fn query_values(query: &str) -> BTreeMap<String, String> {
    reqwest::Url::parse(&format!("http://localhost/?{query}"))
        .map(|url| {
            url.query_pairs()
                .map(|(key, value)| (key.into_owned(), value.into_owned()))
                .collect()
        })
        .unwrap_or_default()
}
fn number(query: &BTreeMap<String, String>, key: &str, default: usize) -> Result<usize> {
    query
        .get(key)
        .map(|value| value.parse().map_err(|_| sources::invalid("分页参数无效")))
        .unwrap_or(Ok(default))
}

fn event_cursor(value: &str) -> Result<u64> {
    value
        .parse::<u64>()
        .ok()
        .filter(|v| *v <= i64::MAX as u64)
        .ok_or_else(|| sources::invalid("事件游标无效"))
}

pub(super) fn job_fingerprint(path: &str, request: &Value) -> Result<String> {
    let normalized;
    let request = if let Some(fields) = request.as_object().filter(|fields| {
        path == "/api/agent/start"
            && fields.get("hostExecutionConfirmed") == Some(&Value::Bool(false))
    }) {
        let mut fields = fields.clone();
        fields.remove("hostExecutionConfirmed");
        normalized = Value::Object(fields);
        &normalized
    } else {
        request
    };
    Ok(Sha256::of(
        &serde_json::to_vec(&(path, request)).map_err(|_| sources::invalid("执行请求编码失败"))?,
    )
    .as_str()
    .to_owned())
}

type Probe = Arc<OnceLock<Result<Value>>>;
static PI_PROBES: Mutex<BTreeMap<String, Probe>> = Mutex::new(BTreeMap::new());

fn shared_pi_probe(
    operation: &str,
    protected: &[PathBuf],
    model: Option<&agent_protocol::PiModel>,
    probe: impl FnOnce() -> Result<Value>,
) -> Result<Value> {
    let key = job_fingerprint(operation, &json!([protected, model]))?;
    let inflight = {
        let mut probes = PI_PROBES
            .lock()
            .map_err(|_| sources::invalid("Pi 探测状态失效"))?;
        probes.entry(key.clone()).or_default().clone()
    };
    let result = inflight.get_or_init(probe).clone();
    let mut probes = PI_PROBES
        .lock()
        .map_err(|_| sources::invalid("Pi 探测状态失效"))?;
    if probes
        .get(&key)
        .is_some_and(|previous| Arc::ptr_eq(previous, &inflight))
    {
        probes.remove(&key);
    }
    result
}

impl PlatformApi {
    fn pi_runtime_status(
        &self,
        protected: &[PathBuf],
        model: Option<&agent_protocol::PiModel>,
    ) -> Result<agent_protocol::RuntimeStatusView> {
        let status = agent_protocol::runtime_status(&self.cancel, protected, model);
        #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
        {
            let mut status = status;
            let mut store = jobs::lock_store(&self.jobs.store)?;
            if let Err(error) = super::command_recovery::check_diagnostics(&mut store) {
                status.sandbox_ready = false;
                status.sandbox_message = Some(error.message.clone());
                status.sandbox_issue = Some("commandRecoveryRequired");
                if status.available {
                    status.available = false;
                    status.issue = Some("commandRecoveryRequired".into());
                    status.message = Some(error.message);
                }
            }
            Ok(status.with_capabilities())
        }
        #[cfg(not(all(target_os = "windows", target_arch = "x86_64")))]
        Ok(status.with_capabilities())
    }

    pub fn source_state(&self) -> Result<Value> {
        let (state, mut records, external_links) = {
            let mut store = jobs::lock_store(&self.jobs.store)?;
            (
                store.state().clone(),
                super::progress::load(&mut store)?,
                store.records::<integrations::ExternalLink>("externalLink")?,
            )
        };
        let external_version = Sha256::of(
            &serde_json::to_vec(&external_links)
                .map_err(|_| sources::invalid("研发关联版本编码失败"))?,
        )
        .as_str()
        .to_owned();
        let mut project_ids: std::collections::BTreeSet<String> = records
            .goals
            .iter()
            .flat_map(|goal| {
                goal.goal
                    .participants
                    .iter()
                    .map(|participant| participant.project_id.clone())
            })
            .collect();
        for goal in &records.goals {
            if let Some(spec) = state.specs.iter().find(|spec| spec.id == goal.goal.spec_id) {
                project_ids.insert(spec.project_id.clone());
            }
        }
        let progress_versions = super::progress::current_versions(&state, &project_ids);
        super::progress::refresh_inputs(&mut records, &state, &self.cancel);
        let input_versions = super::progress::input_versions_digest(&records);
        let mut versions = BTreeMap::new();
        for spec in &state.specs {
            let Some(project) = state
                .projects
                .iter()
                .find(|project| project.id == spec.project_id)
            else {
                continue;
            };
            let version = (|| -> Result<String> {
                let root = Root::open(Path::new(&project.root_path))?;
                let text = sources::text(&root, &spec.source_path)?;
                let metadata = super::content::metadata(&text)?;
                let resources =
                    super::content::resolved_resources(spec, metadata.as_ref(), Some(&root));
                let mut paths = vec![spec.source_path.clone()];
                paths.extend(
                    resources
                        .iter()
                        .filter(|resource| resource.target_type == TargetType::Local)
                        .map(|resource| resource.target.clone()),
                );
                paths.sort();
                paths.dedup();
                let mut metadata = Vec::new();
                for path in paths {
                    let relative = RelPath::new(&path)?;
                    match root.source_metadata(&relative) {
                        Ok(file) => metadata.push(json!([
                            path,
                            file.len(),
                            file.modified()
                                .ok()
                                .and_then(|time| time.into_std().duration_since(UNIX_EPOCH).ok())
                                .map(|time| time.as_nanos().to_string())
                        ])),
                        Err(error) => metadata.push(json!([path, error.code])),
                    }
                    if resources.iter().any(|resource| {
                        resource.kind == ResourceKind::Prototype && resource.target == path
                    }) {
                        let directory = path.rsplit_once('/').map(|(directory, _)| directory);
                        let mut stack = vec![(directory.map(RelPath::new).transpose()?, 0)];
                        let mut budget = 10000;
                        while let Some((directory, depth)) = stack.pop() {
                            let (entries, complete) =
                                root.source_entries(directory.as_ref(), &mut budget, &self.cancel)?;
                            if !complete {
                                metadata.push(json!("partial"));
                                break;
                            }
                            for (name, file) in entries {
                                if name.starts_with('.') || name == "node_modules" || name == "src"
                                {
                                    continue;
                                }
                                let child = directory
                                    .as_ref()
                                    .map(|directory| directory.join(&name))
                                    .unwrap_or_else(|| RelPath::new(&name))?;
                                metadata.push(json!([
                                    child.as_str(),
                                    file.len(),
                                    file.modified()
                                        .ok()
                                        .and_then(|time| time
                                            .into_std()
                                            .duration_since(UNIX_EPOCH)
                                            .ok())
                                        .map(|time| time.as_nanos().to_string())
                                ]));
                                if file.is_dir() && depth < 12 {
                                    stack.push((Some(child), depth + 1));
                                }
                            }
                        }
                    }
                }
                root.revalidate()?;
                Ok(Sha256::of(
                    &serde_json::to_vec(&metadata)
                        .map_err(|_| sources::invalid("来源版本编码失败"))?,
                )
                .as_str()
                .into())
            })();
            versions.insert(
                spec.id.clone(),
                version.unwrap_or_else(|error| format!("{:?}", error.code)),
            );
        }
        Ok(
            json!({"revision":state.revision,"sourceVersions":versions,"progressVersions":progress_versions,"inputVersions":input_versions,"externalVersion":external_version,"readAt":sources::now()}),
        )
    }

    pub fn route(
        &self,
        method: &str,
        path: &str,
        query: &str,
        bytes: &[u8],
        request_cancel: Option<&Cancellation>,
    ) -> Result<Option<Value>> {
        let query = query_values(query);
        if method == "POST" && path == "/api/agent/start" {
            let value: Value = decode(bytes)?;
            if query.get("admission").is_some_and(|mode| mode == "async")
                || value.get("host").and_then(Value::as_str) == Some("grok")
                || value
                    .get("handoff")
                    .is_some_and(|handoff| !handoff.is_null())
            {
                let fingerprint = job_fingerprint(path, &value)?;
                return Ok(Some(encode(agent::start_async(
                    self.jobs.clone(),
                    decode(bytes)?,
                    fingerprint,
                )?)?));
            }
        }
        if method == "POST"
            && [
                "/api/agent/start",
                "/api/engineering/execute",
                "/api/external/refresh",
                "/api/backups",
                "/api/restore",
            ]
            .contains(&path)
        {
            let request: Value = decode(bytes)?;
            let id = request
                .get("requestId")
                .and_then(Value::as_str)
                .filter(|id| !id.is_empty() && id.len() <= 128)
                .ok_or_else(|| sources::invalid("执行请求 ID 无效"))?;
            let fingerprint = job_fingerprint(path, &request)?;
            let mut store = jobs::lock_store(&self.jobs.store)?;
            jobs::ensure_request_not_deleted(&mut store, id)?;
            let previous = store.record::<String>("jobRequest", id)?;
            if previous.as_ref().is_some_and(|value| value != &fingerprint) {
                return Err(crate::error::YssError::new(
                    crate::error::ErrorCode::Conflict,
                    "执行请求 ID 已用于不同参数",
                ));
            }
            if let Some(job) = jobs::job_by_request(&mut store, id)? {
                if job.local_project_task.is_some() {
                    return Err(crate::error::YssError::new(
                        crate::error::ErrorCode::Conflict,
                        "请求 ID 已用于本机测试或构建",
                    ));
                }
                if previous.is_some() {
                    return Ok(Some(encode(job)?));
                }
            }
            if previous.is_none() {
                store.put_record("jobRequest", id, &fingerprint)?;
            }
        }
        let parts = path.trim_matches('/').split('/').collect::<Vec<_>>();
        let result = match (method, parts.as_slice()) {
            ("GET", ["api", "desktop", "notification-baseline"]) => {
                jobs::lock_store(&self.jobs.store)?.desktop_notification_baseline()?
            }
            ("GET", ["api", "desktop", "notification-pending-page"]) => {
                let parse = |name: &str| {
                    query
                        .get(name)
                        .ok_or_else(|| sources::invalid("作业通知基线参数缺失"))
                        .and_then(|value| event_cursor(value))
                };
                jobs::lock_store(&self.jobs.store)?.desktop_notification_pending_page(
                    parse("baseline")?,
                    parse("eventCount")?,
                    parse("after")?,
                )?
            }
            ("GET", ["api", "desktop", "notification-events"]) => {
                let after = query
                    .get("after")
                    .map_or(Ok(0), |value| event_cursor(value))?;
                jobs::lock_store(&self.jobs.store)?.desktop_notification_page(after)?
            }
            ("GET", ["api", "desktop", "jobs", id, "pending"]) => {
                if !desktop_identifier(id) {
                    return Err(sources::invalid("作业标识无效"));
                }
                jobs::lock_store(&self.jobs.store)?.desktop_notification_pending(id)?
            }
            ("POST", ["api", "desktop", "flush"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Flush {
                    request_id: String,
                    editor_id: String,
                    ok: bool,
                }
                let request: Flush = decode(bytes)?;
                if !desktop_identifier(&request.request_id)
                    || !desktop_identifier(&request.editor_id)
                {
                    return Err(sources::invalid("桌面草稿回执身份无效"));
                }
                let mut receipts = DESKTOP_FLUSH
                    .get_or_init(Default::default)
                    .lock()
                    .map_err(|_| sources::invalid("桌面草稿回执不可用"))?;
                receipts.retain(|_, (_, _, at)| at.elapsed() < Duration::from_secs(60));
                if receipts.len() >= 128 && !receipts.contains_key(&request.request_id) {
                    return Err(sources::invalid("桌面草稿回执数量超限"));
                }
                receipts.insert(
                    request.request_id,
                    (request.editor_id, request.ok, Instant::now()),
                );
                json!({"recorded":true})
            }
            ("GET", ["api", "desktop", "flush", id]) => {
                if !desktop_identifier(id) {
                    return Err(sources::invalid("桌面草稿回执身份无效"));
                }
                let mut receipts = DESKTOP_FLUSH
                    .get_or_init(Default::default)
                    .lock()
                    .map_err(|_| sources::invalid("桌面草稿回执不可用"))?;
                receipts.retain(|_, (_, _, at)| at.elapsed() < Duration::from_secs(60));
                match receipts.get(*id) {
                    Some((editor_id, ok, _)) => json!({"found":true,"editorId":editor_id,"ok":ok}),
                    None => json!({"found":false}),
                }
            }
            ("GET", ["api", "receipts"]) | ("GET", ["api", "receipts", _]) => {
                let id = parts
                    .get(2)
                    .copied()
                    .or_else(|| query.get("requestId").map(String::as_str))
                    .filter(|id| !id.is_empty() && id.len() <= 128)
                    .ok_or_else(|| sources::invalid("请求 ID 无效"))?;
                let mut store = jobs::lock_store(&self.jobs.store)?;
                if let Some(receipt) = store.record::<Value>("mutationReceipt", id)? {
                    json!({"found":true,"result":receipt["result"],"requestDigest":receipt["request_digest"]})
                } else if let Some(job) = jobs::job_by_request(&mut store, id)? {
                    json!({"found":true,"result":job})
                } else {
                    json!({"found":false})
                }
            }
            ("POST", ["api", "receipts", id, "match"]) => {
                let request = decode::<super::model::MutationRequest>(bytes)?;
                let mut store = jobs::lock_store(&self.jobs.store)?;
                super::service::match_receipt(&mut store, id, request)?
            }
            ("POST", ["api", "receipts", id, "ack"]) => {
                if id.len() > 128 {
                    return Err(sources::invalid("请求 ID 无效"));
                }
                jobs::lock_store(&self.jobs.store)?.put_record(
                    "requestAck",
                    id,
                    &sources::now(),
                )?;
                json!({"acknowledged":true})
            }
            ("GET", ["api", "requests"]) => {
                let mut store = jobs::lock_store(&self.jobs.store)?;
                let acknowledged = store
                    .record_entries::<String>("requestAck")?
                    .into_iter()
                    .map(|(id, _)| id)
                    .collect::<std::collections::BTreeSet<_>>();
                let mut items = BTreeMap::new();
                for (id, receipt) in store.record_entries::<Value>("mutationReceipt")? {
                    if !acknowledged.contains(&id) {
                        items.insert(id.clone(),json!({"requestId":id,"kind":"mutation","status":"completed","result":receipt["result"]}));
                    }
                }
                for (id, _) in store.record_entries::<String>("jobRequest")? {
                    if !acknowledged.contains(&id) {
                        items.entry(id.clone()).or_insert_with(
                            || json!({"requestId":id,"kind":"job","status":"unconfirmed"}),
                        );
                    }
                }
                for job in store.records::<super::jobs::JobRecord>("job")? {
                    if !acknowledged.contains(&job.request_id) {
                        items.insert(job.request_id.clone(),json!({"requestId":job.request_id,"kind":job.kind,"status":job.status,"jobId":job.id,"result":job.result,"createdAt":job.created_at,"updatedAt":job.updated_at}));
                    }
                }
                json!({"total":items.len(),"items":items.into_values().rev().take(200).collect::<Vec<_>>()})
            }
            ("POST", ["api", "targets"]) => {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct Request {
                    action: Action,
                }
                let request: Request = decode(bytes)?;
                drafts::target(jobs::lock_store(&self.jobs.store)?.state(), &request.action)?
            }
            ("GET", ["api", "drafts"]) => encode(drafts::list(
                &mut *jobs::lock_store(&self.jobs.store)?,
                query.get("targetType").map(String::as_str),
                query.get("targetId").map(String::as_str),
            )?)?,
            ("GET", ["api", "drafts", id]) => {
                json!(jobs::lock_store(&self.jobs.store)?.record::<drafts::Draft>("draft", id)?)
            }
            ("PUT", ["api", "drafts", id]) => encode(drafts::save(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                decode(bytes)?,
            )?)?,
            ("DELETE", ["api", "drafts", id]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    expected_revision: u64,
                }
                let request: Request = decode(bytes)?;
                drafts::delete(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    request.expected_revision,
                )?;
                json!({"deleted":true})
            }
            ("GET", ["api", "jobs"]) => encode(self.jobs.list()?)?,
            ("GET", ["api", "jobs", id]) => encode(self.jobs.get(id)?)?,
            ("GET", ["api", "jobs", id, "activities"]) => {
                let before = query
                    .get("before")
                    .map(|value| event_cursor(value))
                    .transpose()?;
                if query.get("view").is_some_and(|view| view == "conversation") {
                    super::activities::conversation(&self.jobs, id, before)?
                } else {
                    super::activities::page(&self.jobs, id, before)?
                }
            }
            ("GET", ["api", "jobs", id, "activities", first]) => {
                let first = event_cursor(first)?;
                if first == 0 {
                    return Err(sources::invalid("活动编号无效"));
                }
                super::activities::detail(&self.jobs, id, first)?
            }
            ("GET", ["api", "jobs", id, "event-page"]) => {
                self.jobs.get(id)?;
                let after = query
                    .get("after")
                    .map(|value| event_cursor(value))
                    .transpose()?
                    .unwrap_or(0);
                let mut store = jobs::lock_store(&self.jobs.store)?;
                let items = store.events(id, after)?;
                let next_after = items.last().map(|event| event.id).unwrap_or(after);
                let has_more = store.has_events_after(id, next_after)?;
                json!({"hasMore":has_more,"nextAfter":next_after,"items":items})
            }
            ("POST", ["api", "jobs", id, "cancel"]) => encode(self.jobs.cancel(id)?)?,
            ("POST", ["api", "jobs", id, "input"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    request_id: String,
                    value: Value,
                }
                let request: Request = decode(bytes)?;
                self.jobs.input(id, &request.request_id, request.value)?;
                json!({"accepted":true})
            }
            ("GET", ["api", "jobs", id, "events"]) => encode(
                jobs::lock_store(&self.jobs.store)?
                    .events(id, number(&query, "after", 0)? as u64)?,
            )?,
            ("GET", ["api", "workbench", "catalog"]) => {
                let catalog = read_model::catalog(jobs::lock_store(&self.jobs.store)?.state());
                encode(catalog)?
            }
            ("GET", ["api", "projects", id, "summary"]) => encode(
                read_model::read_project_summary(&self.jobs.store, id, &self.cancel)?,
            )?,
            ("GET", ["api", "projects", id, "directory-status"]) => encode(
                read_model::read_project_directory_status(&self.jobs.store, id)?,
            )?,
            ("GET", ["api", "specs", id, "detail"]) => encode(read_model::read_spec_detail(
                &self.jobs.store,
                &self.reads,
                id,
                query.get("fresh").is_some_and(|value| value == "1"),
                &self.cancel,
            )?)?,
            ("GET", ["api", "workbench", "overview"]) => {
                let (base, published) = {
                    let store = jobs::lock_store(&self.jobs.store)?;
                    (
                        read_model::overview_base(store.state(), store.projection_stamp()),
                        self.reads.latest(),
                    )
                };
                published.overview(base)
            }
            ("GET", ["api", "workbench", "jobs"]) => self.reads.latest().jobs(),
            ("POST", ["api", "workbench", "refresh"]) => {
                let minimum_observation_id = self.reads.request_refresh();
                json!({"requested": true, "minimumObservationId": minimum_observation_id})
            }
            ("POST", ["api", "work-items"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    query: WorkQuery,
                    #[serde(default)]
                    offset: usize,
                    limit: Option<usize>,
                    #[serde(default)]
                    compact: bool,
                    expected_projection_version: Option<String>,
                    expected_query_version: Option<String>,
                    board: Option<read_model::BoardRequest>,
                }
                let request: Request = decode(bytes)?;
                let default_limit = if request.compact && request.board.is_some() {
                    20
                } else {
                    50
                };
                let limit = request.limit.unwrap_or(default_limit).clamp(1, 200);
                if request.compact {
                    let (stamp, published) = {
                        let mut store = jobs::lock_store(&self.jobs.store)?;
                        store.capture_read_records(&[])?;
                        (store.projection_stamp(), self.reads.latest())
                    };
                    published.work_items(
                        stamp,
                        &request.query,
                        (request.offset, limit),
                        request.expected_projection_version.as_deref(),
                        request.expected_query_version.as_deref(),
                        request.board.as_ref(),
                    )?
                } else {
                    if request.board.is_some()
                        || request.expected_projection_version.is_some()
                        || request.expected_query_version.is_some()
                    {
                        return Err(sources::invalid("分页版本和列只适用于轻量规格查询"));
                    }
                    let inputs = read_model::capture(&mut *jobs::lock_store(&self.jobs.store)?)?;
                    let snapshot = read_model::build_snapshot(inputs, None, &self.cancel)?;
                    let mut result = super::query::query(&snapshot, &request.query)?;
                    result.items = result
                        .items
                        .into_iter()
                        .skip(request.offset)
                        .take(limit)
                        .collect();
                    encode(result)?
                }
            }
            ("GET", ["api", "history"]) => {
                let store = jobs::lock_store(&self.jobs.store)?;
                let spec_id = query.get("specId");
                let project_id = query.get("projectId");
                let events = store
                    .state()
                    .history
                    .iter()
                    .rev()
                    .filter(|event| {
                        spec_id.is_none_or(|id| event.spec_id.as_ref() == Some(id))
                            && project_id.is_none_or(|id| event.project_id == *id)
                    })
                    .collect::<Vec<_>>();
                json!({"total":events.len(),"items":events.into_iter().skip(number(&query,"offset",0)?).take(number(&query,"limit",50)?.clamp(1,200)).collect::<Vec<_>>()})
            }
            ("GET", ["api", "desktop", "tool-integrations"]) => {
                // Read only each registered active project's tracker file, with a bounded
                // count. Unavailable projects are reported as incomplete, never inferred.
                let (roots, protected) = {
                    let store = jobs::lock_store(&self.jobs.store)?;
                    (
                        store
                            .state()
                            .projects
                            .iter()
                            .filter(|project| project.active)
                            .map(|project| project.root_path.clone())
                            .collect::<Vec<_>>(),
                        agent::pi_protected_paths(&store),
                    )
                };
                let mut providers = std::collections::BTreeSet::new();
                let mut complete = roots.len() <= 64;
                for root in roots.iter().take(64) {
                    match integrations::tracker(root) {
                        Ok(tracker) => {
                            for key in ["platform", "provider"] {
                                if let Some(value) = tracker
                                    .pointer(&format!("/fields/{key}"))
                                    .and_then(Value::as_str)
                                {
                                    let value = value.to_ascii_lowercase();
                                    if value == "github" || value == "gitlab" {
                                        providers.insert(value);
                                    }
                                }
                            }
                        }
                        Err(_) => complete = false,
                    }
                }
                let canonical = protected
                    .into_iter()
                    .map(std::fs::canonicalize)
                    .collect::<std::io::Result<Vec<_>>>();
                let protected_complete = canonical
                    .as_ref()
                    .is_ok_and(|paths| paths.iter().all(|path| path.to_str().is_some()));
                let protected = canonical
                    .unwrap_or_default()
                    .into_iter()
                    .filter_map(|path| path.to_str().map(str::to_owned))
                    .collect::<Vec<_>>();
                json!({"providers": providers, "complete": complete, "protectedComplete": protected_complete, "protectedPaths": protected})
            }
            ("GET", ["api", "system"]) => {
                let (data_dir, usage, protected) = {
                    let store = jobs::lock_store(&self.jobs.store)?;
                    let protected = agent::pi_protected_paths(&store);
                    (
                        store.data_dir().to_owned(),
                        store.storage_usage()?,
                        protected,
                    )
                };
                let path = std::env::var_os("PATH").unwrap_or_default();
                let tools = super::process::probe_system_tools(&path, &self.cancel, false);
                let executable = std::env::current_exe()?;
                let pi = self.pi_runtime_status(&protected, None)?;
                json!({"version":BuildInfo::current(),"dataDir":data_dir,"executable":executable,"storage":usage,"databaseBytes":std::fs::metadata(data_dir.join(super::storage_migration::DATABASE_NAME)).map(|metadata|metadata.len()).unwrap_or(0),"tools":tools,"pi":pi})
            }
            ("POST", ["api", "backups"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    target_path: String,
                    request_id: String,
                }
                let request: Request = decode(bytes)?;
                encode(backup::create(
                    self.jobs.clone(),
                    request.target_path,
                    request.request_id,
                )?)?
            }
            ("POST", ["api", "restore"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    archive_path: String,
                    target_path: String,
                    request_id: String,
                }
                let request: Request = decode(bytes)?;
                encode(backup::restore(
                    self.jobs.clone(),
                    request.archive_path,
                    request.target_path,
                    request.request_id,
                )?)?
            }
            ("GET", ["api", "specs", id, "view"]) => {
                let inputs = read_model::capture(&mut *jobs::lock_store(&self.jobs.store)?)?;
                let snapshot = read_model::build_snapshot(inputs, Some(id), &self.cancel)?;
                encode(
                    snapshot
                        .specs
                        .into_iter()
                        .find(|spec| spec.record.id == *id)
                        .ok_or_else(|| sources::missing("Spec 不存在"))?,
                )?
            }
            ("GET", ["api", "specs", id, "activity"]) => {
                sources::spec(jobs::lock_store(&self.jobs.store)?.state(), id)?;
                let status = self.reads.latest();
                json!({"items": status.run_activities(
                    id,
                    query.get("goalVersion").map(String::as_str),
                    query.get("specDigest").map(String::as_str),
                )})
            }
            ("GET", ["api", "specs", id, "confirmations"]) => {
                artifacts::confirmations(&mut *jobs::lock_store(&self.jobs.store)?, id)?
            }
            ("POST", ["api", "confirmations", id, "preview"]) => {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct Request {
                    target: String,
                }
                let request: Request = decode(bytes)?;
                let root = {
                    let mut store = jobs::lock_store(&self.jobs.store)?;
                    let (file, _) = artifacts::historical_file(&mut store, id, &request.target)?;
                    if file.kind != "prototype" || file.mime != "text/html" {
                        return Err(sources::invalid("请选择此确认版本中的静态HTML原型入口"));
                    }
                    artifacts::historical_root(&mut store, id)?
                };
                encode(super::preview::start(
                    root.to_str()
                        .ok_or_else(|| sources::invalid("历史预览目录不是UTF-8"))?,
                    &request.target,
                    &self.origin,
                    &self.cancel,
                )?)?
            }
            ("GET", ["api", "confirmations", id, "artifacts"]) => {
                artifacts::manifest(&mut *jobs::lock_store(&self.jobs.store)?, id)?
            }
            ("POST", ["api", "confirmations", id, "snapshot"]) => {
                artifacts::backfill_snapshot(&self.jobs.store, id)?
            }
            ("POST", ["api", "specs", id, "diff"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    base_confirmation_id: String,
                    path: Option<String>,
                    #[serde(default)]
                    basis: artifacts::ComparisonBasis,
                }
                let request: Request = decode(bytes)?;
                artifacts::diff(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    &request.base_confirmation_id,
                    request.path.as_deref(),
                    request.basis,
                )?
            }
            ("GET", ["api", "engineering", "catalog"]) => engineering::catalog(&self.bundle)?,
            ("POST", ["api", "engineering", "inspect"]) => {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct Request {
                    root: String,
                }
                engineering::inspect(&self.bundle, &decode::<Request>(bytes)?.root)?
            }
            ("POST", ["api", "engineering", "preview"]) => encode(engineering::preview(
                &self.jobs.store,
                &self.bundle,
                decode(bytes)?,
            )?)?,
            ("POST", ["api", "engineering", "execute"]) => encode(engineering::start(
                self.jobs.clone(),
                self.bundle.clone(),
                decode(bytes)?,
            )?)?,
            ("GET", ["api", "homes"]) => integrations::homes(&self.jobs.store)?,
            ("POST", ["api", "home", "import"]) => {
                encode(integrations::import_home(&self.jobs.store, decode(bytes)?)?)?
            }
            ("POST", ["api", "home", "bind"]) => {
                integrations::bind_home(&self.jobs.store, decode(bytes)?)?
            }
            ("GET", ["api", "merge-requests", "sources"]) => encode(merge_requests::discover(
                &self.jobs.store,
                request_cancel.unwrap_or(&self.cancel),
            )?)?,
            ("PUT", ["api", "merge-requests", "sources"]) => encode(merge_requests::save(
                &self.jobs.store,
                decode(bytes)?,
                &self.cancel,
            )?)?,
            ("POST", ["api", "merge-requests", "page"]) => encode(merge_requests::page(
                &self.jobs.store,
                decode(bytes)?,
                request_cancel.unwrap_or(&self.cancel),
            )?)?,
            ("POST", ["api", "merge-requests", "detail"]) => encode(merge_requests::detail(
                &self.jobs.store,
                decode(bytes)?,
                request_cancel.unwrap_or(&self.cancel),
            )?)?,
            ("GET", ["api", "external"]) => encode(integrations::external_list(&self.jobs.store)?)?,
            ("POST", ["api", "external"]) => encode(integrations::external_add(
                &self.jobs.store,
                decode(bytes)?,
            )?)?,
            ("POST", ["api", "external", "refresh"]) => encode(integrations::external_refresh(
                self.jobs.clone(),
                decode(bytes)?,
            )?)?,
            ("DELETE", ["api", "external", id]) => {
                jobs::lock_store(&self.jobs.store)?.delete_record("externalLink", id)?;
                json!({"deleted":true})
            }
            ("POST", ["api", "agent", "start"]) => {
                encode(agent::start(self.jobs.clone(), decode(bytes)?)?)?
            }
            ("GET", ["api", "specs", id, "agent-projects"]) => {
                integrations::agent_projects(&self.jobs.store, id, &self.cancel)?
            }
            ("GET", ["api", "specs", id, "agent-handoff"]) => {
                let source = agent_scope::ContinuationSource {
                    run_id: query
                        .get("runId")
                        .cloned()
                        .ok_or_else(|| sources::invalid("缺少来源会话"))?,
                    job_id: query
                        .get("jobId")
                        .cloned()
                        .ok_or_else(|| sources::invalid("缺少来源轮次"))?,
                };
                let host = query
                    .get("host")
                    .ok_or_else(|| sources::invalid("请选择交接目标 CLI"))?;
                agent_scope::handoff_preview(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    &source,
                    agent::AgentHost::parse(Some(host))?,
                )?
            }
            ("POST", ["api", "specs", id, "agent-handoff"]) => agent_scope::confirm_handoff(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                decode(bytes)?,
            )?,
            ("GET", ["api", "specs", id, "agent-continuation"]) => {
                let source = agent_scope::ContinuationSource {
                    run_id: query
                        .get("runId")
                        .cloned()
                        .ok_or_else(|| sources::invalid("缺少来源会话"))?,
                    job_id: query
                        .get("jobId")
                        .cloned()
                        .ok_or_else(|| sources::invalid("缺少来源轮次"))?,
                };
                encode(agent_scope::preview(
                    &self.jobs.store,
                    id,
                    &source,
                    request_cancel.unwrap_or(&self.cancel),
                )?)?
            }
            ("POST", ["api", "specs", id, "agent-continuation", "draft"]) => agent_scope::draft(
                &self.jobs.store,
                id,
                decode(bytes)?,
                request_cancel.unwrap_or(&self.cancel),
            )?,
            ("POST", ["api", "specs", id, "agent-ci-job", "preview"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Preview {
                    project_id: String,
                    link_id: String,
                    job_ref: String,
                }
                let request: Preview = decode(bytes)?;
                ci_job::preview(
                    &self.jobs.store,
                    id,
                    &request.project_id,
                    &request.link_id,
                    &request.job_ref,
                    &self.cancel,
                )?
            }
            ("GET", ["api", "specs", id, "agent-sessions"]) => {
                if query.get("compact").is_some_and(|value| value == "1") {
                    agent::reads::sessions(&self.jobs, &self.reads, id, &query, &self.cancel)?
                } else {
                    agent::sessions(&self.jobs, id)?
                }
            }
            ("GET", ["api", "agent", "models"]) => {
                let protected = agent::pi_protected_paths(&*jobs::lock_store(&self.jobs.store)?);
                if agent::AgentHost::parse(query.get("host").map(String::as_str))?
                    == agent::AgentHost::Grok
                {
                    let models = grok::models(request_cancel.unwrap_or(&self.cancel), &protected)?;
                    json!({"models": models})
                } else {
                    shared_pi_probe("models", &protected, None, || {
                        let models = agent_protocol::available_models(&self.cancel, &protected)?;
                        Ok(json!({"models": models}))
                    })?
                }
            }
            ("GET", ["api", "agent", "runtime"]) => {
                let host = agent::AgentHost::parse(query.get("host").map(String::as_str))?;
                let selected = match (query.get("selectedProvider"), query.get("selectedModel")) {
                    (None, None) => None,
                    (Some(provider), Some(id)) => Some(
                        agent_protocol::PiModel::new(provider, id)
                            .ok_or_else(|| sources::invalid("Agent 供应商或模型 ID 无效"))?,
                    ),
                    _ => return Err(sources::invalid("请选择完整的 Agent 供应商和模型")),
                };
                let (protected, model, resume_identity) = agent::runtime_target_for(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    query.get("resumeRunId").map(String::as_str),
                    selected,
                    host,
                )?;
                if host == agent::AgentHost::Grok {
                    let mut status = grok::probe(
                        request_cancel.unwrap_or(&self.cancel),
                        &protected,
                        model.as_ref(),
                    );
                    status["host"] = json!(host);
                    if let Some(expected) = resume_identity
                        && status["available"] == true
                        && status["identity"].as_str() != Some(&expected)
                    {
                        status["available"] = json!(false);
                        status["issue"] = json!("resumeIdentityChanged");
                        status["message"] = json!(
                            "Grok Build 的登录或安装已变化。切回原配置后重新检测，或从 Spec 新建会话。"
                        );
                    }
                    status
                } else {
                    shared_pi_probe("runtime", &protected, model.as_ref(), || {
                        encode(self.pi_runtime_status(&protected, model.as_ref())?)
                    })?
                }
            }
            ("POST", ["api", "agent", "test-model"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    resume_run_id: Option<String>,
                    selected_model: Option<agent_protocol::PiModel>,
                    expected_model: agent_protocol::PiModel,
                }
                let request: Request = decode(bytes)?;
                let (protected, model) = agent::runtime_target(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    request.resume_run_id.as_deref(),
                    request.selected_model,
                )?;
                agent_protocol::test_model(
                    &self.cancel,
                    &protected,
                    model.as_ref(),
                    &request.expected_model,
                )?;
                json!({"connected": true})
            }
            ("GET", ["api", "agent", id]) => agent::view(&self.jobs.store, id, &self.cancel)?,
            ("GET", ["api", "agent", id, "local-tasks"]) => {
                agent::local_tasks::list(&mut *jobs::lock_store(&self.jobs.store)?, id)?
            }
            ("POST", ["api", "agent", id, "local-tasks"]) => encode(agent::local_tasks::start(
                self.jobs.clone(),
                id,
                decode(bytes)?,
            )?)?,
            ("POST", ["api", "agent", id, "queue", "receipt"]) => {
                let request: agent::queue::Draft = decode(bytes)?;
                // Verify the queue target exists before consulting its idempotency record.
                let _ = agent::queue::view(&self.jobs, id)?;
                let (key, expected) = agent::queue::enqueue_receipt(id, &request)?
                    .ok_or_else(|| sources::invalid("队列请求 ID 无效"))?;
                let receipt = jobs::lock_store(&self.jobs.store)?
                    .record::<String>("agentQueueRequest", &key)?;
                json!({"found":receipt.is_some(),"matches":receipt.as_ref().map(|value| value == &expected)})
            }
            ("GET", ["api", "agent", id, "queue"]) => encode(agent::queue::view(&self.jobs, id)?)?,
            ("POST", ["api", "agent", id, "queue"]) => {
                encode(agent::queue::put(&self.jobs, id, None, decode(bytes)?)?)?
            }
            ("PATCH", ["api", "agent", id, "queue", entry]) => encode(agent::queue::put(
                &self.jobs,
                id,
                Some(entry),
                decode(bytes)?,
            )?)?,
            ("DELETE", ["api", "agent", id, "queue", entry]) => {
                encode(agent::queue::remove(&self.jobs, id, entry, decode(bytes)?)?)?
            }
            ("POST", ["api", "agent", id, "queue", "resume"]) => {
                encode(agent::queue::resume(&self.jobs, id, decode(bytes)?)?)?
            }
            ("DELETE", ["api", "agent", id]) => {
                agent::delete_session(&self.jobs, id, decode(bytes)?)?
            }
            ("GET", ["api", "agent", id, "rounds"]) => {
                agent::rounds(&mut *jobs::lock_store(&self.jobs.store)?, id)?
            }
            ("POST", ["api", "agent", id, "document"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    spec_id: String,
                    path: String,
                }
                let request: Request = decode(bytes)?;
                let state = agent::artifact_state(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    query.get("jobId").map(String::as_str),
                    &request.spec_id,
                )?;
                encode(super::registration::read_document(
                    &state,
                    &request.spec_id,
                    &request.path,
                )?)?
            }
            ("GET", ["api", "agent", id, "specs", spec_id, "view"]) => {
                let mut store = jobs::lock_store(&self.jobs.store)?;
                let state = agent::artifact_state(
                    &mut store,
                    id,
                    query.get("jobId").map(String::as_str),
                    spec_id,
                )?;
                let spec = sources::spec(&state, spec_id)?;
                let project = sources::project(&state, &spec.project_id)?;
                let root = Root::open(std::path::Path::new(&project.root_path));
                let mut view = sources::spec_view(&state, spec, &root, None);
                view.content = super::content::read_view(&state, spec_id)?;
                encode(view)?
            }
            ("POST", ["api", "agent", id, "preview"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    spec_id: String,
                    target: String,
                }
                let request: Request = decode(bytes)?;
                let (root, target) = {
                    let mut store = jobs::lock_store(&self.jobs.store)?;
                    let state = agent::artifact_state(
                        &mut store,
                        id,
                        query.get("jobId").map(String::as_str),
                        &request.spec_id,
                    )?;
                    let document = sources::read_source(&state, &request.spec_id)?;
                    let metadata = super::content::metadata(&document.text)?;
                    let spec = sources::spec(&state, &request.spec_id)?;
                    let project = sources::project(&state, &spec.project_id)?;
                    let path = std::path::PathBuf::from(&project.root_path);
                    let root = Root::open(&path)?;
                    let target = super::content::preview_target(
                        spec,
                        metadata.as_ref(),
                        &root,
                        &request.target,
                    )
                    .ok_or_else(|| sources::invalid("只能预览此运行已关联的静态原型"))?;
                    (path, target)
                };
                encode(super::preview::start(
                    root.to_str()
                        .ok_or_else(|| sources::invalid("运行目录不是UTF-8"))?,
                    &target,
                    &self.origin,
                    &self.cancel,
                )?)?
            }
            ("GET", ["api", "agent", id, "diff"]) => {
                let job_id = query.get("jobId").map(String::as_str);
                if query.get("compact").is_some_and(|value| value == "1") {
                    agent::compact_round_diff(&self.jobs.store, id, job_id, &self.cancel)?
                } else {
                    encode(agent::round_diff(&self.jobs.store, id, job_id)?)?
                }
            }
            ("GET", ["api", "agent", id, "diff", "file"]) => {
                let required = |key| {
                    query
                        .get(key)
                        .filter(|value| !value.is_empty())
                        .map(String::as_str)
                        .ok_or_else(|| sources::invalid("请指定差异版本、轮次、工程和文件"))
                };
                agent::round_diff_file(
                    &self.jobs.store,
                    id,
                    agent::FileDiffQuery {
                        job_id: required("jobId")?,
                        project_id: required("projectId")?,
                        path: required("path")?,
                        review_token: required("reviewToken")?,
                        full: query.get("full").is_some_and(|value| value == "1"),
                    },
                    &self.cancel,
                )?
            }
            ("GET", ["api", "agent", id, "acceptance"]) => agent::acceptance_view(
                &self.jobs.store,
                id,
                None,
                query.get("attemptId").map(String::as_str),
                false,
            )?,
            ("POST", ["api", "agent", id, "verify-acceptance"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    job_id: String,
                    attempt_id: Option<String>,
                }
                let request: Request = decode(bytes)?;
                agent::acceptance_view(
                    &self.jobs.store,
                    id,
                    Some(&request.job_id),
                    request.attempt_id.as_deref(),
                    true,
                )?
            }
            ("GET", ["api", "agent", id, "rebind-preview"]) => {
                encode(agent::rebind_preview(&self.jobs.store, id)?)?
            }
            ("POST", ["api", "agent", id, "rebind"]) => {
                agent::rebind(&self.jobs.store, id, decode(bytes)?)?
            }
            ("POST", ["api", "agent", id, "accept"]) => {
                agent::accept_shared(&self.jobs.store, id, decode(bytes)?, &self.cancel)?
            }
            (
                "GET",
                [
                    "api",
                    "agent",
                    id,
                    "specs",
                    spec_id,
                    kind @ ("source" | "content"),
                ],
            ) => {
                let state = agent::artifact_state(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    query.get("jobId").map(String::as_str),
                    spec_id,
                )?;
                if *kind == "source" {
                    encode(sources::read_source(&state, spec_id)?)?
                } else {
                    encode(super::content::read_view(&state, spec_id)?)?
                }
            }
            ("POST", ["api", "agent", id, "respond"]) => encode(agent::respond(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                decode(bytes)?,
            )?)?,
            ("GET", ["api", "source-state"]) => {
                if query.get("compact").is_some_and(|value| value == "1") {
                    self.reads.latest().change_state()
                } else {
                    self.source_state()?
                }
            }
            _ => return Ok(None),
        };
        Ok(Some(result))
    }
}
