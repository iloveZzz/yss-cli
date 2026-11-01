mod apply;
mod journal;
mod recover;
use crate::{
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root},
    model::{FileDescriptor, RelPath, RenderedFile},
};
pub use apply::apply;
pub use recover::recover;
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, path::Path};

#[derive(Debug, Clone)]
pub enum Operation {
    Create {
        path: RelPath,
        file: RenderedFile,
    },
    Replace {
        path: RelPath,
        before: FileSnapshot,
        file: RenderedFile,
    },
    Delete {
        path: RelPath,
        before: FileSnapshot,
    },
    CreateDir {
        path: RelPath,
    },
}
impl Operation {
    pub fn path(&self) -> &RelPath {
        match self {
            Self::Create { path, .. }
            | Self::Replace { path, .. }
            | Self::Delete { path, .. }
            | Self::CreateDir { path } => path,
        }
    }
    pub fn before(&self) -> FileSnapshot {
        match self {
            Self::Replace { before, .. } | Self::Delete { before, .. } => before.clone(),
            _ => FileSnapshot::Missing,
        }
    }
}
pub struct ChangeSet {
    pub root_identity: crate::fs::RootIdentity,
    pub operations: Vec<Operation>,
    pub guards: BTreeMap<RelPath, FileSnapshot>,
    pub metadata: Operation,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum TransactionPhase {
    Preparing,
    Prepared,
    Applying,
    Committed,
    RollingBack,
    RolledBack,
}
impl TransactionPhase {
    pub fn terminal(self) -> bool {
        matches!(self, Self::Committed | Self::RolledBack)
    }
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TransactionReport {
    pub id: Option<String>,
    pub status: String,
    pub operations: usize,
    pub cleanup: String,
    pub dry_run: bool,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TransactionSummary {
    pub id: String,
    pub phase: TransactionPhase,
    pub operations: usize,
}
#[derive(Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TransactionStatus {
    pub transactions: Vec<TransactionSummary>,
    pub pending: Vec<String>,
}
fn required(message: &str) -> YssError {
    YssError::new(ErrorCode::RecoveryRequired, message)
}
pub fn status(root: &Path) -> Result<TransactionStatus> {
    status_root(&Root::open(root)?)
}
fn status_root(root: &Root) -> Result<TransactionStatus> {
    let path = RelPath::new(".yss/transactions")?;
    if root.inspect(&path)? == FileSnapshot::Missing {
        return Ok(TransactionStatus::default());
    }
    let mut status = TransactionStatus::default();
    for id in root.list(Some(&path))? {
        let loaded = journal::load(root, &id, false)?;
        if !loaded.phase.terminal() {
            status.pending.push(id.clone());
        }
        status.transactions.push(TransactionSummary {
            id,
            phase: loaded.phase,
            operations: loaded.plan.as_ref().map_or(0, |p| p.records.len()),
        });
    }
    if status.pending.len() > 1 {
        return Err(required("存在多个未完成事务；拒绝自动修改"));
    }
    Ok(status)
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
enum Desired {
    Missing,
    File { descriptor: FileDescriptor },
    Directory,
}
impl Desired {
    fn matches(&self, snapshot: &FileSnapshot) -> bool {
        match (self, snapshot) {
            (Self::Missing, FileSnapshot::Missing)
            | (Self::Directory, FileSnapshot::Directory { .. }) => true,
            (Self::File { descriptor: a }, FileSnapshot::File { descriptor: b, .. }) => a == b,
            _ => false,
        }
    }
}
