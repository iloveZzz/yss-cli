use super::{agent::CommandExecution, agent_workspace::Workspace, jobs::JobContext};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, FileSnapshot, Root},
    model::{RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::{Path, PathBuf};

#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
#[path = "pi/mod.rs"]
mod pi;

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
pub(crate) use pi::{
    StoppedCommand,
    windows_mapping::{Change as WindowsCommandChange, VerifiedManifest},
};

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[path = "pi/windows_pipe.rs"]
mod windows_pipe;

#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[allow(dead_code)]
#[path = "pi/windows_sandbox.rs"]
mod windows_sandbox;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct PiSession {
    pub id: String,
    pub file: String,
}

pub(crate) const NATIVE_FORK_ANCHOR_RECORD: &str = "agentNativeAnchor";
const NATIVE_FORK_SANDBOX: &str = "/usr/bin/sandbox-exec";

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct NativeForkAnchor {
    pub run_id: String,
    pub job_id: String,
    pub session: PiSession,
    pub entry_id: String,
    pub parent_id: Option<String>,
    pub leaf_id: String,
    pub model: PiModel,
    config_dir: PathBuf,
    config_identity: NativeForkConfig,
    model_identity: Sha256,
    source_identity: FileIdentity,
    source_len: u64,
    source_sha256: Sha256,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct NativeForkConfig {
    directory: FileIdentity,
    model_registry: Option<Sha256>,
}

impl NativeForkConfig {
    fn read(path: &Path) -> Result<Self> {
        let root = Root::open(path)?;
        let registry = RelPath::new("models.json")?;
        let model_registry = if matches!(root.inspect(&registry)?, FileSnapshot::Missing) {
            None
        } else {
            let checked = root.read_checked_single_link(&registry, 2 * 1024 * 1024)?;
            Some(
                checked
                    .snapshot
                    .descriptor()
                    .ok_or_else(native_fork_unavailable)?
                    .sha256
                    .clone(),
            )
        };
        Ok(Self {
            directory: root.identity,
            model_registry,
        })
    }
}

pub(crate) const fn native_fork_supported() -> bool {
    cfg!(all(target_os = "macos", target_arch = "aarch64"))
}

pub(crate) fn native_fork_version_supported(version: &semver::Version) -> bool {
    native_fork_supported() && *version == semver::Version::new(1, 6, 1)
}

fn native_fork_model_identity(state: &Value) -> Result<Sha256> {
    let model = &state["model"];
    if !model.is_object() {
        return Err(native_fork_unavailable());
    }
    let encoded = serde_json::to_vec(model).map_err(|_| native_fork_unavailable())?;
    Ok(Sha256::of(&encoded))
}

fn native_fork_unavailable() -> YssError {
    YssError::new(
        ErrorCode::NotReady,
        "此轮暂不能从 Pi 原生会话分叉，请使用摘要续办",
    )
    .context("piIssue", "nativeForkUnavailable")
    .context("httpStatus", 409)
}

pub(crate) fn verify_native_fork_source(
    data_dir: &Path,
    cwd: &Path,
    anchor: &NativeForkAnchor,
    cancel: &Cancellation,
) -> Result<()> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::verify_native_fork_source(data_dir, cwd, anchor, cancel)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (data_dir, cwd, anchor, cancel);
        Err(native_fork_unavailable())
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PiModel {
    pub provider: String,
    pub id: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum ThinkingLevel {
    Off,
    Minimal,
    Low,
    Medium,
    High,
    Xhigh,
    Max,
}

impl PiModel {
    pub fn new(provider: &str, id: &str) -> Option<Self> {
        (Self::valid_part(provider) && Self::valid_part(id)).then(|| Self {
            provider: provider.into(),
            id: id.into(),
        })
    }

    fn valid_part(value: &str) -> bool {
        !value.trim().is_empty()
            && value.len() <= 256
            && !value.starts_with('-')
            && !value.chars().any(char::is_control)
    }

    pub(crate) fn validate_selection(&self) -> Result<()> {
        if !Self::valid_part(&self.provider) || !Self::valid_part(&self.id) {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "Pi 供应商或模型 ID 无效",
            ));
        }
        Ok(())
    }
}

#[cfg_attr(
    not(all(target_os = "macos", target_arch = "aarch64")),
    allow(dead_code)
)]
pub(crate) struct PiRuntime {
    pub executable: PathBuf,
    pub entry: Option<PathBuf>,
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    pub(super) command_sandbox: Option<Box<pi::WindowsSandbox>>,
    pub config_dir: PathBuf,
    pub version: semver::Version,
    pub(super) protected: Vec<PathBuf>,
    pub model: Option<PiModel>,
    native_fork_model_identity: Option<Sha256>,
    pub thinking_levels: Vec<ThinkingLevel>,
    pub check_model_in_session: bool,
    pub sandbox_message: Option<String>,
    pub host_commands_message: Option<String>,
}

