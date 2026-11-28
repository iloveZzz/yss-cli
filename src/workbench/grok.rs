use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{
        agent_protocol::{PiModel, ThinkingLevel},
        jobs::JobContext,
    },
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
mod acp;
#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
mod launch;
#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
mod macos;
#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
mod source;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(super) struct Session {
    pub id: String,
    pub identity: String,
    #[serde(default)]
    pub scope_identity: String,
    #[serde(default)]
    pub git_scope_identity: Option<String>,
}

pub(super) struct Runtime {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    expected_model: PiModel,
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    expected_identity: String,
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    select_default: bool,
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    installation: launch::Installation,
}

fn not_ready(issue: &str, message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message).context("grokIssue", issue)
}

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
fn model_changed() -> YssError {
    YssError::new(ErrorCode::Conflict, "Grok Build 模型已变化，请重新选择")
        .context("grokIssue", "modelChanged")
}

#[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
fn unavailable() -> YssError {
    not_ready("platform", "本机平台尚未支持 Grok Build 只读分析")
}

pub(super) fn probe(
    cancel: &Cancellation,
    protected: &[PathBuf],
    selected: Option<&PiModel>,
) -> Value {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    let status = (|| -> Result<Value> {
        let (installation, catalog, identity) = macos::inspect(cancel, protected)?;
        let entry = catalog.select(selected)?;
        Ok(json!({
            "available":true, "model":entry.model,
            "thinkingLevels":entry.efforts, "thinkingLevel":entry.effort,
            "identity":identity, "executable":installation.binary, "version":macos::VERSION,
            "message":"Grok Build 协议、登录和模型目录已核对"
        }))
    })();
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    let status: Result<Value> = {
        let _ = (cancel, protected, selected);
        Err(unavailable())
    };
    status.unwrap_or_else(|error| {
        json!({
            "available":false,
            "issue":error.context.get("grokIssue").map(String::as_str).unwrap_or("runtime"),
            "message":error.message,
            "model":null, "thinkingLevels":[], "thinkingLevel":null,
            "executable":error.context.get("executable"),
            "version":error.context.get("version"),
            "loginCommand":error.context.get("loginCommand"),
        })
    })
}

pub(super) fn models(cancel: &Cancellation, protected: &[PathBuf]) -> Result<Vec<PiModel>> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        let (_, catalog, _) = macos::inspect(cancel, protected)?;
        Ok(catalog
            .entries
            .into_iter()
            .map(|entry| entry.model)
            .collect())
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (cancel, protected);
        Err(unavailable())
    }
}

pub(super) fn prepare_run(
    cancel: &Cancellation,
    protected: &[PathBuf],
    selected: Option<&PiModel>,
    expected: &PiModel,
    expected_identity: &str,
) -> Result<Runtime> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        if selected.is_some_and(|model| model != expected) {
            return Err(model_changed());
        }
        if expected.provider != "xai" || expected.validate_selection().is_err() {
            return Err(not_ready(
                "modelUnavailable",
                "所选 Grok Build 模型已不可用，请重新选择",
            ));
        }
        Ok(Runtime {
            expected_model: expected.clone(),
            expected_identity: expected_identity.into(),
            select_default: selected.is_none(),
            installation: launch::Installation::discover(protected, cancel)?,
        })
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (cancel, protected, selected, expected, expected_identity);
        Err(unavailable())
    }
}

pub(super) fn session_available(data_dir: &Path, run_id: &str, session: &Session) -> bool {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        launch::session_available(data_dir, run_id, &session.id)
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (data_dir, run_id, session);
        false
    }
}

#[allow(clippy::too_many_arguments)]
pub(super) fn run(
    runtime: Runtime,
    data_dir: &Path,
    run_id: &str,
    project_root: &Path,
    excluded: &[PathBuf],
    scope_identity: &str,
    request: &str,
    source_task: &str,
    spec_path: &str,
    handoff: Option<&str>,
    resume: Option<&Session>,
    effort: Option<ThinkingLevel>,
    context: &JobContext,
    persist: impl FnMut(Session, Value) -> Result<()>,
    before_prompt: impl FnOnce() -> Result<()>,
) -> Result<()> {
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        macos::run(
            runtime,
            data_dir,
            run_id,
            project_root,
            excluded,
            scope_identity,
            request,
            source_task,
            spec_path,
            handoff,
            resume,
            effort,
            context,
            persist,
            before_prompt,
        )
    }
    #[cfg(not(all(target_os = "macos", target_arch = "aarch64")))]
    {
        let _ = (
            runtime,
            data_dir,
            run_id,
            project_root,
            excluded,
            scope_identity,
            request,
            source_task,
            spec_path,
            handoff,
            resume,
            effort,
            context,
            persist,
            before_prompt,
        );
        Err(unavailable())
    }
}
