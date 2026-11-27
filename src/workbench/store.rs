use super::{
    model::{MAX_STATE, WorkbenchState},
    sqlite::{CommitEffects, Database, EventWrite, ExtensionChange, ExtensionRecords, RecordWrite},
    storage_migration::{self, DATABASE_NAME, StorageState},
};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root, RootLock},
    model::RelPath,
};
#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
use std::time::{Duration, Instant};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, Weak},
};
use tokio::sync::Notify;

#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
const DELTA_WINDOW: Duration = Duration::from_millis(32);
#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
const DELTA_BYTES: usize = 16 * 1024;

#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
struct BufferedDelta {
    kind: &'static str,
    item_id: String,
    text: String,
    deadline: Instant,
}

pub struct Store {
    database: Database,
    state: WorkbenchState,
    marker: FileSnapshot,
    uncertain: bool,
    projection_generation: u64,
    projection_changed: Arc<Notify>,
    activity_changed: Arc<Notify>,
    job_event_subscribers: BTreeMap<String, Weak<Notify>>,
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    job_deltas: BTreeMap<String, BufferedDelta>,
    // Keep the directory locked until the SQLite connection has closed.
    lock: RootLock,
}

fn state_limits() -> Limits {
    Limits {
        bytes: MAX_STATE,
        ..Limits::manifest()
    }
}

fn state_path() -> Result<RelPath> {
    RelPath::new("state.json")
}
fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsupportedFormat, message)
}

pub fn resolve_data_dir(override_dir: Option<&Path>) -> Result<PathBuf> {
    let path = match override_dir {
        Some(path) if path.is_absolute() => path.to_path_buf(),
        Some(_) => {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "数据目录必须是绝对路径",
            ));
        }
        None => directories::ProjectDirs::from("com", "ysstech", "yss-workbench")
            .ok_or_else(|| YssError::new(ErrorCode::Io, "无法确定个人数据目录"))?
            .data_local_dir()
            .to_path_buf(),
    };
    fs::absolute(&path)
}

pub(crate) fn reject_git(path: &Path) -> Result<()> {
    if path.ancestors().any(|p| {
        p.join(".git").symlink_metadata().is_ok()
            || p.join(".gitmodules").symlink_metadata().is_ok()
    }) {
        return Err(YssError::new(
            ErrorCode::UnsafePath,
            "工作台数据目录不能位于 Git 工作树内",
        ));
    }
    Ok(())
}

fn prepare_dir(path: &Path) -> Result<PathBuf> {
    let path = resolve_data_dir(Some(path))?;
    reject_git(&path)?;
    let mut parent = path.as_path();
    let mut missing = Vec::new();
    while !parent.try_exists()? {
        missing.push(
            parent
                .file_name()
                .ok_or_else(|| invalid("数据目录不合法"))?
                .to_owned(),
        );
        parent = parent
            .parent()
            .ok_or_else(|| invalid("数据目录没有父目录"))?;
    }
    let mut actual = std::fs::canonicalize(parent)?;
    reject_git(&actual)?;
    for name in missing.into_iter().rev() {
        let root = Root::open(&actual)?;
        let rel = RelPath::new(name.to_str().ok_or_else(|| invalid("数据目录不是 UTF-8"))?)?;
        match root.inspect(&rel)? {
            FileSnapshot::Missing => {
                root.create_dir(&rel)?;
            }
            FileSnapshot::Directory { .. } => {}
            _ => {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "数据目录包含不安全对象",
                ));
            }
        }
        actual.push(name);
    }
    Root::open(&actual)?;
    reject_git(&actual)?;
    Ok(actual)
}