impl PiRuntime {
    pub fn verify_native_fork(&self, anchor: &NativeForkAnchor) -> Result<()> {
        if !native_fork_version_supported(&self.version)
            || !Path::new(NATIVE_FORK_SANDBOX).is_file()
            || self.config_dir != anchor.config_dir
            || !NativeForkConfig::read(&self.config_dir)
                .is_ok_and(|identity| identity == anchor.config_identity)
            || self
                .model
                .as_ref()
                .is_some_and(|model| model != &anchor.model)
            || self
                .native_fork_model_identity
                .as_ref()
                .is_some_and(|identity| identity != &anchor.model_identity)
        {
            return Err(native_fork_unavailable());
        }
        Ok(())
    }

    pub fn verify_commands(&self, allow_commands: bool, execution: CommandExecution) -> Result<()> {
        match execution {
            CommandExecution::Host => {
                verify_host_commands_platform()?;
                if !allow_commands {
                    return Err(YssError::new(
                        ErrorCode::InvalidInput,
                        "本机命令需要显式授权至少一个工程运行命令",
                    ));
                }
                if let Some(message) = &self.host_commands_message {
                    return Err(host_commands_unavailable(message));
                }
            }
            CommandExecution::Sandbox => {
                if allow_commands && let Some(message) = &self.sandbox_message {
                    return Err(YssError::new(ErrorCode::NotReady, message)
                        .context("piIssue", "commandsUnavailable")
                        .context("httpStatus", 409));
                }
            }
        }
        Ok(())
    }

    pub fn verify_expected(&self, expected: Option<&PiModel>) -> Result<()> {
        if let Some(model) = &self.model {
            Self::verify_model_choice(model, expected)?;
        }
        Ok(())
    }

    pub(crate) fn verify_model_choice(model: &PiModel, expected: Option<&PiModel>) -> Result<()> {
        if expected.is_some_and(|expected| expected != model) {
            return Err(
                YssError::new(ErrorCode::Conflict, "Pi 模型配置已变化，请重新检测后重试")
                    .context("piIssue", "modelChanged"),
            );
        }
        Ok(())
    }

    pub fn verify_thinking(&self, selected: Option<ThinkingLevel>) -> Result<()> {
        Self::verify_thinking_choice(&self.thinking_levels, selected)
    }

    pub(crate) fn verify_thinking_choice(
        levels: &[ThinkingLevel],
        selected: Option<ThinkingLevel>,
    ) -> Result<()> {
        if selected.is_some_and(|level| !levels.contains(&level)) {
            return Err(YssError::new(
                ErrorCode::Conflict,
                "所选模型不支持这个推理级别，请重新选择",
            )
            .context("piIssue", "thinkingLevelUnavailable"));
        }
        Ok(())
    }
}

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct RuntimeStatus {
    pub available: bool,
    pub managed: bool,
    pub explicit_executable: bool,
    pub sandbox_ready: bool,
    pub sandbox_message: Option<String>,
    #[serde(skip)]
    pub sandbox_issue: Option<&'static str>,
    pub host_commands_supported: bool,
    pub host_commands_ready: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub host_commands_message: Option<String>,
    pub version: Option<semver::Version>,
    pub model: Option<PiModel>,
    pub thinking_levels: Vec<ThinkingLevel>,
    pub thinking_level: Option<ThinkingLevel>,
    pub context_window: Option<u64>,
    pub credentials_ready: Option<bool>,
    pub config_dir: Option<String>,
    pub executable: Option<String>,
    pub node_executable: Option<String>,
    pub issue: Option<String>,
    pub message: Option<String>,
}

