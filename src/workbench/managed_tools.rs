//! User-private, immutable gh/glab releases and the active release pointer.
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root},
    model::RelPath,
};
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    path::{Component, Path, PathBuf},
    process::Command,
    time::Duration,
};

const LIMIT: u64 = 128 * 1024 * 1024;
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tool {
    Gh,
    Glab,
}
impl Tool {
    pub fn name(self) -> &'static str {
        match self {
            Self::Gh => "gh",
            Self::Glab => "glab",
        }
    }
    fn active(self) -> Result<RelPath> {
        RelPath::new(format!("{}-active.json", self.name()))
    }
    pub fn environment(self) -> &'static str {
        match self {
            Self::Gh => "YSS_DESKTOP_MANAGED_GH",
            Self::Glab => "YSS_DESKTOP_MANAGED_GLAB",
        }
    }
}
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ManagedTool {
    pub path: PathBuf,
    pub version: Version,
    pub sha256: String,
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Receipt {
    tool: String,
    id: String,
    version: Version,
    sha256: String,
}
fn bad(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
}
fn release_name(tool: Tool, id: &str) -> Result<RelPath> {
    if id.len() != 32
        || !id
            .bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
    {
        return Err(bad("受管工具版本标识无效"));
    }
    RelPath::new(format!(
        "{}-release-{}{}",
        tool.name(),
        id,
        if cfg!(windows) { ".exe" } else { "" }
    ))
}
fn receipt_name(tool: Tool, id: &str) -> Result<RelPath> {
    RelPath::new(format!("{}-release-{id}.json", tool.name()))
}
fn verify_private(root: &Root) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let metadata = std::fs::symlink_metadata(&root.path)?;
        if !metadata.is_dir()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
        {
            return Err(bad("受管工具目录权限不安全"));
        }
    }
    #[cfg(windows)]
    root.verify_private_dir()
        .map_err(|_| bad("受管工具目录权限不安全"))?;
    root.revalidate()
}
fn verify_parent(root: &Root) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let m = std::fs::symlink_metadata(&root.path)?;
        if !m.is_dir() || m.uid() != unsafe { libc::geteuid() } || m.mode() & 0o022 != 0 {
            return Err(bad("受管工具父目录权限不安全"));
        }
    }
    #[cfg(windows)]
    root.verify_trusted_dir()
        .map_err(|_| bad("受管工具父目录权限不安全"))?;
    root.revalidate()
}
struct ToolsRoot {
    root: Root,
    #[cfg(windows)]
    _pins: Vec<std::fs::File>,
}
pub fn directory() -> Option<PathBuf> {
    if !cfg!(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )) {
        return None;
    }
    Some(
        directories::ProjectDirs::from("com", "ysstech", "yss-workbench-desktop")?
            .data_local_dir()
            .join("tools/official"),
    )
}

pub(crate) fn protected_directory() -> Result<Option<PathBuf>> {
    let Some(target) = directory() else {
        return Ok(None);
    };
    #[cfg(unix)]
    {
        let base = directories::BaseDirs::new().ok_or_else(|| bad("无法确定当前用户目录"))?;
        let origin = base.home_dir();
        let suffix = target
            .strip_prefix(origin)
            .map_err(|_| bad("受管工具路径不在用户目录内"))?;
        Ok(Some(origin.canonicalize()?.join(suffix)))
    }
    #[cfg(windows)]
    Ok(Some(target))
}

