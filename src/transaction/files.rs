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
    )
}