#[derive(Serialize)]
pub(crate) struct RuntimeStatusView {
    #[serde(flatten)]
    status: RuntimeStatus,
    capabilities: RuntimeCapabilities,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeCapabilities {
    pi: RuntimeCapability,
    sandbox: RuntimeCapability,
    host_commands: RuntimeCapability,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeCapability {
    state: CapabilityState,
    reason: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    reason_code: Option<String>,
    isolation: CapabilityIsolation,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
enum CapabilityState {
    Available,
    Blocked,
    Unsupported,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
enum CapabilityIsolation {
    PiTools,
    Offline,
    None,
}

impl RuntimeCapability {
    fn new(
        supported: bool,
        ready: bool,
        isolation: CapabilityIsolation,
        reason: Option<&str>,
        reason_code: Option<&str>,
    ) -> Self {
        let state = if !supported {
            CapabilityState::Unsupported
        } else if ready {
            CapabilityState::Available
        } else {
            CapabilityState::Blocked
        };
        Self {
            state,
            reason: reason.map(str::to_owned),
            reason_code: reason_code.map(str::to_owned),
            isolation,
        }
    }
}

impl RuntimeStatus {
    pub(crate) fn with_capabilities(self) -> RuntimeStatusView {
        let capabilities = RuntimeCapabilities {
            pi: RuntimeCapability::new(
                supported(),
                self.available,
                CapabilityIsolation::PiTools,
                self.message.as_deref(),
                self.issue
                    .as_deref()
                    .or((!supported()).then_some("platformUnsupported")),
            ),
            sandbox: RuntimeCapability::new(
                supported(),
                self.sandbox_ready,
                CapabilityIsolation::Offline,
                if self.sandbox_ready {
                    None
                } else if !supported() {
                    Some("当前平台不支持 Pi 沙箱离线命令")
                } else {
                    self.sandbox_message
                        .as_deref()
                        .or(self.message.as_deref())
                        .or(Some("沙箱离线命令未就绪"))
                },
                (!self.sandbox_ready).then_some(if !supported() {
                    "platformUnsupported"
                } else {
                    self.sandbox_issue.unwrap_or("commandsUnavailable")
                }),
            ),
            host_commands: RuntimeCapability::new(
                self.host_commands_supported,
                self.host_commands_ready,
                CapabilityIsolation::None,
                if self.host_commands_ready {
                    None
                } else if !self.host_commands_supported {
                    Some(HOST_COMMANDS_PLATFORM_MESSAGE)
                } else {
                    self.host_commands_message
                        .as_deref()
                        .or(Some("本机命令未就绪"))
                },
                (!self.host_commands_ready).then_some(if self.host_commands_supported {
                    "hostCommandsUnavailable"
                } else {
                    "platformUnsupported"
                }),
            ),
        };
        RuntimeStatusView {
            status: self,
            capabilities,
        }
    }
}

#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
pub fn verify_managed_pi_package(
    executable: &Path,
    entry: &Path,
    expected: &semver::Version,
    cancel: &Cancellation,
) -> Result<()> {
    pi::verify_managed_package(executable, entry, expected, cancel)
}

#[cfg_attr(
    not(all(target_os = "macos", target_arch = "aarch64")),
    allow(dead_code)
)]
pub(crate) struct RunRequest<'a> {
    pub runtime: PiRuntime,
    pub data_dir: &'a Path,
    pub run_id: &'a str,
    pub cwd: &'a Path,
    pub primary_project: &'a str,
    pub workspaces: &'a [Workspace],
    pub unselected_projects: &'a [PathBuf],
    pub prompt: &'a str,
    pub citations: &'a [Value],
    pub resume: Option<&'a PiSession>,
    pub native_fork: Option<&'a NativeForkAnchor>,
    pub expected_model: Option<&'a PiModel>,
    pub thinking_level: Option<ThinkingLevel>,
    pub remembered_thinking: Option<(PiModel, ThinkingLevel)>,
    pub read_only: bool,
    pub allow_commands: bool,
    pub command_execution: CommandExecution,
    pub explicit_access: bool,
}

#[derive(Clone, Copy)]
pub(super) enum CommandChecks {
    All,
    Round(Option<CommandExecution>),
}

impl CommandChecks {
    pub(super) fn includes(self, execution: CommandExecution) -> bool {
        matches!(self, Self::All)
            || matches!(self, Self::Round(Some(selected)) if selected == execution)
    }
}

const fn host_commands_supported() -> bool {
    cfg!(all(target_os = "macos", target_arch = "aarch64"))
}

const HOST_COMMANDS_PLATFORM_MESSAGE: &str = "本机命令目前支持 macOS ARM64";

fn host_commands_unavailable(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
        .context("piIssue", "hostCommandsUnavailable")
        .context("httpStatus", 409)
}

pub(super) fn verify_host_commands_platform() -> Result<()> {
    if !host_commands_supported() {
        return Err(host_commands_unavailable(HOST_COMMANDS_PLATFORM_MESSAGE));
    }
    Ok(())
}

pub(crate) const fn supported() -> bool {
    cfg!(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))
}