pub(crate) fn validate_state(state: &WorkbenchState) -> Result<()> {
    if state.schema_version != 3 {
        return Err(invalid("不支持的工作台数据版本；原文件已保留"));
    }
    if state.revision > 9_007_199_254_740_991 {
        return Err(invalid("工作台 revision 超出可表示范围"));
    }
    let mut ids = BTreeSet::new();
    let mut roots = BTreeSet::new();
    let valid_id = |id: &str| {
        id.len() == 32
            && id
                .bytes()
                .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
    };
    for project in &state.projects {
        if !valid_id(&project.id)
            || !ids.insert(&project.id)
            || project.name.trim().is_empty()
            || !Path::new(&project.root_path).is_absolute()
            || !roots.insert(fs::alias_key(&project.root_path))
        {
            return Err(invalid("项目记录无效"));
        }
    }
    let mut paths = BTreeSet::new();
    for spec in &state.specs {
        if !valid_id(&spec.id)
            || !ids.insert(&spec.id)
            || !state.projects.iter().any(|p| p.id == spec.project_id)
            || spec.title.trim().is_empty()
            || !paths.insert((&spec.project_id, fs::alias_key(&spec.source_path)))
        {
            return Err(invalid("Spec 记录无效"));
        }
        super::sources::markdown_path(&spec.source_path)
            .map_err(|_| invalid("Spec 主规格路径无效"))?;
        if spec.status == super::model::Status::Blocked && spec.blocker.trim().is_empty() {
            return Err(invalid("阻塞 Spec 缺少说明"));
        }
        super::service::validate_resources(&spec.resources)
            .map_err(|_| invalid("Spec 资源无效"))?;
        super::service::validate_resources(&spec.inferred_resources)
            .map_err(|_| invalid("Spec 推断资源无效"))?;
    }
    for response in &state.question_responses {
        if !valid_id(&response.id)
            || !ids.insert(&response.id)
            || !state.specs.iter().any(|s| s.id == response.spec_id)
            || response.question_id.trim().is_empty()
            || response.question_digest.len() != 64
        {
            return Err(invalid("问题回复记录无效"));
        }
        super::content::validate_response(response)?;
    }
    for confirmation in &state.confirmations {
        if !valid_id(&confirmation.id)
            || !ids.insert(&confirmation.id)
            || !state.specs.iter().any(|s| s.id == confirmation.spec_id)
            || confirmation.scope.trim().is_empty()
            || confirmation.artifact_digest.len() != 64
        {
            return Err(invalid("内容确认记录无效"));
        }
    }
    for task in &state.tasks {
        if !valid_id(&task.id)
            || !ids.insert(&task.id)
            || !state.specs.iter().any(|s| s.id == task.spec_id)
            || task.title.trim().is_empty()
            || (task.status == super::model::Status::Blocked && task.blocker.trim().is_empty())
        {
            return Err(invalid("任务记录无效"));
        }
        super::service::validate_resources(&task.resources)?;
    }
    let mut stages = BTreeSet::new();
    for record in &state.stage_records {
        if record.stage == super::model::Stage::Unmarked
            || !state.specs.iter().any(|s| s.id == record.spec_id)
            || !stages.insert((&record.spec_id, record.stage))
            || (matches!(
                record.progress,
                super::model::StageProgress::Blocked | super::model::StageProgress::Skipped
            ) && record.note.trim().is_empty())
        {
            return Err(invalid("阶段记录无效"));
        }
        super::service::validate_resources(&record.resources)?;
    }
    for view in &state.saved_views {
        if !valid_id(&view.id)
            || !ids.insert(&view.id)
            || view.name.trim().is_empty()
            || view.name.chars().count() > 80
        {
            return Err(invalid("保存视图无效"));
        }
    }
    Ok(())
}

pub(crate) fn validate_state_limits(state: &WorkbenchState) -> Result<()> {
    let bytes = codec::encode_json(state)?;
    if bytes.len() as u64 > MAX_STATE {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "工作台数据已达到 16 MiB 上限；原数据保持可读",
        )
        .context("httpStatus", 413));
    }
    let _: WorkbenchState = codec::decode_json(&bytes, state_limits()).map_err(|_| {
        YssError::new(
            ErrorCode::InvalidInput,
            "工作台数据已达到结构容量上限；原数据保持可读",
        )
        .context("httpStatus", 413)
    })?;
    Ok(())
}

