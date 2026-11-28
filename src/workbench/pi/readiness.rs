use super::{bridge::Bridge, not_ready, rpc::Session, sandbox};
#[cfg(windows)]
use crate::model::RelPath;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, FileSnapshot, Root},
    workbench::{
        agent::CommandExecution,
        agent_protocol::{
            CommandChecks, PiModel, PiRuntime, RuntimeStatus, native_fork_model_identity,
            native_fork_version_supported,
        },
        managed_pi, process,
    },
};
use semver::Version;
use serde::Deserialize;
use serde_json::Value;
use std::{
    env, fs,
    io::{ErrorKind, Read},
    path::{Path, PathBuf},
    process::Command,
    sync::Mutex,
    time::{Duration, SystemTime},
};
#[cfg(unix)]
use std::{
    ffi::{CStr, OsStr, OsString},
    mem::MaybeUninit,
    os::unix::{ffi::OsStrExt, fs::MetadataExt},
    ptr,
};

const SETTINGS_LIMIT: u64 = 1024 * 1024;
static CONNECTION_TEST: Mutex<()> = Mutex::new(());
static CONTINUATION_DRAFT: Mutex<()> = Mutex::new(());
static MANAGED_VERSION: Mutex<Option<ManagedVersionKey>> = Mutex::new(None);
const DRAFT_PROMPT_HOOK: &str = "yss-draft-prompt-ready";

#[derive(PartialEq, Eq)]
struct ExecutableStamp {
    identity: FileIdentity,
    bytes: u64,
    modified: SystemTime,
    #[cfg(unix)]
    change_time: (i64, i64),
}

impl ExecutableStamp {
    fn read(path: &Path) -> Option<Self> {
        let file = fs::File::open(path).ok()?;
        let metadata = file.metadata().ok()?;
        if !metadata.is_file() {
            return None;
        }
        let file = cap_std::fs::File::from_std(file);
        Some(Self {
            identity: crate::fs::platform::identity_file(&file).ok()?,
            bytes: metadata.len(),
            modified: metadata.modified().ok()?,
            #[cfg(unix)]
            change_time: (metadata.ctime(), metadata.ctime_nsec()),
        })
    }
}

#[derive(PartialEq, Eq)]
struct ManagedVersionKey {
    activation: FileSnapshot,
    expected: Version,
    node: (PathBuf, ExecutableStamp),
    entry: (PathBuf, ExecutableStamp),
    environment: Vec<(std::ffi::OsString, Option<std::ffi::OsString>)>,
}

