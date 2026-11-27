use super::{not_ready, rpc::Session, sandbox};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::Root,
    workbench::{
        agent_protocol::{PiModel, PiRuntime, RuntimeStatus},
        managed_pi, process,
    },
};
#[cfg(windows)]
use crate::{fs::FileSnapshot, model::RelPath};
use semver::Version;
use serde::Deserialize;
use serde_json::Value;
use std::{
    env, fs,
    io::{ErrorKind, Read},
    path::{Path, PathBuf},
    process::Command,
    sync::Mutex,
    time::Duration,
};
#[cfg(unix)]
use std::{
    ffi::{CStr, OsStr},
    mem::MaybeUninit,
    os::unix::{ffi::OsStrExt, fs::MetadataExt},
    ptr,
};

const SUPPORTED_VERSIONS: [Version; 3] = [
    Version::new(1, 5, 2),
    Version::new(1, 6, 0),
    Version::new(1, 6, 1),
];
const SETTINGS_LIMIT: u64 = 1024 * 1024;
static CONNECTION_TEST: Mutex<()> = Mutex::new(());

#[cfg(unix)]
fn locate_pi() -> Result<PathBuf> {
    let pi = if let Some(path) = env::var_os("YSS_PI_EXECUTABLE") {
        let path = PathBuf::from(path);
        if !path.is_absolute() {
            return Err(not_ready("YSS_PI_EXECUTABLE 须使用 Pi 的绝对路径"));
        }
        Some(path)
    } else {
        env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute())
            .map(|path| path.join("pi"))
            .find(|path| is_executable(path))
    };
    pi.and_then(|path| fs::canonicalize(path).ok())
        .filter(|path| is_executable(path))
        .ok_or_else(|| {
            not_ready("未找到本机 Pi，请安装 Pi 1.5.2、1.6.0 或 1.6.1，或设置 YSS_PI_EXECUTABLE")
        })
}

#[cfg(unix)]
fn is_executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|metadata| metadata.is_file() && metadata.mode() & 0o111 != 0)
}

#[cfg(windows)]
fn is_executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|metadata| metadata.is_file())
        && path
            .extension()
            .is_some_and(|extension| extension.eq_ignore_ascii_case("exe"))
}

#[cfg(windows)]
fn locate_pi() -> Result<PathBuf> {
    if let Some(path) = env::var_os("YSS_PI_EXECUTABLE") {
        let path = PathBuf::from(path);
        if !path.is_absolute() {
            return Err(not_ready("YSS_PI_EXECUTABLE 须使用 Pi 的绝对路径"));
        }
        return fs::canonicalize(&path)
            .ok()
            .filter(|path| path.is_absolute() && is_executable(path))
            .ok_or_else(|| not_ready("YSS_PI_EXECUTABLE 须指向本机 Pi .exe 的绝对路径"));
    }
    env::split_paths(&env::var_os("PATH").unwrap_or_default())
        .filter(|path| path.is_absolute())
        .filter_map(|path| fs::canonicalize(path.join("pi.exe")).ok())
        .find(|path| is_executable(path))
        .ok_or_else(|| not_ready("未找到本机 Pi"))
}

