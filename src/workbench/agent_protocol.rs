use super::{agent_workspace::Workspace, jobs::JobContext};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::{Path, PathBuf};

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
#[path = "pi/mod.rs"]
mod pi;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub(crate) struct PiSession {
    pub id: String,
    pub file: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PiModel {
    pub provider: String,
    pub id: String,
}

impl PiModel {
    pub fn new(provider: &str, id: &str) -> Option<Self> {
        let valid = |value: &str| {
            !value.trim().is_empty()
                && value.len() <= 256
                && !value.starts_with('-')
                && !value.chars().any(char::is_control)
        };
        (valid(provider) && valid(id)).then(|| Self {
            provider: provider.into(),
            id: id.into(),
        })
    }
}

#[cfg_attr(
    not(all(target_os = "macos", target_arch = "aarch64")),
    allow(dead_code)
)]
pub(crate) struct PiRuntime {
    pub executable: PathBuf,
    pub config_dir: PathBuf,
    pub version: semver::Version,
    pub model: PiModel,
}

impl PiRuntime {
    pub fn verify_expected(&self, expected: Option<&PiModel>) -> Result<()> {
        if expected.is_some_and(|expected| *expected != self.model) {
            return Err(
                YssError::new(ErrorCode::Conflict, "Pi 模型配置已变化，请重新检测后重试")
                    .context("piIssue", "modelChanged"),
            );
        }
        Ok(())
    }
}

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct RuntimeStatus {
    pub available: bool,
    pub sandbox_ready: bool,
    pub version: Option<semver::Version>,
    pub model: Option<PiModel>,
    pub credentials_ready: Option<bool>,
    pub config_dir: Option<String>,
    pub executable: Option<String>,
    pub issue: Option<String>,
    pub message: Option<String>,
}

#[cfg_attr(
    not(all(target_os = "macos", target_arch = "aarch64")),
    allow(dead_code)
)]
pub(crate) struct RunRequest<'a> {
    pub runtime: &'a PiRuntime,
    pub data_dir: &'a Path,
    pub run_id: &'a str,
    pub cwd: &'a Path,
    pub primary_project: &'a str,
    pub workspaces: &'a [Workspace],
    pub prompt: &'a str,
    pub resume: Option<&'a PiSession>,
    pub allow_commands: bool,
}

pub(crate) const fn supported() -> bool {
    cfg!(all(target_os = "macos", target_arch = "aarch64"))
}

#[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
fn unsupported() -> YssError {
    YssError::new(ErrorCode::NotReady, "Pi Agent 运行当前仅支持 macOS ARM64")
}

fn check(
    cancel: &Cancellation,
    status: &mut RuntimeStatus,
    protected: &[PathBuf],
    model: Option<&PiModel>,
) -> Result<PiRuntime> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::check_runtime(cancel, status, protected, model)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (cancel, status, protected, model);
        Err(unsupported())
    }
}

pub(crate) fn runtime_status(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
) -> RuntimeStatus {
    let mut status = RuntimeStatus::default();
    match check(cancel, &mut status, protected, model) {
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
) -> Result<PiRuntime> {
    check(cancel, &mut RuntimeStatus::default(), protected, model)
}

pub(crate) fn test_model(
    cancel: &Cancellation,
    protected: &[PathBuf],
    model: Option<&PiModel>,
    expected: &PiModel,
) -> Result<()> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::test_model(cancel, protected, model, expected)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (cancel, protected, model, expected);
        Err(unsupported())
    }
}

pub(crate) fn session_available(
    data_dir: &Path,
    run_id: &str,
    cwd: &Path,
    session: &PiSession,
) -> bool {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::verify_session(data_dir, run_id, cwd, session).is_ok()
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
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
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::run(request, context, save_session, ask)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (request, context, save_session, ask);
        Err(unsupported())
    }
}
