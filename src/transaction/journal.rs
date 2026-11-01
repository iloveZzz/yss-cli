use super::{Desired, TransactionPhase, required};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result},
    fs::{FileIdentity, FileSnapshot, Root},
    model::{MAX_ASSET, MAX_MANIFEST, RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct Identity {
    pub format: String,
    pub format_version: u32,
    pub id: String,
    pub root: FileIdentity,
    pub directory: FileIdentity,
    pub plan_sha256: Sha256,
    pub private_files: BTreeSet<RelPath>,
    pub private_directories: BTreeSet<RelPath>,
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct Plan {
    pub format: String,
    pub format_version: u32,
    pub root: FileIdentity,
    pub id: String,
    pub records: Vec<Record>,
    pub guards: BTreeMap<RelPath, FileSnapshot>,
    pub objects: BTreeMap<Sha256, u64>,
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Record {
    pub path: RelPath,
    pub before: FileSnapshot,
    pub after: Desired,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(super) enum ReplacementDirection {
    Apply,
    Rollback,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct ExecutableReplacement {
    pub direction: ReplacementDirection,
    pub target: RelPath,
    pub retained: RelPath,
    pub candidate: RelPath,
    pub before: FileSnapshot,
    pub after: FileSnapshot,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "event", rename_all = "kebab-case", deny_unknown_fields)]
pub(super) enum Event {
    ExecutableReplacement {
        index: usize,
        step: ExecutableReplacement,
    },
    Publishing {
        index: usize,
    },
    Phase {
        phase: TransactionPhase,
    },
    Owned {
        path: RelPath,
        snapshot: FileSnapshot,
    },
    Intent {
        index: usize,
    },
    BackedUp {
        index: usize,
    },
    Applied {
        index: usize,
        snapshot: FileSnapshot,
    },
    Restoring {
        index: usize,
    },
    Restored {
        index: usize,
        snapshot: FileSnapshot,
    },
    Cleaning {
        path: RelPath,
        snapshot: FileSnapshot,
    },
    Cleaned {
        path: RelPath,
    },
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Line {
    sequence: usize,
    previous: Sha256,
    event: Event,
}
pub(super) struct Loaded {
    pub identity: Identity,
    pub plan: Option<Plan>,
    pub phase: TransactionPhase,
    pub events: Vec<Event>,
    pub chain: Sha256,
    pub log_identity: Option<FileIdentity>,
    pub owned: BTreeMap<RelPath, FileSnapshot>,
    pub cleaned: BTreeSet<RelPath>,
}
pub(super) fn base(id: &str) -> Result<RelPath> {
    if id.len() != 32
        || !id
            .bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
    {
        return Err(required("事务目录身份不合法"));
    }
    RelPath::new(format!(".yss/transactions/{id}"))
}
pub(super) fn candidate(id: &str, index: usize) -> Result<RelPath> {
    base(id)?.join(&format!("candidates/{index}"))
}
pub(super) fn backup(id: &str, index: usize) -> Result<RelPath> {
    base(id)?.join(&format!("backups/{index}"))
}
pub(super) fn restore(id: &str, index: usize) -> Result<RelPath> {
    base(id)?.join(&format!("restores/{index}"))
}
pub(super) fn object(id: &str, hash: &Sha256) -> Result<RelPath> {
    base(id)?.join(&format!("objects/{hash}"))
}

fn read_optional(root: &Root, path: &RelPath) -> Result<Option<Vec<u8>>> {
    match root.inspect(path)? {
        FileSnapshot::Missing => Ok(None),
        FileSnapshot::File { .. } => Ok(Some(root.read_checked(path, MAX_MANIFEST)?.bytes)),
        _ => Err(required("事务元数据不是普通文件")),
    }
}
pub(super) fn load(root: &Root, id: &str, verify_objects: bool) -> Result<Loaded> {
    let base = base(id)?;
    let identity: Identity = codec::decode_json(
        &root
            .read_checked(&base.join("identity.json")?, MAX_MANIFEST)?
            .bytes,
        Limits::manifest(),
    )?;
    codec::ensure_format(&identity.format, identity.format_version, "yss.transaction")?;
    if identity.id != id
        || identity.root != root.identity
        || root.inspect(&base)?.identity() != Some(&identity.directory)
    {
        return Err(required("事务与当前根目录身份不匹配"));
    }
    for path in identity
        .private_files
        .iter()
        .chain(identity.private_directories.iter())
    {
        if !path.as_str().starts_with(&format!("{base}/")) {
            return Err(required("事务私有范围越界"));
        }
    }
    let plan = if let Some(bytes) = read_optional(root, &base.join("plan.json")?)? {
        if Sha256::of(&bytes) != identity.plan_sha256 {
            return Err(required("事务计划原始字节摘要不一致"));
        }
        let plan: Plan = codec::decode_json(&bytes, Limits::manifest())?;
        codec::ensure_format(&plan.format, plan.format_version, "yss.transaction")?;
        if plan.root != root.identity || plan.id != id || plan.records.is_empty() {
            return Err(required("事务计划身份不合法"));
        }
        validate_records(&plan)?;
        Some(plan)
    } else {
        None
    };
    let log = base.join("journal.jsonl")?;
    let log_state = root.inspect(&log)?;
    let bytes = read_optional(root, &log)?.unwrap_or_default();
    if !bytes.is_empty() && !bytes.ends_with(b"\n") {
        return Err(required("事务日志被截断"));
    }
    let mut loaded = Loaded {
        identity,
        plan,
        phase: TransactionPhase::Preparing,
        events: Vec::new(),
        chain: Sha256::of(b""),
        log_identity: log_state.identity().cloned(),
        owned: BTreeMap::new(),
        cleaned: BTreeSet::new(),
    };
    for bytes in bytes.split_inclusive(|b| *b == b'\n') {
        let line: Line = codec::decode_json(bytes, Limits::manifest())?;
        if line.sequence != loaded.events.len() || line.previous != loaded.chain {
            return Err(required("事务日志顺序或摘要链不一致"));
        }
        loaded.accept(&line.event)?;
        loaded.events.push(line.event);
        loaded.chain = Sha256::of(bytes);
    }
    if loaded.plan.is_none()
        && loaded.phase != TransactionPhase::Preparing
        && loaded.phase != TransactionPhase::RolledBack
    {
        return Err(required("事务缺少不可变计划"));
    }
    validate_private(root, &loaded)?;
    if verify_objects
        && !matches!(
            loaded.phase,
            TransactionPhase::Preparing
                | TransactionPhase::Committed
                | TransactionPhase::RolledBack
        )
    {
        let plan = loaded
            .plan
            .as_ref()
            .ok_or_else(|| required("事务缺少计划"))?;
        for (hash, size) in &plan.objects {
            let bytes = root.read_checked(&object(id, hash)?, MAX_ASSET)?.bytes;
            if bytes.len() as u64 != *size || Sha256::of(&bytes) != *hash {
                return Err(required("事务内容对象不完整"));
            }
        }
    }
    Ok(loaded)
}
fn validate_records(plan: &Plan) -> Result<()> {
    let mut seen = BTreeSet::new();
    for (index, record) in plan.records.iter().enumerate() {
        if !seen.insert(crate::fs::alias_key(record.path.as_str())) {
            return Err(required("事务计划包含重复路径"));
        }
        if record.path.as_str().starts_with(".yss/") {
            if index + 1 != plan.records.len()
                || !matches!(record.path.as_str(), ".yss/project.json" | ".yss/tool.json")
                || !matches!(record.after, Desired::File { .. })
            {
                return Err(required("事务元数据必须最后提交"));
            }
        } else if index + 1 == plan.records.len() {
            return Err(required("事务缺少最后的安装记录"));
        }
        if matches!(
            record.before,
            FileSnapshot::Dangerous | FileSnapshot::Directory { .. }
        ) {
            return Err(required("事务前置状态不合法"));
        }
        if let Desired::File { descriptor } = &record.after
            && plan.objects.get(&descriptor.sha256) != Some(&descriptor.size)
        {
            return Err(required("事务 after 对象未登记"));
        }
        if let Some(descriptor) = record.before.descriptor()
            && plan.objects.get(&descriptor.sha256) != Some(&descriptor.size)
        {
            return Err(required("事务 before 对象未登记"));
        }
    }
    Ok(())
}
impl Loaded {
    fn accept(&mut self, event: &Event) -> Result<()> {
        match event {
            Event::Phase { phase } => {
                let valid = matches!(
                    (self.phase, phase),
                    (
                        TransactionPhase::Preparing,
                        TransactionPhase::Prepared | TransactionPhase::RolledBack
                    ) | (
                        TransactionPhase::Prepared,
                        TransactionPhase::Applying | TransactionPhase::RollingBack
                    ) | (
                        TransactionPhase::Applying,
                        TransactionPhase::Committed | TransactionPhase::RollingBack
                    ) | (
                        TransactionPhase::RollingBack,
                        TransactionPhase::RollingBack | TransactionPhase::RolledBack
                    )
                );
                if !valid {
                    return Err(required("事务阶段顺序不合法"));
                }
                if *phase == TransactionPhase::Committed
                    && self.plan.as_ref().is_none_or(|p| {
                        self.events
                            .iter()
                            .filter(|e| matches!(e, Event::Applied { .. }))
                            .count()
                            != p.records.len()
                    })
                {
                    return Err(required("提交前操作记录不完整"));
                }
                self.phase = *phase;
            }
            Event::Owned { path, snapshot } => {
                if !self.identity.private_files.contains(path)
                    && !self.identity.private_directories.contains(path)
                {
                    return Err(required("事务私有路径未登记"));
                }
                if self.phase.terminal()
                    || snapshot.identity().is_none()
                    || self.owned.contains_key(path)
                {
                    return Err(required("私有对象身份不合法"));
                }
                self.owned.insert(path.clone(), snapshot.clone());
            }
            Event::Cleaned { path } => {
                if !self.phase.terminal()
                    || !self
                        .events
                        .iter()
                        .any(|e| matches!(e,Event::Cleaning {path:p,..} if p==path))
                {
                    return Err(required("清理完成缺少意图记录"));
                }
                self.cleaned.insert(path.clone());
            }
            Event::ExecutableReplacement { index, step } => {
                let plan = self
                    .plan
                    .as_ref()
                    .ok_or_else(|| required("替换步骤缺少计划"))?;
                let record = plan
                    .records
                    .get(*index)
                    .ok_or_else(|| required("替换步骤编号越界"))?;
                let private = [&step.retained, &step.candidate]
                    .iter()
                    .all(|p| self.identity.private_files.contains(*p));
                if record.path != step.target
                    || step.retained == step.candidate
                    || !private
                    || step.before.identity().is_none()
                    || step.after.identity().is_none()
                {
                    return Err(required("原生替换步骤身份不合法"));
                }
                match step.direction {
                    ReplacementDirection::Apply
                        if self.phase == TransactionPhase::Applying
                            && step.before == record.before
                            && record.after.matches(&step.after)
                            && step.retained == backup(&plan.id, *index)?
                            && step.candidate == candidate(&plan.id, *index)? => {}
                    ReplacementDirection::Rollback
                        if self.phase == TransactionPhase::RollingBack
                            && step.after.descriptor() == record.before.descriptor() => {}
                    _ => return Err(required("原生替换步骤状态不合法")),
                }
            }
            Event::Publishing { index }
            | Event::Intent { index }
            | Event::BackedUp { index }
            | Event::Applied { index, .. }
            | Event::Restoring { index }
            | Event::Restored { index, .. } => {
                if self.plan.as_ref().is_none_or(|p| *index >= p.records.len()) {
                    return Err(required("事务操作编号越界"));
                }
                let record = &self.plan.as_ref().unwrap().records[*index];
                let intent = self
                    .events
                    .iter()
                    .any(|e| matches!(e,Event::Intent {index:i} if i==index));
                let applied = self
                    .events
                    .iter()
                    .any(|e| matches!(e,Event::Applied {index:i,..} if i==index));
                let restoring = self
                    .events
                    .iter()
                    .any(|e| matches!(e,Event::Restoring {index:i} if i==index));
                let valid = match event {
                    Event::Intent { .. } => {
                        self.phase == TransactionPhase::Applying
                            && !intent
                            && self
                                .events
                                .iter()
                                .filter(|e| matches!(e, Event::Applied { .. }))
                                .count()
                                == *index
                    }
                    Event::BackedUp { .. } => {
                        self.phase == TransactionPhase::Applying
                            && intent
                            && !applied
                            && record.before.descriptor().is_some()
                            && !self
                                .events
                                .iter()
                                .any(|e| matches!(e,Event::BackedUp {index:i} if i==index))
                    }
                    Event::Publishing { .. } => {
                        self.phase == TransactionPhase::Applying
                            && !applied
                            && self
                                .events
                                .iter()
                                .any(|e| matches!(e,Event::BackedUp {index:i} if i==index))
                    }
                    Event::Applied { snapshot, .. } => {
                        self.phase == TransactionPhase::Applying
                            && intent
                            && !applied
                            && record.after.matches(snapshot)
                    }
                    Event::Restoring { .. } => self.phase == TransactionPhase::RollingBack,
                    Event::Restored { snapshot, .. } => {
                        self.phase == TransactionPhase::RollingBack
                            && restoring
                            && (snapshot == &FileSnapshot::Missing
                                && record.before == FileSnapshot::Missing
                                || snapshot.descriptor().is_some()
                                    && snapshot.descriptor() == record.before.descriptor())
                    }
                    _ => false,
                };
                if !valid {
                    return Err(required("事务操作事件顺序或状态不合法"));
                }
            }
            Event::Cleaning { path, .. } => {
                if !self.phase.terminal() {
                    return Err(required("未终结事务不能清理材料"));
                }
                if !self.identity.private_files.contains(path)
                    && !self.identity.private_directories.contains(path)
                {
                    return Err(required("清理路径未登记"));
                }
            }
        }
        Ok(())
    }
    pub fn append(&mut self, root: &Root, event: Event) -> Result<()> {
        let log = base(&self.identity.id)?.join("journal.jsonl")?;
        if self.log_identity.is_none() {
            self.log_identity = root.create_file(&log, b"", 0o600)?.identity().cloned();
        }
        let bytes = codec::encode_json(&Line {
            sequence: self.events.len(),
            previous: self.chain.clone(),
            event: event.clone(),
        })?;
        root.append(
            &log,
            &bytes,
            self.log_identity
                .as_ref()
                .ok_or_else(|| required("日志身份缺失"))?,
        )?;
        self.accept(&event)?;
        self.events.push(event);
        self.chain = Sha256::of(&bytes);
        Ok(())
    }
    pub fn own_file(
        &mut self,
        root: &Root,
        path: &RelPath,
        bytes: &[u8],
        mode: u32,
    ) -> Result<FileSnapshot> {
        let empty = root.create_file(path, b"", 0o600)?;
        self.append(
            root,
            Event::Owned {
                path: path.clone(),
                snapshot: empty.clone(),
            },
        )?;
        root.fill_reserved(path, &empty, bytes, mode)
    }
    pub fn own_dir(&mut self, root: &Root, path: &RelPath) -> Result<FileSnapshot> {
        let snapshot = FileSnapshot::Directory {
            identity: root.create_dir(path)?,
        };
        self.append(
            root,
            Event::Owned {
                path: path.clone(),
                snapshot: snapshot.clone(),
            },
        )?;
        Ok(snapshot)
    }
}
fn validate_private(root: &Root, loaded: &Loaded) -> Result<()> {
    let base = base(&loaded.identity.id)?;
    let mut pending = vec![base.clone()];
    while let Some(dir) = pending.pop() {
        for name in root.list(Some(&dir))? {
            let path = dir.join(&name)?;
            if ["identity.json", "plan.json", "journal.jsonl"]
                .iter()
                .any(|n| base.join(n).is_ok_and(|p| p == path))
            {
                continue;
            }
            if !loaded.identity.private_files.contains(&path)
                && !loaded.identity.private_directories.contains(&path)
            {
                return Err(required("事务目录包含未知材料").at(path));
            }
            match root.inspect(&path)? {
                FileSnapshot::Directory { .. } => pending.push(path),
                FileSnapshot::File { .. } => {}
                _ => return Err(required("事务目录包含危险对象").at(path)),
            }
        }
    }
    Ok(())
}
pub(super) fn cleanup(root: &Root, loaded: &mut Loaded) -> Result<bool> {
    let mut paths: Vec<_> = loaded.identity.private_files.iter().cloned().collect();
    let mut directories: Vec<_> = loaded
        .identity
        .private_directories
        .iter()
        .cloned()
        .collect();
    directories.sort_by_key(|p| std::cmp::Reverse(p.as_str().len()));
    paths.extend(directories);
    let mut complete = true;
    for path in paths {
        let current = root.inspect(&path)?;
        if current == FileSnapshot::Missing {
            continue;
        }
        let owned = loaded
            .owned
            .get(&path)
            .is_some_and(|s| s.identity() == current.identity())
            || loaded.events.iter().any(
                |e| matches!(e,Event::Cleaning {path:p,snapshot} if p==&path && snapshot==&current),
            )
            || private_moved_identity(loaded, &path, current.identity());
        if !owned {
            complete = false;
            continue;
        }
        loaded.append(
            root,
            Event::Cleaning {
                path: path.clone(),
                snapshot: current.clone(),
            },
        )?;
        match root.remove(&path, &current) {
            Ok(()) => loaded.append(root, Event::Cleaned { path })?,
            Err(e) if e.code == ErrorCode::Io => complete = false,
            Err(e) => return Err(e),
        }
    }
    Ok(complete)
}
fn private_moved_identity(
    loaded: &Loaded,
    path: &RelPath,
    identity: Option<&FileIdentity>,
) -> bool {
    let Some(plan) = &loaded.plan else {
        return false;
    };
    for (index, record) in plan.records.iter().enumerate() {
        if [
            candidate(&plan.id, index),
            backup(&plan.id, index),
            restore(&plan.id, index),
        ]
        .iter()
        .any(|p| p.as_ref().is_ok_and(|p| p == path))
        {
            if record.before.identity() == identity && identity.is_some() {
                return true;
            }
            return loaded.events.iter().any(|event|matches!(event,Event::Applied {index:i,snapshot}|Event::Restored {index:i,snapshot} if *i==index && snapshot.identity()==identity));
        }
    }
    false
}