#[cfg(windows)]
fn windows_user_pi(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
    explicit: Option<&Path>,
) -> Result<(PathBuf, PathBuf)> {
    let roots: Vec<PathBuf> = if let Some(explicit) = explicit {
        if !explicit.is_absolute()
            || !explicit
                .file_name()
                .is_some_and(|name| name.eq_ignore_ascii_case("pi.cmd"))
            || !explicit.is_file()
        {
            return Err(not_ready(
                "YSS_PI_EXECUTABLE 须指向 Pi 的绝对 .exe 或 pi.cmd 路径",
            ));
        }
        vec![
            explicit
                .parent()
                .ok_or_else(|| not_ready("Pi 路径无效"))?
                .to_owned(),
        ]
    } else {
        env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute())
            .collect()
    };
    for path in roots {
        let shim = path.join("pi.cmd");
        if !shim.is_file() {
            continue;
        }
        // Preserve discovery even when the package or its Node cannot be used.
        if status.executable.is_none() {
            status.executable = Some(shim.to_string_lossy().into_owned());
        }
        let package = path.join("node_modules/@earendil-works/pi-coding-agent");
        let Ok(root) = Root::open(&package) else {
            continue;
        };
        let Ok(file) = root.read_checked(&RelPath::new("package.json")?, 1024 * 1024) else {
            continue;
        };
        let Ok(metadata) = serde_json::from_slice::<Value>(&file.bytes) else {
            continue;
        };
        let version = metadata["version"]
            .as_str()
            .and_then(|value| Version::parse(value).ok());
        if metadata["name"] != "@earendil-works/pi-coding-agent"
            || !version
                .as_ref()
                .is_some_and(|value| SUPPORTED_VERSIONS.contains(value))
        {
            continue;
        }
        let Some(entry) = metadata["bin"]["pi"].as_str() else {
            continue;
        };
        let Ok(entry) = RelPath::new(entry) else {
            continue;
        };
        if !entry.as_str().ends_with(".js")
            || !matches!(root.inspect(&entry), Ok(FileSnapshot::File { .. }))
        {
            continue;
        }
        if root.read_checked_single_link(&entry, 1024 * 1024).is_err() {
            continue;
        }
        let Ok(entry) = fs::canonicalize(root.path.join(entry.as_str())) else {
            continue;
        };
        if is_protected_path(&entry, protected) {
            validate_protected_paths(protected)?;
            return Err(not_ready(
                "已有 Pi 位于工程或工作台数据目录中，无法安全运行",
            ));
        }
        status.executable = Some(entry.to_string_lossy().into_owned());
        validate_protected_paths(protected)?;
        if let Some(node) = windows_node(cancel, protected)? {
            return Ok((node, entry));
        }
        return Err(not_ready(
            "已发现 Pi，但未找到可用的 Node 22.19.0 或更新版本",
        ));
    }
    Err(not_ready(if status.executable.is_some() {
        "已发现 pi.cmd，但无法验证对应 Pi 包，请修复现有安装"
    } else {
        "未找到可验证的本机 Pi 包"
    }))
}

#[cfg(windows)]
fn windows_node(cancel: &Cancellation, protected: &[PathBuf]) -> Result<Option<PathBuf>> {
    let path = env::var_os("PATH").unwrap_or_default();
    let nodes = env::split_paths(&path)
        .filter(|path| path.is_absolute())
        .filter_map(|path| fs::canonicalize(path.join("node.exe")).ok())
        .filter(|path| is_executable(path));
    for node in nodes {
        match validate_node(&node, cancel, protected) {
            Ok(()) => return Ok(Some(node)),
            Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
            Err(_) => continue,
        }
    }
    Ok(None)
}

fn managed_pi(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
) -> Result<(PathBuf, PathBuf, Version)> {
    let tools = managed_pi::directory().ok_or_else(|| not_ready("无法确定桌面版工具目录"))?;
    let missing = || not_ready("未找到本机 Pi，请安装 Pi 1.6.1");
    let root = match Root::open(&tools) {
        Ok(root) => root,
        Err(_)
            if fs::symlink_metadata(&tools)
                .is_err_and(|error| error.kind() == ErrorKind::NotFound) =>
        {
            return Err(missing());
        }
        Err(_) => return Err(not_ready("受管工具目录路径不安全，请检查后重试")),
    };
    let managed = managed_pi::active_installation(&root)?.ok_or_else(missing)?;
    let entry_path = fs::canonicalize(&managed.entry)?;
    status.executable = Some(entry_path.to_string_lossy().into_owned());
    status.managed = true;
    let node = fs::canonicalize(&managed.node)
        .ok()
        .filter(|path| is_executable(path))
        .ok_or_else(|| not_ready("受管 Pi 所需 Node 不可用，请重新安装"))?;
    if is_protected_path(&node, protected) || is_protected_path(&entry_path, protected) {
        #[cfg(windows)]
        validate_protected_paths(protected)?;
        return Err(not_ready("受管 Pi 必须位于工程和工作台数据目录之外"));
    }
    validate_node(&node, cancel, protected)?;
    Ok((node, entry_path, managed.version))
}

