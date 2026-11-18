use super::{agent_workspace::Workspace, jobs::JobContext};
#[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
use crate::error::{ErrorCode, YssError};
use crate::{cancel::Cancellation, error::Result};
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

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct RuntimeStatus {
    pub available: bool,
    pub sandbox_ready: bool,
    pub version: Option<semver::Version>,
    pub message: Option<String>,
}

#[cfg_attr(
    not(all(target_os = "macos", target_arch = "aarch64")),
    allow(dead_code)
)]
pub(crate) struct RunRequest<'a> {
    pub executable: &'a Path,
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

fn check(cancel: &Cancellation, status: &mut RuntimeStatus) -> Result<PathBuf> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        pi::check_runtime(cancel, status)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (cancel, status);
        Err(unsupported())
    }
}

pub(crate) fn runtime_status(cancel: &Cancellation) -> RuntimeStatus {
    let mut status = RuntimeStatus::default();
    match check(cancel, &mut status) {
        Ok(_) => status.available = true,
        Err(error) => status.message = Some(error.message),
    }
    status
}

pub(crate) fn require_runtime(cancel: &Cancellation) -> Result<PathBuf> {
    check(cancel, &mut RuntimeStatus::default())
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
