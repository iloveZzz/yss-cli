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
    pub request_id: String,
    pub roots: Vec<String>,
    pub created_at: String,
    pub updated_at: String,
    pub result: Option<Value>,
    pub error: Option<Value>,
}

impl JobRecord {
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
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    saw_text: AtomicBool,
    recovery_required: AtomicBool,
}

struct Admission {
    kind: String,
    title: String,
    request_id: String,
    roots: Vec<String>,
    agent: Option<DeferredAgent>,
}

pub(super) struct DeferredAgent {
    pub spec_id: String,
    pub run_id: Option<String>,
    pub fingerprint: String,
}

fn check_roots(jobs: &[JobRecord], roots: &[String], exclude: Option<&str>) -> Result<()> {
    for active in jobs
        .iter()
        .filter(|job| !job.terminal() && Some(job.id.as_str()) != exclude)
    {
        if roots.iter().any(|root| {
            active.roots.iter().any(|other| {
                std::path::Path::new(root).starts_with(other)
                    || std::path::Path::new(other).starts_with(root)
            })
        }) {
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
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    pub(super) fn mark_agent_started(&self) -> Result<()> {
        let mut store = lock_store(&self.store)?;
        let mut job: JobRecord = store
            .record("job", &self.id)?
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "执行记录不存在"))?;
        if !job.terminal() && job.agent_started_at.is_none() {
            job.agent_started_at = Some(now());
            store.put_record("job", &job.id, &job)?;
        }
        Ok(())
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
        lock_store(&self.store)?.append_event(&self.id, kind, &payload)?;
        if kind == "piTool" && !self.control.saw_tool.swap(true, Ordering::Relaxed) {
            self.trace("firstTool");
        }
        Ok(())
    }

    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    pub(super) fn emit_pi_delta(
        &self,
        kind: &'static str,
        item_id: &str,
        text: &str,
    ) -> Result<Option<Instant>> {
        let mut store = lock_store(&self.store)?;
        let first_text =
            kind == "item/agentMessage/delta" && !self.control.saw_text.load(Ordering::Relaxed);
        let deadline = store.append_pi_delta(&self.id, kind, item_id, text, first_text)?;
        if first_text {
            self.control.saw_text.store(true, Ordering::Relaxed);
            drop(store);
            self.trace("firstTextStored");
        }
        Ok(deadline)
    }

    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
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
            for mut job in state.records::<JobRecord>("job")? {
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
            },
            check,
            work,
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
            },
            check,
            |context, ()| work(context),
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
        F: FnOnce(JobContext, T) -> Result<Value> + Send + 'static,
        T: Send + 'static,
    {
        let started = Instant::now();
        let Admission {
            kind,
            title,
            request_id,
            roots,
            agent,
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
                    || (agent.is_none()
                        && (existing.roots != job.roots || existing.title != job.title))
                {
                    return Err(YssError::new(
                        ErrorCode::Conflict,
                        "请求 ID 已用于另一项操作",
                    ));
                }
                return Ok(existing.clone());
            }
            if let Some(run_id) = agent.as_ref().and_then(|value| value.run_id.as_ref())
                && let Some(active) = jobs.iter().find(|existing| {
                    !existing.terminal() && existing.agent_run_id.as_ref() == Some(run_id)
                })
            {
                return Err(
                    YssError::new(ErrorCode::Locked, "此会话已有准备或运行中的轮次")
                        .context("jobId", &active.id),
                );
            }
            check_roots(&jobs, &job.roots, None)?;
            super::agent::ensure_roots_available(&mut store, &job.roots)?;
            let admission = check(&mut store, &mut job)?;
            if let Some(agent) = &agent {
                let records = [
                    ("job", id.as_str(), serde_json::to_string(&job)),
                    (
                        "jobRequest",
                        job.request_id.as_str(),
                        serde_json::to_string(&agent.fingerprint),
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
                store.write_extensions(&super::sqlite::CommitEffects {
                    records,
                    blobs: Vec::new(),
                })?;
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
            #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
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
                Err(YssError::new(
                    ErrorCode::Io,
                    "执行进程异常退出，请核对实际结果",
                ))
            });
            if let Ok(mut store) = lock_store(&context.store)
                && let Ok(Some(mut job)) = store.record::<JobRecord>("job", &id)
            {
                match result {
                    Ok(value) => {
                        if context.control.recovery_required.load(Ordering::Relaxed) {
                            job.status = "recoveryRequired".into();
                        } else {
                            job.status = "completed".into();
                        }
                        job.result = Some(value);
                    }
                    Err(error) => {
                        job.status = match error.code {
                            ErrorCode::Cancelled => "canceled",
                            ErrorCode::RecoveryRequired => "recoveryRequired",
                            _ => "failed",
                        }
                        .into();
                        job.error = serde_json::to_value(error).ok();
                    }
                }
                if manager.stopping.is_cancelled() && job.status == "canceled" {
                    job.status = "interrupted".into();
                }
                job.updated_at = now();
                let _ = job.persist_event(&mut store, "finished", &json!(job));
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
        let job = self.get(id)?;
        if let Some(control) = self
            .controls
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "作业调度器失效"))?
            .get(id)
        {
            control.cancel.cancel();
            control.changed.notify_all();
        }
        Ok(job)
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