fn validate_node(node: &Path, cancel: &Cancellation, protected: &[PathBuf]) -> Result<()> {
    let mut command = pi_command(node, None, protected)?;
    command.arg("--version");
    let (exit, stdout, _) = process::capture_command(command, cancel, Duration::from_secs(5))?;
    let version = std::str::from_utf8(&stdout)
        .ok()
        .and_then(|s| Version::parse(s.trim().trim_start_matches('v')).ok())
        .filter(|_| exit.success())
        .ok_or_else(|| not_ready("受管 Pi 的 Node 版本探测失败"))?;
    if version < Version::new(22, 19, 0) {
        return Err(not_ready("受管 Pi 需要 Node 22.19.0 或更新版本"));
    }
    Ok(())
}

#[cfg(windows)]
fn validate_protected_paths(protected: &[PathBuf]) -> Result<()> {
    for path in protected {
        let canonical = fs::canonicalize(path).map_err(|_| {
            not_ready(&format!(
                "无法核验登记工程或工作台数据目录 {}；请恢复目录或移除不再使用的登记后重试",
                path.display()
            ))
            .context("piIssue", "projectUnavailable")
        })?;
        Root::open(&canonical).map_err(|_| {
            not_ready(&format!(
                "无法安全核验登记工程或工作台数据目录 {}",
                path.display()
            ))
            .context("piIssue", "projectUnavailable")
        })?;
    }
    Ok(())
}

#[cfg(unix)]
fn is_protected_path(path: &Path, protected: &[PathBuf]) -> bool {
    protected.iter().any(|root| path.starts_with(root))
}

#[cfg(windows)]
fn is_protected_path(path: &Path, protected: &[PathBuf]) -> bool {
    // Compare opened directory identities, not lexical prefixes: Windows paths
    // can vary in case, verbatim prefix, junctions, or mounted-volume aliases.
    let Ok(path) = fs::canonicalize(path) else {
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
        let Ok(root) = Root::open(ancestor) else {
            return true;
        };
        ancestors.push(root.identity);
    }
    for protected_path in protected {
        let canonical = match fs::canonicalize(protected_path) {
            Ok(path) => path,
            Err(_) => return true,
        };
        let Ok(root) = Root::open(&canonical) else {
            return true;
        };
        if ancestors.contains(&root.identity) {
            return true;
        }
    }
    false
}

fn pi_command(executable: &Path, entry: Option<&Path>, protected: &[PathBuf]) -> Result<Command> {
    #[cfg(windows)]
    validate_protected_paths(protected)?;
    let is_protected = |path: &Path| is_protected_path(path, protected);
    if is_protected(executable) {
        return Err(not_ready("Pi 可执行文件须在工程和工作台数据目录之外"));
    }
    let path = env::join_paths(
        env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute())
            .filter_map(|path| fs::canonicalize(path).ok())
            .filter(|path| path.is_dir() && !is_protected(path)),
    )
    .map_err(|_| not_ready("Pi 运行环境的 PATH 无效"))?;
    let mut command = Command::new(executable);
    if let Some(entry) = entry {
        if is_protected(entry) {
            return Err(not_ready("Pi 程序须在工程和工作台数据目录之外"));
        }
        command.arg(entry);
    }
    command
        .env("PATH", path)
        .env("HOME", host_home()?)
        .env("PI_TELEMETRY", "0");
    for variable in [
        "NODE_OPTIONS",
        "NODE_PATH",
        "BASH_ENV",
        "ENV",
        "DYLD_INSERT_LIBRARIES",
    ] {
        command.env_remove(variable);
    }
    Ok(command)
}

fn configuration_error(message: &str) -> YssError {
    not_ready(message).context("piIssue", "configuration")
}