fn tools_root(create: bool) -> Result<Option<ToolsRoot>> {
    let Some(target) = directory() else {
        return Ok(None);
    };
    let base = directories::BaseDirs::new().ok_or_else(|| bad("无法确定当前用户目录"))?;
    #[cfg(unix)]
    let origin = base.home_dir();
    #[cfg(windows)]
    let origin = base.data_local_dir();
    let suffix = target
        .strip_prefix(origin)
        .map_err(|_| bad("受管工具路径不在用户目录内"))?;
    #[cfg(windows)]
    let mut pins = Vec::new();
    #[cfg(windows)]
    {
        let mut ancestors = origin
            .ancestors()
            .filter(|p| p.parent().is_some())
            .collect::<Vec<_>>();
        ancestors.reverse();
        for path in ancestors {
            let root = Root::open(path)?;
            pins.push(root.pin_directory()?);
            root.revalidate()?;
        }
    }
    let mut parent = Root::open(origin)?;
    verify_parent(&parent)?;
    let components: Vec<_> = suffix.components().collect();
    for (index, component) in components.iter().enumerate() {
        let Component::Normal(name) = component else {
            return Err(bad("受管工具路径无效"));
        };
        let rel = RelPath::new(name.to_str().ok_or_else(|| bad("受管目录名无效"))?)?;
        let snapshot = parent.inspect(&rel)?;
        let identity = match snapshot {
            FileSnapshot::Missing if !create => return Ok(None),
            FileSnapshot::Missing => parent.create_private_dir(&rel)?,
            FileSnapshot::Directory { identity } => identity,
            _ => return Err(bad("受管工具目录路径不安全")),
        };
        let child = Root::open(&parent.path.join(rel.as_str()))?;
        #[cfg(windows)]
        pins.push(child.pin_directory()?);
        if child.identity != identity {
            return Err(bad("受管工具目录身份已变化"));
        }
        if index + 1 == components.len() {
            verify_private(&child)?
        } else {
            verify_parent(&child)?
        };
        parent.revalidate()?;
        parent = child;
    }
    Ok(Some(ToolsRoot {
        root: parent,
        #[cfg(windows)]
        _pins: pins,
    }))
}
fn load_release(root: &Root, tool: Tool, receipt: &Receipt) -> Result<ManagedTool> {
    if receipt.tool != tool.name()
        || !receipt.version.pre.is_empty()
        || !receipt.version.build.is_empty()
        || receipt.sha256.len() != 64
        || !receipt
            .sha256
            .bytes()
            .all(|v| v.is_ascii_digit() || (b'a'..=b'f').contains(&v))
    {
        return Err(bad("受管工具记录无效"));
    }
    let name = release_name(tool, &receipt.id)?;
    let file = root
        .read_checked_single_link(&name, LIMIT)
        .map_err(|_| bad("受管工具不可读取或不安全"))?;
    if format!("{:x}", Sha256::digest(&file.bytes)) != receipt.sha256 {
        return Err(bad("受管工具摘要不匹配"));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let m = std::fs::symlink_metadata(root.path.join(name.as_str()))?;
        if !m.is_file()
            || m.uid() != unsafe { libc::geteuid() }
            || m.mode() & 0o077 != 0
            || m.mode() & 0o100 == 0
        {
            return Err(bad("受管工具文件权限不安全"));
        }
    }
    root.revalidate()?;
    Ok(ManagedTool {
        path: root.path.join(name.as_str()),
        version: receipt.version.clone(),
        sha256: receipt.sha256.clone(),
    })
}
fn active_at(root: &Root, tool: Tool) -> Result<Option<ManagedTool>> {
    let name = tool.active()?;
    if root.inspect(&name)? == FileSnapshot::Missing {
        return Ok(None);
    }
    let file = root
        .read_private_checked(&name, 4096)
        .map_err(|_| bad("受管工具活动记录不安全"))?;
    let receipt: Receipt =
        serde_json::from_slice(&file.bytes).map_err(|_| bad("受管工具活动记录无效"))?;
    let sealed = root
        .read_private_checked(&receipt_name(tool, &receipt.id)?, 4096)
        .map_err(|_| bad("受管工具版本记录不安全"))?;
    if sealed.bytes != file.bytes {
        return Err(bad("受管工具版本记录不一致"));
    }
    load_release(root, tool, &receipt).map(Some)
}
pub fn inspect(tool: Tool) -> Result<Option<ManagedTool>> {
    let Some(dir) = tools_root(false)? else {
        return Ok(None);
    };
    active_at(&dir.root, tool)
}
/// Resolve an explicitly pinned release even after a newer one becomes active.
pub fn resolve_pinned(tool: Tool, path: &Path) -> Result<Option<PathBuf>> {
    let Some(dir) = tools_root(false)? else {
        return Ok(None);
    };
    if path.parent() != Some(dir.root.path.as_path()) {
        return Ok(None);
    }
    let name = path
        .file_name()
        .and_then(|s| s.to_str())
        .ok_or_else(|| bad("受管工具路径无效"))?;
    let prefix = format!("{}-release-", tool.name());
    let suffix = if cfg!(windows) { ".exe" } else { "" };
    let Some(id) = name
        .strip_prefix(&prefix)
        .and_then(|v| v.strip_suffix(suffix))
    else {
        return Ok(None);
    };
    let receipt = dir
        .root
        .read_private_checked(&receipt_name(tool, id)?, 4096)?;
    let receipt: Receipt =
        serde_json::from_slice(&receipt.bytes).map_err(|_| bad("受管工具版本记录无效"))?;
    if receipt.id != id {
        return Err(bad("受管工具版本标识不匹配"));
    }
    Ok(Some(load_release(&dir.root, tool, &receipt)?.path))
}
/// The caller supplies bytes already checked against the fixed official release digest.
pub fn publish(
    tool: Tool,
    version: &Version,
    bytes: &[u8],
    expected: Option<&ManagedTool>,
) -> Result<ManagedTool> {
    if bytes.is_empty()
        || bytes.len() as u64 > LIMIT
        || !version.pre.is_empty()
        || !version.build.is_empty()
    {
        return Err(bad("受管工具版本或二进制大小无效"));
    }
    let dir = tools_root(true)?.ok_or_else(|| bad("无法创建受管工具目录"))?;
    let lock = crate::fs::lock_file(
        dir.root,
        &RelPath::new(format!(".{}-update.lock", tool.name()))?,
    )?;
    let root = &lock.root;
    verify_private(root)?;
    let previous = active_at(root, tool)?;
    if previous.as_ref() != expected {
        return Err(bad("受管工具活动版本已变化，请重新检测"));
    }
    let active = tool.active()?;
    let previous_snapshot = root.inspect(&active)?;
    let id = crate::fs::unique_id()?;
    let binary = release_name(tool, &id)?;
    let created = root.create_file(&binary, bytes, 0o700)?;
    let mut published = false;
    let result = (|| {
        let path = root.path.join(binary.as_str());
        let mut command = Command::new(&path);
        command
            .arg("--version")
            .env_clear()
            .env("HOME", &root.path)
            .env("GH_CONFIG_DIR", &root.path)
            .env("GLAB_CONFIG_DIR", &root.path)
            .env("PATH", if cfg!(windows) { "" } else { "/usr/bin:/bin" });
        #[cfg(windows)]
        if let Some(system) = std::env::var_os("SystemRoot") {
            command.env("SystemRoot", system);
        }
        let (status, stdout, _) = super::process::capture_command(
            command,
            &Cancellation::new(),
            Duration::from_secs(10),
        )?;
        let first = String::from_utf8_lossy(&stdout);
        let parsed = first.lines().next().and_then(|line| {
            let mut parts = line.split_whitespace();
            if parts.next() != Some(tool.name()) {
                return None;
            }
            parts.find_map(|part| Version::parse(part.trim_start_matches('v')).ok())
        });
        if !status.success() || parsed.as_ref() != Some(version) {
            return Err(bad("受管工具版本核验失败，原有工具已保留"));
        }
        let receipt = Receipt {
            tool: tool.name().into(),
            id: id.clone(),
            version: version.clone(),
            sha256: format!("{:x}", Sha256::digest(bytes)),
        };
        let verified = load_release(root, tool, &receipt)?;
        let data = serde_json::to_vec(&receipt).map_err(|_| bad("无法编码受管工具记录"))?;
        let sealed_name = receipt_name(tool, &id)?;
        let sealed = root.create_private_file(&sealed_name, &data)?;
        let tmp = RelPath::new(format!(".{}-active-{id}.tmp", tool.name()))?;
        let tmp_snapshot = match root.create_private_file(&tmp, &data) {
            Ok(v) => v,
            Err(e) => {
                let _ = root.remove(&sealed_name, &sealed);
                return Err(e);
            }
        };
        if let Err(error) = root.rename(
            &tmp,
            &active,
            &previous_snapshot,
            previous_snapshot != FileSnapshot::Missing,
        ) {
            if error
                .context
                .get("renameApplied")
                .is_none_or(|v| v != "true")
            {
                let _ = root.remove(&tmp, &tmp_snapshot);
                let _ = root.remove(&sealed_name, &sealed);
            } else {
                published = true;
            }
            return Err(error);
        }
        published = true;
        Ok(verified)
    })();
    if result.is_err() && !published {
        let _ = root.remove(&binary, &created);
    }
    result
}
