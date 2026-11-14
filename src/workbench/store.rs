use super::model::{MAX_STATE, WorkbenchState};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root, RootLock},
    model::RelPath,
};
use std::{
    collections::BTreeSet,
    path::{Path, PathBuf},
};

pub struct Store {
    lock: RootLock,
    state: WorkbenchState,
    file: FileSnapshot,
    uncertain: bool,
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

fn reject_git(path: &Path) -> Result<()> {
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

fn validate(state: &WorkbenchState) -> Result<()> {
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
    validate(&state)?;
    Ok((state, file.snapshot))
}

pub fn read_state(data_dir: &Path) -> Result<WorkbenchState> {
    let path = resolve_data_dir(Some(data_dir))?;
    reject_git(&path)?;
    if !path.try_exists()? {
        return Ok(WorkbenchState::default());
    }
    let path = std::fs::canonicalize(path)?;
    reject_git(&path)?;
    Ok(load(&Root::open(&path)?)?.0)
}

impl Store {
    pub fn open(data_dir: &Path) -> Result<Self> {
        let path = prepare_dir(data_dir)?;
        let lock = fs::lock_file(Root::open(&path)?, &RelPath::new("lock")?).map_err(|e| {
            if e.code == ErrorCode::Locked {
                YssError::new(ErrorCode::Locked, "工作台已运行；请使用已有窗口")
            } else {
                e
            }
        })?;
        let (state, file) = load(&lock.root)?;
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
            lock,
            state,
            file,
            uncertain: false,
        })
    }
    pub fn state(&self) -> &WorkbenchState {
        &self.state
    }
    pub fn data_dir(&self) -> &Path {
        &self.lock.root.path
    }
    pub fn commit(&mut self, expected_revision: u64, mut next: WorkbenchState) -> Result<u64> {
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
        next.revision = expected_revision
            .checked_add(1)
            .ok_or_else(|| invalid("revision 已溢出"))?;
        validate(&next)?;
        let bytes = codec::encode_json(&next)?;
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
        let root = &self.lock.root;
        root.revalidate()?;
        let (_, actual) = load(root)?;
        if actual != self.file {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "数据文件已被外部修改；请重启后核实",
            ));
        }
        if self.file != FileSnapshot::Missing {
            let original = root.read_checked(&state_path()?, MAX_STATE)?;
            let value: serde_json::Value = codec::decode_json(&original.bytes, state_limits())?;
            if let Some(version @ (1 | 2)) = value
                .get("schemaVersion")
                .and_then(serde_json::Value::as_u64)
            {
                backup_previous(root, &original.bytes, version)?;
                if root.read_checked(&state_path()?, MAX_STATE)?.snapshot != self.file {
                    return Err(YssError::new(
                        ErrorCode::ConcurrentChange,
                        "备份期间数据文件改变；原文件已保留",
                    ));
                }
            }
        }
        let temp = RelPath::new(format!("state-{}.tmp", fs::unique_id()?))?;
        let written = root.create_file(&temp, &bytes, 0o600)?;
        let published = root.rename(&temp, &state_path()?, &self.file, true);
        if let Err(error) = published {
            if error
                .context
                .get("renameApplied")
                .is_some_and(|v| v == "true")
            {
                self.uncertain = true;
                if let Ok(file) = root.read_checked(&state_path()?, MAX_STATE)
                    && file.bytes == bytes
                {
                    self.state = next;
                    self.file = file.snapshot;
                }
                return Err(YssError::new(
                    ErrorCode::RecoveryRequired,
                    "写入后校验失败；请刷新并核实结果，勿重复提交",
                )
                .context("revision", self.state.revision));
            }
            let _ = root.remove(&temp, &written);
            return Err(error);
        }
        let file = root
            .read_checked(&state_path()?, MAX_STATE)
            .inspect_err(|_e| {
                self.uncertain = true;
            })?;
        if file.bytes != bytes {
            self.uncertain = true;
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "写入后数据改变；请重启核实",
            ));
        }
        self.state = next;
        self.file = file.snapshot;
        Ok(self.state.revision)
    }
}

fn backup_previous(root: &Root, bytes: &[u8], version: u64) -> Result<()> {
    use sha2::{Digest, Sha256};
    let path = RelPath::new(format!(
        "state.v{version}-backup-{:x}.json",
        Sha256::digest(bytes)
    ))?;
    match root.read_checked(&path, MAX_STATE) {
        Ok(existing) if existing.bytes == bytes => return Ok(()),
        Ok(_) => return Err(invalid("已有升级备份内容不同；未覆盖")),
        Err(e) if e.context.get("kind").is_some_and(|k| k == "NotFound") => {}
        Err(e) => return Err(e),
    }
    let temp = RelPath::new(format!("backup-{}.tmp", fs::unique_id()?))?;
    let file = root.create_file(&temp, bytes, 0o600)?;
    if let Err(e) = root.rename(&temp, &path, &FileSnapshot::Missing, false) {
        let _ = root.remove(&temp, &file);
        return Err(e);
    }
    if root.read_checked(&path, MAX_STATE)?.bytes != bytes {
        return Err(invalid("升级备份校验失败；原状态未升级"));
    }
    Ok(())
}