#[cfg(unix)]
fn host_home() -> Result<PathBuf> {
    // A workbench launcher can supply a temporary HOME. Ask macOS for the current account home.
    let mut account = MaybeUninit::<libc::passwd>::uninit();
    let mut result = ptr::null_mut();
    let size = unsafe { libc::sysconf(libc::_SC_GETPW_R_SIZE_MAX) };
    let mut buffer = vec![0; usize::try_from(size).unwrap_or(0).max(16 * 1024)];
    let code = unsafe {
        libc::getpwuid_r(
            libc::geteuid(),
            account.as_mut_ptr(),
            buffer.as_mut_ptr(),
            buffer.len(),
            &mut result,
        )
    };
    if code == 0 && !result.is_null() {
        let directory = unsafe { (*result).pw_dir };
        if !directory.is_null() {
            let home = Path::new(OsStr::from_bytes(
                unsafe { CStr::from_ptr(directory) }.to_bytes(),
            ));
            if home.is_absolute() {
                return Ok(home.to_owned());
            }
        }
    }
    Err(configuration_error(
        "无法定位当前 macOS 用户的主目录，请用配置 Pi 的账户启动工作台",
    ))
}

#[cfg(windows)]
fn host_home() -> Result<PathBuf> {
    directories::BaseDirs::new()
        .map(|dirs| dirs.home_dir().to_owned())
        .filter(|path| path.is_absolute())
        .ok_or_else(|| {
            configuration_error("无法定位当前 Windows 用户的主目录，请用配置 Pi 的账户启动工作台")
        })
}

fn config_directory(protected: &[PathBuf]) -> Result<PathBuf> {
    let path = match env::var_os("PI_CODING_AGENT_DIR").filter(|value| !value.is_empty()) {
        Some(value) => {
            let path = PathBuf::from(value);
            match path.strip_prefix("~") {
                Ok(relative) => host_home()?.join(relative),
                Err(_) => std::path::absolute(path)?,
            }
        }
        None => host_home()?.join(".pi/agent"),
    };
    let path = fs::canonicalize(&path).unwrap_or(path);
    #[cfg(unix)]
    let protected_config = protected.iter().any(|root| path.starts_with(root));
    #[cfg(windows)]
    let protected_config = {
        let mut existing = path.as_path();
        while !existing.exists() {
            existing = existing
                .parent()
                .ok_or_else(|| configuration_error("无法核对 Pi 配置目录"))?;
        }
        is_protected_path(existing, protected)
    };
    if protected_config {
        return Err(configuration_error(
            "Pi 配置目录须在工程和工作台数据目录之外，请核对 PI_CODING_AGENT_DIR",
        ));
    }
    Ok(path)
}

fn fixed_default_model(config_dir: &Path) -> Result<Option<PiModel>> {
    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct Settings {
        default_provider: Option<String>,
        default_model: Option<String>,
        enabled_models: Option<Vec<String>>,
    }

    let unavailable =
        || configuration_error("无法读取 Pi 默认模型，请核对 settings.json 中的供应商和模型 ID");
    let file = match fs::File::open(config_dir.join("settings.json")) {
        Ok(file) => file,
        Err(error) if error.kind() == ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(unavailable()),
    };
    let mut bytes = Vec::new();
    file.take(SETTINGS_LIMIT + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| unavailable())?;
    if bytes.len() as u64 > SETTINGS_LIMIT {
        return Err(unavailable());
    }
    let json = bytes.strip_prefix(b"\xef\xbb\xbf").unwrap_or(&bytes);
    let settings: Settings = serde_json::from_slice(json).map_err(|_| unavailable())?;
    // Ask Pi to resolve startup patterns and partial defaults using its own selection rules.
    if settings
        .enabled_models
        .is_some_and(|models| !models.is_empty())
    {
        return Ok(None);
    }
    match (settings.default_provider, settings.default_model) {
        (Some(provider), Some(id)) => PiModel::new(&provider, &id)
            .map(Some)
            .ok_or_else(unavailable),
        _ => Ok(None),
    }
}

