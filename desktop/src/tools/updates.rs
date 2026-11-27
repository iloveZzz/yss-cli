use super::{
    INSTALL, InstallResult, ToolId, ToolStatus, ToolsStatus, UPDATE_IN_PROGRESS, UpdateTarget,
    begin_action, channel, compatible_node, connect, end_install, freeze_and_flush, get,
    mark_completed, node_options, protected_path, protected_paths, retire_connection, settings,
    status, versions,
};
use crate::{Session, State};
use serde::{Deserialize, Serialize};
#[cfg(target_os = "macos")]
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{
    path::{Path, PathBuf},
    process::Command,
    sync::{Mutex, atomic::Ordering},
    time::Duration,
};
use tauri::Manager;
use yss::workbench::{
    managed_pi,
    managed_tools::{self, ManagedTool},
};

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolUpdate {
    id: ToolId,
    current_version: Option<String>,
    current_path: Option<String>,
    installation_id: Option<String>,
    release: Option<versions::Release>,
    updateable: bool,
    official_available: bool,
    message: String,
    node_plan: Option<String>,
    prerequisites: Vec<String>,
}

struct PiNode {
    reuse: Option<(PathBuf, PathBuf, semver::Version)>,
    installer: Option<PathBuf>,
    plan: String,
    prerequisites: Vec<String>,
}

fn connection(app: &tauri::AppHandle) -> Result<(Session, bool), String> {
    let state = app.state::<Mutex<State>>();
    let state = state.lock().map_err(|_| "桌面状态不可用")?;
    let conn = state.connection.as_ref().ok_or("请先连接工作台")?;
    Ok((conn.session.clone(), conn.child.is_some()))
}

fn node_ranges(requirement: &str) -> Option<Vec<semver::VersionReq>> {
    requirement
        .split("||")
        .map(|alternative| {
            let mut tokens = alternative.split_ascii_whitespace();
            let mut comparators = Vec::new();
            while let Some(token) = tokens.next() {
                let comparator = if matches!(token, ">" | ">=" | "<" | "<=" | "=" | "^" | "~") {
                    format!("{token}{}", tokens.next()?.trim_start_matches('v'))
                } else if token.starts_with(|c: char| c.is_ascii_digit() || c == 'v') {
                    format!("={}", token.trim_start_matches('v'))
                } else {
                    token.to_owned()
                };
                comparators.push(comparator);
            }
            if comparators.is_empty() {
                return None;
            }
            semver::VersionReq::parse(&comparators.join(", ")).ok()
        })
        .collect()
}

fn private_node_version() -> Option<semver::Version> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    return semver::Version::parse(super::portable_node_macos::VERSION).ok();
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    return Some(semver::Version::new(24, 22, 1));
    #[allow(unreachable_code)]
    None
}

