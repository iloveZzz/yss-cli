use crate::{
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root, RootLock},
    model::RelPath,
};
use semver::Version;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{io::ErrorKind, path::PathBuf};

pub const PACKAGE: &str = "@earendil-works/pi-coding-agent";
pub const BASELINE: Version = Version::new(1, 6, 1);
pub const ENTRY: &str = "node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js";
const PACKAGE_MANIFEST: &str = "node_modules/@earendil-works/pi-coding-agent/package.json";
const ACTIVE: &str = "pi-active.json";
const RELEASE_PREFIX: &str = "pi-release-";
const MAX_MANIFEST: u64 = 1024 * 1024;

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Release {
    directory: String,
    version: Version,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct NodeRecord {
    pub node_path: PathBuf,
}

pub struct ActiveInstall {
    pub entry: PathBuf,
    pub version: Version,
    pub node: PathBuf,
    activation: FileSnapshot,
}

impl ActiveInstall {
    pub(crate) fn activation(&self) -> &FileSnapshot {
        &self.activation
    }
}

#[derive(Clone, Copy)]
pub enum UpdateOrigin<'a> {
    Managed(&'a Version),
    External(&'a Version),
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
}

pub fn directory() -> Option<PathBuf> {
    Some(
        directories::ProjectDirs::from("com", "ysstech", "yss-workbench-desktop")?
            .data_local_dir()
            .join("tools"),
    )
}

pub(crate) fn has_selected_release() -> Result<bool> {
    let directory = directory().ok_or_else(|| invalid("无法确定桌面版工具目录"))?;
    match std::fs::symlink_metadata(directory.join(ACTIVE)) {
        Ok(_) => Ok(true),
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(false),
        Err(_) => Err(invalid("无法读取受管 Pi 的活动版本记录")),
    }
}

pub fn supports_update_version(version: &Version) -> bool {
    version.major == BASELINE.major
        && *version >= BASELINE
        && version.pre.is_empty()
        && version.build.is_empty()
}

fn release_directory(name: &str) -> Result<RelPath> {
    let id = name
        .strip_prefix(RELEASE_PREFIX)
        .filter(|id| id.len() == 32 && id.bytes().all(|byte| byte.is_ascii_hexdigit()))
        .ok_or_else(|| invalid("受管 Pi 版本目录名称无效"))?;
    RelPath::new(format!("{RELEASE_PREFIX}{id}"))
}

fn verify_private(root: &Root) -> Result<()> {
    #[cfg(windows)]
    root.verify_private_dir()
        .map_err(|_| invalid("受管 Pi 目录权限不安全"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let current_uid = unsafe { libc::geteuid() };
        let metadata = std::fs::symlink_metadata(&root.path)?;
        if !metadata.is_dir() || metadata.uid() != current_uid || metadata.mode() & 0o077 != 0 {
            return Err(invalid("受管 Pi 目录权限不安全"));
        }
    }
    root.revalidate()
}

pub fn lock(tools: Root) -> Result<RootLock> {
    verify_private(&tools)?;
    crate::fs::lock_file(tools, &RelPath::new(".pi-update.lock")?)
}

pub fn verify_package(root: &Root, version: &Version) -> Result<PathBuf> {
    verify_private(root)?;
    let file = root.read_checked_single_link(&RelPath::new(PACKAGE_MANIFEST)?, MAX_MANIFEST)?;
    let metadata: Value =
        serde_json::from_slice(&file.bytes).map_err(|_| invalid("Pi 包信息无效"))?;
    let actual_version = metadata["version"]
        .as_str()
        .and_then(|value| Version::parse(value).ok());
    if metadata["name"] != PACKAGE
        || actual_version.as_ref() != Some(version)
        || metadata["bin"]["pi"] != "dist/bundle/cli.js"
    {
        return Err(invalid("Pi 包身份、版本或入口不匹配"));
    }
    let entry = RelPath::new(ENTRY)?;
    root.read_checked_single_link(&entry, MAX_MANIFEST)?;
    Ok(root.path.join(entry.as_str()))
}

pub fn active_installation(tools: &Root) -> Result<Option<ActiveInstall>> {
    verify_private(tools)?;
    let active = RelPath::new(ACTIVE)?;
    let (directory, version, activation) = if tools.inspect(&active)? == FileSnapshot::Missing {
        (RelPath::new("pi")?, BASELINE, FileSnapshot::Missing)
    } else {
        let file = tools
            .read_private_checked(&active, 4096)
            .map_err(|_| invalid("受管 Pi 的活动版本记录不安全"))?;
        let release: Release = serde_json::from_slice(&file.bytes)
            .map_err(|_| invalid("受管 Pi 的活动版本记录无效"))?;
        if !supports_update_version(&release.version) {
            return Err(invalid("受管 Pi 的活动版本不受当前工作台支持"));
        }
        (
            release_directory(&release.directory)?,
            release.version,
            file.snapshot,
        )
    };
    let installation = match tools.inspect(&directory)? {
        FileSnapshot::Missing if activation == FileSnapshot::Missing => return Ok(None),
        FileSnapshot::Directory { identity } => {
            let child = Root::open(&tools.path.join(directory.as_str()))?;
            if child.identity != identity {
                return Err(invalid("受管 Pi 的活动版本目录已变化"));
            }
            child
        }
        _ => return Err(invalid("受管 Pi 的活动版本目录缺失或不安全")),
    };
    let entry = verify_package(&installation, &version)?;
    let file = installation
        .read_private_checked(&RelPath::new("node-path.json")?, 16 * 1024)
        .map_err(|_| invalid("受管 Pi 的 Node 配置不安全或不存在"))?;
    let node: NodeRecord =
        serde_json::from_slice(&file.bytes).map_err(|_| invalid("受管 Pi 的 Node 配置无效"))?;
    if !node.node_path.is_absolute() {
        return Err(invalid("受管 Pi 的 Node 路径无效"));
    }
    tools.revalidate()?;
    Ok(Some(ActiveInstall {
        entry,
        version,
        node: node.node_path,
        activation,
    }))
}

pub fn activation_directory(id: &str) -> Result<RelPath> {
    release_directory(&format!("{RELEASE_PREFIX}{id}"))
}

pub fn prepared_update(
    tools: &Root,
    origin: UpdateOrigin<'_>,
    version: &Version,
) -> Result<FileSnapshot> {
    let current = match origin {
        UpdateOrigin::Managed(current) | UpdateOrigin::External(current) => current,
    };
    if !supports_update_version(version) || !version.cmp_precedence(current).is_gt() {
        return Err(invalid("所选 Pi 版本不受支持，或不比当前版本更新"));
    }
    match (origin, active_installation(tools)?) {
        (UpdateOrigin::Managed(_), Some(old)) if old.version == *current => Ok(old.activation),
        (UpdateOrigin::External(_), None) => Ok(FileSnapshot::Missing),
        (UpdateOrigin::External(_), Some(old)) if old.activation == FileSnapshot::Missing => {
            Ok(old.activation)
        }
        _ => Err(invalid("受管 Pi 的当前版本与更新计划不一致，请重新检测")),
    }
}

pub fn activate(
    tools: &Root,
    stage: &Root,
    previous: &FileSnapshot,
    version: &Version,
    id: &str,
) -> Result<()> {
    let directory = activation_directory(id)?;
    if stage.path != tools.path.join(directory.as_str())
        || tools.inspect(&directory)?.identity() != Some(&stage.identity)
    {
        return Err(invalid("受管 Pi 的待激活目录已变化"));
    }
    verify_private(tools)?;
    verify_private(stage)?;
    let destination = RelPath::new(ACTIVE)?;
    let temporary = RelPath::new(format!(".pi-active-{id}.tmp"))?;
    let receipt = serde_json::to_vec(&Release {
        directory: directory.as_str().into(),
        version: version.clone(),
    })
    .map_err(|_| invalid("无法记录受管 Pi 的活动版本"))?;
    let snapshot = tools.create_private_file(&temporary, &receipt)?;
    let published = tools.rename(
        &temporary,
        &destination,
        previous,
        *previous != FileSnapshot::Missing,
    );
    if published.is_err() {
        let _ = tools.remove(&temporary, &snapshot);
    }
    published
}