impl ManagedVersionKey {
    fn capture(
        expected: &Version,
        activation: &FileSnapshot,
        executable: &Path,
        entry: &Path,
        command: &Command,
    ) -> Option<Self> {
        Some(Self {
            activation: activation.clone(),
            expected: expected.clone(),
            node: (executable.to_owned(), ExecutableStamp::read(executable)?),
            entry: (entry.to_owned(), ExecutableStamp::read(entry)?),
            environment: command
                .get_envs()
                .map(|(name, value)| (name.to_owned(), value.map(<_>::to_owned)))
                .collect(),
        })
    }
}

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
            not_ready(&format!(
                "未找到本机 Pi，请安装受支持的 Pi（{}），或设置 YSS_PI_EXECUTABLE",
                managed_pi::EXTERNAL_VERSION_LABEL
            ))
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
                .is_some_and(managed_pi::supports_external_version)
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
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
) -> Result<(PathBuf, PathBuf, (Version, FileSnapshot))> {
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
    let activation = managed.activation().clone();
    Ok((node, entry_path, (managed.version, activation)))
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

#[cfg(unix)]
pub(super) fn host_command_environment() -> Result<Vec<(OsString, OsString)>> {
    let path = env::var_os("PATH").unwrap_or_default();
    let path = env::join_paths(env::split_paths(&path).filter(|entry| entry.is_absolute()))
        .map_err(|_| not_ready("本机命令无法解析工作台启动时的 PATH"))?;
    if path.is_empty() {
        return Err(not_ready("本机命令需要工作台 PATH 中的绝对目录"));
    }
    // Keep toolchain locations and package caches without inheriting credentials or shell hooks.
    const TOOL_VARIABLES: &[&str] = &[
        "TMPDIR",
        "LANG",
        "LC_ALL",
        "LC_CTYPE",
        "XDG_CACHE_HOME",
        "XDG_CONFIG_HOME",
        "XDG_DATA_HOME",
        "JAVA_HOME",
        "MAVEN_HOME",
        "M2_HOME",
        "MAVEN_USER_HOME",
        "GRADLE_USER_HOME",
        "COREPACK_HOME",
        "PNPM_HOME",
        "PYENV_ROOT",
        "VIRTUAL_ENV",
        "UV_CACHE_DIR",
        "CARGO_HOME",
        "RUSTUP_HOME",
        "GOPATH",
    ];
    let mut variables = vec![
        ("PATH".into(), path),
        ("HOME".into(), host_home()?.into_os_string()),
    ];
    variables.extend(
        TOOL_VARIABLES
            .iter()
            .filter_map(|name| env::var_os(name).map(|value| (OsString::from(*name), value))),
    );
    Ok(variables)
}

#[cfg(unix)]
fn probe_host_commands(cancel: &Cancellation) -> Result<()> {
    let mut command = Command::new("/bin/bash");
    command
        .args(["--noprofile", "--norc", "-c", ":"])
        .current_dir("/")
        .env_clear()
        .envs(host_command_environment()?);
    match process::capture_command(command, cancel, Duration::from_secs(5)) {
        Ok((exit, _, _)) if exit.success() => Ok(()),
        Err(error) if error.code == ErrorCode::Cancelled => Err(error),
        _ => Err(not_ready("本机命令无法启动 /bin/bash")),
    }
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

pub(in super::super) fn verify_managed_package(
    executable: &Path,
    entry: &Path,
    expected: &Version,
    cancel: &Cancellation,
) -> Result<()> {
    let private = sandbox::PrivateDir::new()?;
    let path = private.path();
    let command = || {
        let mut command = Command::new(executable);
        command
            .arg(entry)
            .env_clear()
            .env("HOME", path)
            .env("PI_CODING_AGENT_DIR", path.join("agent"))
            .env("PI_OFFLINE", "1")
            .env("PI_TELEMETRY", "0")
            .current_dir(path);
        #[cfg(unix)]
        command.env("PATH", "/usr/bin:/bin").env("TMPDIR", path);
        #[cfg(windows)]
        {
            command
                .env("USERPROFILE", path)
                .env("APPDATA", path.join("AppData/Roaming"))
                .env("LOCALAPPDATA", path.join("AppData/Local"))
                .env("TMP", path)
                .env("TEMP", path);
            if let Some(root) = env::var_os("SystemRoot")
                .map(PathBuf::from)
                .filter(|p| p.is_absolute())
            {
                command
                    .env("PATH", root.join("System32"))
                    .env("SystemRoot", root);
            }
        }
        command
    };
    let mut version = command();
    version.arg("--version");
    let (status, stdout, _) = process::capture_command(version, cancel, Duration::from_secs(15))?;
    let actual = std::str::from_utf8(&stdout)
        .ok()
        .and_then(|text| Version::parse(text.trim().trim_start_matches('v')).ok());
    if !status.success() || actual.as_ref() != Some(expected) {
        return Err(not_ready("Pi 入口版本校验失败"));
    }

    let extension = path.join("workbench.ts");
    fs::write(&extension, include_bytes!("tools.mjs"))?;
    let bridge = Bridge::start_probe(path, cancel)?;
    let mut command = command();
    command
        .args(RPC_ARGS)
        .args(["--no-session", "--extension"])
        .arg(&extension)
        .env("YSS_PI_READ_ONLY", "1");
    bridge.configure(&mut command);
    let mut session = Session::open(command, cancel)?;
    session.state(cancel)?;
    if !session.available_models(cancel)?["models"].is_array() {
        return Err(not_ready("受管 Pi 返回的模型列表格式不兼容"));
    }
    bridge.await_ready()
}

pub(super) fn select_model(command: &mut Command, model: &PiModel) {
    command.args(["--provider", &model.provider, "--model", &model.id]);
}

fn model_identity(model: &Value) -> Option<PiModel> {
    PiModel::new(model["provider"].as_str()?, model["id"].as_str()?)
}

pub(super) fn model_unavailable() -> YssError {
    not_ready("Pi 中找不到要使用的模型，请核对本机供应商与模型配置")
        .context("piIssue", "modelUnavailable")
}

pub(super) fn verify_model(state: &Value, expected: &PiModel) -> Result<()> {
    if model_identity(&state["model"]).as_ref() != Some(expected) {
        return Err(model_unavailable());
    }
    Ok(())
}

pub(super) fn state_model(state: &Value) -> Result<PiModel> {
    model_identity(&state["model"]).ok_or_else(|| {
        not_ready("Pi 没有可用模型，请在本机 Pi 中选择模型并配置凭证")
            .context("piIssue", "modelUnavailable")
    })
}

pub(super) fn verify_available_model(
    session: &mut Session,
    model: &PiModel,
    cancel: &Cancellation,
) -> Result<()> {
    let available = session.available_models(cancel)?;
    if !available["models"].as_array().is_some_and(|models| {
        models
            .iter()
            .any(|entry| model_identity(entry).as_ref() == Some(model))
    }) {
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
            let (node, entry, version) = managed_pi(status, &protected)?;
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
                            let (node, entry, version) = managed_pi(status, &protected)?;
                            (node, Some(entry), Some(version))
                        }
                    };
                    #[cfg(unix)]
                    let discovered = {
                        let (node, entry, version) = managed_pi(status, &protected)?;
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
        let managed_stamp = || {
            let (expected, activation) = managed_version.as_ref()?;
            ManagedVersionKey::capture(expected, activation, &executable, entry.as_deref()?, &probe)
        };
        let stamp = managed_stamp();
        cancel.check()?;
        let version = if let Some(cached) = stamp.as_ref().filter(|key| {
            MANAGED_VERSION
                .lock()
                .is_ok_and(|current| current.as_ref() == Some(key))
        }) {
            cached.expected.clone()
        } else {
            if managed_version.is_some() {
                validate_node(&executable, cancel, &protected)?;
            }
            let (exit, stdout, _) =
                process::capture_command(probe, cancel, Duration::from_secs(5))?;
            let version = std::str::from_utf8(&stdout)
                .ok()
                .and_then(|text| Version::parse(text.trim()).ok())
                .filter(|_| exit.success())
                .ok_or_else(|| not_ready("Pi 版本探测失败，请检查可执行文件和运行环境"))?;
            status.version = Some(version.clone());
            if let Some((expected, _)) = managed_version {
                if version != expected {
                    return Err(not_ready("受管 Pi 的实际版本与活动记录不一致"));
                }
                if let Some(stamp) = stamp
                    && ExecutableStamp::read(&stamp.node.0).as_ref() == Some(&stamp.node.1)
                    && ExecutableStamp::read(&stamp.entry.0).as_ref() == Some(&stamp.entry.1)
                    && let Ok(mut current) = MANAGED_VERSION.lock()
                {
                    *current = Some(stamp);
                }
            } else if !managed_pi::supports_external_version(&version) {
                return Err(not_ready(&format!(
                    "当前检测到 Pi {version}，此平台支持 {}",
                    managed_pi::EXTERNAL_VERSION_LABEL
                ))
                .context("piIssue", "versionUnsupported"));
            }
            version
        };
        status.version = Some(version.clone());
        Ok(Self {
            private,
            protected,
            executable,
            entry,
            config_dir,
            version,
        })
    }

    fn command(&self, model: Option<&PiModel>) -> Result<Command> {
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
        Ok(command)
    }

    fn open(&self, cancel: &Cancellation, model: Option<&PiModel>) -> Result<Session> {
        Session::open(self.command(model)?, cancel)
    }

    fn credentials_command(&self, model: &PiModel) -> Result<Command> {
        credentials_command(
            &self.executable,
            self.entry.as_deref(),
            &self.config_dir,
            &self.protected,
            self.private.path(),
            model,
        )
    }
}

fn credentials_command(
    executable: &Path,
    entry: Option<&Path>,
    config_dir: &Path,
    protected: &[PathBuf],
    private: &Path,
    model: &PiModel,
) -> Result<Command> {
    let mut command = pi_command(executable, entry, protected)?;
    command
        .current_dir(private)
        .env("PI_CODING_AGENT_DIR", config_dir)
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

pub(super) fn verify_credentials(
    runtime: &PiRuntime,
    private: &Path,
    model: &PiModel,
    cancel: &Cancellation,
) -> Result<()> {
    let command = credentials_command(
        &runtime.executable,
        runtime.entry.as_deref(),
        &runtime.config_dir,
        &runtime.protected,
        private,
        model,
    )?;
    check_credentials(command, cancel, &mut RuntimeStatus::default())
}

pub(crate) fn generate_continuation_draft(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: &PiModel,
    system_prompt: &str,
    input: &str,
) -> Result<String> {
    let _draft = CONTINUATION_DRAFT
        .try_lock()
        .map_err(|_| YssError::new(ErrorCode::Locked, "已有接续草稿正在生成，请稍后重试"))?;
    let mut status = RuntimeStatus::default();
    let probe = PiProbe::new(cancel, &mut status, protected)?;
    check_credentials(probe.credentials_command(model)?, cancel, &mut status)?;
    let extension = probe.private.path().join("draft-prompt.mjs");
    let system_prompt = serde_json::to_string(system_prompt)
        .map_err(|_| not_ready("无法准备接续草稿系统提示词"))?;
    // Pi normally appends cwd even for a custom prompt. Force the exact
    // previewed text and verify the hook is active before submitting data.
    fs::write(
        &extension,
        format!(
            "export default function(pi) {{\n  pi.on('before_agent_start', () => ({{ systemPrompt: {system_prompt} }}));\n  pi.registerCommand('{DRAFT_PROMPT_HOOK}', {{ handler: async () => {{}} }});\n}}\n"
        ),
    )?;
    let mut command = probe.command(Some(model))?;
    command
        .args(["--thinking", "off", "--extension"])
        .arg(extension);
    let mut session = Session::open(command, cancel)?;
    let state = session.state(cancel)?;
    verify_model(&state, model)?;
    if !state["sessionFile"].is_null() || !session.has_only_command(DRAFT_PROMPT_HOOK, cancel)? {
        return Err(not_ready("Pi 接续草稿会话的隔离或提示词自检未通过"));
    }
    session.generate_draft(input, cancel)
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
    commands: CommandChecks,
    check_model_in_session: bool,
) -> Result<PiRuntime> {
    #[cfg(unix)]
    if commands.includes(CommandExecution::Host) {
        match probe_host_commands(cancel) {
            Ok(()) => status.host_commands_ready = true,
            Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
            Err(error) => status.host_commands_message = Some(error.message),
        }
    }
    #[cfg(windows)]
    if commands.includes(CommandExecution::Sandbox) {
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
    #[cfg(windows)]
    let mut command_sandbox = None;
    if commands.includes(CommandExecution::Sandbox) {
        #[cfg(unix)]
        let sandbox = sandbox::probe(cancel);
        #[cfg(windows)]
        let sandbox = {
            let command_node = if probe.entry.is_some() {
                Some(probe.executable.clone())
            } else {
                windows_node(cancel, &probe.protected)?
            };
            sandbox::Sandbox::new(command_node.as_deref(), cancel)
        };
        match sandbox {
            Ok(sandbox) => {
                #[cfg(windows)]
                {
                    command_sandbox = Some(Box::new(sandbox));
                }
                #[cfg(unix)]
                let () = sandbox;
                status.sandbox_ready = true;
                status.sandbox_message = None;
            }
            Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
            Err(error) => status.sandbox_message = Some(error.message),
        }
    }

    status.model = match model {
        Some(model) => Some(model.clone()),
        None => fixed_default_model(&probe.config_dir)?,
    };
    let native_fork_model_identity = if check_model_in_session {
        None
    } else {
        let state = check_model_status(&probe, status, cancel)?;
        if native_fork_version_supported(&probe.version) {
            native_fork_model_identity(&state).ok()
        } else {
            None
        }
    };
    Ok(PiRuntime {
        executable: probe.executable,
        entry: probe.entry,
        #[cfg(windows)]
        command_sandbox,
        config_dir: probe.config_dir,
        version: probe.version,
        protected: probe.protected,
        model: status.model.clone(),
        native_fork_model_identity,
        thinking_levels: status.thinking_levels.clone(),
        check_model_in_session,
        sandbox_message: status.sandbox_message.clone(),
        host_commands_message: status.host_commands_message.clone(),
    })
}

fn check_model_status(
    probe: &PiProbe,
    status: &mut RuntimeStatus,
    cancel: &Cancellation,
) -> Result<Value> {
    let preferred = status.model.clone();
    if let Some(model) = &preferred {
        check_credentials(probe.credentials_command(model)?, cancel, status)?;
    }
    let mut session = probe.open(cancel, preferred.as_ref())?;
    let mut state = session.state(cancel)?;
    if let Some(model) = &preferred {
        verify_available_model(&mut session, model, cancel)?;
        if verify_model(&state, model).is_err() {
            session.set_model(model, cancel)?;
            state = session.state(cancel)?;
            verify_model(&state, model)?;
        }
    } else {
        let model = state_model(&state)?;
        status.model = Some(model.clone());
        check_credentials(probe.credentials_command(&model)?, cancel, status)?;
    }
    status.thinking_levels = session.thinking_levels(cancel)?;
    status.thinking_level = serde_json::from_value(state["thinkingLevel"].clone()).ok();
    status.context_window = state["model"]["contextWindow"]
        .as_u64()
        .filter(|value| *value > 0);
    Ok(state)
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
    let runtime = check_runtime(
        cancel,
        &mut RuntimeStatus::default(),
        protected,
        model,
        CommandChecks::All,
        false,
    )?;
    runtime.verify_expected(Some(expected))?;
    let model = runtime.model.as_ref().ok_or_else(model_unavailable)?;
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
    select_model(&mut command, model);
    let mut session = Session::open(command, cancel)?;
    verify_model(&session.state(cancel)?, model)?;
    session.test_connection(cancel)
}