fn managed_pi_destination() -> Result<(PathBuf, PathBuf), String> {
    let directory = managed_pi::directory().ok_or("无法定位工作台的工具目录。")?;
    for ancestor in directory.ancestors() {
        match ancestor.canonicalize() {
            Ok(existing) => {
                let suffix = directory
                    .strip_prefix(ancestor)
                    .map_err(|_| "受管 Pi 路径无效。")?;
                let target = if suffix.as_os_str().is_empty() {
                    existing.clone()
                } else {
                    existing.join(suffix)
                };
                return Ok((target, existing));
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
            Err(_) => break,
        }
    }
    Err("无法核对受管 Pi 目录。".into())
}

fn pi_node(session: &Session, requirement: Option<&str>) -> Result<PiNode, String> {
    let requirement = requirement.unwrap_or(">=22.19.0");
    let ranges =
        node_ranges(requirement).ok_or("无法核对新版 Pi 的 Node.js 版本范围，可查看官方发行。")?;
    let accepts = |version: &semver::Version| ranges.iter().any(|range| range.matches(version));
    let trackers = get(session, "/api/desktop/tool-integrations")?;
    let protected = protected_paths(Some(&trackers)).ok_or("无法核验受保护路径")?;
    let (_, existing) = managed_pi_destination()?;
    if protected_path(&existing, &protected) {
        return Err("受管 Pi 须位于工程和工作台数据目录之外。".into());
    }
    let system = get(session, "/api/system")?;
    let preferred = system["pi"]["nodeExecutable"].as_str().map(PathBuf::from);
    let reuse = compatible_node(&protected, preferred, accepts);
    if reuse.is_none() && !private_node_version().as_ref().is_some_and(accepts) {
        return Err(format!(
            "新版 Pi 需要 Node.js {requirement}。未找到符合要求的本机 Node 或可下载的受管版本。"
        ));
    }
    let (_, installer, guidance) = if reuse.is_none() {
        channel()
    } else {
        ("", None, "")
    };
    let (plan, prerequisites) = node_options(reuse.as_ref(), true, installer.as_deref(), guidance);
    Ok(PiNode {
        reuse,
        installer,
        plan,
        prerequisites,
    })
}

enum InstallationKind {
    ManagedPi,
    ExternalPi,
    Official,
    #[cfg(target_os = "macos")]
    Homebrew,
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    WinGet {
        scope: &'static str,
        installer: &'static str,
    },
}

struct Installation {
    executable: PathBuf,
    anchor: PathBuf,
    kind: InstallationKind,
}

impl Installation {
    fn locator(&self, id: ToolId) -> String {
        Self::locator_at(id, &self.kind, &self.anchor)
    }

    fn locator_at(id: ToolId, kind: &InstallationKind, anchor: &Path) -> String {
        let mut digest = Sha256::new();
        digest.update(id.name());
        digest.update([0]);
        match kind {
            InstallationKind::ManagedPi => digest.update("npm"),
            InstallationKind::ExternalPi => digest.update("external-pi"),
            InstallationKind::Official => digest.update("official-release"),
            #[cfg(target_os = "macos")]
            InstallationKind::Homebrew => digest.update("homebrew/core"),
            #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
            InstallationKind::WinGet { scope, installer } => {
                digest.update(super::WINGET_SOURCE_ID);
                digest.update([0]);
                digest.update(scope);
                digest.update([0]);
                digest.update(installer);
            }
        }
        digest.update([0]);
        digest.update(anchor.as_os_str().as_encoded_bytes());
        format!("{:x}", digest.finalize())
    }

    fn plan_id(&self, id: ToolId) -> String {
        let mut digest = Sha256::new();
        digest.update(self.locator(id));
        digest.update([0]);
        digest.update(self.executable.as_os_str().as_encoded_bytes());
        format!("{:x}", digest.finalize())
    }
}

fn official_tool(id: ToolId) -> Option<managed_tools::Tool> {
    match id {
        ToolId::Gh => Some(managed_tools::Tool::Gh),
        ToolId::Glab => Some(managed_tools::Tool::Glab),
        ToolId::Git | ToolId::Pi => None,
    }
}

fn official_installation(id: ToolId, executable: &Path) -> Option<Installation> {
    let tool = official_tool(id)?;
    let anchor = managed_tools::directory()?;
    if !executable.starts_with(anchor.canonicalize().ok()?) {
        return None;
    }
    let active = managed_tools::inspect(tool).ok()??;
    (active.path.canonicalize().ok()? == executable).then(|| Installation {
        executable: executable.to_owned(),
        anchor,
        kind: InstallationKind::Official,
    })
}

pub(super) fn is_official_tool(id: ToolId, path: Option<&str>) -> bool {
    path.and_then(|path| official_installation(id, Path::new(path)))
        .is_some()
}

fn installation(id: ToolId, path: Option<&str>, managed: bool) -> Option<Installation> {
    let path = Path::new(path?);
    if !path.is_absolute() {
        return None;
    }
    let executable = path.canonicalize().ok()?;
    if let Some(official) = official_installation(id, &executable) {
        return Some(official);
    }
    if id == ToolId::Pi {
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        {
            if !managed {
                return Some(Installation {
                    anchor: executable.clone(),
                    executable,
                    kind: InstallationKind::ExternalPi,
                });
            }
            let dir = managed_pi::directory()?;
            let root = yss::fs::Root::open(&dir).ok()?;
            let active = managed_pi::active_installation(&root).ok()??;
            if active.entry.canonicalize().ok()? == executable {
                return Some(Installation {
                    executable,
                    anchor: dir.canonicalize().ok()?,
                    kind: InstallationKind::ManagedPi,
                });
            }
        }
        return None;
    }
    #[cfg(target_os = "macos")]
    {
        let cellar = Path::new("/opt/homebrew/Cellar").canonicalize().ok()?;
        let anchor = cellar.join(id.name());
        // Only the keg component changes when Homebrew upgrades the formula.
        let mut parts = executable.strip_prefix(&anchor).ok()?.components();
        parts.next()?;
        if parts.as_path() == Path::new("bin").join(id.name()) {
            return Some(Installation {
                executable,
                anchor,
                kind: InstallationKind::Homebrew,
            });
        }
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        return winget_installation(id, executable);
    }
    #[allow(unreachable_code)]
    None
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
fn winget_installation(id: ToolId, executable: PathBuf) -> Option<Installation> {
    let user = directories::BaseDirs::new()?.data_local_dir().to_path_buf();
    let machine = std::env::var_os("ProgramW6432")
        .or_else(|| std::env::var_os("ProgramFiles"))
        .map(PathBuf::from)?;
    let portable_dir = format!("{}_{}", id.winget_id()?, super::WINGET_SOURCE_ID);
    let (application, installer) = match id {
        ToolId::Git => ("Git", "inno"),
        ToolId::Gh => ("GitHub CLI", "wix"),
        ToolId::Glab => ("glab", "inno"),
        ToolId::Pi => return None,
    };
    let matching_location = |root: &Path| -> Option<String> {
        let root = root.canonicalize().ok()?;
        let path = executable.to_str()?.to_ascii_lowercase();
        let root = root.to_str()?.to_ascii_lowercase();
        let relative = Path::new(&path).strip_prefix(Path::new(&root)).ok()?;
        Some(relative.to_str()?.to_owned())
    };
    let exe = format!("{}.exe", id.name());
    let bin = format!("bin\\{exe}");
    let mut matches = [
        (
            "user",
            user.join("Programs").join(application),
            user.join("Microsoft/WinGet/Packages"),
        ),
        (
            "machine",
            machine.join(application),
            machine.join("WinGet/Packages"),
        ),
    ]
    .into_iter()
    .flat_map(|(scope, default, portable)| {
        let default_match = matching_location(&default).is_some_and(|relative| {
            relative == exe || relative == bin || id == ToolId::Git && relative == "cmd\\git.exe"
        });
        let portable_match = matching_location(&portable.join(&portable_dir))
            .is_some_and(|relative| relative == exe || relative == bin);
        [
            default_match.then_some((scope, installer)),
            portable_match.then_some((scope, "portable")),
        ]
        .into_iter()
        .flatten()
    });
    let (scope, installer) = matches.next()?;
    if matches.next().is_some() {
        return None;
    }
    Some(Installation {
        anchor: executable.clone(),
        executable,
        kind: InstallationKind::WinGet { scope, installer },
    })
}

pub(super) fn installation_locator(
    id: ToolId,
    path: Option<&str>,
    managed: bool,
) -> Option<String> {
    Some(installation(id, path, managed)?.locator(id))
}

fn eligible(
    tool: &ToolStatus,
    system: &ToolsStatus,
    owned: bool,
) -> Result<Installation, &'static str> {
    if tool.pending_confirmation {
        return Err("该工具的上次操作结果待确认，请先复检。");
    }
    if !owned {
        return Err("当前为外部工作台服务。请通过官方来源更新工具，由服务所有者刷新后复检。");
    }
    if tool.id == ToolId::Pi {
        let explicit = tool
            .explicit_pi_executable
            .ok_or("工作台未能核验 Pi 的启动方式，请重新连接后检查更新。")?;
        if explicit {
            return Err("工作台无法切换手动指定的 Pi，请通过原安装渠道更新。");
        }
        return installation(tool.id, tool.path.as_deref(), tool.managed)
            .ok_or("无法确认工作台当前使用的 Pi，请重新检测。");
    }
    if !system.channel_available {
        return Err("更新渠道当前不可用，请查看渠道说明或使用官方来源。");
    }
    let installed = installation(tool.id, tool.path.as_deref(), tool.managed)
        .ok_or("无法确认当前执行文件属于所示安装渠道，请通过原安装渠道更新。")?;
    if matches!(installed.kind, InstallationKind::Official) {
        return Err("工作台当前使用官方发行包，可继续通过官方发行包更新。");
    }
    Ok(installed)
}

fn official_eligible(tool: &ToolStatus, owned: bool, release: &versions::Release) -> bool {
    owned
        && !tool.pending_confirmation
        && official_tool(tool.id).is_some()
        && release.official_archive.is_some()
        && (tool.version.as_deref().map_or(!tool.detected, |current| {
            versions::is_newer(tool.id, &release.version, current) == Some(true)
        }))
}

fn checked_tool<'a>(
    system: &'a ToolsStatus,
    id: ToolId,
    current_version: Option<&str>,
    current_path: Option<&str>,
) -> Result<&'a ToolStatus, &'static str> {
    let tool = system
        .tools
        .iter()
        .find(|tool| tool.id == id)
        .ok_or("工作台未报告该工具")?;
    if tool.version.as_deref() != current_version || tool.path.as_deref() != current_path {
        return Err("实际工具版本或路径已变化，请重新检查并确认。");
    }
    Ok(tool)
}

