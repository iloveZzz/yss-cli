//! Windows user-local Pi install in a protected staging directory.
use semver::Version;
use std::{
    fs,
    path::{Component, Path, PathBuf},
    process::Command,
};
use yss::{
    fs::{FileSnapshot, Root},
    model::RelPath,
    workbench::managed_pi,
};

#[path = "pi_staging.rs"]
mod staging;
use staging::PiApplyError;

fn isolated_command(node: &Path, stage: &Path) -> Command {
    let mut command = Command::new(node);
    for (key, _) in std::env::vars_os() {
        let upper = key.to_string_lossy().to_ascii_uppercase();
        if upper.starts_with("NPM_CONFIG_")
            || upper.starts_with("NODE_")
            || upper.starts_with("PI_")
            || matches!(upper.as_str(), "BASH_ENV" | "ENV")
        {
            command.env_remove(key);
        }
    }
    command
        .env("HOME", stage)
        .env("USERPROFILE", stage)
        .env("APPDATA", stage.join("AppData/Roaming"))
        .env("LOCALAPPDATA", stage.join("AppData/Local"))
        .env("PI_CODING_AGENT_DIR", stage.join(".pi-agent"))
        .env("PI_TELEMETRY", "0")
        .current_dir(stage);
    command
}

fn update_command(node: &Path, stage: &Path) -> Command {
    let mut command = Command::new(node);
    command
        .env_clear()
        .env("HOME", stage)
        .env("USERPROFILE", stage)
        .env("APPDATA", stage.join("AppData/Roaming"))
        .env("LOCALAPPDATA", stage.join("AppData/Local"))
        .env("TEMP", stage)
        .env("TMP", stage)
        .env("PI_CODING_AGENT_DIR", stage.join(".pi-agent"))
        .env("PI_TELEMETRY", "0")
        .current_dir(stage);
    if let Some(root) = std::env::var_os("SystemRoot")
        .map(PathBuf::from)
        .filter(|p| p.is_absolute())
    {
        let system = root.join("System32");
        command
            .env("PATH", &system)
            .env("ComSpec", system.join("cmd.exe"))
            .env("SystemRoot", root);
    }
    command
}
fn private_child(parent: &Root, name: &str) -> Result<(Root, fs::File), String> {
    let relative = RelPath::new(name).map_err(|_| "受管目录名称无效")?;
    let previous = parent.inspect(&relative).map_err(|_| "无法检查受管目录")?;
    if previous == FileSnapshot::Missing {
        parent
            .create_private_dir(&relative)
            .map_err(|_| "无法安全创建受管目录")?;
    } else if !matches!(previous, FileSnapshot::Directory { .. }) {
        return Err("受管目录路径不安全，原内容已保留".into());
    }
    let child = Root::open(&parent.path.join(name)).map_err(|_| "无法打开受管目录")?;
    let pin = child
        .pin_directory()
        .map_err(|_| "无法固定受管目录，请关闭占用目录的程序后重试")?;
    if parent
        .inspect(&relative)
        .map_err(|_| "受管目录状态未知")?
        .identity()
        != Some(&child.identity)
    {
        return Err("受管目录身份已变化，原内容已保留".into());
    }
    child
        .verify_private_dir()
        .map_err(|_| "受管目录权限不安全，原内容已保留")?;
    parent.revalidate().map_err(|_| "受管目录父路径已变化")?;
    Ok((child, pin))
}
struct BaseGuard {
    root: Root,
    _pins: Vec<fs::File>,
}

pub(super) fn desktop_directory(path: &Path) -> Result<Root, String> {
    let base = directories::BaseDirs::new().ok_or("无法确定当前用户目录")?;
    #[cfg(debug_assertions)]
    if !path.starts_with(base.config_dir()) {
        let root = Root::open(path).map_err(|_| "隔离配置目录必须已存在且路径安全")?;
        root.verify_trusted_dir()
            .map_err(|_| "隔离配置目录权限不安全")?;
        return Ok(root);
    }
    installation_base_from(base.config_dir(), path).map(|guard| guard.root)
}

