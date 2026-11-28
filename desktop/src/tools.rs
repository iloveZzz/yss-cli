//! Narrow, fixed desktop tool installation commands. The remote workbench never receives them.
use super::{State, connect, freeze_and_flush, get, retire_connection};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{
    path::PathBuf,
    process::{Command, Stdio},
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
};
use tauri::Manager;

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
#[path = "tools/pi_macos.rs"]
mod pi_install;
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[path = "tools/pi_windows.rs"]
mod pi_install;
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[path = "tools/portable_node.rs"]
mod portable_node;
#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
#[path = "tools/portable_node_macos.rs"]
mod portable_node_macos;

static INSTALL: Mutex<()> = Mutex::new(());
static UPDATE_IN_PROGRESS: AtomicBool = AtomicBool::new(false);
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
const WINGET_SOURCE_ID: &str = "Microsoft.Winget.Source_8wekyb3d8bbwe";
pub(super) mod updates;
mod versions;

pub(super) fn updating() -> bool {
    UPDATE_IN_PROGRESS.load(Ordering::Acquire)
}

#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum ToolId {
    Git,
    Gh,
    Glab,
    Pi,
}
impl ToolId {
    fn name(self) -> &'static str {
        match self {
            Self::Git => "git",
            Self::Gh => "gh",
            Self::Glab => "glab",
            Self::Pi => "pi",
        }
    }
    fn source(self) -> &'static str {
        match (std::env::consts::OS, self) {
            (_, Self::Pi) => "npm: @earendil-works/pi-coding-agent",
            ("macos", _) => "Homebrew",
            ("windows", _) => "WinGet / winget",
            _ => "不支持",
        }
    }
    #[cfg(windows)]
    fn winget_id(self) -> Option<&'static str> {
        match self {
            Self::Git => Some("Git.Git"),
            Self::Gh => Some("GitHub.cli"),
            Self::Glab => Some("GLab.GLab"),
            Self::Pi => None,
        }
    }
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolStatus {
    id: ToolId,
    installed: bool,
    detected: bool,
    version: Option<String>,
    path: Option<String>,
    message: Option<String>,
    basic_ready: Option<bool>,
    sandbox_ready: Option<bool>,
    sandbox_message: Option<String>,
    source: &'static str,
    prerequisites: Vec<String>,
    installable: bool,
    node_plan: Option<String>,
    pending_confirmation: bool,
    pending_completed: bool,
    pending_update: bool,
    managed: bool,
    #[serde(skip)]
    explicit_pi_executable: Option<bool>,
    selected_by_default: bool,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ToolsStatus {
    channel: &'static str,
    channel_available: bool,
    channel_message: String,
    integration_message: String,
    tools: Vec<ToolStatus>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InstallResult {
    id: ToolId,
    success: bool,
    uncertain: bool,
    requires_refresh: bool,
    message: String,
}

fn settings(view: &tauri::Webview) -> Result<(), String> {
    if view.label() != "settings" {
        return Err("仅桌面设置可管理本机工具".into());
    }
    Ok(())
}
fn session(app: &tauri::AppHandle) -> Result<super::Session, String> {
    let state = app.state::<Mutex<State>>();
    let state = state.lock().map_err(|_| "桌面状态不可用")?;
    Ok(state
        .connection
        .as_ref()
        .ok_or("请先连接工作台，再检测本机工具")?
        .session
        .clone())
}
fn marker_name(id: ToolId) -> Result<yss::model::RelPath, String> {
    yss::model::RelPath::new(format!("tool-install-{}.pending", id.name()))
        .map_err(|_| "工具标记无效".into())
}
fn marker_root(create: bool) -> Result<Option<yss::fs::Root>, String> {
    let config = super::config_file()?;
    let parent = config.parent().ok_or("桌面设置目录无效")?;
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    if create {
        return pi_install::desktop_directory(parent).map(Some);
    }
    match std::fs::symlink_metadata(parent) {
        Ok(_) => yss::fs::Root::open(parent)
            .map(Some)
            .map_err(|_| "桌面设置目录不安全".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound && !create => Ok(None),
        Err(_) => Err("桌面设置目录不可用".into()),
    }
}
const MAX_MARKER_BYTES: u64 = 128;

#[derive(PartialEq, Eq)]
struct UpdateTarget {
    version: String,
    installation: Option<String>,
}

impl UpdateTarget {
    fn record(&self, kind: &str) -> String {
        match &self.installation {
            Some(id) => format!("{kind}\n{}\n{id}\n", self.version),
            None => format!("{kind}\n{}\n", self.version),
        }
    }

    fn confirmed(
        &self,
        id: ToolId,
        version: Option<&str>,
        path: Option<&str>,
        managed: bool,
    ) -> bool {
        let Some(expected) = self.installation.as_deref() else {
            return false;
        };
        version.and_then(|actual| versions::is_newer(id, &self.version, actual)) == Some(false)
            && updates::installation_locator(id, path, managed).as_deref() == Some(expected)
    }
}

#[derive(PartialEq, Eq)]
enum PendingState {
    Running,
    Completed,
    Updating(UpdateTarget),
    Updated(UpdateTarget),
}
impl PendingState {
    fn parse(bytes: &[u8]) -> Option<Self> {
        match std::str::from_utf8(bytes).ok()? {
            "pending\n" => Some(Self::Running),
            "completed\n" => Some(Self::Completed),
            text => {
                let (kind, fields) = text.strip_suffix('\n')?.split_once('\n')?;
                let (version, installation) = match fields.split_once('\n') {
                    Some((version, id))
                        if id.len() == 64
                            && id.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f')) =>
                    {
                        (version, Some(id.to_owned()))
                    }
                    Some(_) => return None,
                    None => (fields, None),
                };
                if version.is_empty()
                    || version.len() > 48
                    || !version
                        .bytes()
                        .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'.' | b'-' | b'+'))
                {
                    return None;
                }
                let target = UpdateTarget {
                    version: version.to_owned(),
                    installation,
                };
                match kind {
                    "updating" => Some(Self::Updating(target)),
                    "updated" => Some(Self::Updated(target)),
                    _ => None,
                }
            }
        }
    }
}
fn pending_state(id: ToolId) -> Result<Option<PendingState>, String> {
    let Some(root) = marker_root(false)? else {
        return Ok(None);
    };
    let name = marker_name(id)?;
    if root.inspect(&name).map_err(|_| "无法检查安装状态")? == yss::fs::FileSnapshot::Missing
    {
        return Ok(None);
    }
    let file = root
        .read_private_checked(&name, MAX_MARKER_BYTES)
        .map_err(|_| "安装状态不安全，请手动核对")?;
    PendingState::parse(&file.bytes)
        .map(Some)
        .ok_or_else(|| "工具操作状态无效，请手动核对".into())
}
fn pending(id: ToolId) -> Result<bool, String> {
    Ok(pending_state(id)?.is_some())
}
fn begin_action(id: ToolId, target: Option<&UpdateTarget>) -> Result<(), String> {
    let root = marker_root(true)?.ok_or("无法创建安装状态")?;
    let record = target.map(|target| target.record("updating"));
    if record
        .as_ref()
        .is_some_and(|record| PendingState::parse(record.as_bytes()).is_none())
    {
        return Err("工具目标版本无效，未执行更新。".into());
    }
    root.create_private_file(
        &marker_name(id)?,
        record.as_deref().unwrap_or("pending\n").as_bytes(),
    )
    .map_err(|_| "该工具有待确认的安装，或无法安全保存安装状态")?;
    Ok(())
}
fn mark_completed(id: ToolId, target: Option<&UpdateTarget>) -> Result<(), String> {
    let root = marker_root(false)?.ok_or("无法核对安装状态")?;
    let name = marker_name(id)?;
    let old = root
        .read_private_checked(&name, MAX_MARKER_BYTES)
        .map_err(|_| "安装状态不安全")?;
    let (expected, completed) = match target {
        Some(target) => (target.record("updating"), target.record("updated")),
        None => ("pending\n".into(), "completed\n".into()),
    };
    if old.bytes != expected.as_bytes() {
        return Err("安装状态无效".into());
    }
    let temp = yss::model::RelPath::new(format!("tool-install-{}.tmp", super::random_id()?))
        .map_err(|_| "安装状态路径无效")?;
    let written = root
        .create_private_file(&temp, completed.as_bytes())
        .map_err(|_| "无法记录安装结果")?;
    if root.rename(&temp, &name, &old.snapshot, true).is_err() {
        let _ = root.remove(&temp, &written);
        return Err("安装结果待确认".into());
    }
    Ok(())
}
fn end_install(id: ToolId) -> Result<(), String> {
    let root = marker_root(false)?.ok_or("无法核对安装状态")?;
    let name = marker_name(id)?;
    let file = root
        .read_private_checked(&name, MAX_MARKER_BYTES)
        .map_err(|_| "安装状态不安全，请手动核对")?;
    if PendingState::parse(&file.bytes).is_none() {
        return Err("安装状态无效，请手动核对".into());
    }
    root.remove(&name, &file.snapshot)
        .map_err(|_| "无法确认安装状态；请手动核对".into())
}
fn protected_paths(trackers: Option<&Value>) -> Option<Vec<PathBuf>> {
    let value = trackers?;
    if value.get("protectedComplete").and_then(Value::as_bool) != Some(true) {
        return None;
    }
    let paths = value.get("protectedPaths")?.as_array()?;
    paths
        .iter()
        .map(|p| p.as_str().map(PathBuf::from).filter(|p| p.is_absolute()))
        .collect()
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
fn winget_failure(status: std::process::ExitStatus) -> Option<&'static str> {
    // WinGet returns HRESULT values through the signed platform exit code.
    match status.code()? as u32 {
        0x8A150005 | 0x8A15010C | 0x800704C7 => Some("WinGet 操作已取消；需要时可重试。"),
        0x8A150041 | 0x8A150042 | 0x8A150046 => {
            Some("WinGet 需要用户同意协议。请在终端查看官方源或软件包协议，再按需手动确认。")
        }
        0x8A15003A | 0x8A15006C | 0x8A15010F => {
            Some("WinGet 或软件包受组织策略限制；请联系系统管理员。")
        }
        0x8A150008 | 0x8A150086 | 0x8A150107 | 0x80072EE2 | 0x80072EE7 | 0x80072EFD => {
            Some("WinGet 下载未完成；请检查网络及代理后重试。")
        }
        0x8A150010 | 0x8A150014 | 0x8A150113 => {
            Some("固定 WinGet 源中没有适用的软件包；请核对系统架构和应用安装程序版本。")
        }
        0x8A15000F | 0x8A150012 | 0x8A150015 => {
            Some("WinGet 内置社区源不可用；请检查 WinGet 源配置或联系系统管理员。")
        }
        0x8A15002B | 0x8A15010D | 0x8A15010E => {
            Some("WinGet 已发现现有安装；请检查 PATH，刷新工作台后重新检测。")
        }
        0x8A150002 | 0x8A150007 => {
            Some("当前应用安装程序不支持这项 WinGet 操作；请更新应用安装程序后重试。")
        }
        _ => None,
    }
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
fn is_builtin_winget_source(output: &[u8]) -> bool {
    let Ok(text) = std::str::from_utf8(output) else {
        return false;
    };
    let Ok(source) = serde_json::from_str::<Value>(text.trim().trim_start_matches('\u{feff}'))
    else {
        return false;
    };
    if source["Name"] != "winget"
        || source["Arg"] != "https://cdn.winget.microsoft.com/cache"
        || source["Type"] != "Microsoft.PreIndexed.Package"
        || source["Identifier"] != WINGET_SOURCE_ID
    {
        return false;
    }
    let Some(trust) = source["TrustLevel"].as_array() else {
        return false;
    };
    if trust.len() != 2
        || !trust.iter().any(|item| item == "Trusted")
        || !trust.iter().any(|item| item == "StoreOrigin")
    {
        return false;
    }
    let Some(data) = source["Data"].as_str() else {
        return false;
    };
    // Newer WinGet releases also carry the signed delta and baseline identities.
    data == WINGET_SOURCE_ID
        || serde_json::from_str::<Value>(data).is_ok_and(|value| {
            value
                == serde_json::json!({
                    "fullIdentity": WINGET_SOURCE_ID,
                    "deltaIdentity": "Microsoft.Winget.Source.Delta_8wekyb3d8bbwe",
                    "baselineIdentity": "Microsoft.Winget.Source.Baseline_8wekyb3d8bbwe",
                })
        })
}

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
fn verify_winget_source(executable: &std::path::Path) -> Result<(), &'static str> {
    use std::{os::windows::process::CommandExt, time::Duration};
    const GUIDANCE: &str =
        "无法确认 WinGet 内置社区源；请检查源配置和应用安装程序版本，或联系系统管理员。";
    let mut command = Command::new(executable);
    command
        .args([
            "source",
            "export",
            "--name",
            "winget",
            "--disable-interactivity",
        ])
        .creation_flags(0x08000000);
    let (status, output, _) = yss::workbench::process::capture_command(
        command,
        &yss::cancel::Cancellation::new(),
        Duration::from_secs(10),
    )
    .map_err(|_| GUIDANCE)?;
    if !status.success() {
        return Err(winget_failure(status).unwrap_or(GUIDANCE));
    }
    if !is_builtin_winget_source(&output) {
        return Err(GUIDANCE);
    }
    Ok(())
}

fn channel() -> (&'static str, Option<PathBuf>, &'static str) {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        let path = PathBuf::from("/opt/homebrew/bin/brew");
        if path.is_file() {
            return (
                "Homebrew",
                Some(path),
                "使用已有的 Apple Silicon Homebrew；安装可能增加或升级依赖，请核对计划后继续。",
            );
        }
        return (
            "Homebrew",
            None,
            "未发现 /opt/homebrew/bin/brew；请先按 Homebrew 官方说明安装。",
        );
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        let path = directories::BaseDirs::new().map(|dirs| {
            dirs.data_local_dir()
                .join("Microsoft")
                .join("WindowsApps")
                .join("winget.exe")
        });
        let Some(path) = path.filter(|path| path.is_file()) else {
            return (
                "WinGet",
                None,
                "未发现 WinGet；请检查应用安装程序及组织策略。",
            );
        };
        return match verify_winget_source(&path) {
            Ok(()) => (
                "WinGet",
                Some(path),
                "使用已有的 WinGet 和 winget 固定源。安装后如工作台仍未发现工具，请刷新本机服务或重启桌面应用；外部服务需由用户自行刷新。",
            ),
            Err(guidance) => ("WinGet", None, guidance),
        };
    }
    #[allow(unreachable_code)]
    (
        "不支持",
        None,
        "本机工具安装仅支持 macOS ARM64 与 Windows x64。",
    )
}
#[cfg(not(windows))]
fn protected_path(path: &std::path::Path, protected: &[PathBuf]) -> bool {
    protected.iter().any(|root| path.starts_with(root))
}
#[cfg(windows)]
fn protected_path(path: &std::path::Path, protected: &[PathBuf]) -> bool {
    let Ok(path) = std::fs::canonicalize(path) else {
        return true;
    };
    let directory = if path.is_dir() {
        path.as_path()
    } else if let Some(parent) = path.parent() {
        parent
    } else {
        return true;
    };
    let mut ancestors = Vec::new();
    for ancestor in directory.ancestors() {
        let Ok(root) = yss::fs::Root::open(ancestor) else {
            return true;
        };
        ancestors.push(root.identity);
    }
    for path in protected {
        let Ok(root) = yss::fs::Root::open(path) else {
            return true;
        };
        if ancestors.contains(&root.identity) {
            return true;
        }
    }
    false
}

