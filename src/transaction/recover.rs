use super::{
    Desired, TransactionPhase, TransactionReport,
    journal::{self, Event, ExecutableReplacement, Loaded, ReplacementDirection},
    required, status_root,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{MAX_ASSET, Sha256},
};
use std::path::Path;

pub fn recover(path: &Path, dry_run: bool, cancel: &Cancellation) -> Result<TransactionReport> {
    cancel.check()?;
    let root = Root::open(path)?;
    let status = status_root(&root)?;
    if status.pending.is_empty() {
        if !dry_run && !status.transactions.is_empty() {
            let lock = fs::lock_root(path)?;
            let mut complete = true;
            for summary in status.transactions {
                let mut loaded = journal::load(&lock.root, &summary.id, false)?;
                complete &= journal::cleanup(&lock.root, &mut loaded)?;
            }
            return Ok(TransactionReport {
                id: None,
                status: "unchanged".into(),
                operations: 0,
                cleanup: if complete { "complete" } else { "pending" }.into(),
                dry_run,
            });
        }
        return Ok(TransactionReport {
            id: None,
            status: "unchanged".into(),
            operations: 0,
            cleanup: "unchanged".into(),
            dry_run,
        });
    }
    let id = &status.pending[0];
    let loaded = journal::load(&root, id, true)?;
    preflight(&root, &loaded)?;
    if dry_run {
        return Ok(TransactionReport {
            id: Some(id.clone()),
            status: "would-rollback".into(),
            operations: loaded.plan.as_ref().map_or(0, |p| p.records.len()),
            cleanup: "unchanged".into(),
            dry_run,
        });
    }
    let lock = fs::lock_root(path)?;
    let current = status_root(&lock.root)?;
    if current.pending != status.pending {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "加锁后事务状态改变",
        ));
    }
    rollback(&lock.root, id, cancel)
}
fn before_matches(expected: &FileSnapshot, current: &FileSnapshot) -> bool {
    match (expected, current) {
        (FileSnapshot::Missing, FileSnapshot::Missing) => true,
        (FileSnapshot::File { descriptor: a, .. }, FileSnapshot::File { descriptor: b, .. }) => {
            a == b
        }
        _ => false,
    }
}
fn after_matches(loaded: &Loaded, index: usize, current: &FileSnapshot) -> bool {
    let record = &loaded.plan.as_ref().unwrap().records[index];
    if !record.after.matches(current) {
        return false;
    }
    if !loaded
        .events
        .iter()
        .any(|e| matches!(e,Event::Intent {index:i} if *i == index))
    {
        return false;
    }
    if record.after == Desired::Missing {
        return false;
    }
    let candidate = journal::candidate(&loaded.identity.id, index).unwrap();
    loaded
        .owned
        .get(&candidate)
        .is_some_and(|s| s.identity() == current.identity())
        || loaded.events.iter().any(
            |e| matches!(e,Event::Applied {index:i,snapshot} if *i==index && snapshot == current),
        )
}
fn gap_matches(root: &Root, loaded: &Loaded, index: usize, current: &FileSnapshot) -> Result<bool> {
    if *current != FileSnapshot::Missing {
        return Ok(false);
    }
    let record = &loaded.plan.as_ref().unwrap().records[index];
    if record.before == FileSnapshot::Missing {
        return Ok(false);
    }
    if !loaded
        .events
        .iter()
        .any(|e| matches!(e,Event::Intent {index:i} if *i == index))
    {
        return Ok(false);
    }
    if root.inspect(&journal::backup(&loaded.identity.id, index)?)? == record.before {
        return Ok(true);
    }
    for event in loaded.events.iter().rev() {
        if let Event::ExecutableReplacement { index: i, step } = event
            && *i == index
            && matches!(step.direction, ReplacementDirection::Rollback)
            && root.inspect(&step.retained)? == step.before
            && root.inspect(&step.candidate)? == step.after
        {
            return Ok(true);
        }
    }
    Ok(false)
}
fn preflight(root: &Root, loaded: &Loaded) -> Result<()> {
    if loaded.phase.terminal() {
        return Ok(());
    }
    if let Some(plan) = &loaded.plan {
        for (index, record) in plan.records.iter().enumerate() {
            let current = root.inspect(&record.path)?;
            if record.after == Desired::Directory
                && matches!(current, FileSnapshot::Directory { .. })
            {
                let prefix = format!("{}/", record.path);
                let children: std::collections::BTreeSet<_> = plan
                    .records
                    .iter()
                    .filter_map(|r| r.path.as_str().strip_prefix(&prefix)?.split('/').next())
                    .collect();
                if root
                    .list(Some(&record.path))?
                    .iter()
                    .any(|p| !children.contains(p.as_str()))
                {
                    return Err(YssError::new(
                        ErrorCode::Conflict,
                        "待恢复目录含不属于本事务的内容",
                    )
                    .at(&record.path));
                }
            }
            if loaded.phase == TransactionPhase::Preparing
                || loaded.phase == TransactionPhase::Prepared
            {
                if !before_matches(&record.before, &current) {
                    return Err(required("未应用事务的业务文件已改变").at(&record.path));
                }
            } else if !before_matches(&record.before, &current)
                && !after_matches(loaded, index, &current)
                && !gap_matches(root, loaded, index, &current)?
            {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "恢复前发现既不是 before 也不是 after 的文件；未写入任何恢复内容",
                )
                .at(&record.path));
            }
        }
    }
    root.revalidate()
}
pub(super) fn rollback(root: &Root, id: &str, cancel: &Cancellation) -> Result<TransactionReport> {
    let mut loaded = journal::load(root, id, true)?;
    preflight(root, &loaded)?;
    let count = loaded.plan.as_ref().map_or(0, |p| p.records.len());
    if loaded.phase.terminal() {
        return Ok(TransactionReport {
            id: Some(id.into()),
            status: if loaded.phase == TransactionPhase::Committed {
                "committed"
            } else {
                "rolled-back"
            }
            .into(),
            operations: count,
            cleanup: "unchanged".into(),
            dry_run: false,
        });
    }
    if loaded.phase == TransactionPhase::Preparing {
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::RolledBack,
            },
        )?;
    } else {
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::RollingBack,
            },
        )?;
        for index in (0..count).rev() {
            cancel.check()?;
            restore_record(root, &mut loaded, index)?;
        }
        for record in &loaded.plan.as_ref().unwrap().records {
            if !before_matches(&record.before, &root.inspect(&record.path)?) {
                return Err(required("最终回滚核验发现漂移").at(&record.path));
            }
        }
        loaded.append(
            root,
            Event::Phase {
                phase: TransactionPhase::RolledBack,
            },
        )?;
    }
    let complete = journal::cleanup(root, &mut loaded)?;
    Ok(TransactionReport {
        id: Some(id.into()),
        status: "rolled-back".into(),
        operations: count,
        cleanup: if complete { "complete" } else { "pending" }.into(),
        dry_run: false,
    })
}
fn restore_record(root: &Root, loaded: &mut Loaded, index: usize) -> Result<()> {
    let record = &loaded.plan.as_ref().unwrap().records[index];
    let path = record.path.clone();
    let before = record.before.clone();
    let id = loaded.identity.id.clone();
    let current = root.inspect(&path)?;
    if before_matches(&before, &current) {
        return Ok(());
    }
    if !after_matches(loaded, index, &current) && !gap_matches(root, loaded, index, &current)? {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "恢复期间文件再次改变，保留进度",
        )
        .at(path));
    }
    loaded.append(root, Event::Restoring { index })?;
    if before == FileSnapshot::Missing {
        root.remove(&path, &current)?;
    } else {
        let descriptor = before
            .descriptor()
            .ok_or_else(|| required("原文件描述符缺失"))?;
        let backup = journal::backup(&id, index)?;
        let candidate = journal::candidate(&id, index)?;
        let restore = journal::restore(&id, index)?;
        let source = if root.inspect(&backup)? == before {
            backup.clone()
        } else if root.inspect(&candidate)? == before {
            candidate.clone()
        } else {
            let bytes = root
                .read_checked(&journal::object(&id, &descriptor.sha256)?, MAX_ASSET)?
                .bytes;
            if Sha256::of(&bytes) != descriptor.sha256 {
                return Err(required("回滚内容对象摘要不一致"));
            }
            match root.inspect(&restore)? {
                FileSnapshot::Missing => {
                    loaded.own_file(root, &restore, &bytes, descriptor.mode)?;
                }
                ref snapshot if before_matches(&before, snapshot) => {}
                _ => return Err(required("恢复候选文件被改变")),
            }
            restore
        };
        if current == FileSnapshot::Missing {
            root.rename(&source, &path, &current, false)?;
        } else if fs::platform::REPLACE_REQUIRES_BACKUP {
            // Keep the currently installed file in a recorded private slot before publishing the original.
            let moved = if source == candidate {
                backup
            } else {
                candidate
            };
            if root.inspect(&moved)? != FileSnapshot::Missing {
                return Err(required("回滚备份槽已占用"));
            }
            loaded.append(
                root,
                Event::ExecutableReplacement {
                    index,
                    step: ExecutableReplacement {
                        direction: ReplacementDirection::Rollback,
                        target: path.clone(),
                        retained: moved.clone(),
                        candidate: source.clone(),
                        before: current.clone(),
                        after: root.inspect(&source)?,
                    },
                },
            )?;
            root.rename(&path, &moved, &FileSnapshot::Missing, false)?;
            root.rename(&source, &path, &FileSnapshot::Missing, false)?;
        } else {
            root.exchange(&source, &path, &current)?;
        }
    }
    let restored = root.inspect(&path)?;
    if !before_matches(&before, &restored) {
        return Err(required("回滚后内容校验失败").at(path));
    }
    loaded.append(
        root,
        Event::Restored {
            index,
            snapshot: restored,
        },
    )
}