fn installation_base() -> Result<BaseGuard, String> {
    let local = directories::BaseDirs::new().ok_or("无法确定当前用户目录")?;
    let expected = directories::ProjectDirs::from("com", "ysstech", "yss-workbench-desktop")
        .ok_or("无法确定个人数据目录")?;
    installation_base_from(local.data_local_dir(), expected.data_local_dir())
}
fn installation_base_from(local: &Path, expected: &Path) -> Result<BaseGuard, String> {
    let suffix = expected
        .strip_prefix(local)
        .map_err(|_| "个人数据路径不在系统用户目录下")?;
    let mut ancestors = local
        .ancestors()
        .filter(|path| path.parent().is_some())
        .collect::<Vec<_>>();
    ancestors.reverse();
    let mut roots = Vec::new();
    let mut pins = Vec::new();
    for path in ancestors {
        let root = Root::open(path).map_err(|_| "本机数据目录的祖先不可安全打开")?;
        let pin = root
            .pin_directory()
            .map_err(|_| "无法固定本机数据目录的祖先，请关闭占用目录的程序后重试")?;
        roots.push(root);
        pins.push(pin);
    }
    for root in &roots {
        root.revalidate().map_err(|_| "本机数据目录的祖先已变化")?;
    }
    let mut parent = Root::open(local).map_err(|_| "本机数据目录不可用或不安全")?;
    if roots
        .last()
        .is_none_or(|root| root.identity != parent.identity)
    {
        return Err("本机数据目录身份已变化".into());
    }
    parent
        .verify_trusted_dir()
        .map_err(|_| "本机数据目录的访问权限无法安全确认，请检查目录 ACL 后重试")?;
    // Each missing directory is private at creation; existing directories are preserved.
    for component in suffix.components() {
        let Component::Normal(name) = component else {
            return Err("桌面目录路径无效".into());
        };
        let name = name.to_str().ok_or("桌面目录名称无效")?;
        let relative = RelPath::new(name).map_err(|_| "桌面目录名称无效")?;
        let previous = parent.inspect(&relative).map_err(|_| "无法检查桌面目录")?;
        let created = if previous == FileSnapshot::Missing {
            Some(
                parent
                    .create_private_dir(&relative)
                    .map_err(|_| "无法安全创建桌面目录")?,
            )
        } else if matches!(previous, FileSnapshot::Directory { .. }) {
            None
        } else {
            return Err("桌面目录路径不安全，原内容已保留".into());
        };
        let child = Root::open(&parent.path.join(name)).map_err(|_| "桌面目录不安全")?;
        let pin = child
            .pin_directory()
            .map_err(|_| "无法固定桌面目录，请关闭占用目录的程序后重试")?;
        if parent
            .inspect(&relative)
            .map_err(|_| "桌面目录状态未知")?
            .identity()
            != Some(&child.identity)
            || created
                .as_ref()
                .is_some_and(|identity| identity != &child.identity)
        {
            return Err("桌面目录身份已变化".into());
        }
        child.verify_trusted_dir().map_err(
            |_| "桌面数据目录的访问权限无法安全确认；原权限已保留，请检查当前用户的目录 ACL 后重试",
        )?;
        if created.is_some() {
            child
                .verify_private_dir()
                .map_err(|_| "新建桌面目录权限不安全")?;
        }
        parent.revalidate().map_err(|_| "桌面目录父路径已变化")?;
        pins.push(pin);
        parent = child;
    }
    let expected = Root::open(expected).map_err(|_| "无法核验桌面目录")?;
    if expected.identity != parent.identity {
        return Err("桌面目录与系统配置不一致".into());
    }
    Ok(BaseGuard {
        root: parent,
        _pins: pins,
    })
}
fn find_archive(
    root: &Root,
    parent: &RelPath,
    depth: usize,
    count: &mut usize,
    found: &mut Vec<RelPath>,
) -> Result<(), String> {
    if depth > 3 {
        return Err("WinGet 下载目录层级过深".into());
    }
    for name in root
        .list(Some(parent))
        .map_err(|_| "无法检查 WinGet 下载目录")?
    {
        *count += 1;
        if *count > 64 {
            return Err("WinGet 下载目录包含过多条目".into());
        }
        let child = parent.join(&name).map_err(|_| "WinGet 下载路径无效")?;
        match root
            .inspect(&child)
            .map_err(|_| "无法检查 WinGet 下载文件")?
        {
            FileSnapshot::Directory { .. } => find_archive(root, &child, depth + 1, count, found)?,
            FileSnapshot::File { .. } if name.to_ascii_lowercase().ends_with(".zip") => {
                found.push(child)
            }
            FileSnapshot::File { .. } => {}
            _ => return Err("WinGet 下载目录包含不安全条目".into()),
        }
    }
    Ok(())
}
fn acquire_node(root: &Root, winget: &Path) -> Result<(PathBuf, PathBuf), String> {
    let download = RelPath::new("node-download").map_err(|_| "Node 下载路径无效")?;
    root.create_private_dir(&download)
        .map_err(|_| "无法创建私有 Node 下载目录")?;
    let download_path = root.path.join(download.as_str());
    let download_root = Root::open(&download_path).map_err(|_| "Node 下载目录不安全")?;
    let pin = download_root
        .pin_directory()
        .map_err(|_| "无法固定 Node 下载目录")?;
    download_root
        .verify_private_dir()
        .map_err(|_| "Node 下载目录权限不安全")?;
    let mut command = Command::new(winget);
    use std::os::windows::process::CommandExt;
    command.creation_flags(0x08000000);
    command
        .args([
            "download",
            "--id",
            "OpenJS.NodeJS.24",
            "--exact",
            "--version",
            "24.22.1",
            "--source",
            "winget",
            "--architecture",
            "x64",
            "--installer-type",
            "zip",
            "--skip-dependencies",
            "--disable-interactivity",
            "--download-directory",
        ])
        .arg(&download_path);
    let (status, _, _) = yss::workbench::process::capture_command(
        command,
        &yss::cancel::Cancellation::new(),
        std::time::Duration::from_secs(900),
    )
    .map_err(|_| "WinGet 下载 Node 失败或超时")?;
    if !status.success() {
        return Err(super::winget_failure(status)
            .map(str::to_owned)
            .unwrap_or_else(|| {
                format!("WinGet 下载 Node 未完成（状态 {status}）；请检查网络或渠道策略")
            }));
    }
    download_root
        .verify_private_dir()
        .map_err(|_| "Node 下载目录权限已变化")?;
    let mut archives = Vec::new();
    find_archive(root, &download, 0, &mut 0, &mut archives)?;
    if archives.len() != 1 {
        return Err("WinGet 未返回唯一的 Node 压缩包")?;
    }
    let archive = root
        .read_checked_single_link(&archives[0], 64 * 1024 * 1024)
        .map_err(|_| "无法安全读取 Node 压缩包")?;
    super::portable_node::extract(archive.bytes, root)?;
    drop(pin);
    fs::remove_dir_all(&download_path).map_err(|_| "无法清理私有 Node 下载目录")?;
    Ok((
        root.path.join("node-runtime/node.exe"),
        root.path
            .join("node-runtime/node_modules/npm/bin/npm-cli.js"),
    ))
}
pub(super) fn install(
    existing: Option<(PathBuf, PathBuf)>,
    winget: Option<PathBuf>,
) -> Result<String, PiApplyError> {
    install_or_update(existing, winget, None)
}

