use super::{sources::now, store::Store};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    sync::{Arc, Condvar, Mutex},
    time::Duration,
};

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JobRecord {
    pub id: String,
    pub kind: String,
    pub title: String,
    pub status: String,
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
}

pub struct JobManager {
    pub store: Arc<Mutex<Store>>,
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
}

pub fn lock_store(store: &Arc<Mutex<Store>>) -> Result<std::sync::MutexGuard<'_, Store>> {
    store
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "工作台数据锁失效，请重启核实"))
}

impl JobContext {
    pub fn emit(&self, kind: &str, payload: Value) -> Result<()> {
        lock_store(&self.store)?.append_event(&self.id, kind, &payload)?;
        Ok(())
    }

    pub fn set_status(&self, status: &str) -> Result<()> {
        let mut store = lock_store(&self.store)?;
        let mut job: JobRecord = store
            .record("job", &self.id)?
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "执行记录不存在"))?;
        job.status = status.into();
        job.updated_at = now();
        store.put_record("job", &job.id, &job)?;
        store.append_event(&self.id, "status", &json!({"status": status}))?;
        Ok(())
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
        self.set_status("waitingInput")?;
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
                    state.put_record("job", &job.id, &job)?;
                }
            }
        }
        Ok(Arc::new(Self {
            store,
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
        if let Some(existing) = self
            .list()?
            .into_iter()
            .find(|job| job.request_id == request_id)
        {
            if existing.kind != kind || existing.roots != roots || existing.title != title {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "请求 ID 已用于另一项操作",
                ));
            }
            return Ok(existing);
        }
        for active in self.list()?.into_iter().filter(|job| !job.terminal()) {
            if roots.iter().any(|root| {
                active.roots.iter().any(|other| {
                    std::path::Path::new(root).starts_with(other)
                        || std::path::Path::new(other).starts_with(root)
                })
            }) {
                return Err(
                    YssError::new(ErrorCode::Locked, "目标目录已有运行中的写入操作")
                        .context("jobId", active.id),
                );
            }
        }
        let id = fs::unique_id()?;
        let at = now();
        let job = JobRecord {
            id: id.clone(),
            kind,
            title,
            status: "preparing".into(),
            request_id,
            roots,
            created_at: at.clone(),
            updated_at: at,
            result: None,
            error: None,
        };
        {
            let mut store = lock_store(&self.store)?;
            super::agent::ensure_roots_available(&mut store, &job.roots)?;
            store.put_record("job", &id, &job)?;
        }
        let control = Arc::new(Control {
            cancel: Cancellation::new(),
            input: Mutex::new(InputState::default()),
            changed: Condvar::new(),
        });
        controls.insert(id.clone(), control.clone());
        drop(controls);
        let context = JobContext {
            id: id.clone(),
            store: self.store.clone(),
            cancel: control.cancel.clone(),
            control,
        };
        let manager = self.clone();
        let handle = std::thread::spawn(move || {
            let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                context.set_status("running")?;
                work(context.clone())
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
                        if job.status != "recoveryRequired" {
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
                let _ = store.put_record("job", &id, &job);
                let _ = store.append_event(&id, "finished", &json!(job));
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
