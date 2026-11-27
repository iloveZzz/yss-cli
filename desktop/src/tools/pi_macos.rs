//! Staged user-local Pi install. No global npm prefix, Node, or Pi configuration is changed.
use super::portable_node_macos as portable;
use semver::Version;
use std::{
    fs,
    os::unix::fs::MetadataExt,
    path::{Component, Path, PathBuf},
    process::{Command, Stdio},
};
use yss::{
    fs::{FileSnapshot, Root},
    model::RelPath,
    workbench::managed_pi,
};

#[path = "pi_staging.rs"]
mod staging;
use staging::PiApplyError;

fn command(node: &Path, stage: &Path) -> Command {
    let mut command = Command::new(node);
    for (key, _) in std::env::vars_os() {
        let upper = key.to_string_lossy().to_ascii_uppercase();
        if upper.starts_with("NPM_CONFIG_")
            || upper.starts_with("NODE_")
            || upper.starts_with("PI_")
            || matches!(upper.as_str(), "BASH_ENV" | "ENV" | "DYLD_INSERT_LIBRARIES")
        {
            command.env_remove(key);
        }
    }
    command
        .env("HOME", stage)
        .env("PI_CODING_AGENT_DIR", stage.join(".pi-agent"))
        .env("PI_TELEMETRY", "0")
        .current_dir(stage)
        .stdin(Stdio::null());
    command
}

fn update_command(node: &Path, stage: &Path) -> Command {
    let mut command = command(node, stage);
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("HOME", stage)
        .env("PI_CODING_AGENT_DIR", stage.join(".pi-agent"))
        .env("PI_TELEMETRY", "0");
    command
}
fn checked_dir(root: &Root, uid: u32, private: bool) -> Result<(), String> {
    let path = &root.path;
    let metadata = fs::symlink_metadata(path).map_err(|_| "无法检查受管目录")?;
    let forbidden = if private { 0o077 } else { 0o022 };
    if !metadata.is_dir() || metadata.uid() != uid || metadata.mode() & forbidden != 0 {
        return Err(format!(
            "受管目录 {} 的所有权或权限不安全，已保留",
            path.display()
        ));
    }
    root.revalidate().map_err(|_| "受管目录路径已变化".into())
}
fn current_uid() -> Result<u32, String> {
    let output = Command::new("/usr/bin/id")
        .arg("-u")
        .output()
        .map_err(|_| "无法确定当前用户")?;
    if !output.status.success() {
        return Err("无法确定当前用户".into());
    }
    String::from_utf8_lossy(&output.stdout)
        .trim()
        .parse()
        .map_err(|_| "无法确定当前用户".into())
}

fn checked_child(
    parent: &Root,
    name: &RelPath,
    uid: u32,
    private: bool,
    create_new: bool,
) -> Result<Root, String> {
    let previous = parent.inspect(name).map_err(|_| "无法检查受管目录")?;
    let identity = match previous {
        FileSnapshot::Missing => parent
            .create_private_dir(name)
            .map_err(|_| "无法安全创建受管目录")?,
        FileSnapshot::Directory { identity } if !create_new => identity,
        _ => return Err("受管目录不安全或已存在，原内容已保留".into()),
    };
    let child = Root::open(&parent.path.join(name.as_str())).map_err(|_| "受管目录不安全")?;
    if child.identity != identity {
        return Err("受管目录身份已变化".into());
    }
    checked_dir(&child, uid, private)?;
    parent.revalidate().map_err(|_| "受管目录父路径已变化")?;
    Ok(child)
}

pub(super) fn desktop_directory(path: &Path) -> Result<Root, String> {
    let base = directories::BaseDirs::new().ok_or("无法确定当前用户目录")?;
    let uid = current_uid()?;
    #[cfg(debug_assertions)]
    if !path.starts_with(base.home_dir()) {
        let root = Root::open(path).map_err(|_| "隔离配置目录必须已存在且路径安全")?;
        checked_dir(&root, uid, true)?;
        return Ok(root);
    }
    installation_base(base.home_dir(), path, uid)
}

fn installation_base(home: &Path, base: &Path, uid: u32) -> Result<Root, String> {
    let mut parent = Root::open(home).map_err(|_| "当前用户目录路径不安全")?;
    checked_dir(&parent, uid, false)?;
    let suffix = base
        .strip_prefix(home)
        .map_err(|_| "个人数据路径不在当前用户目录下")?;
    for component in suffix.components() {
        let Component::Normal(name) = component else {
            return Err("个人数据路径无效".into());
        };
        let name = RelPath::new(name.to_str().ok_or("个人数据路径无效")?)
            .map_err(|_| "个人数据路径无效")?;
        parent = checked_child(&parent, &name, uid, false, false)?;
    }
    Ok(parent)
}

pub(super) fn install(
    existing: Option<(PathBuf, PathBuf)>,
    brew: Option<PathBuf>,
) -> Result<String, PiApplyError> {
    apply_at(tools_base()?, existing, brew, None)
}