fn check_credentials(
    command: Command,
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
) -> Result<()> {
    let (exit, stdout, _) = process::capture_command(command, cancel, Duration::from_secs(8))?;
    let result: Value = serde_json::from_slice(&stdout).unwrap_or(Value::Null);
    if exit.success() && result["status"] == "ready" {
        status.credentials_ready = Some(true);
        return Ok(());
    }
    match result["reason"].as_str() {
        Some("provider_not_found") => {
            Err(not_ready("本机 Pi 中未找到该模型供应商").context("piIssue", "modelUnavailable"))
        }
        Some("invalid_state") => Err(configuration_error(
            "Pi 无法读取有效的供应商或凭证配置，请核对 models.json 和登录状态",
        )),
        Some("credentials_not_configured" | "credential_not_available") => {
            status.credentials_ready = Some(false);
            Err(
                not_ready("Pi 未找到该供应商的凭证，请在本机 Pi 中登录或配置 API Key")
                    .context("piIssue", "credentials"),
            )
        }
        _ => Err(
            not_ready("无法检查 Pi 凭证，请核对本机 Pi 的供应商和凭证配置")
                .context("piIssue", "credentials"),
        ),
    }
}

pub(super) fn rpc_command(
    executable: &Path,
    entry: Option<&Path>,
    config_dir: &Path,
    protected: &[PathBuf],
) -> Result<Command> {
    let mut command = pi_command(executable, entry, protected)?;
    command
        .env("PI_CODING_AGENT_DIR", config_dir)
        .args(RPC_ARGS);
    Ok(command)
}

const RPC_ARGS: &[&str] = &[
    "--mode",
    "rpc",
    "--offline",
    "--no-builtin-tools",
    "--no-extensions",
    "--no-mcp",
    "--no-approve",
    "--no-prompt-templates",
    "--no-context-files",
    "--no-themes",
    "--no-skills",
];

pub(in super::super) fn probe_update_rpc(
    mut command: Command,
    cancel: &Cancellation,
) -> Result<()> {
    command.args(RPC_ARGS).args(["--no-session", "--no-tools"]);
    let mut session = Session::open(command, cancel)?;
    if !session.state(cancel)?.is_object()
        || !session.available_models(cancel)?["models"].is_array()
    {
        return Err(not_ready("受管 Pi 新版本返回的 RPC 状态或模型列表不兼容"));
    }
    Ok(())
}

pub(super) fn select_model(command: &mut Command, model: &PiModel) {
    command.args(["--provider", &model.provider, "--model", &model.id]);
}

fn model_identity(model: &Value) -> Option<PiModel> {
    PiModel::new(model["provider"].as_str()?, model["id"].as_str()?)
}

fn model_unavailable() -> YssError {
    not_ready("Pi 中找不到要使用的模型，请核对本机供应商与模型配置")
        .context("piIssue", "modelUnavailable")
}

pub(super) fn verify_model(state: &Value, expected: &PiModel) -> Result<()> {
    if model_identity(&state["model"]).as_ref() != Some(expected) {
        return Err(model_unavailable());
    }
    Ok(())
}

struct PiProbe {
    private: sandbox::PrivateDir,
    protected: Vec<PathBuf>,
    executable: PathBuf,
    entry: Option<PathBuf>,
    config_dir: PathBuf,
    version: Version,
}

