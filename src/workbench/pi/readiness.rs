use super::{not_ready, rpc::Session, sandbox};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{
        agent_protocol::{PiModel, PiRuntime, RuntimeStatus},
        process,
    },
};
use semver::Version;
use serde::Deserialize;
use serde_json::Value;
use std::{
    env, fs,
    io::{ErrorKind, Read},
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    process::Command,
    sync::Mutex,
    time::Duration,
};

const SUPPORTED_VERSIONS: [Version; 2] = [Version::new(1, 5, 2), Version::new(1, 6, 0)];
const SETTINGS_LIMIT: u64 = 1024 * 1024;
static CONNECTION_TEST: Mutex<()> = Mutex::new(());

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
            not_ready("未找到本机 Pi，请安装 Pi 1.5.2 或 1.6.0，或设置 YSS_PI_EXECUTABLE")
        })
}

fn is_executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|metadata| metadata.is_file() && metadata.mode() & 0o111 != 0)
}

fn pi_command(executable: &Path, protected: &[PathBuf]) -> Result<Command> {
    let is_protected = |path: &Path| protected.iter().any(|root| path.starts_with(root));
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
    command.env("PATH", path).env("PI_TELEMETRY", "0");
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

fn config_directory(protected: &[PathBuf]) -> Result<PathBuf> {
    let home = || {
        directories::BaseDirs::new()
            .map(|dirs| dirs.home_dir().to_owned())
            .ok_or_else(|| not_ready("无法定位 Pi 配置目录，请设置 PI_CODING_AGENT_DIR"))
    };
    let path = match env::var_os("PI_CODING_AGENT_DIR").filter(|value| !value.is_empty()) {
        Some(value) => {
            let path = PathBuf::from(value);
            match path.strip_prefix("~") {
                Ok(relative) => home()?.join(relative),
                Err(_) => std::path::absolute(path)?,
            }
        }
        None => home()?.join(".pi/agent"),
    };
    let path = fs::canonicalize(&path).unwrap_or(path);
    if protected.iter().any(|root| path.starts_with(root)) {
        return Err(not_ready("Pi 配置目录须在工程和工作台数据目录之外"));
    }
    Ok(path)
}

fn configured_model(config_dir: &Path) -> Result<Option<PiModel>> {
    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct Settings {
        default_provider: Option<String>,
        default_model: Option<String>,
    }

    let unavailable = || not_ready("无法读取 Pi 默认模型配置，请在 Pi 中重新选择模型");
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
    let settings: Settings = serde_json::from_slice(&bytes).map_err(|_| unavailable())?;
    match (settings.default_provider, settings.default_model) {
        (None, None) => Ok(None),
        (Some(provider), Some(id)) => PiModel::new(&provider, &id)
            .map(Some)
            .ok_or_else(unavailable),
        _ => Err(unavailable()),
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
        Some("provider_not_found") => Err(model_unavailable()),
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
    config_dir: &Path,
    protected: &[PathBuf],
) -> Result<Command> {
    let mut command = pi_command(executable, protected)?;
    command.env("PI_CODING_AGENT_DIR", config_dir).args([
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
    ]);
    Ok(command)
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

pub(crate) fn check_runtime(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
    model: Option<&PiModel>,
) -> Result<PiRuntime> {
    let protected: Vec<_> = protected
        .iter()
        .map(|path| fs::canonicalize(path).unwrap_or_else(|_| path.clone()))
        .collect();
    let private = sandbox::PrivateDir::new()?;
    let executable = locate_pi()?;
    let mut probe = pi_command(&executable, &protected)?;
    probe.current_dir(private.path()).arg("--version");
    let (exit, stdout, _) = process::capture_command(probe, cancel, Duration::from_secs(5))?;
    let version = std::str::from_utf8(&stdout)
        .ok()
        .and_then(|text| Version::parse(text.trim()).ok())
        .filter(|_| exit.success())
        .ok_or_else(|| not_ready("Pi 版本探测失败，请检查可执行文件和运行环境"))?;
    status.version = Some(version.clone());
    if !SUPPORTED_VERSIONS.contains(&version) {
        return Err(not_ready(
            "工作台当前支持 Pi 1.5.2 和 1.6.0，请核对本机 Pi 版本",
        ));
    }
    sandbox::probe(cancel)?;
    status.sandbox_ready = true;

    let config_dir = config_directory(&protected)?;
    status.model = match model {
        Some(model) => Some(model.clone()),
        None => configured_model(&config_dir)?,
    };
    let mut command = rpc_command(&executable, &config_dir, &protected)?;
    command
        .current_dir(private.path())
        .args(["--no-session", "--no-tools"]);
    let credentials_command = |model: &PiModel| -> Result<Command> {
        let mut command = pi_command(&executable, &protected)?;
        command
            .current_dir(private.path())
            .env("PI_CODING_AGENT_DIR", &config_dir)
            .args([
                "auth",
                "check",
                "--provider",
                &model.provider,
                "--json",
                "--no-refresh",
            ]);
        Ok(command)
    };
    if let Some(model) = &status.model {
        check_credentials(credentials_command(model)?, cancel, status)?;
    }

    let mut session = Session::open(command, cancel)?;
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
            model.clone()
        }
        None => {
            let state = session.state(cancel)?;
            let model = model_identity(&state["model"]).ok_or_else(|| {
                not_ready("Pi 没有可用模型，请在本机 Pi 中选择模型并配置凭证")
                    .context("piIssue", "modelUnavailable")
            })?;
            status.model = Some(model.clone());
            check_credentials(credentials_command(&model)?, cancel, status)?;
            model
        }
    };
    Ok(PiRuntime {
        executable,
        config_dir,
        version,
        model,
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
    let mut command = rpc_command(&runtime.executable, &runtime.config_dir, protected)?;
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
