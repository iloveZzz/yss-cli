use super::{
    agent, agent_protocol, artifacts, backup, drafts, engineering, integrations,
    jobs::{self, JobManager},
    model::*,
    service, sources,
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
    path::Path,
    sync::Arc,
    time::{Duration, UNIX_EPOCH},
};

#[derive(Clone)]
pub struct PlatformApi {
    pub jobs: Arc<JobManager>,
    pub bundle: Arc<[u8]>,
    pub cancel: Cancellation,
    pub origin: String,
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

impl PlatformApi {
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
    ) -> Result<Option<Value>> {
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
            let fingerprint = Sha256::of(
                &serde_json::to_vec(&(path, &request))
                    .map_err(|_| sources::invalid("执行请求编码失败"))?,
            )
            .as_str()
            .to_owned();
            let mut store = jobs::lock_store(&self.jobs.store)?;
            if let Some(previous) = store.record::<String>("jobRequest", id)? {
                if previous != fingerprint {
                    return Err(crate::error::YssError::new(
                        crate::error::ErrorCode::Conflict,
                        "执行请求 ID 已用于不同参数",
                    ));
                }
                if let Some(job) = store
                    .records::<super::jobs::JobRecord>("job")?
                    .into_iter()
                    .find(|job| job.request_id == id)
                {
                    return Ok(Some(encode(job)?));
                }
            } else {
                store.put_record("jobRequest", id, &fingerprint)?;
            }
        }
        let query = query_values(query);
        let parts = path.trim_matches('/').split('/').collect::<Vec<_>>();
        let result = match (method, parts.as_slice()) {
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
                } else if let Some(job) = store
                    .records::<super::jobs::JobRecord>("job")?
                    .into_iter()
                    .find(|job| job.request_id == id)
                {
                    json!({"found":true,"result":job})
                } else {
                    json!({"found":false})
                }
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
            ("POST", ["api", "work-items"]) => {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct Request {
                    query: WorkQuery,
                    #[serde(default)]
                    offset: usize,
                    #[serde(default = "page_size")]
                    limit: usize,
                }
                fn page_size() -> usize {
                    50
                }
                let request: Request = decode(bytes)?;
                let snapshot = service::snapshot_from_store(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    &self.cancel,
                )?;
                let mut result = super::query::query(&snapshot, &request.query)?;
                result.items = result
                    .items
                    .into_iter()
                    .skip(request.offset)
                    .take(request.limit.clamp(1, 200))
                    .collect();
                encode(result)?
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
                let mut tools = Vec::new();
                for program in ["git", "gh", "glab"] {
                    let result = super::process::run(
                        program,
                        &["--version".into()],
                        &self.cancel,
                        Duration::from_secs(5),
                    );
                    tools.push(match result {Ok(bytes)=>json!({"name":program,"available":true,"version":String::from_utf8_lossy(&bytes).lines().next().unwrap_or("")}),Err(error)=>json!({"name":program,"available":false,"message":error.message})});
                }
                let executable = std::env::current_exe()?;
                let pi = agent_protocol::runtime_status(&self.cancel, &protected, None);
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
                let snapshot = service::snapshot_from_store(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    &self.cancel,
                )?;
                encode(
                    snapshot
                        .specs
                        .into_iter()
                        .find(|spec| spec.record.id == *id)
                        .ok_or_else(|| sources::missing("Spec 不存在"))?,
                )?
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
            ("GET", ["api", "agent", "models"]) => {
                let protected = agent::pi_protected_paths(&*jobs::lock_store(&self.jobs.store)?);
                let models = agent_protocol::available_models(&self.cancel, &protected)?;
                json!({"models": models})
            }
            ("GET", ["api", "agent", "runtime"]) => {
                let selected = match (query.get("selectedProvider"), query.get("selectedModel")) {
                    (None, None) => None,
                    (Some(provider), Some(id)) => Some(
                        agent_protocol::PiModel::new(provider, id)
                            .ok_or_else(|| sources::invalid("Pi 供应商或模型 ID 无效"))?,
                    ),
                    _ => return Err(sources::invalid("请选择完整的 Pi 供应商和模型")),
                };
                let (protected, model) = agent::runtime_target(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    query.get("resumeRunId").map(String::as_str),
                    selected,
                )?;
                encode(agent_protocol::runtime_status(
                    &self.cancel,
                    &protected,
                    model.as_ref(),
                ))?
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
            ("GET", ["api", "agent", id]) => {
                agent::view(&mut *jobs::lock_store(&self.jobs.store)?, id)?
            }
            ("POST", ["api", "agent", id, "document"]) => {
                #[derive(Deserialize)]
                #[serde(rename_all = "camelCase", deny_unknown_fields)]
                struct Request {
                    spec_id: String,
                    path: String,
                }
                let request: Request = decode(bytes)?;
                encode(agent::document(
                    &mut *jobs::lock_store(&self.jobs.store)?,
                    id,
                    &request.spec_id,
                    &request.path,
                )?)?
            }
            ("GET", ["api", "agent", id, "specs", spec_id, "view"]) => {
                let mut store = jobs::lock_store(&self.jobs.store)?;
                let original = sources::spec(store.state(), spec_id)?.clone();
                let root = agent::project_root(&mut store, id, &original.project_id)?;
                let mut state = store.state().clone();
                let project = state
                    .projects
                    .iter_mut()
                    .find(|project| project.id == original.project_id)
                    .ok_or_else(|| sources::invalid("运行工程不存在"))?;
                project.root_path = root.to_string_lossy().into_owned();
                let mut view = sources::spec_view(&state, &original, &Root::open(&root), None);
                view.content = agent::content(&mut store, id, spec_id)?;
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
                    let document = agent::source(&mut store, id, &request.spec_id)?;
                    let metadata = super::content::metadata(&document.text)?;
                    let spec = sources::spec(store.state(), &request.spec_id)?;
                    let project_id = spec.project_id.clone();
                    let path = agent::project_root(&mut store, id, &project_id)?;
                    let root = Root::open(&path)?;
                    let spec = sources::spec(store.state(), &request.spec_id)?;
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
                encode(agent::diff_shared(&self.jobs.store, id)?)?
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
            ("GET", ["api", "agent", id, "specs", spec_id, "source"]) => encode(agent::source(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                spec_id,
            )?)?,
            ("GET", ["api", "agent", id, "specs", spec_id, "content"]) => encode(agent::content(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                spec_id,
            )?)?,
            ("POST", ["api", "agent", id, "respond"]) => encode(agent::respond(
                &mut *jobs::lock_store(&self.jobs.store)?,
                id,
                decode(bytes)?,
            )?)?,
            ("GET", ["api", "source-state"]) => self.source_state()?,
            _ => return Ok(None),
        };
        Ok(Some(result))
    }
}