// A Homebrew keg may be present without its opt or bin symlink. Reuse its
// directly installed Node without changing links or the user's default Node.
#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
fn homebrew_node_candidates(cellar: &std::path::Path) -> Vec<PathBuf> {
    let mut candidates = Vec::new();
    for formula in ["node", "node@24", "node@22"] {
        let Ok(entries) = std::fs::read_dir(cellar.join(formula)) else {
            continue;
        };
        let mut versions = Vec::new();
        for (index, entry) in entries.enumerate() {
            if index >= 256 {
                versions.clear();
                break;
            }
            let Ok(entry) = entry else {
                versions.clear();
                break;
            };
            if entry.file_type().is_ok_and(|kind| kind.is_dir()) {
                versions.push(entry.path());
            }
        }
        // Newer keg names are considered first; the actual Node version is
        // always verified before a candidate is accepted.
        versions.sort();
        versions.reverse();
        candidates.extend(versions.into_iter().map(|path| path.join("bin/node")));
    }
    candidates
}

fn compatible_node(
    protected: &[PathBuf],
    preferred: Option<PathBuf>,
    accepts: impl Fn(&semver::Version) -> bool,
) -> Option<(PathBuf, PathBuf, semver::Version)> {
    let path = std::env::var_os("PATH").unwrap_or_default();
    let inherited = std::env::split_paths(&path)
        .filter(|p| p.is_absolute())
        .map(|p| p.join(if cfg!(windows) { "node.exe" } else { "node" }));
    let candidates = preferred
        .into_iter()
        .filter(|p| p.is_absolute())
        .chain(inherited)
        .collect::<Vec<_>>();
    #[cfg(target_os = "macos")]
    let candidates = {
        let mut candidates = candidates;
        candidates.extend([
            PathBuf::from("/opt/homebrew/bin/node"),
            PathBuf::from("/opt/homebrew/opt/node@22/bin/node"),
            PathBuf::from("/usr/local/bin/node"),
        ]);
        #[cfg(target_arch = "aarch64")]
        candidates.extend(homebrew_node_candidates(std::path::Path::new(
            "/opt/homebrew/Cellar",
        )));
        candidates
    };
    for node in candidates {
        if !node.is_file() {
            continue;
        }
        let Ok(node) = std::fs::canonicalize(node) else {
            continue;
        };
        if protected_path(&node, protected) {
            continue;
        }
        let Some(dir) = node.parent() else { continue };
        let npm = [
            dir.join("node_modules/npm/bin/npm-cli.js"),
            dir.join("../lib/node_modules/npm/bin/npm-cli.js"),
        ]
        .into_iter()
        .filter_map(|p| std::fs::canonicalize(p).ok())
        .find(|p| p.is_file() && !protected_path(p, protected));
        let Some(npm) = npm else { continue };
        let mut command = Command::new(&node);
        command
            .arg("--version")
            .env_remove("NODE_OPTIONS")
            .env_remove("NODE_PATH")
            .env_remove("NODE_EXTRA_CA_CERTS")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000);
        }
        let Ok((exit, bytes, _)) = yss::workbench::process::capture_command(
            command,
            &yss::cancel::Cancellation::new(),
            std::time::Duration::from_secs(5),
        ) else {
            continue;
        };
        if !exit.success() {
            continue;
        }
        let Ok(version) = semver::Version::parse(
            String::from_utf8_lossy(&bytes)
                .trim()
                .trim_start_matches('v'),
        ) else {
            continue;
        };
        if version < semver::Version::new(22, 19, 0) || !accepts(&version) {
            continue;
        }
        return Some((node, npm, version));
    }
    None
}
fn node_download(
    path: Option<&std::path::Path>,
    guidance: &str,
) -> Result<(String, String), String> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        portable_node_macos::check_channel(path.ok_or(guidance)?)?;
        let version = portable_node_macos::VERSION;
        Ok((
            format!("download:node-{version}-darwin-arm64"),
            format!(
                "使用 Homebrew 官方 node-build 版本定义下载 Node.js {version} ARM64，只安装到 Pi 受管目录，不更改默认 Node 或 PATH"
            ),
        ))
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        path.ok_or(guidance)?;
        Ok((
            "download:node-24.22.1-win-x64".into(),
            "将使用 WinGet 固定源下载 Node.js 24.22.1 x64 ZIP，校验摘要后仅安装到 Pi 受管目录，不更改默认 Node 或 PATH".into(),
        ))
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (path, guidance);
        Err("此系统不支持自动安装受管 Node；请准备兼容 Node 和 npm 后重试".into())
    }
}

