use super::{
    AgentHost, AgentRun, JobManager, JobRecord, Store, WorkspaceMode, acceptance, accepted_result,
    deletion_issue, invalid, last_accepted_file, lock_store, review_paths, sources, terminal,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{activities, read_model},
};
use serde::{Deserialize, Serialize, de::IgnoredAny};
use serde_json::{Value, json};
use std::{collections::BTreeMap, sync::Arc};

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct StoredRun {
    id: String,
    spec_id: String,
    prompt: String,
    #[serde(default)]
    session_title: String,
    status: String,
    job_id: String,
    updated_at: String,
    #[serde(default)]
    host: AgentHost,
    #[serde(default)]
    workspace_mode: WorkspaceMode,
    #[serde(default)]
    binding_revision: u64,
    snapshot_error: Option<String>,
    workspaces: Vec<StoredWorkspace>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct StoredWorkspace {
    result_snapshot: Option<IgnoredAny>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SessionRow {
    id: String,
    job_id: String,
    title: String,
    status: String,
    updated_at: String,
    attention: Vec<Value>,
    workspace_mode: WorkspaceMode,
    has_snapshot: bool,
    has_acceptance: bool,
    delete_issue: Option<&'static str>,
    snapshot_error: Option<String>,
    review_error: Option<String>,
    pending_files: Option<usize>,
    review_targets: [(String, String); 0],
}

#[derive(Default)]
struct SessionRead {
    rows: BTreeMap<String, SessionRow>,
    job_versions: BTreeMap<String, JobVersion>,
    review_versions: BTreeMap<String, String>,
    receipts: Vec<String>,
}

#[derive(Serialize)]
struct JobVersion {
    status: String,
    updated_at: String,
}

impl SessionRead {
    fn version(&self) -> Result<String> {
        read_model::fingerprint(&(
            &self.rows,
            &self.job_versions,
            &self.review_versions,
            &self.receipts,
        ))
    }
}

fn pending_review(store: &mut Store, id: &str) -> Result<bool> {
    let run: AgentRun = store
        .record("agentRun", id)?
        .ok_or_else(|| invalid("Agent 运行不存在").context("httpStatus", 404))?;
    for workspace in &run.workspaces {
        let Some(result) = &workspace.result_snapshot else {
            continue;
        };
        for path in review_paths(&run, workspace, result) {
            let baseline = last_accepted_file(&run, workspace, path)
                .map(accepted_result)
                .transpose()?
                .unwrap_or_else(|| workspace.initial.get(path).cloned());
            if baseline.as_ref() != result.get(path) {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

fn job_attention(store: &Store, job: Option<&JobRecord>, status: &str) -> Result<Vec<Value>> {
    let mut attention = match job {
        Some(job) => activities::agent_question_attention(store, job)?,
        None => Vec::new(),
    };
    match status {
        "interrupted" => attention.push(json!({"kind": "interrupted"})),
        "failed" => attention.push(json!({"kind": "failed"})),
        "recoveryRequired" => attention.insert(0, json!({"kind": "recovery"})),
        _ => {}
    }
    Ok(attention)
}

fn run_row(
    store: &mut Store,
    run: StoredRun,
    job: Option<&JobRecord>,
) -> Result<(SessionRow, String)> {
    let status = job.map_or(run.status.as_str(), |job| &job.status);
    let updated_at = job.map_or(&run.updated_at, |job| {
        (&run.updated_at).max(&job.updated_at)
    });
    let has_snapshot = terminal(status)
        && !run.workspaces.is_empty()
        && run
            .workspaces
            .iter()
            .all(|workspace| workspace.result_snapshot.is_some());
    let mut attention = job_attention(store, job, status)?;
    let (reports, mut review_error) =
        match acceptance::summary_for_round(store, &run.id, &run.job_id, run.binding_revision) {
            Ok(summary) => (
                summary
                    .and_then(|value| value["projects"].as_array().cloned())
                    .unwrap_or_default(),
                None,
            ),
            Err(error) => (Vec::new(), Some(error.message)),
        };
    if status != "recoveryRequired"
        && reports.iter().any(|report| {
            report["disposition"]
                .as_str()
                .is_some_and(acceptance::requires_verification)
        })
    {
        attention.insert(0, json!({"kind": "recovery"}));
    }
    if has_snapshot && run.workspace_mode == WorkspaceMode::Isolated {
        match pending_review(store, &run.id) {
            Ok(true) => attention.push(json!({"kind": "review"})),
            Ok(false) => {}
            Err(error) => review_error = Some(error.message),
        }
    }
    let version = read_model::fingerprint(&(run.binding_revision, &reports))?;
    Ok((
        SessionRow {
            id: run.id,
            job_id: run.job_id,
            title: if run.session_title.is_empty() {
                run.prompt.chars().take(96).collect()
            } else {
                run.session_title
            },
            status: status.into(),
            updated_at: updated_at.clone(),
            attention,
            workspace_mode: run.workspace_mode,
            has_snapshot,
            has_acceptance: !reports.is_empty(),
            delete_issue: deletion_issue(status, &reports),
            snapshot_error: run.snapshot_error,
            review_error,
            pending_files: None,
            review_targets: [],
        },
        version,
    ))
}

fn collect(
    store: &mut Store,
    jobs: &[JobRecord],
    spec_id: Option<&str>,
) -> Result<BTreeMap<String, SessionRead>> {
    let by_id: BTreeMap<_, _> = jobs.iter().map(|job| (job.id.as_str(), job)).collect();
    let mut specs = BTreeMap::<String, SessionRead>::new();
    let mut owners = BTreeMap::new();
    for run in store.records::<StoredRun>("agentRun")? {
        if run.host != AgentHost::Pi || spec_id.is_some_and(|id| id != run.spec_id) {
            continue;
        }
        let spec = run.spec_id.clone();
        owners.insert(run.id.clone(), spec.clone());
        let sessions = specs.entry(spec).or_default();
        let job = by_id.get(run.job_id.as_str()).copied();
        let (row, version) = run_row(store, run, job)?;
        sessions.review_versions.insert(row.id.clone(), version);
        sessions.rows.insert(row.id.clone(), row);
    }
    for job in jobs {
        let Some((id, spec)) = job.agent_run_id.as_ref().and_then(|run| {
            let spec = owners.get(run).or(job.agent_spec_id.as_ref())?;
            Some((run, spec))
        }) else {
            continue;
        };
        if job.kind != "agent" || spec_id.is_some_and(|wanted| wanted != spec) {
            continue;
        }
        let sessions = specs.entry(spec.clone()).or_default();
        sessions.job_versions.insert(
            job.id.clone(),
            JobVersion {
                status: job.status.clone(),
                updated_at: job.updated_at.clone(),
            },
        );
        if let Some(row) = sessions.rows.get(id)
            && (row.job_id == job.id || job.terminal() || row.updated_at > job.updated_at)
        {
            continue;
        }
        let title = sessions
            .rows
            .get(id)
            .map_or_else(|| job.title.clone(), |row| row.title.clone());
        sessions.rows.insert(
            id.clone(),
            SessionRow {
                id: id.clone(),
                job_id: job.id.clone(),
                title,
                status: job.status.clone(),
                updated_at: job.updated_at.clone(),
                attention: job_attention(store, Some(job), &job.status)?,
                workspace_mode: WorkspaceMode::Direct,
                has_snapshot: false,
                has_acceptance: false,
                delete_issue: Some("本轮尚未建立可删除的会话记录"),
                snapshot_error: None,
                review_error: None,
                pending_files: None,
                review_targets: [],
            },
        );
    }
    for (key, _) in store.record_entries::<IgnoredAny>(acceptance::RECEIPT)? {
        if let Some((id, _)) = key.split_once('/')
            && let Some(sessions) = owners.get(id).and_then(|spec| specs.get_mut(spec))
        {
            sessions.receipts.push(key);
        }
    }
    Ok(specs)
}

pub(in crate::workbench) fn versions(
    store: &mut Store,
    jobs: &[JobRecord],
) -> Result<BTreeMap<String, String>> {
    collect(store, jobs, None)?
        .into_iter()
        .map(|(id, sessions)| Ok((id, sessions.version()?)))
        .collect()
}

pub(in crate::workbench) fn queue_versions(store: &mut Store) -> Result<BTreeMap<String, String>> {
    #[derive(Deserialize)]
    struct StoredQueueVersion {
        version: u64,
    }

    Ok(store
        .record_entries::<StoredQueueVersion>(super::queue::KIND)?
        .into_iter()
        .map(|(id, queue)| (id, queue.version.to_string()))
        .collect())
}

pub(in crate::workbench) fn sessions(
    jobs: &JobManager,
    reads: &Arc<read_model::ReadModel>,
    spec_id: &str,
    query: &BTreeMap<String, String>,
    cancel: &Cancellation,
) -> Result<Value> {
    let limit = query.get("limit").map_or(Ok(20), |limit| {
        limit
            .parse::<usize>()
            .ok()
            .filter(|limit| (1..=100).contains(limit))
            .ok_or_else(|| invalid("会话分页数量须为 1 到 100"))
    })?;
    let search = query.get("q").map_or("", |search| search.trim());
    if search.chars().count() > 256 {
        return Err(invalid("会话搜索最多支持 256 个字符"));
    }
    let search = search.to_lowercase();
    let attention_only = match query.get("attention").map(String::as_str) {
        None | Some("0") => false,
        Some("1") => true,
        _ => return Err(invalid("会话待处理筛选无效")),
    };
    let (spec, projects, stamp, sessions) = {
        let mut store = lock_store(&jobs.store)?;
        let spec = sources::spec(store.state(), spec_id)?.clone();
        let projects: Vec<_> = store.state().projects.iter()
            .filter(|project| project.active || project.id == spec.project_id)
            .map(|project| json!({"id":project.id,"name":project.name,"rootPath":project.root_path,"active":project.active}))
            .collect();
        let jobs = store.records::<JobRecord>("job")?;
        let sessions = collect(&mut store, &jobs, Some(spec_id))?
            .remove(spec_id)
            .unwrap_or_default();
        (spec, projects, store.projection_stamp(), sessions)
    };
    let version = sessions.version()?;
    let cursor_version = read_model::fingerprint(&(spec_id, &version, &search, attention_only))?;
    let offset = match query.get("cursor") {
        None => 0,
        Some(cursor) => {
            let (expected, offset) = cursor
                .split_once(':')
                .and_then(|(expected, offset)| Some((expected, offset.parse::<usize>().ok()?)))
                .ok_or_else(|| invalid("会话分页游标无效"))?;
            if expected != cursor_version {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "会话列表已变化，请重新读取第一页",
                )
                .context("httpStatus", 409)
                .context("version", &version));
            }
            offset
        }
    };
    let mut matches: Vec<_> = sessions
        .rows
        .values()
        .filter(|row| {
            search.is_empty()
                || [&row.title, &row.id, &row.job_id]
                    .iter()
                    .any(|value| value.to_lowercase().contains(&search))
        })
        .collect();
    let attention_total = matches
        .iter()
        .filter(|row| !row.attention.is_empty())
        .count();
    if attention_only {
        matches.retain(|row| !row.attention.is_empty());
    }
    matches.sort_unstable_by(|left, right| {
        (&right.updated_at, &right.id).cmp(&(&left.updated_at, &left.id))
    });
    let total = matches.len();
    let end = offset.saturating_add(limit);
    let next_cursor = (end < total).then(|| format!("{cursor_version}:{end}"));
    reads.request_agent_context(&jobs.store, spec_id, stamp, cancel);
    let mut response = reads.latest().agent_context(spec_id, stamp);
    response["spec"] = json!(spec);
    response["projects"] = json!(projects);
    response["sessions"] = json!(matches.iter().skip(offset).take(limit).collect::<Vec<_>>());
    response["total"] = json!(total);
    response["attentionTotal"] = json!(attention_total);
    response["version"] = json!(version);
    response["nextCursor"] = json!(next_cursor);
    if let Some(id) = query.get("runId") {
        response["selectedSession"] = json!(sessions.rows.get(id));
    }
    Ok(response)
}
