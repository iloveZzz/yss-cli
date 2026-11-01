use super::{
    ChangeSet, Desired, Operation, TransactionPhase, TransactionReport,
    journal::{self, Event, ExecutableReplacement, Identity, Plan, Record, ReplacementDirection},
    required, status_root,
};
use crate::{
    cancel::Cancellation,
    codec,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{FileDescriptor, MAX_ASSET, RelPath, RenderedTree, Sha256},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

pub fn apply(
    path: &Path,
    mut changes: ChangeSet,
    cancel: &Cancellation,
) -> Result<TransactionReport> {
    cancel.check()?;
    let root = Root::open(path)?;
    preflight(&root, &changes)?;
    if !status_root(&root)?.pending.is_empty() {
        return Err(required("存在未完成事务，请先 recover"));
    }
    let lock = fs::lock_root(path)?;
    let root = &lock.root;
    preflight(root, &changes)?;
    if !status_root(root)?.pending.is_empty() {
        return Err(required("加锁后发现未完成事务"));
    }
    let id = fs::unique_id()?;
    let base = journal::base(&id)?;
    let mut objects = BTreeMap::new();
    let mut records = Vec::new();
    changes.operations.push(changes.metadata);
    for operation in changes.operations {
        cancel.check()?;
        let path = operation.path().clone();
        let before = operation.before();
        if let FileSnapshot::File { descriptor, .. } = &before {
            let checked = root.read_checked(&path, MAX_ASSET)?;
            if checked.snapshot != before {
                return Err(
                    YssError::new(ErrorCode::ConcurrentChange, "准备前原文件改变").at(path),
                );
            }
            objects.insert(descriptor.sha256.clone(), checked.bytes);
        }
        let after = match operation {
            Operation::Create { file, .. } | Operation::Replace { file, .. } => {
                let descriptor = FileDescriptor::new(&file.bytes, fs::observable_mode(file.mode));
                objects.insert(descriptor.sha256.clone(), file.bytes);
                Desired::File { descriptor }
            }
            Operation::Delete { .. } => Desired::Missing,
            Operation::CreateDir { .. } => Desired::Directory,
        };
        records.push(Record {
            path,
            before,
            after,
        });
    }
    if objects.values().map(|b| b.len() as u64).sum::<u64>() > MAX_ASSET {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "事务内容超过累计大小限制",
        ));
    }
    let plan = Plan {
        format: "yss.transaction".into(),
        format_version: 1,
        root: root.identity.clone(),
        id: id.clone(),
        records,
        guards: changes.guards,
        objects: objects
            .iter()
            .map(|(h, b)| (h.clone(), b.len() as u64))
            .collect(),
    };
    let plan_bytes = codec::encode_json(&plan)?;
    if plan_bytes.len() as u64 > crate::model::MAX_MANIFEST {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "事务计划超过大小限制",
        ));
    }
    let transactions = RelPath::new(".yss/transactions")?;
    if root.inspect(&transactions)? == FileSnapshot::Missing {
        root.create_dir(&transactions)?;
    }
    let directory = root.create_dir(&base)?;
    let mut private_files = BTreeSet::new();
    let mut private_directories = BTreeSet::new();
    for name in ["objects", "candidates", "backups", "restores"] {
        private_directories.insert(base.join(name)?);
    }
    for hash in objects.keys() {
        private_files.insert(journal::object(&id, hash)?);
    }
    for (index, record) in plan.records.iter().enumerate() {
        if record.after == Desired::Directory {
            private_directories.insert(journal::candidate(&id, index)?);
        } else {
            private_files.insert(journal::candidate(&id, index)?);
        }
        private_files.insert(journal::backup(&id, index)?);
        private_files.insert(journal::restore(&id, index)?);
    }
    let identity = Identity {
        format: "yss.transaction".into(),
        format_version: 1,
        id: id.clone(),
        root: root.identity.clone(),
        directory,
        plan_sha256: Sha256::of(&plan_bytes),
        private_files,
        private_directories,
    };
    root.create_file(
        &base.join("identity.json")?,
        &codec::encode_json(&identity)?,
        0o600,
    )?;
    let preparation = (|| {
        root.create_file(&base.join("plan.json")?, &plan_bytes, 0o600)?;
        root.create_file(&base.join("journal.jsonl")?, b"", 0o600)?;
        let mut loaded = journal::load(root, &id, false)?;
        for name in ["objects", "candidates", "backups", "restores"] {
            loaded.own_dir(root, &base.join(name)?)?;
        }
        for (hash, bytes) in &objects {
            cancel.check()?;
            let path = journal::object(&id, hash)?;
            let written = loaded.own_file(root, &path, bytes, 0o600)?;
            if written
                .descriptor()
                .is_none_or(|d| d.sha256 != *hash || d.size != bytes.len() as u64)
            {
                return Err(required("准备对象写入不完整"));
            }
        }
        cancel.check()?;
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::Prepared,
            },
        )?;
        Ok(loaded)
    })();
    let mut loaded = match preparation {
        Ok(loaded) => loaded,
        Err(error) => return failed(root, &id, error),
    };
    let execution = (|| {
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::Applying,
            },
        )?;
        let count = loaded.plan.as_ref().unwrap().records.len();
        recheck_progress(root, &loaded, 0)?;
        for index in 0..count {
            cancel.check()?;
            root.revalidate()?;
            if index + 1 == count {
                recheck_progress(root, &loaded, index)?;
            }
            apply_record(root, &mut loaded, index, cancel)?;
        }
        recheck_progress(root, &loaded, count)?;
        cancel.check()?;
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::Committed,
            },
        )?;
        Ok(())
    })();
    if let Err(error) = execution {
        return failed(root, &id, error);
    }
    let count = loaded.plan.as_ref().unwrap().records.len();
    let clean = journal::cleanup(root, &mut loaded).unwrap_or(false);
    Ok(TransactionReport {
        id: Some(id),
        status: "committed".into(),
        operations: count,
        cleanup: if clean { "complete" } else { "pending" }.into(),
        dry_run: false,
    })
}
fn failed(root: &Root, id: &str, error: YssError) -> Result<TransactionReport> {
    match super::recover::rollback(root, id, &Cancellation::new()) {
        Ok(report) if report.status == "committed" => {
            let mut error = error;
            error.message = format!("事务已提交但收尾失败：{}", error.message);
            Err(error
                .context("transaction", "committed")
                .context("rollback", "not-performed")
                .context("cleanup", "pending"))
        }
        Ok(report) => Err(error.context("rollback", report.status)),
        Err(rollback) => Err(required("操作失败且回滚未完成；保留现场供 recover 处理")
            .context("originalCode", format!("{:?}", error.code))
            .context("originalMessage", error.message)
            .context("originalPath", error.path.unwrap_or_default())
            .context("rollbackCode", format!("{:?}", rollback.code))
            .context("rollbackMessage", rollback.message)
            .context("rollbackPath", rollback.path.unwrap_or_default())),
    }
}
fn preflight(root: &Root, changes: &ChangeSet) -> Result<()> {
    if root.identity != changes.root_identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "安装根目录在规划后改变",
        ));
    }
    let metadata = changes.metadata.path().as_str();
    if !matches!(metadata, ".yss/project.json" | ".yss/tool.json")
        || !matches!(
            changes.metadata,
            Operation::Create { .. } | Operation::Replace { .. }
        )
    {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "缺少最后提交的安装记录",
        ));
    }
    let mut tree = RenderedTree::default();
    for operation in changes
        .operations
        .iter()
        .chain(std::iter::once(&changes.metadata))
    {
        let path = operation.path();
        if path.as_str().starts_with(".yss") && path.as_str() != metadata {
            return Err(
                YssError::new(ErrorCode::UnsafePath, "业务操作不可写 YSS 控制状态").at(path),
            );
        }
        match operation {
            Operation::CreateDir { .. } => {
                if !tree.directories.insert(path.clone()) {
                    return Err(YssError::new(ErrorCode::InvalidInput, "重复目录操作"));
                }
            }
            Operation::Create { file, .. } | Operation::Replace { file, .. } => {
                tree.insert(path.clone(), file.bytes.clone(), file.mode)?
            }
            Operation::Delete { .. } => tree.insert(path.clone(), Vec::new(), 0o644)?,
        }
        if matches!(operation,Operation::Replace {before,..}|Operation::Delete {before,..} if !matches!(before,FileSnapshot::File {..}))
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "替换或删除必须有原文件基线",
            ));
        }
        if root.inspect(path)? != operation.before() {
            return Err(YssError::new(ErrorCode::ConcurrentChange, "事务输入已改变").at(path));
        }
    }
    fs::validate_tree(&tree)?;
    for (path, expected) in &changes.guards {
        if &root.inspect(path)? != expected {
            return Err(YssError::new(ErrorCode::ConcurrentChange, "只读输入已改变").at(path));
        }
    }
    root.revalidate()
}
fn recheck_progress(root: &Root, loaded: &journal::Loaded, done: usize) -> Result<()> {
    let plan = loaded
        .plan
        .as_ref()
        .ok_or_else(|| required("事务缺少计划"))?;
    for (index, record) in plan.records.iter().enumerate() {
        let expected = if index < done {
            loaded
                .events
                .iter()
                .rev()
                .find_map(|e| {
                    if let Event::Applied { index: i, snapshot } = e {
                        (*i == index).then_some(snapshot)
                    } else {
                        None
                    }
                })
                .ok_or_else(|| required("缺少已应用操作记录"))?
        } else {
            &record.before
        };
        if &root.inspect(&record.path)? != expected {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "事务执行期间文件改变").at(&record.path),
            );
        }
    }
    for (path, expected) in &plan.guards {
        if !plan.records.iter().any(|r| &r.path == path) && &root.inspect(path)? != expected {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "只读输入在执行期间改变").at(path),
            );
        }
    }
    Ok(())
}
fn apply_record(
    root: &Root,
    loaded: &mut journal::Loaded,
    index: usize,
    cancel: &Cancellation,
) -> Result<()> {
    let plan = loaded.plan.as_ref().unwrap();
    let record = &plan.records[index];
    let path = record.path.clone();
    let before = record.before.clone();
    let after = record.after.clone();
    let id = plan.id.clone();
    loaded.append(root, Event::Intent { index })?;
    let candidate = journal::candidate(&id, index)?;
    if let Desired::File { descriptor } = &after {
        let bytes = root
            .read_checked(&journal::object(&id, &descriptor.sha256)?, MAX_ASSET)?
            .bytes;
        if Sha256::of(&bytes) != descriptor.sha256 {
            return Err(required("候选对象摘要改变"));
        }
        let snapshot = loaded.own_file(root, &candidate, &bytes, descriptor.mode)?;
        if !after.matches(&snapshot) {
            return Err(required("候选文件写入不完整"));
        }
    } else if after == Desired::Directory {
        loaded.own_dir(root, &candidate)?;
    }
    cancel.check()?;
    if root.inspect(&path)? != before {
        return Err(YssError::new(ErrorCode::ConcurrentChange, "发布前基线改变").at(path));
    }
    if before != FileSnapshot::Missing {
        if after == Desired::Missing || fs::platform::REPLACE_REQUIRES_BACKUP {
            let backup = journal::backup(&id, index)?;
            if fs::platform::REPLACE_REQUIRES_BACKUP && after != Desired::Missing {
                loaded.append(
                    root,
                    Event::ExecutableReplacement {
                        index,
                        step: ExecutableReplacement {
                            direction: ReplacementDirection::Apply,
                            target: path.clone(),
                            retained: backup.clone(),
                            candidate: candidate.clone(),
                            before: before.clone(),
                            after: root.inspect(&candidate)?,
                        },
                    },
                )?;
            }
            root.rename(&path, &backup, &FileSnapshot::Missing, false)?;
            if root.inspect(&backup)? != before {
                return Err(required("移出的原文件与基线不一致"));
            }
            loaded.append(root, Event::BackedUp { index })?;
            if after != Desired::Missing {
                loaded.append(root, Event::Publishing { index })?;
                root.rename(&candidate, &path, &FileSnapshot::Missing, false)?;
            }
        } else {
            root.exchange(&candidate, &path, &before)?;
            if root.inspect(&candidate)? != before {
                return Err(required("交换后的原文件与基线不一致"));
            }
        }
    } else {
        root.rename(&candidate, &path, &FileSnapshot::Missing, false)?;
    }
    let actual = root.inspect(&path)?;
    if !after.matches(&actual) {
        return Err(required("发布后校验失败").at(&path));
    }
    loaded.append(
        root,
        Event::Applied {
            index,
            snapshot: actual,
        },
    )
}