pub(super) fn update(
    version: &Version,
    origin: managed_pi::UpdateOrigin<'_>,
    existing: Option<(PathBuf, PathBuf)>,
    brew: Option<PathBuf>,
) -> Result<String, PiApplyError> {
    apply_at(tools_base()?, existing, brew, Some((version, origin)))
}

fn tools_base() -> Result<Root, String> {
    let dirs = directories::ProjectDirs::from("com", "ysstech", "yss-workbench-desktop")
        .ok_or("无法确定个人数据目录")?;
    desktop_directory(dirs.data_local_dir())
}

fn apply_at(
    base: Root,
    existing: Option<(PathBuf, PathBuf)>,
    brew: Option<PathBuf>,
    update: Option<(&Version, managed_pi::UpdateOrigin<'_>)>,
) -> Result<String, PiApplyError> {
    let uid = current_uid()?;
    let tools = RelPath::new("tools").map_err(|_| "受管工具路径无效")?;
    let tools = managed_pi::lock(checked_child(&base, &tools, uid, true, false)?)
        .map_err(|error| error.message)?;
    let root = &tools.root;
    let target = RelPath::new("pi").map_err(|_| "受管路径无效")?;
    let previous = update
        .map(|(version, origin)| managed_pi::prepared_update(root, origin, version))
        .transpose()
        .map_err(|error| error.message)?;
    if previous.is_none()
        && managed_pi::active_installation(root)
            .map_err(|error| error.message)?
            .is_some()
    {
        return Err("受管 Pi 目录已存在，已保留原内容；请先核对现有安装".into());
    }
    let id = super::super::random_id()?;
    let stage_rel = if previous.is_some() {
        managed_pi::activation_directory(&id).map_err(|error| error.message)?
    } else {
        RelPath::new(format!(".pi-install-{id}")).map_err(|_| "暂存路径无效")?
    };
    let stage_root = checked_child(root, &stage_rel, uid, true, true)?;
    let stage = &stage_root.path;
    let baseline = managed_pi::BASELINE;
    let version = update.map_or(&baseline, |(version, _)| version);
    let stage_command = if previous.is_some() {
        update_command
    } else {
        command
    };
    let mut keep_stage = false;
    let result = (|| {
        let downloaded = existing.is_none();
        let (node, npm) = match existing {
            Some(paths) => paths,
            None => {
                portable::prepare(
                    brew.as_deref().ok_or("安装受管 Node 需要已有 Homebrew")?,
                    &stage_root,
                )?;
                let node = stage_root.path.join(portable::NODE);
                let npm = stage_root.path.join(portable::NPM);
                let mut check = stage_command(&node, stage);
                check.arg("--version");
                let (status, stdout, _) = yss::workbench::process::capture_command(
                    check,
                    &yss::cancel::Cancellation::new(),
                    std::time::Duration::from_secs(15),
                )
                .map_err(|_| "无法验证 macOS ARM64 Node 运行环境")?;
                if !status.success()
                    || String::from_utf8_lossy(&stdout)
                        .trim()
                        .trim_start_matches('v')
                        != portable::VERSION
                {
                    return Err("macOS ARM64 Node 运行版本校验失败".into());
                }
                (node, npm)
            }
        };
        let path = if downloaded && previous.is_none() {
            root.path.join("pi").join(portable::NODE)
        } else {
            fs::canonicalize(&node).map_err(|_| "无法核实 Node 路径")?
        };
        staging::PackageStage {
            root: &stage_root,
            node: &node,
            npm: &npm,
            installed_node: &path,
            version,
            update: previous.is_some(),
        }
        .install(stage_command)?;
        base.revalidate()
            .map_err(|_| "桌面目录身份已变化，Pi 尚未发布")?;
        if let Some(previous) = &previous {
            managed_pi::activate(root, &stage_root, previous, version, &id).map_err(|error| {
                let failure = PiApplyError::publishing(
                    error,
                    "未能切换受管 Pi 版本，请重新检查后重试。",
                    "Pi 版本切换结果待确认，请复检；原版本目录仍然保留。",
                );
                keep_stage = failure.uncertain;
                failure
            })?;
            keep_stage = true;
            return Ok(format!("受管 Pi 已切换到 {version}，请刷新工作台并复检。"));
        }
        root.rename(&stage_rel, &target, &FileSnapshot::Missing, false)
            .map_err(|error| {
                PiApplyError::publishing(
                    error,
                    "无法发布 Pi；原有目录已保留",
                    "Pi 安装结果待确认，请重新检测；不会覆盖现有目录",
                )
            })?;
        Ok("Pi 包已安装到当前用户受管目录；请确认结果并刷新工作台检测。".to_owned())
    })();
    // A generation selected by the active pointer remains available to running sessions.
    if !keep_stage && checked_dir(&stage_root, uid, true).is_ok() {
        let _ = fs::remove_dir_all(stage);
    }
    result
}
