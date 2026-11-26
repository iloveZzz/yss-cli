use super::*;
use crate::workbench::{api::job_fingerprint, progress::load_goal_version};
use std::{thread, time::Duration};

pub(in crate::workbench) const KIND: &str = "agentQueue";
pub(in crate::workbench) const ENQUEUE_RECEIPT: &str = "agentQueueRequest";
const MAX_ITEMS: usize = 20;
// This only serializes queue mutations and job admission. Runtime checks run in the job worker.
static DISPATCH: Mutex<()> = Mutex::new(());

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(in crate::workbench) struct Queue {
    run_id: String,
    spec_id: String,
    version: u64,
    paused_reason: Option<String>,
    last_job_id: String,
    items: Vec<Entry>,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Entry {
    id: String,
    prompt: String,
    citations: Vec<ReviewCitation>,
    read_only: bool,
    goal_version: Option<String>,
    dispatching: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    attempt_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    job_id: Option<String>,
}

impl Entry {
    fn request_id(&self) -> &str {
        self.attempt_id.as_deref().unwrap_or(&self.id)
    }

    fn clear_attempt(&mut self) {
        self.dispatching = false;
        self.attempt_id = None;
        self.job_id = None;
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(in crate::workbench) struct Draft {
    version: u64,
    client_id: Option<String>,
    prompt: String,
    #[serde(default)]
    citations: Vec<ReviewCitation>,
    #[serde(default = "default_read_only")]
    read_only: bool,
    expected_goal_version: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(in crate::workbench) struct Version {
    version: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(in crate::workbench) struct Resume {
    version: u64,
    #[serde(default)]
    confirm_direct_edit: bool,
}

fn gate() -> Result<std::sync::MutexGuard<'static, ()>> {
    DISPATCH
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "续办队列状态失效"))
}

fn current(store: &mut Store, run: &AgentRun) -> Result<Queue> {
    Ok(store.record(KIND, &run.id)?.unwrap_or_else(|| Queue {
        run_id: run.id.clone(),
        spec_id: run.spec_id.clone(),
        version: 0,
        paused_reason: None,
        last_job_id: run.job_id.clone(),
        items: Vec::new(),
    }))
}

fn conflict(message: &str) -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, message)
        .context("httpStatus", 409)
        .context("queueRejected", true)
}

fn check_version(queue: &Queue, version: u64) -> Result<()> {
    if queue.version != version {
        return Err(conflict("队列已变化，请刷新后重试"));
    }
    Ok(())
}

fn save(store: &mut Store, queue: &mut Queue) -> Result<()> {
    advance(queue)?;
    store.put_record(KIND, &queue.run_id, queue)
}

fn advance(queue: &mut Queue) -> Result<()> {
    queue.version = queue
        .version
        .checked_add(1)
        .ok_or_else(|| invalid("队列版本无效"))?;
    Ok(())
}

pub(in crate::workbench) fn enqueue_receipt(
    run: &str,
    draft: &Draft,
) -> Result<Option<(String, String)>> {
    let Some(client) = &draft.client_id else {
        return Ok(None);
    };
    if client.is_empty()
        || client.len() > 64
        || !client
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
    {
        return Err(invalid("队列请求 ID 无效"));
    }
    let digest = job_fingerprint(
        "queue",
        &json!([
            draft.prompt,
            draft.citations,
            draft.read_only,
            draft.expected_goal_version
        ]),
    )?;
    Ok(Some((format!("{run}/{client}"), digest)))
}

fn save_enqueued(
    store: &mut Store,
    queue: &mut Queue,
    receipt: Option<(String, String)>,
) -> Result<()> {
    let Some((id, digest)) = receipt else {
        return save(store, queue);
    };
    advance(queue)?;
    let records = [
        (KIND, queue.run_id.clone(), serde_json::to_string(queue)),
        (ENQUEUE_RECEIPT, id, serde_json::to_string(&digest)),
    ]
    .into_iter()
    .map(|(kind, id, payload)| {
        Ok(super::super::sqlite::RecordWrite {
            kind: kind.into(),
            id,
            payload: Some(payload.map_err(|_| invalid("队列回执编码失败"))?),
        })
    })
    .collect::<Result<Vec<_>>>()?;
    store.write_extensions(&super::super::sqlite::CommitEffects {
        records,
        blobs: Vec::new(),
    })
}