fn describe(
    session: &Session,
    system: &ToolsStatus,
    owned: bool,
    tool: &ToolStatus,
    latest: Result<versions::Release, String>,
) -> ToolUpdate {
    let mut result = ToolUpdate {
        id: tool.id,
        current_version: tool.version.clone(),
        current_path: tool.path.clone(),
        installation_id: None,
        release: None,
        updateable: false,
        official_available: false,
        message: String::new(),
        node_plan: None,
        prerequisites: Vec::new(),
    };
    let release = match latest {
        Ok(release) => release,
        Err(message) => {
            result.message = message;
            return result;
        }
    };
    let newer = tool
        .version
        .as_deref()
        .and_then(|version| versions::is_newer(tool.id, &release.version, version));
    let supported = tool.id != ToolId::Pi || supported_pi_target(&release.version);
    let requirement = release.node_requirement.clone();
    result.official_available = official_eligible(tool, owned, &release);
    result.release = Some(release);
    result.message = match newer {
        Some(false) => "工作台检测到的版本已达到上游版本。".into(),
        None if tool.detected => "无法比较当前版本，可以查看官方发行。".into(),
        None => "本机尚未检测到该工具。".into(),
        Some(true) => match eligible(tool, system, owned) {
            Err(message) => message.into(),
            Ok(_) if !supported => "此发行版需要新版工作台支持，可查看官方发行。".into(),
            Ok(installation) if tool.id == ToolId::Pi => {
                match pi_node(session, requirement.as_deref()) {
                    Ok(node) if node.plan != "unavailable" => {
                        let external = matches!(installation.kind, InstallationKind::ExternalPi);
                        result.node_plan = Some(node.plan);
                        result.prerequisites = node.prerequisites;
                        result.installation_id = Some(installation.plan_id(tool.id));
                        result.updateable = true;
                        if external {
                            result.prerequisites.push(
                                "新版 Pi 将安装到工作台管理目录，保留现有 Pi 和登录配置。".into(),
                            );
                            "可为工作台启用新版 Pi。".into()
                        } else {
                            "可更新受管 Pi。".into()
                        }
                    }
                    Ok(node) => {
                        result.prerequisites = node.prerequisites;
                        "更新前需要可用的 Node.js 和 npm。".into()
                    }
                    Err(message) => message,
                }
            }
            Ok(installation) => {
                result.installation_id = Some(installation.plan_id(tool.id));
                result.updateable = true;
                "所用安装渠道可能稍晚提供上游版本。".into()
            }
        },
    };
    result
}

