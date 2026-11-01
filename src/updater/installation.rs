use super::release;
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{BuildInfo, FileDescriptor, MAX_MANIFEST, RelPath, Sha256},
    transaction,
};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};
pub const RECEIPT: &str = ".yss/tool.json";
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ToolReceipt {
    pub format: String,
    pub format_version: u32,
    pub build: BuildInfo,
    pub archive_sha256: Sha256,
    pub payload: BTreeMap<RelPath, FileDescriptor>,
}
pub(super) struct Preflight {
    pub path: PathBuf,
    pub root: Option<Root>,
    pub receipt: Option<ToolReceipt>,
    pub before: FileSnapshot,
    pub guards: BTreeMap<RelPath, FileSnapshot>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolDoctorReport {
    pub root: String,
    pub installation: Option<ToolReceipt>,
    pub differences: Vec<RelPath>,
    pub transactions: transaction::TransactionStatus,
}
fn conflict(message: &str) -> YssError {
    YssError::new(ErrorCode::Conflict, message)
}
pub fn is_tool(path: &Path) -> Result<bool> {
    let root = Root::open(path)?;
    Ok(root.inspect(&RelPath::new(RECEIPT)?)? != FileSnapshot::Missing)
}
fn detect(root: &Root) -> Result<(Option<ToolReceipt>, FileSnapshot)> {
    for marker in crate::project::state::LEGACY_MARKERS
        .iter()
        .copied()
        .chain(["installation.json"])
    {
        if root.inspect(&RelPath::new(marker)?)? != FileSnapshot::Missing {
            return Err(YssError::new(
                ErrorCode::UnsupportedFormat,
                "旧安装格式不受支持；请使用新的工具目录",
            )
            .at(marker));
        }
    }
    for marker in [".yss/project.json", ".yss/scaffold.json"] {
        if root.inspect(&RelPath::new(marker)?)? != FileSnapshot::Missing {
            return Err(conflict("工具目录不能兼作项目目录"));
        }
    }
    if root.inspect(&RelPath::new(".yss")?)? != FileSnapshot::Missing {
        fs::validate_control(root)?;
    }
    let path = RelPath::new(RECEIPT)?;
    let before = root.inspect(&path)?;
    if before == FileSnapshot::Missing {
        return Ok((None, before));
    }
    let checked = root.read_checked(&path, MAX_MANIFEST)?;
    let receipt: ToolReceipt = codec::decode_json(&checked.bytes, Limits::manifest())?;
    codec::ensure_format(&receipt.format, receipt.format_version, "yss.tool")?;
    release::version(&receipt.build.version, false)?;
    let name = RelPath::new(release::binary_name(&receipt.build.target)?)?;
    if receipt.payload.len() != 1
        || !receipt.payload.contains_key(&name)
        || receipt.payload[&name].mode != fs::observable_mode(0o755)
    {
        return Err(conflict("工具收据载荷不合法"));
    }
    Ok((Some(receipt), checked.snapshot))
}
pub(super) fn preflight(path: &Path, explicit: bool, target: &str) -> Result<Preflight> {
    let path = fs::absolute(path)?;
    for ancestor in path.ancestors() {
        for marker in [".git", ".gitmodules"] {
            match std::fs::symlink_metadata(ancestor.join(marker)) {
                Ok(_) => return Err(conflict("工具目录不能位于 Git 工作树内")),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(e) if e.kind() == std::io::ErrorKind::NotADirectory => {
                    return Err(conflict("工具根不是目录"));
                }
                Err(e) => return Err(e.into()),
            }
        }
    }
    let parent = path.parent().ok_or_else(|| conflict("工具根缺少父目录"))?;
    let parent_root = Root::open(parent)?;
    let name = RelPath::new(
        path.file_name()
            .and_then(|n| n.to_str())
            .ok_or_else(|| conflict("工具根名称不合法"))?,
    )?;
    if parent_root.inspect(&name)? == FileSnapshot::Missing {
        if !explicit {
            return Err(conflict("默认工具根必须已有 Rust 安装收据"));
        }
        return Ok(Preflight {
            path,
            root: None,
            receipt: None,
            before: FileSnapshot::Missing,
            guards: BTreeMap::new(),
        });
    }
    let root = Root::open(&path)?;
    let (receipt, before) = detect(&root)?;
    if !transaction::status(&path)?.pending.is_empty() {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "工具目录存在未完成事务；先执行 recover",
        ));
    }
    let mut guards = BTreeMap::new();
    if let Some(receipt) = &receipt {
        if receipt.build.target != target {
            return Err(YssError::new(
                ErrorCode::UnsupportedPlatform,
                "已有安装 target 与当前程序不同",
            ));
        }
        for (name, descriptor) in &receipt.payload {
            let actual = root.inspect(name)?;
            if actual.descriptor() != Some(descriptor) {
                return Err(conflict("受管程序已被修改或删除").at(name));
            }
            let bytes = root.read_checked(name, crate::model::MAX_ASSET)?;
            if release::binary_info(&bytes.bytes)? != receipt.build {
                return Err(conflict("程序构建信息与收据不一致"));
            }
            guards.insert(name.clone(), actual);
        }
    } else {
        if !explicit {
            return Err(conflict(
                "默认工具根缺少新格式收据；请显式指定新的 --tool-root",
            ));
        }
        if root.list(None)?.iter().any(|n| n != ".yss") {
            return Err(conflict("初装工具根必须是新目录或空目录"));
        }
        guards.insert(
            RelPath::new(release::binary_name(target)?)?,
            FileSnapshot::Missing,
        );
    }
    guards.insert(RelPath::new(RECEIPT)?, before.clone());
    Ok(Preflight {
        path,
        root: Some(root),
        receipt,
        before,
        guards,
    })
}
pub fn doctor(path: &Path) -> Result<ToolDoctorReport> {
    let root = Root::open(path)?;
    let (receipt, _) = detect(&root)?;
    let mut differences = Vec::new();
    if let Some(receipt) = &receipt {
        for (path, descriptor) in &receipt.payload {
            if root.inspect(path)?.descriptor() != Some(descriptor) {
                differences.push(path.clone());
            }
        }
    }
    Ok(ToolDoctorReport {
        root: root.path.display().to_string(),
        installation: receipt,
        differences,
        transactions: transaction::status(path)?,
    })
}
