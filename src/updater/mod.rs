mod installation;
pub mod release;
use crate::{
    cancel::Cancellation,
    codec,
    error::{ErrorCode, Result, YssError},
    fs,
    model::{BuildInfo, MAX_ASSET, RelPath, RenderedFile, RenderedTree, Sha256},
    transaction::{self, ChangeSet, Operation, TransactionReport},
};
pub(crate) use installation::is_tool;
pub use installation::{ToolDoctorReport, doctor};
use serde::Serialize;
use std::path::PathBuf;
pub struct UpgradeRequest {
    pub to: Option<String>,
    pub tool_root: Option<PathBuf>,
    pub artifact: Option<PathBuf>,
    pub sha256: Option<Sha256>,
    pub dry_run: bool,
}
pub struct UpgradePlan {
    pub root: PathBuf,
    pub report: UpgradeReport,
    action: Action,
}
enum Action {
    Preview,
    Unchanged,
    New(RenderedTree),
    Transaction(ChangeSet),
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpgradeReport {
    pub root: String,
    pub status: String,
    pub dry_run: bool,
    pub current_version: Option<String>,
    pub target_version: String,
    pub target: String,
    pub archive_sha256: Sha256,
    pub process_version: String,
    pub transaction: Option<TransactionReport>,
}
pub fn prepare(
    request: &UpgradeRequest,
    build: &BuildInfo,
    cancel: &Cancellation,
) -> Result<UpgradePlan> {
    cancel.check()?;
    let requested = request
        .to
        .as_deref()
        .map(|v| release::version(v, true))
        .transpose()?;
    let root = request.tool_root.clone().map(Ok).unwrap_or_else(|| {
        std::env::current_exe()
            .map(|p| p.parent().map_or_else(PathBuf::new, PathBuf::from))
            .map_err(YssError::from)
    })?;
    let state = installation::preflight(&root, request.tool_root.is_some(), &build.target)?;
    let (artifact, sha) = match (&request.artifact, &request.sha256) {
        (Some(a), Some(s)) => (a, s),
        (None, None) => {
            let auth = crate::network::GitlabAuth::from_environment()?;
            let remote =
                crate::network::resolve_release(requested.as_ref(), &build.target, &auth, cancel)?;
            let current = state.receipt.as_ref().map(|r| r.build.version.clone());
            let mut status = "planned";
            if let Some(installed) = &state.receipt {
                let old = release::version(&installed.build.version, false)?;
                if remote.version() < &old {
                    return Err(YssError::new(ErrorCode::VersionConflict, "不允许版本降级"));
                }
                if remote.version() == &old {
                    if installed.archive_sha256 != *remote.sha256() {
                        return Err(YssError::new(
                            ErrorCode::VersionConflict,
                            "同版本发行摘要不同",
                        ));
                    }
                    status = "unchanged";
                }
            }
            if request.dry_run || status == "unchanged" {
                return Ok(UpgradePlan {
                    root: state.path.clone(),
                    report: UpgradeReport {
                        root: state.path.display().to_string(),
                        status: status.into(),
                        dry_run: request.dry_run,
                        current_version: current,
                        target_version: remote.version().to_string(),
                        target: build.target.clone(),
                        archive_sha256: remote.sha256().clone(),
                        process_version: build.version.clone(),
                        transaction: None,
                    },
                    action: if status == "unchanged" {
                        Action::Unchanged
                    } else {
                        Action::Preview
                    },
                });
            }
            let bytes = crate::network::download(&remote, &auth, cancel)?;
            let verified = release::verify(&bytes, remote.sha256(), &build.target)?;
            if release::version(&verified.manifest().build.version, false)? != *remote.version() {
                return Err(YssError::new(
                    ErrorCode::VersionConflict,
                    "包内版本与 GitLab 正式发行不同",
                ));
            }
            return finish_plan(state, verified, remote.sha256().clone(), build, false);
        }
        _ => {
            return Err(YssError::new(
                ErrorCode::Argument,
                "--artifact 与 --sha256 必须同时提供",
            ));
        }
    };
    let bytes = release::read_local(artifact, MAX_ASSET)?;
    cancel.check()?;
    let verified = release::verify(&bytes, sha, &build.target)?;
    let target_version = release::version(&verified.manifest().build.version, false)?;
    if requested.as_ref().is_some_and(|v| v != &target_version) {
        return Err(YssError::new(
            ErrorCode::VersionConflict,
            "--to 与包内版本不一致",
        ));
    }
    finish_plan(state, verified, sha.clone(), build, request.dry_run)
}
fn finish_plan(
    state: installation::Preflight,
    release: release::VerifiedRelease,
    sha: Sha256,
    build: &BuildInfo,
    dry_run: bool,
) -> Result<UpgradePlan> {
    let manifest = release.manifest().clone();
    let version = release::version(&manifest.build.version, false)?;
    let current = state.receipt.as_ref().map(|r| r.build.version.clone());
    let mut report = UpgradeReport {
        root: state.path.display().to_string(),
        status: "planned".into(),
        dry_run,
        current_version: current.clone(),
        target_version: version.to_string(),
        target: build.target.clone(),
        archive_sha256: sha.clone(),
        process_version: build.version.clone(),
        transaction: None,
    };
    if let Some(current) = &current {
        let old = release::version(current, false)?;
        if version < old {
            return Err(YssError::new(ErrorCode::VersionConflict, "不允许版本降级"));
        }
        if version == old {
            let installed = state.receipt.as_ref().expect("checked receipt");
            if installed.archive_sha256 != sha || installed.build != manifest.build {
                return Err(YssError::new(
                    ErrorCode::VersionConflict,
                    "相同版本的来源或归档摘要不同",
                ));
            }
            report.status = "unchanged".into();
            return Ok(UpgradePlan {
                root: state.path,
                report,
                action: Action::Unchanged,
            });
        }
    }
    let mut tree = release.into_payload_tree();
    let payload = tree
        .files
        .iter()
        .map(|(p, f)| {
            (
                p.clone(),
                crate::model::FileDescriptor::new(&f.bytes, fs::observable_mode(f.mode)),
            )
        })
        .collect();
    let receipt = installation::ToolReceipt {
        format: "yss.tool".into(),
        format_version: 1,
        build: manifest.build,
        archive_sha256: sha,
        payload,
    };
    let receipt_bytes = codec::encode_json(&receipt)?;
    let action = if let Some(root) = state.root {
        let mut operations = Vec::new();
        for (path, file) in tree.files {
            let before = state.guards.get(&path).cloned().ok_or_else(|| {
                YssError::new(ErrorCode::Conflict, "程序载荷缺少预检基线").at(&path)
            })?;
            operations.push(if before == fs::FileSnapshot::Missing {
                Operation::Create { path, file }
            } else {
                Operation::Replace { path, before, file }
            });
        }
        let metadata_path = RelPath::new(installation::RECEIPT)?;
        let file = RenderedFile {
            bytes: receipt_bytes,
            mode: 0o644,
        };
        let metadata = if state.before == fs::FileSnapshot::Missing {
            Operation::Create {
                path: metadata_path,
                file,
            }
        } else {
            Operation::Replace {
                path: metadata_path,
                before: state.before,
                file,
            }
        };
        Action::Transaction(ChangeSet {
            root_identity: root.identity,
            operations,
            guards: state.guards,
            metadata,
        })
    } else {
        tree.insert(RelPath::new(installation::RECEIPT)?, receipt_bytes, 0o644)?;
        Action::New(tree)
    };
    Ok(UpgradePlan {
        root: state.path,
        report,
        action,
    })
}
pub fn execute(mut plan: UpgradePlan, cancel: &Cancellation) -> Result<UpgradeReport> {
    cancel.check()?;
    if plan.report.dry_run {
        return Ok(plan.report);
    }
    match plan.action {
        Action::Preview => {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "在线预览不含可执行载荷",
            ));
        }
        Action::Unchanged => {}
        Action::New(tree) => {
            fs::publish_new_tree(&plan.root, &tree, cancel)?;
            plan.report.status = "installed".into();
        }
        Action::Transaction(changes) => {
            plan.report.transaction = Some(transaction::apply(&plan.root, changes, cancel)?);
            plan.report.status = "installed".into();
        }
    }
    Ok(plan.report)
}