impl PiProbe {
    fn new(
        cancel: &Cancellation,
        status: &mut RuntimeStatus,
        protected: &[PathBuf],
    ) -> Result<Self> {
        let protected: Vec<_> = protected
            .iter()
            .map(|path| fs::canonicalize(path).unwrap_or_else(|_| path.clone()))
            .collect();
        let explicit = env::var_os("YSS_PI_EXECUTABLE");
        status.explicit_executable = explicit.is_some();
        let prefer_selected = !status.explicit_executable
            && env::var_os("YSS_DESKTOP_MANAGED").is_some_and(|value| value == "1")
            && managed_pi::has_selected_release()?;
        let (executable, entry, managed_version) = if prefer_selected {
            let (node, entry, version) = managed_pi(cancel, status, &protected)?;
            (node, Some(entry), Some(version))
        } else {
            match locate_pi() {
                Ok(executable) => (executable, None, None),
                #[cfg(unix)]
                Err(error) if explicit.is_some() => return Err(error),
                #[cfg(windows)]
                Err(error) if explicit.is_some() => {
                    let configured = PathBuf::from(explicit.unwrap_or_default());
                    if !configured.is_absolute() {
                        return Err(error);
                    }
                    let (node, entry) =
                        windows_user_pi(cancel, status, &protected, Some(&configured))?;
                    (node, Some(entry), None)
                }
                Err(_) => {
                    #[cfg(windows)]
                    let discovered = match windows_user_pi(cancel, status, &protected, None) {
                        Ok((node, entry)) => (node, Some(entry), None),
                        Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
                        Err(error) if status.executable.is_some() => return Err(error),
                        Err(_) => {
                            let (node, entry, version) = managed_pi(cancel, status, &protected)?;
                            (node, Some(entry), Some(version))
                        }
                    };
                    #[cfg(unix)]
                    let discovered = {
                        let (node, entry, version) = managed_pi(cancel, status, &protected)?;
                        (node, Some(entry), Some(version))
                    };
                    discovered
                }
            }
        };
        let executable_name = entry
            .as_ref()
            .unwrap_or(&executable)
            .to_str()
            .ok_or_else(|| {
                configuration_error("Pi 可执行文件路径须为有效 UTF-8，请核对 YSS_PI_EXECUTABLE")
            })?;
        status.executable = Some(executable_name.to_owned());
        if entry.is_some() {
            status.node_executable = executable.to_str().map(str::to_owned);
        }
        let private = sandbox::PrivateDir::new()?;
        let mut probe = pi_command(&executable, entry.as_deref(), &protected)?;
        let config_dir = config_directory(&protected)?;
        let config_name = config_dir.to_str().ok_or_else(|| {
            configuration_error("Pi 配置目录须为有效 UTF-8，请核对 PI_CODING_AGENT_DIR")
        })?;
        status.config_dir = Some(config_name.to_owned());
        probe
            .current_dir(private.path())
            .env("PI_CODING_AGENT_DIR", &config_dir)
            .arg("--version");
        let (exit, stdout, _) = process::capture_command(probe, cancel, Duration::from_secs(5))?;
        let version = std::str::from_utf8(&stdout)
            .ok()
            .and_then(|text| Version::parse(text.trim()).ok())
            .filter(|_| exit.success())
            .ok_or_else(|| not_ready("Pi 版本探测失败，请检查可执行文件和运行环境"))?;
        status.version = Some(version.clone());
        if let Some(expected) = managed_version {
            if version != expected {
                return Err(not_ready("受管 Pi 的实际版本与活动记录不一致"));
            }
        } else if !SUPPORTED_VERSIONS.contains(&version) {
            return Err(not_ready(
                "工作台当前支持 Pi 1.5.2、1.6.0 和 1.6.1，请核对本机 Pi 版本",
            ));
        }
        Ok(Self {
            private,
            protected,
            executable,
            entry,
            config_dir,
            version,
        })
    }

    fn open(&self, cancel: &Cancellation, model: Option<&PiModel>) -> Result<Session> {
        let mut command = rpc_command(
            &self.executable,
            self.entry.as_deref(),
            &self.config_dir,
            &self.protected,
        )?;
        command
            .current_dir(self.private.path())
            .args(["--no-session", "--no-tools"]);
        if let Some(model) = model {
            select_model(&mut command, model);
        }
        Session::open(command, cancel)
    }

    fn credentials_command(&self, model: &PiModel) -> Result<Command> {
        let mut command = pi_command(&self.executable, self.entry.as_deref(), &self.protected)?;
        command
            .current_dir(self.private.path())
            .env("PI_CODING_AGENT_DIR", &self.config_dir)
            .args([
                "auth",
                "check",
                "--provider",
                &model.provider,
                "--json",
                "--no-refresh",
            ]);
        Ok(command)
    }
}

pub(crate) fn available_models(
    cancel: &Cancellation,
    protected: &[PathBuf],
) -> Result<Vec<PiModel>> {
    let probe = PiProbe::new(cancel, &mut RuntimeStatus::default(), protected)?;
    let response = probe.open(cancel, None)?.available_models(cancel)?;
    let mut models: Vec<_> = response["models"]
        .as_array()
        .ok_or_else(|| not_ready("Pi 未返回有效的本机模型列表"))?
        .iter()
        .filter_map(model_identity)
        .collect();
    models.sort_unstable_by(|left, right| {
        (&left.provider, &left.id).cmp(&(&right.provider, &right.id))
    });
    models.dedup();
    Ok(models)
}