fn node_options(
    node: Option<&(PathBuf, PathBuf, semver::Version)>,
    protected_complete: bool,
    path: Option<&std::path::Path>,
    guidance: &str,
) -> (String, Vec<String>) {
    if !protected_complete {
        return (
            "unavailable".into(),
            vec!["无法完整核验工程及工作台保护目录，暂不能使用 Node".into()],
        );
    }
    if let Some((node, npm, version)) = node {
        let mut hash = Sha256::new();
        hash.update(node.to_string_lossy().as_bytes());
        hash.update([0]);
        hash.update(npm.to_string_lossy().as_bytes());
        hash.update([0]);
        hash.update(version.to_string().as_bytes());
        return (
            format!("reuse:{:x}", hash.finalize()),
            vec![format!(
                "可复用 Node.js {version}（{}）和 npm",
                node.display()
            )],
        );
    }
    let (plan, description) =
        node_download(path, guidance).unwrap_or_else(|message| ("unavailable".into(), message));
    (
        plan,
        vec![
            "需要 Node.js >= 22.19.0 和 npm；尚未发现可复用版本".into(),
            description,
        ],
    )
}
fn status(session: &super::Session) -> Result<ToolsStatus, String> {
    let system = get(session, "/api/system")?;
    let external = get(session, "/api/external")?;
    let links = external.as_array().ok_or("无法读取工作台集成设置")?;
    let trackers = get(session, "/api/desktop/tool-integrations").ok();
    let protected = protected_paths(trackers.as_ref());
    let node = protected
        .as_ref()
        .and_then(|paths| compatible_node(paths, None, |_| true));
    let (name, path, explanation) = channel();
    let (pi_node_plan, pi_prerequisites) = node_options(
        node.as_ref(),
        protected.is_some(),
        path.as_deref(),
        explanation,
    );
    let complete = trackers
        .as_ref()
        .and_then(|v| v.get("complete"))
        .and_then(Value::as_bool)
        == Some(true);
    let linked = |name: &str| {
        links
            .iter()
            .any(|entry| entry.get("provider").and_then(Value::as_str) == Some(name))
            || trackers
                .as_ref()
                .and_then(|v| v.get("providers"))
                .and_then(Value::as_array)
                .is_some_and(|providers| providers.iter().any(|v| v.as_str() == Some(name)))
    };
    let reported = system
        .get("tools")
        .and_then(Value::as_array)
        .ok_or("工作台未返回工具探测结果")?;
    let mut tools = Vec::new();
    for id in [ToolId::Git, ToolId::Gh, ToolId::Glab, ToolId::Pi] {
        let is_pi = matches!(id, ToolId::Pi);
        let value = if is_pi {
            system.get("pi")
        } else {
            reported
                .iter()
                .find(|v| v.get("name").and_then(Value::as_str) == Some(id.name()))
        }
        .ok_or("工作台未返回完整的工具探测结果")?;
        let version = value.get("version").and_then(Value::as_str);
        let executable = value
            .get(if is_pi { "executable" } else { "path" })
            .and_then(Value::as_str);
        let managed = is_pi && value.get("managed").and_then(Value::as_bool) == Some(true);
        let installed = if matches!(id, ToolId::Pi) {
            executable.is_some()
                && version.is_some_and(|version| {
                    managed
                        || semver::Version::parse(version).is_ok_and(|version| {
                            yss::workbench::managed_pi::supports_external_version(&version)
                        })
                })
        } else {
            value
                .get("available")
                .and_then(Value::as_bool)
                .ok_or("工作台工具探测结果无效")?
        };
        let detected = installed || executable.is_some();
        let definite_missing = if matches!(id, ToolId::Pi) {
            value
                .get("message")
                .and_then(Value::as_str)
                .is_some_and(|s| s.starts_with("未找到本机 Pi"))
        } else {
            !detected
        };
        let state = pending_state(id)?;
        let pending_confirmation = state.is_some();
        let pending_completed = match &state {
            Some(PendingState::Completed) => true,
            Some(PendingState::Updated(target)) => {
                installed && target.confirmed(id, version, executable, managed)
            }
            _ => false,
        };
        tools.push(ToolStatus {
            id,
            installed,
            detected,
            version: version.map(str::to_owned),
            path: executable.map(str::to_owned),
            message: value
                .get("message")
                .and_then(Value::as_str)
                .map(str::to_owned),
            basic_ready: is_pi.then(|| value["available"].as_bool()).flatten(),
            sandbox_ready: is_pi.then(|| value["sandboxReady"].as_bool()).flatten(),
            sandbox_message: is_pi
                .then(|| value["sandboxMessage"].as_str().map(str::to_owned))
                .flatten(),
            source: if updates::is_official_tool(id, executable) {
                "官方发行包（工作台管理）"
            } else {
                id.source()
            },
            prerequisites: if matches!(id, ToolId::Pi) {
                pi_prerequisites.clone()
            } else {
                Vec::new()
            },
            installable: !detected
                && definite_missing
                && !pending_confirmation
                && if matches!(id, ToolId::Pi) {
                    cfg!(any(
                        all(target_os = "macos", target_arch = "aarch64"),
                        all(target_os = "windows", target_arch = "x86_64")
                    )) && pi_node_plan != "unavailable"
                } else {
                    path.is_some()
                },
            node_plan: is_pi.then(|| pi_node_plan.clone()),
            pending_confirmation,
            pending_completed,
            pending_update: matches!(
                state,
                Some(PendingState::Updating(_) | PendingState::Updated(_))
            ),
            managed,
            explicit_pi_executable: value.get("explicitExecutable").and_then(Value::as_bool),
            selected_by_default: !detected
                && !pending_confirmation
                && match id {
                    ToolId::Git => true,
                    ToolId::Gh => linked("github"),
                    ToolId::Glab => linked("gitlab"),
                    ToolId::Pi => false,
                },
        });
    }
    Ok(ToolsStatus {
        channel: name,
        channel_available: path.is_some(),
        channel_message: explanation.into(),
        integration_message: if complete {
            "默认选择依据已关联的研发记录和可读取的启用工程配置。"
        } else {
            "部分工程配置不可读取或服务版本不支持完整检测，请自行核对默认选择。"
        }
        .into(),
        tools,
    })
}
#[tauri::command]
pub async fn desktop_tools_status(view: tauri::Webview) -> Result<ToolsStatus, String> {
    settings(&view)?;
    let app = view.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || status(&session(&app)?))
        .await
        .map_err(|_| "无法检测本机工具")?
}
#[tauri::command]
pub async fn desktop_install_tool(
    view: tauri::Webview,
    tool_id: ToolId,
    node_plan: Option<String>,
) -> Result<InstallResult, String> {
    settings(&view)?;
    let app = view.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let _install = INSTALL
            .try_lock()
            .map_err(|_| "已有工具安装正在进行，请等待结束")?;
        let current = status(&session(&app)?)?;
        let item = current
            .tools
            .iter()
            .find(|v| v.id.name() == tool_id.name()).ok_or("无法核对工具状态")?;
        if item.pending_confirmation { return Err("该工具安装结果待确认；请先核对安装程序和系统安装服务均已结束，再确认并复检".into()); }
        if item.detected {
            return Ok(InstallResult {
                id: tool_id,
                success: item.installed,
                uncertain: false, requires_refresh: false,
                message: if item.installed { "工作台已探测到该工具，已复用现有安装。" } else { "已发现现有工具但版本探测失败；请先检查现有安装，未执行安装。" }.into(),
            });
        }
        if matches!(tool_id, ToolId::Pi)
            && (node_plan.as_deref() != item.node_plan.as_deref() || node_plan.is_none()) {
            return Ok(InstallResult { id: tool_id, success: false, uncertain: false, requires_refresh: true, message: "Node 安装计划已变化，尚未开始安装；请重新检测并核对后重试".into() });
        }
        if !item.installable { return Ok(InstallResult { id: tool_id, success: false, uncertain: false, requires_refresh: true, message: "当前无法确认工具缺失或安装前置条件不满足；尚未开始安装，请检查诊断后重试".into() }); }
        if matches!(tool_id, ToolId::Pi) {
            #[cfg(any(all(target_os = "macos", target_arch = "aarch64"), all(target_os = "windows", target_arch = "x86_64")))]
            {
                let session = session(&app)?;
                let trackers = get(&session, "/api/desktop/tool-integrations")?;
                let paths = protected_paths(Some(&trackers)).ok_or("无法核验受保护路径")?;
                let node = compatible_node(&paths, None, |_| true);
                let (_, installer, guidance) = if node.is_none() { channel() } else { ("", None, "") };
                let (current_plan, options) = node_options(node.as_ref(), true, installer.as_deref(), guidance);
                if current_plan == "unavailable" || node_plan.as_deref() != Some(current_plan.as_str()) {
                    let message = if current_plan == "unavailable" {
                        options.last().cloned().unwrap_or_else(|| "未能确认 Node 安装前置条件".into())
                    } else {
                        "Node 安装计划已变化，尚未开始安装；请重新检测并核对后重试".into()
                    };
                    return Ok(InstallResult { id: tool_id, success: false, uncertain: false, requires_refresh: true, message });
                }
                let existing = node.map(|(node, npm, _)| (node, npm));
                begin_action(tool_id, None)?;
                return Ok(match pi_install::install(existing, installer) {
                    Ok(message) => match mark_completed(tool_id, None) {
                        Ok(()) => InstallResult { id: tool_id, success: true, uncertain: false, requires_refresh: false, message },
                        Err(_) => InstallResult { id: tool_id, success: false, uncertain: true, requires_refresh: false, message: "Pi 安装状态待确认，请核对后复检。".into() },
                    },
                    Err(error) => {
                        let mut result = InstallResult {
                            id: tool_id,
                            success: false,
                            uncertain: error.uncertain,
                            requires_refresh: false,
                            message: error.message,
                        };
                        if !result.uncertain && end_install(tool_id).is_err() {
                            result.uncertain = true;
                            result.message.push_str(" 无法保存最终状态，请核对后复检。");
                        }
                        result
                    }
                });
            }
            #[cfg(not(any(all(target_os = "macos", target_arch = "aarch64"), all(target_os = "windows", target_arch = "x86_64"))))]
            return Ok(InstallResult {
                id: tool_id,
                success: false,
                uncertain: false, requires_refresh: false,
                message: "未发现安全可用的 Node 与 npm，或此平台的受管 Pi 安装尚不可用；原有工具和配置已保留。"
                    .into(),
            });
        }
        let (_, executable, message) = channel();
        let Some(executable) = executable else {
            return Ok(InstallResult {
                id: tool_id,
                success: false,
                uncertain: false, requires_refresh: false,
                message: message.into(),
            });
        };
        #[cfg(target_os = "macos")]
        {
            let mut probe = Command::new(&executable);
            probe.args(["list", "--formula", "--versions"]).env("HOMEBREW_NO_AUTO_UPDATE", "1");
            let (exit, stdout, _) = yss::workbench::process::capture_command(probe, &yss::cancel::Cancellation::new(), std::time::Duration::from_secs(20)).map_err(|_| "无法核对 Homebrew 已有安装；未执行安装")?;
            if !exit.success() { return Err("无法核对 Homebrew 已有安装；未执行安装".into()); }
            if String::from_utf8_lossy(&stdout).lines().any(|line| line.split_whitespace().next().and_then(|name| name.rsplit('/').next()) == Some(tool_id.name())) {
                return Err("Homebrew 已有该工具，但工作台尚未探测到；原有安装已保留，请检查 PATH 或链接".into());
            }
        }
        let mut command = Command::new(executable);
        #[cfg(target_os = "macos")]
        {
            command
                .args(["install", "--formula", &format!("homebrew/core/{}", tool_id.name())])
                .env("HOMEBREW_NO_AUTO_UPDATE", "1")
                .env("HOMEBREW_NO_INSTALL_CLEANUP", "1")
                .env("HOMEBREW_NO_INSTALL_UPGRADE", "1")
                .env("HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK", "1")
                .env("HOMEBREW_NO_ANALYTICS", "1")
                .env("CI", "1");
        }
        #[cfg(target_os = "windows")]
        {
            use std::os::windows::process::CommandExt;
            let id = tool_id.winget_id().ok_or("本机工具不适用 WinGet")?;
            command
                .args([
                    "install",
                    "--id",
                    id,
                    "--exact",
                    "--source",
                    "winget",
                    "--dependency-source",
                    "winget",
                    "--architecture",
                    "x64",
                    "--scope",
                    "user",
                    "--no-upgrade",
                    "--disable-interactivity",
                ])
                .creation_flags(0x08000000);
        }
        command
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        begin_action(tool_id, None)?;
        let result = match yss::workbench::process::capture_command(command, &yss::cancel::Cancellation::new(), std::time::Duration::from_secs(900)) {
            Ok((result, _, _)) if result.success() => InstallResult {
                id: tool_id,
                success: true,
                uncertain: false, requires_refresh: false,
                message: "安装程序已完成；请确认结果并重新检测，工作台实际探测结果为准。".into(),
            },
            Ok((result, _, _)) => {
                #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
                let guidance = winget_failure(result);
                #[cfg(not(all(target_os = "windows", target_arch = "x86_64")))]
                let guidance: Option<&str> = None;
                InstallResult {
                    id: tool_id,
                    success: false,
                    uncertain: false, requires_refresh: false,
                    message: guidance.map(str::to_owned).unwrap_or_else(|| format!(
                        "安装程序未完成（状态 {result}）；请检查网络、渠道策略或授权后重试。"
                    )),
                }
            },
            Err(_) => InstallResult {
                id: tool_id,
                success: false,
                uncertain: true, requires_refresh: false,
                message: "安装渠道无法启动、超时或输出过多；请检查系统策略及渠道状态后重试。".into(),
            },
        };
        // Completed and unknown installs remain distinct across a desktop restart.
        // A completed install stays pending until the service detects its actual path.
        if result.success && mark_completed(tool_id, None).is_err() { return Ok(InstallResult { id: tool_id, success: false, uncertain: true, requires_refresh: false, message: "安装状态待确认；请核对安装程序及系统安装服务已结束后复检。".into() }); }
        if !result.success && !result.uncertain && end_install(tool_id).is_err() { return Ok(InstallResult { id: tool_id, success: false, uncertain: true, requires_refresh: false, message: "安装状态待确认；请核对安装程序及系统安装服务已结束后复检。".into() }); }
        Ok(result)
    })
    .await
    .map_err(|_| "工具安装任务失败")?
}
#[tauri::command]
pub async fn desktop_acknowledge_tool(view: tauri::Webview, tool_id: ToolId) -> Result<(), String> {
    settings(&view)?;
    tauri::async_runtime::spawn_blocking(move || {
        let _install = INSTALL.try_lock().map_err(|_| "工具安装尚未结束")?;
        if pending(tool_id)? {
            end_install(tool_id)
        } else {
            Ok(())
        }
    })
    .await
    .map_err(|_| "无法确认安装状态")?
}
#[tauri::command]
pub async fn desktop_refresh_tools(view: tauri::Webview) -> Result<(), String> {
    settings(&view)?;
    let app = view.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let _install = INSTALL.try_lock().map_err(|_| "工具安装尚未结束")?;
        {
            let state = app.state::<Mutex<State>>();
            let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
            let conn = state.connection.as_ref().ok_or("请先连接工作台")?;
            if conn.child.is_none() {
                return Err("当前连接的是外部服务，请由服务所有者自行刷新后重新检测。".into());
            }
            let freeze = freeze_and_flush(&app, conn)?;
            retire_connection(&app, &mut state)?;
            state.message = "原工作台已停止，正在重新连接".into();
            drop(freeze);
        }
        connect(&app, None, false)
    })
    .await
    .map_err(|_| "刷新工作台失败")?
}