fn supported_pi_target(target: &str) -> bool {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        semver::Version::parse(target)
            .is_ok_and(|version| yss::workbench::managed_pi::supports_update_version(&version))
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = target;
        false
    }
}

#[tauri::command]
pub async fn desktop_check_tool_updates(view: tauri::Webview) -> Result<Vec<ToolUpdate>, String> {
    settings(&view)?;
    let app = view.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let _operation = INSTALL.try_lock().map_err(|_| "已有工具操作正在进行")?;
        let (session, owned) = connection(&app)?;
        let system = status(&session)?;
        std::thread::scope(|scope| {
            let requests: Vec<_> = system
                .tools
                .iter()
                .map(|tool| (tool, scope.spawn(move || versions::latest(tool.id))))
                .collect();
            Ok(requests
                .into_iter()
                .map(|(tool, request)| {
                    let latest = request
                        .join()
                        .unwrap_or_else(|_| Err("版本检查失败，请按需重试".into()));
                    describe(&session, &system, owned, tool, latest)
                })
                .collect())
        })
    })
    .await
    .map_err(|_| "版本检查任务失败")?
}

#[tauri::command]
pub async fn desktop_open_tool_release(
    view: tauri::Webview,
    tool_id: ToolId,
    version: String,
    download: bool,
) -> Result<(), String> {
    settings(&view)?;
    tauri::async_runtime::spawn_blocking(move || {
        let release = versions::latest(tool_id)?;
        if release.version != version {
            return Err("上游版本已变化，请重新检查工具更新。".into());
        }
        let url = if download {
            release.asset_url.ok_or("这个平台暂无官方安装程序。")?
        } else {
            release.release_url
        };
        webbrowser::open(&url).map_err(|_| "无法打开官方链接".into())
    })
    .await
    .map_err(|_| "无法打开官方链接")?
}