pub(crate) fn check_runtime(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
    model: Option<&PiModel>,
) -> Result<PiRuntime> {
    #[cfg(windows)]
    {
        status.sandbox_message = Some(sandbox::OFFLINE_UNAVAILABLE.into());
    }
    let probe = PiProbe::new(cancel, status, protected)?;
    #[cfg(windows)]
    super::probe_base(cancel).map_err(|mut error| {
        if error.code != ErrorCode::Cancelled {
            error.message = format!("Windows Pi 基础作业自检未通过：{}", error.message);
        }
        error
    })?;
    #[cfg(unix)]
    let sandbox = sandbox::probe(cancel);
    #[cfg(windows)]
    let command_node = if probe.entry.is_some() {
        Some(probe.executable.clone())
    } else {
        windows_node(cancel, &probe.protected)?
    };
    #[cfg(windows)]
    let sandbox = sandbox::probe(command_node.as_deref(), cancel);
    match sandbox {
        Ok(()) => {
            status.sandbox_ready = true;
            status.sandbox_message = None;
        }
        Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
        Err(error) => {
            status.sandbox_message = Some(error.message);
        }
    }

    status.model = match model {
        Some(model) => Some(model.clone()),
        None => fixed_default_model(&probe.config_dir)?,
    };
    if let Some(model) = &status.model {
        check_credentials(probe.credentials_command(model)?, cancel, status)?;
    }

    let mut session = probe.open(cancel, status.model.as_ref())?;
    let mut state = session.state(cancel)?;
    let model = match &status.model {
        Some(model) => {
            let available = session.available_models(cancel)?;
            if !available["models"].as_array().is_some_and(|models| {
                models
                    .iter()
                    .any(|entry| model_identity(entry).as_ref() == Some(model))
            }) {
                return Err(model_unavailable());
            }
            if verify_model(&state, model).is_err() {
                session.set_model(model, cancel)?;
                state = session.state(cancel)?;
                verify_model(&state, model)?;
            }
            model.clone()
        }
        None => {
            let model = model_identity(&state["model"]).ok_or_else(|| {
                not_ready("Pi 没有可用模型，请在本机 Pi 中选择模型并配置凭证")
                    .context("piIssue", "modelUnavailable")
            })?;
            status.model = Some(model.clone());
            check_credentials(probe.credentials_command(&model)?, cancel, status)?;
            model
        }
    };
    let thinking_levels = session.thinking_levels(cancel)?;
    status.thinking_levels.clone_from(&thinking_levels);
    status.thinking_level = serde_json::from_value(state["thinkingLevel"].clone()).ok();
    status.context_window = state["model"]["contextWindow"]
        .as_u64()
        .filter(|value| *value > 0);
    Ok(PiRuntime {
        executable: probe.executable,
        entry: probe.entry,
        #[cfg(windows)]
        command_node,
        config_dir: probe.config_dir,
        version: probe.version,
        model,
        thinking_levels,
        sandbox_message: status.sandbox_message.clone(),
    })
}

pub(crate) fn test_model(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    expected: &PiModel,
) -> Result<()> {
    let _test = CONNECTION_TEST
        .try_lock()
        .map_err(|_| YssError::new(ErrorCode::Locked, "已有 Pi 模型连接测试进行中，请稍后重试"))?;
    let runtime = check_runtime(cancel, &mut RuntimeStatus::default(), protected, model)?;
    runtime.verify_expected(Some(expected))?;
    let private = sandbox::PrivateDir::new()?;
    let mut command = rpc_command(
        &runtime.executable,
        runtime.entry.as_deref(),
        &runtime.config_dir,
        protected,
    )?;
    command.current_dir(private.path()).args([
        "--no-session",
        "--no-tools",
        "--thinking",
        "off",
        "--system-prompt",
        "This is a model connection test. Reply with only OK.",
    ]);
    select_model(&mut command, &runtime.model);
    let mut session = Session::open(command, cancel)?;
    verify_model(&session.state(cancel)?, &runtime.model)?;
    session.test_connection(cancel)
}
