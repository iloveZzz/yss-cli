use super::{Operation, TransactionReport};
use crate::{
    cancel::Cancellation,
    error::Result,
    fs::{FileSnapshot, RootIdentity},
    model::RelPath,
};
use std::{collections::BTreeMap, path::Path};

/// Apply business files with the same journal and recovery guarantees as installation writes.
pub fn apply_files(
    path: &Path,
    root_identity: RootIdentity,
    operations: Vec<Operation>,
    guards: BTreeMap<RelPath, FileSnapshot>,
    cancel: &Cancellation,
) -> Result<TransactionReport> {
    apply_files_checked(path, root_identity, operations, guards, cancel, &|| Ok(()))
}

pub fn apply_files_checked(
    path: &Path,
    root_identity: RootIdentity,
    operations: Vec<Operation>,
    guards: BTreeMap<RelPath, FileSnapshot>,
    cancel: &Cancellation,
    validate: &dyn Fn() -> Result<()>,
) -> Result<TransactionReport> {
    if operations.is_empty() {
        cancel.check()?;
        validate()?;
        return Ok(TransactionReport {
            id: None,
            status: "unchanged".into(),
            operations: 0,
            cleanup: "complete".into(),
            dry_run: false,
        });
    }
    super::apply::apply_operations(
        path,
        root_identity,
        operations,
        guards,
        true,
        cancel,
        validate,
        None,
    )
}

/// Compute the exact existing journal Plan digest before it is durably bound to
/// a Windows command-recovery record. This does not open or modify the project;
/// application performs the ordinary locked preflight and repeats this check.
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
pub(crate) fn reserved_files_plan_digest(
    id: &str,
    root_identity: &RootIdentity,
    operations: &[Operation],
    guards: &BTreeMap<RelPath, FileSnapshot>,
) -> Result<crate::model::Sha256> {
    use super::{
        Desired,
        journal::{self, Plan, Record},
    };
    use crate::{
        codec,
        error::{ErrorCode, YssError},
        fs,
        model::{FileDescriptor, MAX_ASSET, MAX_MANIFEST, Sha256},
    };
    journal::base(id)?;
    if operations.is_empty() {
        return Err(YssError::new(ErrorCode::InvalidInput, "空事务无需登记"));
    }
    let mut objects = BTreeMap::<Sha256, u64>::new();
    let mut records = Vec::with_capacity(operations.len());
    for operation in operations {
        let before = operation.before();
        if let FileSnapshot::File { descriptor, .. } = &before {
            objects.insert(descriptor.sha256.clone(), descriptor.size);
        }
        let after = match operation {
            Operation::Create { file, .. } | Operation::Replace { file, .. } => {
                let descriptor = FileDescriptor::new(&file.bytes, fs::observable_mode(file.mode));
                objects.insert(descriptor.sha256.clone(), descriptor.size);
                Desired::File { descriptor }
            }
            Operation::Delete { .. } => Desired::Missing,
            Operation::CreateDir { .. } => Desired::Directory,
        };
        records.push(Record {
            path: operation.path().clone(),
            before,
            after,
        });
    }
    if objects
        .values()
        .try_fold(0u64, |total, size| total.checked_add(*size))
        .is_none_or(|total| total > MAX_ASSET)
    {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "事务内容超过累计大小限制",
        ));
    }
    let plan = Plan {
        format: "yss.transaction".into(),
        format_version: 1,
        root: root_identity.clone(),
        id: id.to_owned(),
        records,
        guards: guards.clone(),
        business_files: true,
        command_writeback: true,
        objects,
    };
    let bytes = codec::encode_json(&plan)?;
    if bytes.len() as u64 > MAX_MANIFEST {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "事务计划超过大小限制",
        ));
    }
    Ok(Sha256::of(&bytes))
}

/// Apply only the exact plan that was reserved durably before any project write.
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
pub(crate) fn apply_reserved_files_checked(
    path: &Path,
    root_identity: RootIdentity,
    id: &str,
    plan_digest: &crate::model::Sha256,
    operations: Vec<Operation>,
    guards: BTreeMap<RelPath, FileSnapshot>,
    started: &std::cell::Cell<bool>,
    cancel: &Cancellation,
    validate: &dyn Fn() -> Result<()>,
) -> Result<TransactionReport> {
    cancel.check()?;
    if operations.is_empty() {
        return Err(crate::error::YssError::new(
            crate::error::ErrorCode::InvalidInput,
            "空事务无需登记",
        ));
    }
    if reserved_files_plan_digest(id, &root_identity, &operations, &guards)? != *plan_digest {
        return Err(crate::error::YssError::new(
            crate::error::ErrorCode::ConcurrentChange,
            "事务计划与已登记的内容不一致，未写入工程",
        ));
    }
    super::apply::apply_operations(
        path,
        root_identity,
        operations,
        guards,
        true,
        cancel,
        validate,
        Some(super::apply::Reservation {
            id,
            digest: plan_digest,
            started,
        }),
    )
}

/// Read a reserved journal only if it is bound to this root and exact plan.
/// Missing material is not evidence that a transaction did not run.
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
pub(crate) struct ReservedFileState {
    pub phase: super::TransactionPhase,
    pub cleanup_complete: bool,
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
pub(crate) fn inspect_reserved_files(
    path: &Path,
    root_identity: &RootIdentity,
    id: &str,
    plan_digest: &crate::model::Sha256,
) -> Result<Option<ReservedFileState>> {
    use super::journal::{self, Inspection};
    use crate::{
        error::{ErrorCode, YssError},
        fs::Root,
    };
    let root = Root::open(path)?;
    if &root.identity != root_identity {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "事务根目录身份已改变",
        ));
    }
    if root.inspect(&journal::base(id)?)? == FileSnapshot::Missing {
        return Ok(None);
    }
    let Inspection::Bound(loaded) = journal::inspect(&root, id)? else {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "事务与当前根目录身份不匹配",
        ));
    };
    if loaded.identity.plan_sha256 != *plan_digest
        || !loaded
            .plan
            .as_ref()
            .is_some_and(|plan| plan.business_files && plan.command_writeback)
    {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "事务计划与恢复登记不匹配",
        ));
    }
    let cleanup_complete = loaded.phase.terminal() && !journal::needs_cleanup(&root, &loaded)?;
    root.revalidate()?;
    Ok(Some(ReservedFileState {
        phase: loaded.phase,
        cleanup_complete,
    }))
}