fn request_for(run: &AgentRun, entry: &Entry) -> Result<AgentStartRequest> {
    let access = run.queue_access();
    Ok(AgentStartRequest {
        spec_id: run.spec_id.clone(),
        prompt: entry.prompt.clone(),
        project_ids: run.project_ids.clone(),
        project_access: access.map(|entries| {
            entries
                .iter()
                .map(|grant| grant.narrowed(entry.read_only, false))
                .collect()
        }),
        continuation_source: None,
        ci_job: None,
        carry_files: Default::default(),
        request_id: entry.request_id().to_owned(),
        resume_run_id: Some(run.id.clone()),
        expected_job_id: Some(run.job_id.clone()),
        citations: entry.citations.clone(),
        selected_model: None,
        expected_model: Some(recorded_model(run)?),
        thinking_level: None,
        read_only: entry.read_only,
        allow_commands: false,
        lifecycle_stage: None,
        expected_goal_version: entry.goal_version.clone(),
    })
}

fn ensure_queueable(run: &AgentRun) -> Result<()> {
    if run.host != AgentHost::Pi || run.workspace_mode != WorkspaceMode::Direct {
        return Err(invalid("该历史会话仅供审阅，请在可续接的 Pi 会话使用队列"));
    }
    Ok(())
}

pub(in crate::workbench) fn view(manager: &JobManager, id: &str) -> Result<Queue> {
    let mut store = lock_store(&manager.store)?;
    let run = load(&mut store, id)?;
    current(&mut store, &run)
}

pub(in crate::workbench) fn put(
    manager: &JobManager,
    id: &str,
    entry_id: Option<&str>,
    draft: Draft,
) -> Result<Queue> {
    let _gate = gate()?;
    let mut store = lock_store(&manager.store)?;
    super::super::server::desktop::check_admission(manager)?;
    let run = load(&mut store, id)?;
    ensure_queueable(&run)?;
    let mut queue = current(&mut store, &run)?;
    let receipt = if entry_id.is_none() {
        enqueue_receipt(id, &draft)?
    } else {
        None
    };
    if let Some((id, digest)) = &receipt
        && let Some(previous) = store.record::<String>(ENQUEUE_RECEIPT, id)?
    {
        if previous == *digest {
            return Ok(queue);
        }
        return Err(conflict("队列请求 ID 已用于不同内容"));
    }
    check_version(&queue, draft.version)?;
    let goal = load_goal_version(&mut store, &run.spec_id)?;
    if draft.expected_goal_version != goal {
        return Err(conflict("目标版本已变化，请核对草稿后重新加入队列"));
    }
    let entry = Entry {
        id: match entry_id {
            Some(id) => id.to_owned(),
            None => format!("pi-queue-{}", crate::fs::unique_id()?),
        },
        prompt: draft.prompt,
        citations: draft.citations,
        read_only: draft.read_only,
        goal_version: goal,
        dispatching: false,
        attempt_id: None,
        job_id: None,
    };
    validate_start_request(&request_for(&run, &entry)?, true)?;
    if let Some(entry_id) = entry_id {
        let item = queue
            .items
            .iter_mut()
            .find(|entry| entry.id == entry_id)
            .ok_or_else(|| conflict("该队列项已派发或移除"))?;
        if item.dispatching {
            return Err(conflict("正在核实派发结果，暂不能编辑"));
        }
        *item = entry;
    } else {
        if queue.items.len() >= MAX_ITEMS {
            return Err(invalid("此会话最多保留 20 项待发任务"));
        }
        if queue.items.is_empty() {
            // A preparing continuation already owns this session, even though its AgentRun
            // still points to the previous round until the preparation checks finish.
            let jobs: Vec<JobRecord> = store.records("job")?;
            queue.last_job_id = jobs
                .into_iter()
                .filter(|job| job.id == run.job_id || job.agent_run_id.as_deref() == Some(&run.id))
                .max_by(|left, right| {
                    (!left.terminal(), &left.created_at, &left.id).cmp(&(
                        !right.terminal(),
                        &right.created_at,
                        &right.id,
                    ))
                })
                .map_or_else(|| run.job_id.clone(), |job| job.id);
        }
        queue.items.push(entry);
    }
    // A new or edited item must be explicitly paused again before a desktop
    // maintenance operation may treat the queue as preserved drafts.
    if queue.paused_reason.as_deref() == Some("desktopPaused") {
        queue.paused_reason = Some("desktopQueueChanged".into());
    }
    save_enqueued(&mut store, &mut queue, receipt)?;
    Ok(queue)
}

pub(in crate::workbench) fn remove(
    manager: &JobManager,
    id: &str,
    entry_id: &str,
    version: Version,
) -> Result<Queue> {
    let _gate = gate()?;
    let mut store = lock_store(&manager.store)?;
    let run = load(&mut store, id)?;
    let mut queue = current(&mut store, &run)?;
    check_version(&queue, version.version)?;
    let index = queue
        .items
        .iter()
        .position(|entry| entry.id == entry_id)
        .ok_or_else(|| conflict("该队列项已派发或移除"))?;
    if queue.items[index].dispatching {
        return Err(conflict("正在核实派发结果，暂不能移除"));
    }
    queue.items.remove(index);
    if queue.items.is_empty() {
        queue.paused_reason = None;
    }
    save(&mut store, &mut queue)?;
    Ok(queue)
}