struct Maintenance;
impl Drop for Maintenance {
    fn drop(&mut self) {
        UPDATE_IN_PROGRESS.store(false, Ordering::Release);
    }
}

fn stop_for_update(app: &tauri::AppHandle, session: &Session) -> Result<Maintenance, String> {
    let state = app.state::<Mutex<State>>();
    let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
    if state.update_checking || crate::EXITING.load(Ordering::Acquire) {
        return Err("桌面应用正在更新或退出，请稍后重试。".into());
    }
    let conn = state.connection.as_ref().ok_or("工作台连接已中断")?;
    if conn.child.is_none()
        || conn.session.token != session.token
        || conn.session.origin != session.origin
    {
        return Err("工作台连接已变化，请重新检查版本。".into());
    }
    let freeze = freeze_and_flush(app, conn)?;
    retire_connection(app, &mut state)?;
    UPDATE_IN_PROGRESS.store(true, Ordering::Release);
    state.message = "本机工具更新中，工作台服务已协调停止。".into();
    drop(freeze);
    Ok(Maintenance)
}

fn capture(
    command: Command,
    timeout: Duration,
) -> yss::error::Result<(std::process::ExitStatus, Vec<u8>, Vec<u8>)> {
    yss::workbench::process::capture_command(command, &yss::cancel::Cancellation::new(), timeout)
}