pub(super) fn update(
    version: &Version,
    origin: managed_pi::UpdateOrigin<'_>,
    existing: Option<(PathBuf, PathBuf)>,
    winget: Option<PathBuf>,
) -> Result<String, PiApplyError> {
    install_or_update(existing, winget, Some((version, origin)))
}

fn install_or_update(
    existing: Option<(PathBuf, PathBuf)>,
    winget: Option<PathBuf>,
    update: Option<(&Version, managed_pi::UpdateOrigin<'_>)>,
) -> Result<String, PiApplyError> {
    let base = installation_base()?;
    let (tools, _tools_pin) = private_child(&base.root, "tools")?;
    let tools = managed_pi::lock(tools).map_err(|error| error.message)?;
    let tools = &tools.root;
    let target = RelPath::new("pi").map_err(|_| "受管路径无效")?;
    let previous = update
        .map(|(version, origin)| managed_pi::prepared_update(tools, origin, version))
        .transpose()
        .map_err(|error| error.message)?;
    if previous.is_none()
        && managed_pi::active_installation(tools)
            .map_err(|error| error.message)?
            .is_some()
    {
        return Err("受管 Pi 目录已存在，已保留原内容；请先核对现有安装".into());
    }
    let id = super::super::random_id()?;
    let stage_rel = if previous.is_some() {
        managed_pi::activation_directory(&id).map_err(|error| error.message)?
    } else {
        RelPath::new(format!(".pi-install-{id}")).map_err(|_| "暂存名称无效")?
    };
    let stage_identity = tools
        .create_private_dir(&stage_rel)
        .map_err(|_| "无法安全创建暂存目录")?;
    let stage = tools.path.join(stage_rel.as_str());
    let baseline = managed_pi::BASELINE;
    let pi_version = update.map_or(&baseline, |(version, _)| version);
    let stage_command = if previous.is_some() {
        update_command
    } else {
        isolated_command
    };
    let mut keep_stage = false;
    let result = (|| {
        let root = Root::open(&stage).map_err(|_| "暂存目录不安全")?;
        if root.identity != stage_identity {
            return Err("暂存目录身份已变化".into());
        }
        let stage_pin = root.pin_directory().map_err(|_| "无法固定 Pi 暂存目录")?;
        root.verify_private_dir()
            .map_err(|_| "暂存目录权限不安全")?;
        let managed_node = existing.is_none();
        let (node, npm) = match existing {
            Some(paths) => paths,
            None => acquire_node(&root, winget.as_deref().ok_or("安装 Node 需要已有 WinGet")?)?,
        };
        let mut node_probe = stage_command(&node, &stage);
        node_probe.arg("--version");
        let (status, version, _) = yss::workbench::process::capture_command(
            node_probe,
            &yss::cancel::Cancellation::new(),
            std::time::Duration::from_secs(15),
        )
        .map_err(|_| "无法验证 Node 运行环境")?;
        let version = semver::Version::parse(
            String::from_utf8_lossy(&version)
                .trim()
                .trim_start_matches('v'),
        )
        .map_err(|_| "Node 版本无效")?;
        if !status.success()
            || version < semver::Version::new(22, 19, 0)
            || (managed_node && version != semver::Version::new(24, 22, 1))
        {
            return Err("Node 版本不满足 Pi 要求".into());
        }
        let path = if managed_node && previous.is_none() {
            tools.path.join("pi/node-runtime/node.exe")
        } else {
            fs::canonicalize(&node).map_err(|_| "无法核实 Node 路径")?
        };
        staging::PackageStage {
            root: &root,
            node: &node,
            npm: &npm,
            installed_node: &path,
            version: pi_version,
            update: previous.is_some(),
        }
        .install(stage_command)?;
        let checked_base = installation_base()?;
        if checked_base.root.identity != base.root.identity {
            return Err("桌面目录身份已变化，安装未发布".into());
        }
        tools
            .verify_private_dir()
            .map_err(|_| "受管目录权限已变化，安装未发布")?;
        if tools
            .inspect(&stage_rel)
            .map_err(|_| "暂存状态未知")?
            .identity()
            != Some(&stage_identity)
        {
            return Err("暂存目录身份已变化".into());
        }
        if let Some(previous) = &previous {
            managed_pi::activate(tools, &root, previous, pi_version, &id).map_err(|error| {
                let failure = PiApplyError::publishing(
                    error,
                    "未能切换受管 Pi 版本，请重新检查后重试。",
                    "Pi 版本切换结果待确认，请复检；原版本目录仍然保留。",
                );
                keep_stage = failure.uncertain;
                failure
            })?;
            keep_stage = true;
            return Ok(format!(
                "受管 Pi 已切换到 {pi_version}，请刷新工作台并复检。"
            ));
        }
        // The protected, pinned parent excludes untrusted replacement; release the
        // staging handle only for the final no-replace rename.
        drop(stage_pin);
        tools
            .rename(&stage_rel, &target, &FileSnapshot::Missing, false)
            .map_err(|error| {
                PiApplyError::publishing(
                    error,
                    "无法发布 Pi；原有目录已保留",
                    "Pi 安装结果待确认，请重新检测；不会覆盖现有目录",
                )
            })?;
        Ok("Pi 包已安装到当前用户受管目录；请确认结果并刷新工作台检测。".to_owned())
    })();
    // Delete only a staging directory whose identity and protected ACL still match.
    if !keep_stage && let Ok(root) = Root::open(&stage) {
        if root.identity == stage_identity
            && root.verify_private_dir().is_ok()
            && tools
                .inspect(&stage_rel)
                .ok()
                .and_then(|v| v.identity().cloned())
                == Some(stage_identity)
        {
            let _ = fs::remove_dir_all(&stage);
        }
    }
    result
}