pub(super) fn check_start(
    store: &mut Store,
    run_id: Option<&str>,
    dispatch_id: Option<&str>,
) -> Result<()> {
    let queue = match run_id {
        Some(run_id) => store.record::<Queue>(KIND, run_id)?,
        None => None,
    };
    match (
        queue.as_ref().and_then(|state| state.items.first()),
        dispatch_id,
    ) {
        (None, None) => Ok(()),
        (Some(head), Some(id)) if head.dispatching && head.request_id() == id => Ok(()),
        (None, Some(_)) => Err(conflict("待派发的队列项已移除")),
        _ => Err(conflict("会话已有排队任务，请先处理队首")),
    }
}

fn record_pause(store: &mut Store, queue: &mut Queue, reason: &str) -> Result<()> {
    if queue.paused_reason.as_deref() != Some(reason) {
        queue.paused_reason = Some(reason.into());
        save(store, queue)?;
    }
    Ok(())
}

fn is_active_for(jobs: &[JobRecord], run_id: &str) -> bool {
    jobs.iter()
        .any(|job| !job.terminal() && job.agent_run_id.as_deref() == Some(run_id))
}

fn has_changes(run: &AgentRun) -> bool {
    run.workspaces.iter().any(|workspace| {
        workspace.result_snapshot.as_ref().is_some_and(|result| {
            review_paths(run, workspace, result)
                .into_iter()
                .any(|path| result.get(path) != workspace.initial.get(path))
        })
    })
}

fn settle_attempt(queue: &mut Queue, job: &JobRecord) -> bool {
    // Keep a draft through asynchronous preparation. A failed check never consumes it.
    queue.last_job_id.clone_from(&job.id);
    let head = &mut queue.items[0];
    if job.agent_started_at.is_some() || job.status == "completed" {
        queue.items.remove(0);
        if queue.items.is_empty() {
            queue.paused_reason = None;
        }
        return true;
    }
    if job.terminal() {
        head.clear_attempt();
        queue.paused_reason = Some(
            if job
                .error
                .as_ref()
                .is_some_and(|error| error["context"]["progressBinding"] == "true")
            {
                "targetChanged"
            } else {
                "previousRoundStopped"
            }
            .into(),
        );
        return true;
    }
    if head.job_id.as_deref() != Some(&job.id) {
        head.job_id = Some(job.id.clone());
        return true;
    }
    false
}

fn dispatch(manager: &Arc<JobManager>, id: &str, user: Option<Resume>) -> Result<Queue> {
    let _gate = gate()?;
    let (mut queue, request) = {
        let mut store = lock_store(&manager.store)?;
        super::super::server::desktop::check_admission(manager)?;
        let run = load(&mut store, id)?;
        let mut queue = current(&mut store, &run)?;
        if let Some(user) = &user {
            check_version(&queue, user.version)?;
        }
        if queue.items.is_empty() {
            return Ok(queue);
        }
        let jobs: Vec<JobRecord> = store.records("job")?;
        if queue.items[0].dispatching {
            let accepted = jobs
                .iter()
                .find(|job| job.request_id == queue.items[0].request_id());
            if let Some(job) = accepted {
                if settle_attempt(&mut queue, job) {
                    save(&mut store, &mut queue)?;
                }
                return Ok(queue);
            }
            // Only an explicit recovery can reopen an interrupted admission.
            if user.is_none() {
                record_pause(&mut store, &mut queue, "dispatchUnknown")?;
                return Ok(queue);
            }
            if store.has_record("jobRequest", queue.items[0].request_id())? {
                record_pause(&mut store, &mut queue, "dispatchUnknown")?;
                return Ok(queue);
            }
            queue.items[0].clear_attempt();
            save(&mut store, &mut queue)?;
        }
        if user.is_none() && queue.paused_reason.is_some() {
            return Ok(queue);
        }
        if is_active_for(&jobs, id) || !terminal(&run.status) {
            return Ok(queue);
        }
        let head = &queue.items[0];
        let prior_status = jobs
            .iter()
            .find(|job| job.id == queue.last_job_id)
            .map(|job| job.status.as_str())
            .unwrap_or(&run.status);
        let reason = if ensure_queueable(&run).is_err() {
            Some("sessionUnavailable")
        } else if load_goal_version(&mut store, &run.spec_id)? != head.goal_version {
            Some("targetChanged")
        } else if prior_status == "recoveryRequired" || snapshot_issue(&run).is_some() {
            Some("verificationRequired")
        } else if user.is_none() && prior_status != "completed" {
            Some("previousRoundStopped")
        } else if user.is_none() && has_changes(&run) {
            Some("reviewRequired")
        } else if run.workspace_mode == WorkspaceMode::Direct
            && !head.read_only
            && run.can_edit_queued_projects()
            && !user.as_ref().is_some_and(|user| user.confirm_direct_edit)
        {
            Some("directEditConfirmation")
        } else {
            None
        };
        if let Some(reason) = reason {
            record_pause(&mut store, &mut queue, reason)?;
            return Ok(queue);
        }
        let mut request = request_for(&run, head)?;
        request.request_id = format!("pi-queue-{}", crate::fs::unique_id()?);
        queue.paused_reason = None;
        queue.items[0].dispatching = true;
        queue.items[0].attempt_id = Some(request.request_id.clone());
        save(&mut store, &mut queue)?;
        (queue, request)
    };
    let fingerprint = job_fingerprint(
        "/api/agent/start",
        &serde_json::to_value(&request).map_err(|_| invalid("队列任务编码失败"))?,
    )?;
    let result = super::admission::start_queued(manager.clone(), request, fingerprint);
    let mut store = lock_store(&manager.store)?;
    if !store.has_record(KIND, id)? {
        return Err(conflict("会话队列已移除"));
    }
    match result {
        Ok(job) => {
            settle_attempt(&mut queue, &job);
        }
        Err(error) if matches!(error.code, ErrorCode::RecoveryRequired | ErrorCode::Io) => {
            queue.paused_reason = Some("dispatchUnknown".into());
        }
        Err(error) => {
            queue.items[0].clear_attempt();
            queue.paused_reason = Some(
                if error.context.contains_key("progressBinding") {
                    "targetChanged"
                } else {
                    "admissionRejected"
                }
                .into(),
            );
        }
    }
    save(&mut store, &mut queue)?;
    Ok(queue)
}