#[cfg(target_os = "macos")]
fn brew_upgrade(tool: &ToolStatus, target: &str, path: &Path) -> Result<Command, String> {
    let formula = format!("homebrew/core/{}", tool.id.name());
    let mut check = Command::new(path);
    check
        .args(["info", "--json=v2", "--formula", &formula])
        .env("HOMEBREW_NO_AUTO_UPDATE", "1");
    let (exit, output, _) =
        capture(check, Duration::from_secs(25)).map_err(|_| "无法核对 Homebrew 配方版本")?;
    let value: Value = serde_json::from_slice(&output).map_err(|_| "无法读取 Homebrew 配方版本")?;
    let entries = value["formulae"]
        .as_array()
        .ok_or("无法读取 Homebrew 配方版本")?;
    if !exit.success()
        || entries.len() != 1
        || entries[0]["tap"] != "homebrew/core"
        || entries[0]["name"] != tool.id.name()
    {
        return Err("无法确认 Homebrew 官方配方，请核对安装渠道。".into());
    }
    let stable = entries[0]["versions"]["stable"]
        .as_str()
        .ok_or("Homebrew 暂未报告稳定版")?;
    if stable.trim_start_matches('v') != target {
        return Err(format!(
            "Homebrew 当前提供 {stable}，上游稳定版为 {target}。{}",
            if official_tool(tool.id).is_some() {
                "可尝试使用官方发行包，或刷新 Homebrew 渠道后重试。"
            } else {
                "请刷新 Homebrew 渠道后重试。"
            }
        ));
    }
    let mut command = Command::new(path);
    command
        .args(["upgrade", "--formula", &formula])
        .env("HOMEBREW_NO_AUTO_UPDATE", "1")
        .env("HOMEBREW_NO_INSTALL_CLEANUP", "1")
        .env("HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK", "1")
        .env("HOMEBREW_NO_ANALYTICS", "1")
        .env("CI", "1");
    Ok(command)
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
fn winget_upgrade(
    tool: &ToolStatus,
    installation: &Installation,
    target: &str,
    path: &Path,
) -> Result<Command, String> {
    use std::os::windows::process::CommandExt;
    let id = tool.id.winget_id().ok_or("此工具不使用 WinGet 更新")?;
    let InstallationKind::WinGet { scope, installer } = installation.kind else {
        return Err("工作台当前使用的工具位置已变化，请重新检查".into());
    };
    let mut command = Command::new(path);
    command
        .args([
            "upgrade",
            "--id",
            id,
            "--exact",
            "--source",
            "winget",
            "--architecture",
            "x64",
            "--scope",
            scope,
            "--installer-type",
            installer,
            "--disable-interactivity",
        ])
        .creation_flags(0x08000000);
    // The WinGet Git manifest omits the initial .windows.1 suffix and uses a
    // fourth numeric segment for subsequent Windows rebuilds.
    let manifest_version = if tool.id == ToolId::Git {
        let (base, revision) = target
            .split_once(".windows.")
            .ok_or("Git for Windows 发行版本无效")?;
        if revision == "1" {
            base.to_owned()
        } else {
            format!("{base}.{revision}")
        }
    } else {
        target.to_owned()
    };
    command.args(["--version", &manifest_version]);
    Ok(command)
}

enum Action {
    Package(Command),
    Official {
        tool: managed_tools::Tool,
        version: semver::Version,
        executable: Vec<u8>,
        expected: Option<ManagedTool>,
    },
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    Pi {
        target: semver::Version,
        current: semver::Version,
        managed: bool,
        node: PiNode,
    },
}

fn prepare_official(
    tool_id: ToolId,
    release: &versions::Release,
) -> Result<(Action, String), String> {
    let tool = official_tool(tool_id).ok_or("该工具当前没有可用的官方发行包安装方式。")?;
    let archive = release
        .official_archive
        .as_ref()
        .ok_or("该平台暂无可校验的官方发行包。")?;
    let anchor = managed_tools::directory().ok_or("无法定位工作台的工具目录。")?;
    let version = semver::Version::parse(&release.version).map_err(|_| "官方版本无效。")?;
    let expected = managed_tools::inspect(tool).map_err(|error| error.to_string())?;
    if expected
        .as_ref()
        .is_some_and(|active| active.version >= version)
    {
        return Err("工作台的官方工具已达到目标版本，请重新连接后检查。".into());
    }
    let executable = archive.executable()?;
    Ok((
        Action::Official {
            tool,
            version,
            executable,
            expected,
        },
        Installation::locator_at(tool_id, &InstallationKind::Official, &anchor),
    ))
}

fn prepare(
    session: &Session,
    tool: &ToolStatus,
    installation: &Installation,
    current: &str,
    target: &str,
    requirement: Option<&str>,
    node_plan: Option<&str>,
) -> Result<Action, String> {
    if tool.id == ToolId::Pi {
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        {
            if !supported_pi_target(target) {
                return Err("此 Pi 发行版需要新版工作台支持。".into());
            }
            let node = pi_node(session, requirement)?;
            if node.plan == "unavailable" || node_plan != Some(node.plan.as_str()) {
                return Err("Node 安装计划已变化，请重新检查并确认。".into());
            }
            return Ok(Action::Pi {
                target: semver::Version::parse(target).map_err(|_| "Pi 更新版本无效")?,
                current: semver::Version::parse(current).map_err(|_| "当前 Pi 版本无效")?,
                managed: matches!(installation.kind, InstallationKind::ManagedPi),
                node,
            });
        }
        #[allow(unreachable_code)]
        return Err("此平台不支持更新受管 Pi。".into());
    }
    let (_, path, guidance) = channel();
    let path = path.ok_or(guidance)?;
    #[cfg(target_os = "macos")]
    {
        return brew_upgrade(tool, target, &path).map(Action::Package);
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        return winget_upgrade(tool, installation, target, &path).map(Action::Package);
    }
    #[allow(unreachable_code)]
    Err("此平台不支持更新本机工具。".into())
}

fn execute(id: ToolId, action: Action) -> InstallResult {
    let outcome = match action {
        Action::Official {
            tool,
            version,
            executable,
            expected,
        } => managed_tools::publish(tool, &version, &executable, expected.as_ref())
            .map(|_| "官方发行包已安装，等待工作台检测目标版本。".into())
            .map_err(|error| {
                let uncertain = error
                    .context
                    .get("renameApplied")
                    .is_some_and(|value| value == "true");
                (uncertain, error.to_string())
            }),
        Action::Package(mut command) => {
            use std::process::Stdio;
            command
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null());
            match capture(command, Duration::from_secs(900)) {
                Ok((exit, _, _)) if exit.success() => {
                    Ok("更新程序已结束，等待工作台检测目标版本。".into())
                }
                Ok((exit, _, _)) => {
                    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
                    let guidance = super::winget_failure(exit);
                    #[cfg(not(all(target_os = "windows", target_arch = "x86_64")))]
                    let guidance: Option<&str> = None;
                    Err((
                        false,
                        guidance.map(str::to_owned).unwrap_or_else(|| {
                            format!("更新程序未完成（{exit}），请核对渠道版本、网络及授权。")
                        }),
                    ))
                }
                Err(_) => Err((
                    true,
                    "无法确认更新程序结果，请核对安装程序和系统安装服务。".into(),
                )),
            }
        }
        #[cfg(any(
            all(target_os = "macos", target_arch = "aarch64"),
            all(target_os = "windows", target_arch = "x86_64")
        ))]
        Action::Pi {
            target,
            current,
            managed,
            node,
        } => super::pi_install::update(
            &target,
            if managed {
                managed_pi::UpdateOrigin::Managed(&current)
            } else {
                managed_pi::UpdateOrigin::External(&current)
            },
            node.reuse.map(|(node, npm, _)| (node, npm)),
            node.installer,
        )
        .map_err(|error| (error.uncertain, error.message)),
    };
    match outcome {
        Ok(message) => InstallResult {
            id,
            success: true,
            uncertain: false,
            requires_refresh: false,
            message,
        },
        Err((uncertain, message)) => InstallResult {
            id,
            success: false,
            uncertain,
            requires_refresh: false,
            message,
        },
    }
}