#[cfg(not(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
)))]
fn unsupported() -> YssError {
    YssError::new(
        ErrorCode::NotReady,
        "Pi Agent 支持 macOS ARM64 与 Windows x64",
    )
}

fn check(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    commands: CommandChecks,
    check_model_in_session: bool,
) -> Result<PiRuntime> {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        pi::check_runtime(
            cancel,
            status,
            protected,
            model,
            commands,
            check_model_in_session,
        )
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (
            cancel,
            status,
            protected,
            model,
            commands,
            check_model_in_session,
        );
        Err(unsupported())
    }
}

pub(crate) fn runtime_status(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
) -> RuntimeStatus {
    let mut status = RuntimeStatus {
        host_commands_supported: host_commands_supported(),
        ..RuntimeStatus::default()
    };
    match check(
        cancel,
        &mut status,
        protected,
        model,
        CommandChecks::All,
        false,
    ) {
        Ok(_) => status.available = true,
        Err(mut error) => {
            status.issue = error.context.remove("piIssue");
            status.message = Some(error.message);
        }
    }
    status
}

pub(crate) fn require_runtime(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    commands: Option<CommandExecution>,
) -> Result<PiRuntime> {
    check(
        cancel,
        &mut RuntimeStatus::default(),
        protected,
        model,
        CommandChecks::Round(commands),
        false,
    )
}

pub(crate) fn prepare_runtime(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    commands: Option<CommandExecution>,
) -> Result<PiRuntime> {
    check(
        cancel,
        &mut RuntimeStatus::default(),
        protected,
        model,
        CommandChecks::Round(commands),
        true,
    )
}

pub(crate) fn available_models(
    cancel: &Cancellation,
    protected: &[PathBuf],
) -> Result<Vec<PiModel>> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::available_models(cancel, protected)
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        pi::probe_base(cancel)?;
        pi::available_models(cancel, protected)
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (cancel, protected);
        Err(unsupported())
    }
}

pub(crate) fn test_model(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    expected: &PiModel,
) -> Result<()> {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        pi::test_model(cancel, protected, model, expected)
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (cancel, protected, model, expected);
        Err(unsupported())
    }
}

pub(super) fn generate_continuation_draft(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: &PiModel,
    system_prompt: &str,
    input: &str,
) -> Result<String> {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        pi::generate_continuation_draft(cancel, protected, model, system_prompt, input)
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (cancel, protected, model, system_prompt, input);
        Err(unsupported())
    }
}

pub(crate) fn session_available(
    data_dir: &Path,
    run_id: &str,
    cwd: &Path,
    session: &PiSession,
) -> bool {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        pi::verify_session(data_dir, run_id, cwd, session).is_ok()
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (data_dir, run_id, cwd, session);
        false
    }
}

pub(crate) fn run(
    request: RunRequest<'_>,
    context: &JobContext,
    save_session: impl FnMut(Option<PiSession>, Value) -> Result<()>,
    ask: impl Fn(&JobContext, &str) -> Result<String> + Send + 'static,
) -> Result<()> {
    #[cfg(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    ))]
    {
        pi::run(request, context, save_session, ask)
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "windows", target_arch = "x86_64")
    )))]
    {
        let _ = (request, context, save_session, ask);
        Err(unsupported())
    }
}