// Called while holding the workbench store lock. The desktop lease uses the same
// lock as queue changes and Job admission, including the dispatching window.
pub(in crate::workbench) fn desktop_pending(store: &mut Store) -> Result<usize> {
    Ok(store
        .records::<Queue>(KIND)?
        .iter()
        .map(|queue| {
            if queue.paused_reason.as_deref() == Some("desktopPaused")
                && !queue.items.iter().any(|item| item.dispatching)
            {
                0
            } else {
                queue.items.len()
            }
        })
        .sum())
}

pub(in crate::workbench) fn desktop_pause(manager: &JobManager) -> Result<usize> {
    let _gate = gate()?;
    let mut store = lock_store(&manager.store)?;
    super::super::server::desktop::check_admission(manager)?;
    let mut queues = store.records::<Queue>(KIND)?;
    if queues
        .iter()
        .any(|queue| queue.items.iter().any(|item| item.dispatching))
    {
        return Err(YssError::new(ErrorCode::Locked, "队列正在派发，请稍后重试"));
    }
    let mut paused = 0;
    for queue in &mut queues {
        if !queue.items.is_empty() {
            record_pause(&mut store, queue, "desktopPaused")?;
            paused += queue.items.len();
        }
    }
    Ok(paused)
}

pub(in crate::workbench) fn resume(
    manager: &Arc<JobManager>,
    id: &str,
    version: Resume,
) -> Result<Queue> {
    dispatch(manager, id, Some(version))
}

pub(in crate::workbench) fn start(
    manager: Arc<JobManager>,
    stopping: Cancellation,
) -> Result<thread::JoinHandle<()>> {
    {
        let mut store = lock_store(&manager.store)?;
        let jobs = store.records::<JobRecord>("job")?;
        for mut queue in store.records::<Queue>(KIND)? {
            if !queue.items.is_empty() {
                let mut reason = if queue.paused_reason.as_deref() == Some("desktopPaused") {
                    "desktopPaused"
                } else {
                    "restarted"
                };
                if queue.items[0].dispatching {
                    if let Some(job) = jobs
                        .iter()
                        .find(|job| job.request_id == queue.items[0].request_id())
                    {
                        settle_attempt(&mut queue, job);
                    } else {
                        reason = "dispatchUnknown";
                    }
                }
                queue.paused_reason = (!queue.items.is_empty()).then(|| reason.into());
                save(&mut store, &mut queue)?;
            }
        }
    }
    Ok(thread::spawn(move || {
        while !stopping.is_cancelled() {
            let candidates =
                lock_store(&manager.store).and_then(|mut store| store.records::<Queue>(KIND));
            if let Ok(candidates) = candidates {
                for queue in candidates {
                    if stopping.is_cancelled() {
                        break;
                    }
                    if queue
                        .items
                        .first()
                        .is_some_and(|head| queue.paused_reason.is_none() || head.dispatching)
                    {
                        let _ = dispatch(&manager, &queue.run_id, None);
                    }
                }
            }
            thread::sleep(Duration::from_millis(400));
        }
    }))
}
