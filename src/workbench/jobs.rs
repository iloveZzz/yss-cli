use super::{sources::now, sqlite::EventWrite, store::Store};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    sync::atomic::{AtomicBool, Ordering},
    sync::{Arc, Condvar, Mutex},
    time::{Duration, Instant},
};

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JobRecord {
    pub id: String,
    pub kind: String,
    pub title: String,
    pub status: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agent_run_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agent_spec_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub agent_started_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pi_phase: Option<PiPhase>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pi_phase_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(super) local_project_task: Option<LocalProjectTask>,
    pub request_id: String,
    pub roots: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    pub result: Option<Value>,
    pub error: Option<Value>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum PiPhase {
    VerifyScope,
    CheckRuntime,
    PrepareWorkspace,
    PromptAccepted,
    FinalizeArtifacts,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
pub(super) enum LocalProjectAction {
    Test,
    Build,
}

impl LocalProjectAction {
    pub(super) fn as_str(self) -> &'static str {
        match self {
            Self::Test => "test",
            Self::Build => "build",
        }
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct LocalProjectTask {
    pub run_id: String,
    pub source_job_id: String,
    pub project_id: String,
    pub directory: String,
    pub action: LocalProjectAction,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_checkout: Option<LocalProjectCheckout>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct LocalProjectCheckout {
    pub root_path: String,
    pub head: String,
    pub branch: Option<String>,
}

pub(super) struct JobFailure {
    pub error: YssError,
    pub result: Option<Value>,
}

impl From<YssError> for JobFailure {
    fn from(error: YssError) -> Self {
        Self {
            error,
            result: None,
        }
    }
}

impl JobRecord {
    fn pi_phase_payload(&self) -> Option<Value> {
        Some(json!({"phase": self.pi_phase?, "at": self.pi_phase_at.as_deref()?}))
    }

    pub(super) fn session_run_id(&self) -> Option<&str> {
        self.agent_run_id.as_deref().or_else(|| {
            self.local_project_task
                .as_ref()
                .map(|task| task.run_id.as_str())
        })
    }

    pub fn terminal(&self) -> bool {
        matches!(
            self.status.as_str(),
            "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
        )
    }

    fn persist_event(&self, store: &mut Store, kind: &str, payload: &Value) -> Result<()> {
        store.put_record_with_event(
            "job",
            &self.id,
            self,
            Some(EventWrite {
                job_id: &self.id,
                kind,
                payload,
            }),
        )
    }
}

pub(super) const DELETED_REQUEST: &str = "deletedJobRequest";

pub(super) fn ensure_request_not_deleted(store: &mut Store, id: &str) -> Result<()> {
    if store.record::<bool>(DELETED_REQUEST, id)?.is_some() {
        return Err(
            YssError::new(ErrorCode::Conflict, "该执行记录已删除").context("httpStatus", 410)
        );
    }
    Ok(())
}

fn check_local_request_available(store: &mut Store, id: &str) -> Result<()> {
    if store.has_record("jobRequest", id)? || store.has_record("mutationReceipt", id)? {
        return Err(request_id_conflict());
    }
    Ok(())
}

fn request_id_conflict() -> YssError {
    YssError::new(ErrorCode::Conflict, "请求 ID 已用于另一项操作")
}

pub(super) fn job_by_request(store: &mut Store, request_id: &str) -> Result<Option<JobRecord>> {
    Ok(store
        .records::<JobRecord>("job")?
        .into_iter()
        .find(|job| job.request_id == request_id))
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JobEvent {
    pub id: u64,
    pub job_id: String,
    pub kind: String,
    pub payload: Value,
    pub created_at: String,
}

#[derive(Default)]
struct InputState {
    pending: BTreeMap<String, Value>,
    answers: BTreeMap<String, Value>,
}

struct Control {
    cancel: Cancellation,
    input: Mutex<InputState>,
    changed: Condvar,
    saw_tool: AtomicBool,
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    saw_text: AtomicBool,
    recovery_required: AtomicBool,
}

struct Admission {
    kind: String,
    title: String,
    request_id: String,
    roots: Vec<String>,
    agent: Option<DeferredAgent>,
    local_project_task: Option<LocalProjectTask>,
}

pub(super) struct DeferredAgent {
    pub spec_id: String,
    pub run_id: Option<String>,
    pub fingerprint: String,
}

#[cfg(windows)]
struct WindowsRoot {
    directory: crate::fs::Root,
    relative: String,
    file_identity: Option<crate::fs::FileIdentity>,
}

#[cfg(windows)]
fn windows_root(path: &std::path::Path) -> Result<WindowsRoot> {
    use std::{io::ErrorKind, path::Component};
    if !path.is_absolute()
        || path
            .components()
            .any(|part| matches!(part, Component::ParentDir))
    {
        return Err(YssError::new(
            ErrorCode::Locked,
            "作业目录无法安全核对，请检查路径",
        ));
    }
    let mut current = path.to_owned();
    let mut existing_file = false;
    loop {
        match std::fs::symlink_metadata(&current) {
            Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => break,
            Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {
                if current == path {
                    existing_file = true;
                }
            }
            Ok(_) => {
                return Err(YssError::new(
                    ErrorCode::Locked,
                    "作业目录包含不安全链接或对象",
                ));
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {}
            Err(_) => {
                return Err(YssError::new(
                    ErrorCode::Locked,
                    "作业目录不可访问，无法确认冲突",
                ));
            }
        }
        current = current
            .parent()
            .ok_or_else(|| YssError::new(ErrorCode::Locked, "作业目录无法安全核对"))?
            .to_owned();
    }
    // Canonical form makes Windows extended paths, case aliases and junctions
    // refer to the same physical directory. Root still validates the opened
    // chain and fails closed if it changes while the comparison is in progress.
    let canonical = std::fs::canonicalize(&current)
        .map_err(|_| YssError::new(ErrorCode::Locked, "作业目录无法解析"))?;
    let directory = crate::fs::Root::open(&canonical)?;
    let relative = directory
        .relative_windows_path(path)?
        .ok_or_else(|| YssError::new(ErrorCode::Locked, "作业目录身份无法确认"))?;
    let file_identity = if existing_file {
        let name = crate::model::RelPath::new(&relative)?;
        Some(directory.regular_file_identity(&name)?)
    } else {
        None
    };
    directory.revalidate()?;
    Ok(WindowsRoot {
        directory,
        relative,
        file_identity,
    })
}

#[cfg(windows)]
fn windows_roots_overlap(left: &str, right: &str) -> Result<bool> {
    use std::path::Path;
    let (left_path, right_path) = (Path::new(left), Path::new(right));
    match (left_path.is_absolute(), right_path.is_absolute()) {
        (false, false) if left.starts_with("external/") && right.starts_with("external/") => {
            return Ok(left.starts_with(right) || right.starts_with(left));
        }
        (false, true) if left.starts_with("external/") => return Ok(false),
        (true, false) if right.starts_with("external/") => return Ok(false),
        (true, true) => {}
        _ => {
            return Err(YssError::new(
                ErrorCode::Locked,
                "作业目录不是可核验的绝对路径",
            ));
        }
    }
    let (left_root, right_root) = (windows_root(left_path)?, windows_root(right_path)?);
    if left_root.file_identity.is_some() && left_root.file_identity == right_root.file_identity {
        return Ok(true);
    }
    let contains = |root: &WindowsRoot, target: &Path| -> Result<bool> {
        let Some(relative) = root.directory.relative_windows_path(target)? else {
            return Ok(false);
        };
        crate::fs::windows_path_under(&relative, &root.relative)
            .ok_or_else(|| YssError::new(ErrorCode::Locked, "无法比较作业目录，请稍后重试"))
    };
    let result = contains(&left_root, right_path)? || contains(&right_root, left_path)?;
    left_root.directory.revalidate()?;
    right_root.directory.revalidate()?;
    Ok(result)
}

pub(super) fn roots_overlap(left: &[String], right: &[String]) -> Result<bool> {
    for root in left {
        for other in right {
            #[cfg(windows)]
            let overlaps = windows_roots_overlap(root, other)?;
            #[cfg(not(windows))]
            let overlaps = std::path::Path::new(root).starts_with(other)
                || std::path::Path::new(other).starts_with(root);
            if overlaps {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

pub(super) fn check_roots(
    jobs: &[JobRecord],
    roots: &[String],
    exclude: Option<&str>,
) -> Result<()> {
    for active in jobs
        .iter()
        .filter(|job| !job.terminal() && Some(job.id.as_str()) != exclude)
    {
        if roots_overlap(roots, &active.roots)? {
            return Err(
                YssError::new(ErrorCode::Locked, "目标目录已有运行中的写入操作")
                    .context("jobId", &active.id),
            );
        }
    }
    Ok(())
}

pub struct JobManager {
    pub store: Arc<Mutex<Store>>,
    pub(crate) activity_cache: Mutex<super::activities::ActivityCache>,
    pub(crate) desktop: Mutex<super::server::desktop::DesktopState>,
    controls: Mutex<BTreeMap<String, Arc<Control>>>,
    handles: Mutex<Vec<std::thread::JoinHandle<()>>>,
    stopping: Cancellation,
}

#[derive(Clone)]
pub struct JobContext {
    pub id: String,
    pub store: Arc<Mutex<Store>>,
    pub cancel: Cancellation,
    control: Arc<Control>,
    started: Instant,
    request_id: String,
}

pub fn lock_store(store: &Arc<Mutex<Store>>) -> Result<std::sync::MutexGuard<'_, Store>> {
    store
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "工作台数据锁失效，请重启核实"))
}

impl JobContext {
    pub(super) fn set_pi_phase(&self, phase: PiPhase) -> Result<()> {
        let mut store = lock_store(&self.store)?;
        let mut job: JobRecord = store
            .record("job", &self.id)?
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "执行记录不存在"))?;
        if job.terminal() || job.kind != "agent" || job.pi_phase >= Some(phase) {
            return Ok(());
        }
        let at = now();
        job.pi_phase = Some(phase);
        job.pi_phase_at = Some(at.clone());
        job.updated_at.clone_from(&at);
        if phase == PiPhase::PromptAccepted {
            job.agent_started_at.get_or_insert_with(|| at.clone());
        }
        job.persist_event(&mut store, "piPhase", &json!({"phase": phase, "at": at}))
    }

    pub(super) fn trace(&self, phase: &str) {
        if std::env::var_os("YSS_PI_TIMINGS").is_some() {
            eprintln!(
                "{}",
                json!({
                    "requestId":self.request_id,"jobId":self.id,"phase":phase,
                    "elapsedUs":self.started.elapsed().as_micros()
                })
            );
        }
    }

    pub(super) fn mark_running(&self) -> Result<()> {
        let is_preparing = lock_store(&self.store)?
            .record::<JobRecord>("job", &self.id)?
            .is_some_and(|job| job.status == "preparing");
        if is_preparing {
            self.set_status("running")?;
        }
        Ok(())
    }

    pub fn emit(&self, kind: &str, payload: Value) -> Result<()> {
        self.emit_with_id(kind, payload).map(|_| ())
    }

    pub(super) fn emit_with_id(&self, kind: &str, payload: Value) -> Result<u64> {
        let id = lock_store(&self.store)?.append_event(&self.id, kind, &payload)?;
        if kind == "piTool" && !self.control.saw_tool.swap(true, Ordering::Relaxed) {
            self.trace("firstTool");
        }
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        if kind == "item/completed"
            && payload["item"]["type"] == "agentMessage"
            && payload["item"]["text"]
                .as_str()
                .is_some_and(|text| !text.trim().is_empty())
            && !self.control.saw_text.swap(true, Ordering::Relaxed)
        {
            self.trace("firstTextStored");
        }
        Ok(id)
    }

    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    pub(super) fn emit_pi_delta(
        &self,
        kind: &'static str,
        item_id: &str,
        text: &str,
    ) -> Result<Option<Instant>> {
        let mut store = lock_store(&self.store)?;
        let awaiting_text =
            kind == "item/agentMessage/delta" && !self.control.saw_text.load(Ordering::Relaxed);
        let deadline = store.append_pi_delta(&self.id, kind, item_id, text, awaiting_text)?;
        if awaiting_text && !text.trim().is_empty() {
            self.control.saw_text.store(true, Ordering::Relaxed);
            drop(store);
            self.trace("firstTextStored");
        }
        Ok(deadline)
    }

    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    pub(super) fn flush_pi_delta(&self) -> Result<()> {
        lock_store(&self.store)?.flush_job_delta(&self.id)
    }

    pub fn set_status(&self, status: &str) -> Result<()> {
        if status == "recoveryRequired" {
            // Publish the final status together with the job result and finished event.
            self.control
                .recovery_required
                .store(true, Ordering::Relaxed);
            return Ok(());
        }
        let mut store = lock_store(&self.store)?;
        let mut job: JobRecord = store
            .record("job", &self.id)?
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "执行记录不存在"))?;
        job.status = status.into();
        job.updated_at = now();
        job.persist_event(&mut store, "status", &json!({"status": status}))
    }

    pub fn request_input(&self, request_id: &str, payload: Value) -> Result<Value> {
        self.cancel.check()?;
        {
            let mut input = self
                .control
                .input
                .lock()
                .map_err(|_| YssError::new(ErrorCode::Io, "输入队列失效"))?;
            if input
                .pending
                .insert(request_id.into(), payload.clone())
                .is_some()
            {
                return Err(YssError::new(ErrorCode::Conflict, "输入请求 ID 重复"));
            }
        }
        let method = payload["protocolMethod"].as_str().unwrap_or_default();
        let approval = method.ends_with("/requestApproval")
            || matches!(method, "execCommandApproval" | "applyPatchApproval");
        self.set_status(if approval {
            "waitingApproval"
        } else {
            "waitingInput"
        })?;
        self.emit(
            "inputRequested",
            json!({"requestId":request_id,"request":payload}),
        )?;
        loop {
            self.cancel.check()?;
            let mut input = self
                .control
                .input
                .lock()
                .map_err(|_| YssError::new(ErrorCode::Io, "输入队列失效"))?;
            if let Some(answer) = input.answers.remove(request_id) {
                input.pending.remove(request_id);
                drop(input);
                self.emit("inputResolved", json!({"requestId":request_id}))?;
                self.set_status("running")?;
                return Ok(answer);
            }
            let _guard = self
                .control
                .changed
                .wait_timeout(input, Duration::from_millis(200))
                .map_err(|_| YssError::new(ErrorCode::Io, "输入队列失效"))?;
        }
    }
}

impl JobManager {
    pub fn new(store: Arc<Mutex<Store>>) -> Result<Arc<Self>> {
        {
            let mut state = lock_store(&store)?;
            #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
            let pending = super::command_recovery::retry_cleanups(&mut state)
                .and_then(|()| super::command_recovery::unresolved(&mut state));
            let jobs = state.records::<JobRecord>("job")?;
            for mut job in jobs {
                #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
                if pending.as_ref().map_or_else(
                    |_| !job.terminal() && job.kind == "agent",
                    |records| records.iter().any(|record| record.job_id == job.id),
                ) {
                    if job.status != "recoveryRequired" {
                        let prior_error = job.error.take();
                        job.status = "recoveryRequired".into();
                        job.updated_at = now();
                        job.error = Some(
                            json!({"code":"RECOVERY_REQUIRED", "message":"命令暂存待恢复，请保留现场并核对", "previousError":prior_error}),
                        );
                        job.persist_event(&mut state, "finished", &json!(job))?;
                    }
                    continue;
                }
                if !job.terminal() {
                    job.status = "interrupted".into();
                    job.updated_at = now();
                    job.error = Some(
                        json!({"code":"INTERRUPTED","message":"工作台服务已重启，请核对工作区后续接"}),
                    );
                    job.persist_event(&mut state, "finished", &json!(job))?;
                }
            }
        }
        Ok(Arc::new(Self {
            store,
            activity_cache: Mutex::default(),
            desktop: Mutex::new(super::server::desktop::DesktopState::new()?),
            controls: Mutex::new(BTreeMap::new()),
            handles: Mutex::new(Vec::new()),
            stopping: Cancellation::new(),
        }))
    }

    pub fn store(&self) -> Arc<Mutex<Store>> {
        self.store.clone()
    }

    pub fn list(&self) -> Result<Vec<JobRecord>> {
        let mut jobs = lock_store(&self.store)?.records::<JobRecord>("job")?;
        jobs.sort_by(|a, b| b.created_at.cmp(&a.created_at).then(b.id.cmp(&a.id)));
        Ok(jobs)
    }

    pub fn get(&self, id: &str) -> Result<JobRecord> {
        lock_store(&self.store)?.record("job", id)?.ok_or_else(|| {
            YssError::new(ErrorCode::InvalidInput, "执行记录不存在").context("httpStatus", 404)
        })
    }

    pub(super) fn existing_local_project_task(
        store: &mut Store,
        request_id: &str,
        task: &LocalProjectTask,
    ) -> Result<Option<JobRecord>> {
        ensure_request_not_deleted(store, request_id)?;
        match job_by_request(store, request_id)? {
            Some(job) if job.local_project_task.as_ref() == Some(task) => Ok(Some(job)),
            Some(_) => Err(request_id_conflict()),
            None => {
                check_local_request_available(store, request_id)?;
                Ok(None)
            }
        }
    }

    pub(super) fn start_local_project_task(
        self: &Arc<Self>,
        task: LocalProjectTask,
        request_id: String,
        root: String,
        check: impl FnOnce(&mut Store) -> Result<()>,
        work: impl FnOnce(JobContext) -> std::result::Result<Value, JobFailure> + Send + 'static,
    ) -> Result<JobRecord> {
        let title = match task.action {
            LocalProjectAction::Test => "本机测试",
            LocalProjectAction::Build => "本机构建",
        };
        self.admit(
            Admission {
                kind: "localProjectTask".into(),
                title: title.into(),
                request_id,
                roots: vec![root],
                agent: None,
                local_project_task: Some(task),
            },
            |store, _| check(store),
            |context, ()| work(context),
        )
    }

    pub fn start<F>(
        self: &Arc<Self>,
        kind: String,
        title: String,
        request_id: String,
        roots: Vec<String>,
        work: F,
    ) -> Result<JobRecord>
    where
        F: FnOnce(JobContext) -> Result<Value> + Send + 'static,
    {
        self.start_checked(
            kind,
            title,
            request_id,
            roots,
            |_, _| Ok(()),
            |context, ()| work(context),
        )
    }

    pub(super) fn start_checked<F, T>(
        self: &Arc<Self>,
        kind: String,
        title: String,
        request_id: String,
        roots: Vec<String>,
        check: impl FnOnce(&mut Store, &mut JobRecord) -> Result<T>,
        work: F,
    ) -> Result<JobRecord>
    where
        F: FnOnce(JobContext, T) -> Result<Value> + Send + 'static,
        T: Send + 'static,
    {
        self.admit(
            Admission {
                kind,
                title,
                request_id,
                roots,
                agent: None,
                local_project_task: None,
            },
            check,
            |context, admission| work(context, admission).map_err(JobFailure::from),
        )
    }

    pub(super) fn start_preparing(
        self: &Arc<Self>,
        title: String,
        request_id: String,
        agent: DeferredAgent,
        check: impl FnOnce(&mut Store, &mut JobRecord) -> Result<()>,
        work: impl FnOnce(JobContext) -> Result<Value> + Send + 'static,
    ) -> Result<JobRecord> {
        self.admit(
            Admission {
                kind: "agent".into(),
                title,
                request_id,
                roots: Vec::new(),
                agent: Some(agent),
                local_project_task: None,
            },
            check,
            |context, ()| work(context).map_err(JobFailure::from),
        )
    }

    pub(super) fn reserve_agent_roots(
        &self,
        context: &JobContext,
        roots: Vec<String>,
        recheck: impl FnOnce(&mut Store) -> Result<()>,
    ) -> Result<()> {
        let _controls = self
            .controls
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业调度器失效"))?;
        let mut store = lock_store(&self.store)?;
        context.cancel.check()?;
        let mut job: JobRecord = store
            .record("job", &context.id)?
            .filter(|job: &JobRecord| job.status == "preparing")
            .ok_or_else(|| YssError::new(ErrorCode::Conflict, "执行已结束，请核对作业回执"))?;
        recheck(&mut store)?;
        check_roots(&store.records("job")?, &roots, Some(&job.id))?;
        super::agent::ensure_roots_available(&mut store, &roots)?;
        job.roots = roots;
        job.updated_at = now();
        store.put_record("job", &job.id, &job)
    }

    fn admit<F, T>(
        self: &Arc<Self>,
        request: Admission,
        check: impl FnOnce(&mut Store, &mut JobRecord) -> Result<T>,
        work: F,
    ) -> Result<JobRecord>
    where
        F: FnOnce(JobContext, T) -> std::result::Result<Value, JobFailure> + Send + 'static,
        T: Send + 'static,
    {
        let started = Instant::now();
        let Admission {
            kind,
            title,
            request_id,
            roots,
            agent,
            local_project_task,
        } = request;
        self.stopping.check()?;
        if request_id.trim().is_empty() || request_id.len() > 128 {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "请提供有效的执行请求 ID",
            ));
        }
        let mut controls = self
            .controls
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业调度器失效"))?;
        let id = fs::unique_id()?;
        let at = now();
        let mut job = JobRecord {
            id: id.clone(),
            kind,
            title,
            status: "preparing".into(),
            agent_run_id: agent
                .as_ref()
                .map(|value| value.run_id.clone().unwrap_or_else(|| id.clone())),
            agent_spec_id: agent.as_ref().map(|value| value.spec_id.clone()),
            agent_started_at: None,
            pi_phase: agent.as_ref().map(|_| PiPhase::VerifyScope),
            pi_phase_at: agent.as_ref().map(|_| at.clone()),
            local_project_task,
            request_id,
            roots,
            created_at: at.clone(),
            updated_at: at,
            result: None,
            error: None,
        };
        let admission = {
            let mut store = lock_store(&self.store)?;
            super::server::desktop::check_admission(self)?;
            ensure_request_not_deleted(&mut store, &job.request_id)?;
            let jobs = store.records::<JobRecord>("job")?;
            if let Some(agent) = &agent
                && store
                    .record::<String>("jobRequest", &job.request_id)?
                    .is_some_and(|previous| previous != agent.fingerprint)
            {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "执行请求 ID 已用于不同参数",
                ));
            }
            if let Some(existing) = jobs
                .iter()
                .find(|existing| existing.request_id == job.request_id)
            {
                if existing.kind != job.kind
                    || existing.local_project_task != job.local_project_task
                    || (agent.is_none()
                        && job.local_project_task.is_none()
                        && (existing.roots != job.roots || existing.title != job.title))
                {
                    return Err(request_id_conflict());
                }
                return Ok(existing.clone());
            }
            if let Some(task) = &job.local_project_task {
                check_local_request_available(&mut store, &job.request_id)?;
                super::agent::queue::check_local_task_start(&mut store, &task.run_id)?;
            }
            // Deferred Pi admissions have no roots until preparation completes.
            // Block them here before enqueue/dispatch can create another job.
            #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
            if agent.is_some() || job.local_project_task.is_some() {
                super::command_recovery::ensure_none(&mut store)?;
            }
            if let Some(run_id) = job.session_run_id()
                && let Some(active) = jobs.iter().find(|existing| {
                    !existing.terminal() && existing.session_run_id() == Some(run_id)
                })
            {
                return Err(YssError::new(
                    ErrorCode::Locked,
                    "此会话已有准备或运行中的轮次或本机任务",
                )
                .context("jobId", &active.id));
            }
            check_roots(&jobs, &job.roots, None)?;
            super::agent::ensure_roots_available(&mut store, &job.roots)?;
            let admission = check(&mut store, &mut job)?;
            let phase = job.pi_phase_payload();
            let fingerprint = if let Some(agent) = &agent {
                Some(agent.fingerprint.clone())
            } else if let Some(task) = &job.local_project_task {
                Some(super::api::job_fingerprint(
                    "agentLocalProjectTask",
                    &json!(task),
                )?)
            } else {
                None
            };
            if let Some(fingerprint) = fingerprint {
                let records = [
                    ("job", id.as_str(), serde_json::to_string(&job)),
                    (
                        "jobRequest",
                        job.request_id.as_str(),
                        serde_json::to_string(&fingerprint),
                    ),
                ]
                .into_iter()
                .map(|(kind, id, payload)| {
                    Ok(super::sqlite::RecordWrite {
                        kind: kind.into(),
                        id: id.into(),
                        payload: Some(
                            payload
                                .map_err(|_| YssError::new(ErrorCode::Io, "作业回执编码失败"))?,
                        ),
                    })
                })
                .collect::<Result<Vec<_>>>()?;
                let effects = super::sqlite::CommitEffects {
                    records,
                    blobs: Vec::new(),
                };
                if let Some(payload) = &phase {
                    store.write_extensions_with_event(
                        &effects,
                        EventWrite {
                            job_id: &id,
                            kind: "piPhase",
                            payload,
                        },
                    )?;
                } else {
                    store.write_extensions(&effects)?;
                }
            } else if let Some(payload) = &phase {
                job.persist_event(&mut store, "piPhase", payload)?;
            } else {
                store.put_record("job", &id, &job)?;
            }
            admission
        };
        let control = Arc::new(Control {
            cancel: Cancellation::new(),
            input: Mutex::new(InputState::default()),
            changed: Condvar::new(),
            saw_tool: AtomicBool::new(false),
            #[cfg(any(
                all(target_os = "macos", target_arch = "aarch64"),
                all(target_os = "windows", target_arch = "x86_64")
            ))]
            saw_text: AtomicBool::new(false),
            recovery_required: AtomicBool::new(false),
        });
        controls.insert(id.clone(), control.clone());
        drop(controls);
        let context = JobContext {
            id: id.clone(),
            store: self.store.clone(),
            cancel: control.cancel.clone(),
            control,
            started,
            request_id: job.request_id.clone(),
        };
        if agent.is_some() {
            context.trace("admitted");
        }
        let manager = self.clone();
        let handle = std::thread::spawn(move || {
            let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                if agent.is_none() {
                    context.set_status("running")?;
                }
                work(context.clone(), admission)
            }))
            .unwrap_or_else(|_| {
                Err(YssError::new(ErrorCode::Io, "执行进程异常退出，请核对实际结果").into())
            });
            if let Ok(mut store) = lock_store(&context.store)
                && let Ok(Some(mut job)) = store.record::<JobRecord>("job", &id)
            {
                #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
                let pending_recovery = super::command_recovery::unresolved(&mut store)
                    .map(|records| records.iter().any(|record| record.job_id == id))
                    .unwrap_or(true);
                #[cfg(not(all(target_os = "windows", target_arch = "x86_64")))]
                let pending_recovery = false;
                match result {
                    Ok(value) => {
                        if context.control.recovery_required.load(Ordering::Relaxed) {
                            job.status = "recoveryRequired".into();
                        } else {
                            job.status = "completed".into();
                        }
                        job.result = Some(value);
                    }
                    Err(JobFailure { error, result }) => {
                        job.status = match error.code {
                            ErrorCode::Cancelled => "canceled",
                            ErrorCode::RecoveryRequired => "recoveryRequired",
                            _ => "failed",
                        }
                        .into();
                        job.result = result;
                        job.error = serde_json::to_value(error).ok();
                    }
                }
                if manager.stopping.is_cancelled() && job.status == "canceled" {
                    job.status = "interrupted".into();
                }
                // A canceled, failed or panicked worker may still own durable
                // scratch; its unresolved registration outranks the exit status.
                if pending_recovery {
                    let prior_error = job.error.take();
                    job.status = "recoveryRequired".into();
                    job.error = Some(
                        json!({"code":"RECOVERY_REQUIRED", "message":"命令暂存待恢复，请保留现场并核对", "previousError":prior_error}),
                    );
                }
                job.updated_at = now();
                if job
                    .persist_event(&mut store, "finished", &json!(job))
                    .is_ok()
                    && job.kind == "agent"
                {
                    context.trace("jobFinished");
                    if job
                        .result
                        .as_ref()
                        .is_some_and(|result| result["diffAvailable"] == true)
                    {
                        context.trace("resultReady");
                    }
                }
            }
            if let Ok(mut controls) = manager.controls.lock() {
                controls.remove(&id);
            }
        });
        self.handles
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业句柄失效"))?
            .push(handle);
        Ok(job)
    }

    pub fn cancel(&self, id: &str) -> Result<JobRecord> {
        if let Some(control) = self
            .controls
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业调度器失效"))?
            .get(id)
        {
            control.cancel.cancel();
            control.changed.notify_all();
        }
        self.get(id)
    }

    pub fn input(&self, id: &str, request_id: &str, value: Value) -> Result<()> {
        let controls = self
            .controls
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业调度器失效"))?;
        let control = controls
            .get(id)
            .ok_or_else(|| YssError::new(ErrorCode::Conflict, "执行已结束或已中断，请重新核对"))?;
        let mut input = control
            .input
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "输入队列失效"))?;
        if !input.pending.contains_key(request_id) || input.answers.contains_key(request_id) {
            return Err(YssError::new(
                ErrorCode::Conflict,
                "该输入请求已处理或已失效",
            ));
        }
        input.answers.insert(request_id.into(), value);
        control.changed.notify_all();
        Ok(())
    }

    pub fn shutdown(&self) {
        self.stopping.cancel();
        if let Ok(controls) = self.controls.lock() {
            for control in controls.values() {
                control.cancel.cancel();
                control.changed.notify_all();
            }
        }
        if let Ok(mut handles) = self.handles.lock() {
            for handle in handles.drain(..) {
                let _ = handle.join();
            }
        }
    }
}