fn load(root: &Root) -> Result<(WorkbenchState, FileSnapshot)> {
    let file = match root.read_checked(&state_path()?, MAX_STATE) {
        Ok(file) => file,
        Err(e) if e.context.get("kind").is_some_and(|k| k == "NotFound") => {
            return Ok((WorkbenchState::default(), FileSnapshot::Missing));
        }
        Err(e) => return Err(e),
    };
    let state = super::migration::decode_state(&file.bytes, state_limits())
        .map_err(|e| invalid("工作台数据损坏；原文件已保留").context("detail", e.message))?;
    validate_state(&state)?;
    Ok((state, file.snapshot))
}

pub fn read_state(data_dir: &Path) -> Result<WorkbenchState> {
    Ok(read_state_with_records(data_dir, &[])?.0)
}

pub(crate) fn read_state_with_records(
    data_dir: &Path,
    kinds: &[&str],
) -> Result<(WorkbenchState, ExtensionRecords)> {
    let storage = storage_migration::probe(data_dir)?;
    if matches!(storage, StorageState::Empty) {
        return Ok((WorkbenchState::default(), ExtensionRecords::new()));
    }
    let path = std::fs::canonicalize(resolve_data_dir(Some(data_dir))?)?;
    reject_git(&path)?;
    let root = Root::open(&path)?;
    match storage {
        StorageState::Empty => Ok((WorkbenchState::default(), ExtensionRecords::new())),
        StorageState::Legacy => Ok((load(&root)?.0, ExtensionRecords::new())),
        StorageState::Preparing(_) => Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "迁移未完成；请启动新版工作台继续迁移",
        )),
        StorageState::Active(marker) => {
            let mut database = Database::open_read_only(&root, &RelPath::new(DATABASE_NAME)?)?;
            if database.identity() != &marker.identity() {
                return Err(YssError::new(
                    ErrorCode::RecoveryRequired,
                    "数据库与存储标记不匹配",
                ));
            }
            database.load_state_with_records(&root, kinds)
        }
    }
}
impl Store {
    pub fn open(data_dir: &Path) -> Result<Self> {
        let path = prepare_dir(data_dir)?;
        let lock = fs::lock_file(Root::open(&path)?, &RelPath::new("lock")?).map_err(|e| {
            if e.code == ErrorCode::Locked {
                YssError::new(
                    ErrorCode::Locked,
                    "工作台已运行；迁移须先退出旧工作台，再重试",
                )
            } else {
                e
            }
        })?;
        let storage = storage_migration::ensure_sqlite(&lock)?;
        let marker = lock.root.read_checked(&state_path()?, 16 * 1024)?.snapshot;
        let mut database = Database::open_read_write(&lock.root, &RelPath::new(DATABASE_NAME)?)?;
        if database.identity() != &storage.identity() {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "数据库与存储标记不匹配",
            ));
        }
        let state = database.load_state(&lock.root)?;
        if state
            .projects
            .iter()
            .any(|p| path.starts_with(&p.root_path))
        {
            return Err(YssError::new(
                ErrorCode::UnsafePath,
                "数据目录不能位于登记项目内",
            ));
        }
        Ok(Self {
            database,
            state,
            marker,
            uncertain: false,
            projection_generation: 0,
            projection_changed: Arc::new(Notify::new()),
            activity_changed: Arc::new(Notify::new()),
            job_event_subscribers: BTreeMap::new(),
            #[cfg(any(
                all(target_os = "macos", target_arch = "aarch64"),
                all(target_os = "windows", target_arch = "x86_64")
            ))]
            job_deltas: BTreeMap::new(),
            lock,
        })
    }
    pub fn state(&self) -> &WorkbenchState {
        &self.state
    }
    pub fn data_dir(&self) -> &Path {
        &self.lock.root.path
    }
    pub(super) fn projection_stamp(&self) -> (u64, u64) {
        (self.state.revision, self.projection_generation)
    }
    pub(super) fn projection_notification(&self) -> Arc<Notify> {
        self.projection_changed.clone()
    }
    pub(super) fn activity_notification(&self) -> Arc<Notify> {
        self.activity_changed.clone()
    }
    pub(super) fn capture_read_records(&mut self, kinds: &[&str]) -> Result<ExtensionRecords> {
        if self.uncertain {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "存储状态待核实，请重启工作台",
            ));
        }
        if self
            .lock
            .root
            .read_checked(&state_path()?, 16 * 1024)?
            .snapshot
            != self.marker
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "存储标记已改变，请重启核实",
            ));
        }
        self.database
            .read_records_at_revision(&self.lock.root, self.state.revision, kinds)
    }
    fn notify_activity(&self, effects: &CommitEffects) {
        if effects.records.iter().any(|write| {
            matches!(
                write.kind.as_str(),
                "job"
                    | "agentRun"
                    | "agentRound"
                    | "agentReviewAttempt"
                    | "agentReviewReceipt"
                    | "agentAcceptance"
                    | "agentQueue"
                    | "externalLink"
            )
        }) {
            self.activity_changed.notify_one();
        }
    }
    pub fn commit(&mut self, expected_revision: u64, next: WorkbenchState) -> Result<u64> {
        self.commit_with_effects(expected_revision, next, &CommitEffects::default())
    }
    pub(crate) fn commit_with_effects(
        &mut self,
        expected_revision: u64,
        mut next: WorkbenchState,
        effects: &CommitEffects,
    ) -> Result<u64> {
        if self.uncertain {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "写入结果待确认；请重启工作台后核实",
            ));
        }
        if expected_revision != self.state.revision {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "内容已被其他窗口修改；请刷新后重新提交",
            )
            .context("revision", self.state.revision));
        }
        if self
            .lock
            .root
            .read_checked(&state_path()?, 16 * 1024)?
            .snapshot
            != self.marker
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "存储标记已被外部修改，请重启核实",
            ));
        }
        if next == self.state && effects.records.is_empty() && effects.blobs.is_empty() {
            return Ok(expected_revision);
        }
        next.revision = expected_revision
            .checked_add(1)
            .ok_or_else(|| invalid("revision 已溢出"))?;
        if let Err(error) = self.database.commit_delta(
            &self.lock.root,
            expected_revision,
            &self.state,
            &next,
            effects,
        ) {
            self.uncertain = error.code == ErrorCode::RecoveryRequired;
            return Err(error);
        }
        if !self
            .lock
            .root
            .read_checked(&state_path()?, 16 * 1024)
            .is_ok_and(|f| f.snapshot == self.marker)
        {
            self.uncertain = true;
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "提交后存储标记改变，请重启核实，勿重复提交",
            ));
        }
        self.state = next;
        self.projection_changed.notify_one();
        self.notify_activity(effects);
        Ok(self.state.revision)
    }
    pub fn record<T: serde::de::DeserializeOwned>(
        &mut self,
        kind: &str,
        id: &str,
    ) -> Result<Option<T>> {
        self.database
            .read_record(kind, id)?
            .map(|payload| serde_json::from_str(&payload).map_err(|_| invalid("扩展记录格式无效")))
            .transpose()
    }
    pub fn record_entries<T: serde::de::DeserializeOwned>(
        &mut self,
        kind: &str,
    ) -> Result<Vec<(String, T)>> {
        self.database
            .read_records(kind)?
            .into_iter()
            .map(|(id, payload)| {
                Ok((
                    id,
                    serde_json::from_str(&payload).map_err(|_| invalid("扩展记录格式无效"))?,
                ))
            })
            .collect()
    }
    pub(crate) fn has_record(&self, kind: &str, id: &str) -> Result<bool> {
        self.database.has_record(kind, id)
    }
    pub fn records<T: serde::de::DeserializeOwned>(&mut self, kind: &str) -> Result<Vec<T>> {
        self.database
            .read_records(kind)?
            .into_iter()
            .map(|(_, payload)| {
                serde_json::from_str(&payload).map_err(|_| invalid("扩展记录格式无效"))
            })
            .collect()
    }
    pub fn put_record<T: serde::Serialize>(
        &mut self,
        kind: &str,
        id: &str,
        value: &T,
    ) -> Result<()> {
        self.put_record_with_event(kind, id, value, None)
    }
    pub(crate) fn put_record_with_event<T: serde::Serialize>(
        &mut self,
        kind: &str,
        id: &str,
        value: &T,
        event: Option<EventWrite<'_>>,
    ) -> Result<()> {
        let effects = CommitEffects {
            records: vec![RecordWrite {
                kind: kind.into(),
                id: id.into(),
                payload: Some(
                    serde_json::to_string(value).map_err(|_| invalid("扩展记录编码失败"))?,
                ),
            }],
            blobs: Vec::new(),
        };
        self.write_extensions_with(&effects, event.map(ExtensionChange::Event))
    }
    pub fn delete_record(&mut self, kind: &str, id: &str) -> Result<()> {
        self.write_extensions(&CommitEffects {
            records: vec![RecordWrite {
                kind: kind.into(),
                id: id.into(),
                payload: None,
            }],
            blobs: Vec::new(),
        })
    }
    pub(crate) fn write_extensions(&mut self, effects: &CommitEffects) -> Result<()> {
        self.write_extensions_with(effects, None)
    }

    /// Commit related extension records and a job event in one SQLite transaction.
    pub(super) fn write_extensions_with_event(
        &mut self,
        effects: &CommitEffects,
        event: EventWrite<'_>,
    ) -> Result<()> {
        self.write_extensions_with(effects, Some(ExtensionChange::Event(event)))
    }

    pub(super) fn delete_agent_history(
        &mut self,
        run_id: &str,
        job_ids: &BTreeSet<String>,
        records: Vec<RecordWrite>,
    ) -> Result<()> {
        self.write_extensions_with(
            &CommitEffects {
                records,
                blobs: Vec::new(),
            },
            Some(ExtensionChange::DeleteAgentHistory { run_id, job_ids }),
        )
    }

    fn write_extensions_with(
        &mut self,
        effects: &CommitEffects,
        change: Option<ExtensionChange<'_>>,
    ) -> Result<()> {
        let event = match &change {
            Some(ExtensionChange::Event(event)) => Some((event.job_id, event.kind)),
            _ => None,
        };
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        if let Some((job_id, _)) = event {
            self.flush_job_delta(job_id)?;
        }
        if self.uncertain {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "写入结果待确认，请重启核实",
            ));
        }
        if self
            .lock
            .root
            .read_checked(&state_path()?, 16 * 1024)?
            .snapshot
            != self.marker
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "存储标记已改变，请重启核实",
            ));
        }
        let deleted_jobs = match &change {
            Some(ExtensionChange::DeleteAgentHistory { job_ids, .. }) => Some(*job_ids),
            _ => None,
        };
        let result = self
            .database
            .write_records(&self.lock.root, effects, change);
        if result.is_ok() {
            if effects.records.iter().any(|write| {
                super::progress::RECORD_KINDS.contains(&write.kind.as_str())
                    || matches!(
                        write.kind.as_str(),
                        super::materials::SNAPSHOTS | super::materials::REVIEWS
                    )
            }) {
                self.projection_generation = self.projection_generation.wrapping_add(1);
                self.projection_changed.notify_one();
            }
            self.notify_activity(effects);
            if let Some((job_id, kind)) = event {
                self.notify_job_event(job_id, kind);
            }
            if let Some(job_ids) = deleted_jobs {
                self.activity_changed.notify_one();
                for job_id in job_ids {
                    #[cfg(any(
                        all(target_os = "macos", target_arch = "aarch64"),
                        all(target_os = "windows", target_arch = "x86_64")
                    ))]
                    self.job_deltas.remove(job_id);
                    self.wake_job_stream(job_id);
                }
            }
        }
        if result
            .as_ref()
            .is_err_and(|error| error.code == ErrorCode::RecoveryRequired)
        {
            self.uncertain = true;
        }
        result
    }

    pub(super) fn subscribe_job_events(&mut self, job_id: &str) -> Arc<Notify> {
        if let Some(notify) = self
            .job_event_subscribers
            .get(job_id)
            .and_then(Weak::upgrade)
        {
            return notify;
        }
        self.job_event_subscribers
            .retain(|_, notify| notify.strong_count() != 0);
        let notify = Arc::new(Notify::new());
        self.job_event_subscribers
            .insert(job_id.into(), Arc::downgrade(&notify));
        notify
    }

    fn wake_job_stream(&self, job_id: &str) {
        if let Some(notify) = self
            .job_event_subscribers
            .get(job_id)
            .and_then(Weak::upgrade)
        {
            notify.notify_waiters();
        }
    }

    fn notify_job_event(&self, job_id: &str, kind: &str) {
        self.wake_job_stream(job_id);
        if matches!(
            kind,
            "inputRequested" | "inputResolved" | "piQuestionResult"
        ) {
            self.activity_changed.notify_one();
        }
    }

    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    pub(super) fn append_pi_delta(
        &mut self,
        job_id: &str,
        kind: &'static str,
        item_id: &str,
        text: &str,
        immediate: bool,
    ) -> Result<Option<Instant>> {
        if immediate || text.len() >= DELTA_BYTES {
            self.append_event(
                job_id,
                kind,
                &serde_json::json!({"itemId":item_id,"delta":text}),
            )?;
            return Ok(None);
        }
        let now = Instant::now();
        if self.job_deltas.get(job_id).is_some_and(|batch| {
            batch.kind != kind
                || batch.item_id != item_id
                || now >= batch.deadline
                || batch.text.len() + text.len() > DELTA_BYTES
        }) {
            self.flush_job_delta(job_id)?;
        }
        let batch = self
            .job_deltas
            .entry(job_id.into())
            .or_insert_with(|| BufferedDelta {
                kind,
                item_id: item_id.into(),
                text: String::new(),
                deadline: now + DELTA_WINDOW,
            });
        batch.text.push_str(text);
        let deadline = batch.deadline;
        if batch.text.len() == DELTA_BYTES || Instant::now() >= deadline {
            self.flush_job_delta(job_id)?;
            return Ok(None);
        }
        Ok(Some(deadline))
    }

    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    pub(super) fn flush_job_delta(&mut self, job_id: &str) -> Result<()> {
        let Some(batch) = self.job_deltas.remove(job_id) else {
            return Ok(());
        };
        let payload = serde_json::json!({"itemId":batch.item_id,"delta":batch.text});
        if let Err(error) = self.persist_event(job_id, batch.kind, &payload) {
            self.job_deltas.insert(job_id.into(), batch);
            return Err(error);
        }
        Ok(())
    }

    pub fn append_event(
        &mut self,
        job_id: &str,
        kind: &str,
        payload: &serde_json::Value,
    ) -> Result<u64> {
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        self.flush_job_delta(job_id)?;
        self.persist_event(job_id, kind, payload)
    }

    fn persist_event(
        &mut self,
        job_id: &str,
        kind: &str,
        payload: &serde_json::Value,
    ) -> Result<u64> {
        if self.uncertain {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "写入结果待核实，请重启工作台",
            ));
        }
        if self
            .lock
            .root
            .read_checked(&state_path()?, 16 * 1024)?
            .snapshot
            != self.marker
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "存储标记已改变，请重启核实",
            ));
        }
        let result = self.database.append_event(
            &self.lock.root,
            job_id,
            kind,
            &serde_json::to_string(payload).map_err(|_| invalid("作业事件编码失败"))?,
            &super::sources::now(),
        );
        if result.is_ok() {
            self.notify_job_event(job_id, kind);
        }
        if result
            .as_ref()
            .is_err_and(|error| error.code == ErrorCode::RecoveryRequired)
        {
            self.uncertain = true;
        }
        result
    }
    pub fn events(&mut self, job_id: &str, after: u64) -> Result<Vec<super::jobs::JobEvent>> {
        self.events_through(job_id, after, i64::MAX as u64)
    }
    pub(crate) fn events_through(
        &self,
        job_id: &str,
        after: u64,
        through: u64,
    ) -> Result<Vec<super::jobs::JobEvent>> {
        self.database.events_through(job_id, after, through)
    }
    pub(crate) fn last_event_id(&self, job_id: &str) -> Result<u64> {
        self.database.last_event_id(job_id)
    }

    /// Capture a stable event boundary while the caller holds the Store mutex.
    pub(crate) fn desktop_notification_baseline(&mut self) -> Result<serde_json::Value> {
        let cursor = self.database.desktop_notification_cursor()?;
        let event_count = self.database.desktop_notification_event_count(cursor)?;
        Ok(serde_json::json!({"cursor": cursor, "eventCount": event_count}))
    }

    pub(crate) fn desktop_notification_pending_page(
        &mut self,
        baseline: u64,
        event_count: u64,
        after: u64,
    ) -> Result<serde_json::Value> {
        if baseline > i64::MAX as u64 || after > baseline || event_count > i64::MAX as u64 {
            return Err(YssError::new(ErrorCode::InvalidInput, "作业通知基线无效"));
        }
        if self.database.desktop_notification_event_count(baseline)? != event_count {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "作业通知基线已变化，请重新读取",
            )
            .context("httpStatus", 409));
        }
        let (items, has_more) = self
            .database
            .desktop_notification_pending_page(baseline, after)?;
        let next_after = if has_more {
            items.last().map_or(after, |event| event.event_id)
        } else {
            baseline
        };
        Ok(serde_json::json!({"items": items, "nextAfter": next_after, "hasMore": has_more}))
    }

    pub(crate) fn desktop_notification_page(&mut self, after: u64) -> Result<serde_json::Value> {
        if after > i64::MAX as u64 {
            return Err(YssError::new(ErrorCode::InvalidInput, "作业事件游标无效"));
        }
        let (items, has_more) = self.database.desktop_notification_events(after)?;
        let next_after = if has_more {
            items.last().map_or(after, |event| event.id)
        } else {
            after.max(self.database.desktop_notification_cursor()?)
        };
        Ok(serde_json::json!({"items": items, "nextAfter": next_after, "hasMore": has_more}))
    }

    pub(crate) fn desktop_notification_pending(
        &mut self,
        job_id: &str,
    ) -> Result<serde_json::Value> {
        let job = self
            .record::<super::jobs::JobRecord>("job", job_id)?
            .ok_or_else(|| {
                YssError::new(ErrorCode::InvalidInput, "执行记录不存在").context("httpStatus", 404)
            })?;
        let pending = if job.terminal() {
            Vec::new()
        } else {
            self.database.desktop_notification_pending(job_id)?
        };
        let items = pending.into_iter().map(|(event_id, request_id)| {
            serde_json::json!({"eventId": event_id, "requestId": request_id})
        }).collect::<Vec<_>>();
        Ok(serde_json::json!({"items": items}))
    }
    pub(crate) fn run_event(&self, job_id: &str) -> Result<Option<serde_json::Value>> {
        self.database.run_event(job_id)
    }
    pub(super) fn ci_evidence(&self, job_id: &str) -> Result<Option<serde_json::Value>> {
        self.database.ci_evidence(job_id)
    }
    pub(super) fn continuation_tasks(
        &self,
        run_id: &str,
        job_id: &str,
        per_project_limit: usize,
    ) -> Result<Vec<serde_json::Value>> {
        self.database
            .continuation_tasks(run_id, job_id, per_project_limit)
    }
    pub(super) fn continuation_response_ids(
        &self,
        job_id: &str,
        limit: usize,
    ) -> Result<Vec<String>> {
        self.database.continuation_response_ids(job_id, limit)
    }
    pub(crate) fn agent_question_events(&self, job_id: &str) -> Result<Vec<super::jobs::JobEvent>> {
        self.database.agent_question_events(job_id)
    }
    pub fn has_events_after(&self, job_id: &str, after: u64) -> Result<bool> {
        self.database.has_events_after(job_id, after)
    }
    pub(crate) fn blob(&self, digest: &str) -> Result<Vec<u8>> {
        self.database.blob(digest)
    }
    pub fn get_blob(&self, digest: &str) -> Result<Vec<u8>> {
        self.database.blob(digest)
    }
    pub fn put_blob(&mut self, bytes: &[u8]) -> Result<String> {
        let digest = crate::model::Sha256::of(bytes).as_str().to_owned();
        self.write_extensions(&CommitEffects {
            records: Vec::new(),
            blobs: vec![(digest.clone(), bytes.to_vec())],
        })?;
        Ok(digest)
    }
    pub fn storage_usage(&self) -> Result<serde_json::Value> {
        self.database.storage_usage()
    }
}