#[derive(Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum UpdateSource {
    Official,
}

#[tauri::command]
#[allow(clippy::too_many_arguments)]
pub async fn desktop_update_tool(
    view: tauri::Webview,
    tool_id: ToolId,
    version: String,
    current_version: Option<String>,
    current_path: Option<String>,
    installation_id: Option<String>,
    node_plan: Option<String>,
    source: Option<UpdateSource>,
) -> Result<InstallResult, String> {
    settings(&view)?;
    let app = view.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let _operation = INSTALL.try_lock().map_err(|_| "已有工具操作正在进行")?;
        let official = source == Some(UpdateSource::Official);
        let preflight = (|| {
            let release = versions::latest(tool_id)?;
            if release.version != version {
                return Err("上游版本已变化，请重新检查。".into());
            }
            let (session, owned) = connection(&app)?;
            let system = status(&session)?;
            let tool = checked_tool(
                &system,
                tool_id,
                current_version.as_deref(),
                current_path.as_deref(),
            )?;
            let (action, locator) = if official {
                if !official_eligible(tool, owned, &release) {
                    return Err("当前无法使用官方发行包，请重新检查工具状态和工作台连接。".into());
                }
                let prepared = prepare_official(tool_id, &release)?;
                let latest_system = status(&session)?;
                let latest_tool = checked_tool(
                    &latest_system,
                    tool_id,
                    current_version.as_deref(),
                    current_path.as_deref(),
                )?;
                if latest_tool.pending_confirmation {
                    return Err("该工具有新的待确认操作，请复检后重试。".into());
                }
                prepared
            } else {
                let current = current_version
                    .as_deref()
                    .ok_or("缺少当前工具版本，请重新检查。")?;
                if versions::is_newer(tool_id, &version, current) != Some(true) {
                    return Err("当前工具无需更新，请重新检查。".into());
                }
                let selected = eligible(tool, &system, owned)?;
                if Some(selected.plan_id(tool_id)).as_ref() != installation_id.as_ref() {
                    return Err("实际工具安装位置已变化，请重新检查并确认。".into());
                }
                let action = prepare(
                    &session,
                    tool,
                    &selected,
                    current,
                    &version,
                    release.node_requirement.as_deref(),
                    node_plan.as_deref(),
                )?;
                let locator = if matches!(selected.kind, InstallationKind::ExternalPi) {
                    let (target, _) = managed_pi_destination()?;
                    Installation::locator_at(tool_id, &InstallationKind::ManagedPi, &target)
                } else {
                    selected.locator(tool_id)
                };
                (action, locator)
            };
            let target = UpdateTarget {
                version,
                installation: Some(locator),
            };
            Ok::<_, String>((session, action, target, tool.managed))
        })();
        let rejected = |message| InstallResult {
            id: tool_id,
            success: false,
            uncertain: false,
            requires_refresh: true,
            message,
        };
        let (session, action, target, managed) = match preflight {
            Ok(ready) => ready,
            Err(error) => return Ok(rejected(error)),
        };
        let maintenance = match stop_for_update(&app, &session) {
            Ok(guard) => guard,
            Err(error) => return Ok(rejected(error)),
        };
        let mut result = if !official
            && !installation(tool_id, current_path.as_deref(), managed).is_some_and(|observed| {
                Some(observed.plan_id(tool_id)).as_ref() == installation_id.as_ref()
            }) {
            rejected("工作台停止期间工具安装位置已变化，请重新检查。".into())
        } else {
            match begin_action(tool_id, Some(&target)) {
                Ok(()) => {
                    let mut result = execute(tool_id, action);
                    let marker = if result.success {
                        mark_completed(tool_id, Some(&target))
                    } else if !result.uncertain {
                        end_install(tool_id)
                    } else {
                        Ok(())
                    };
                    if marker.is_err() {
                        result.success = false;
                        result.uncertain = true;
                        result.message.push_str(" 无法保存最终状态，请核对后复检。");
                    }
                    result
                }
                Err(error) => rejected(error),
            }
        };
        drop(maintenance);
        if let Err(error) = connect(&app, None, false) {
            result
                .message
                .push_str(&format!(" 工作台未重新连接，请重试连接后核对版本。{error}"));
        }
        Ok(result)
    })
    .await
    .map_err(|_| "更新任务结果未知，请核对后复检")?
}
