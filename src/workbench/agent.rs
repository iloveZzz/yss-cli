use super::{
    agent_protocol::{self, PiModel, PiRuntime, PiSession, RunRequest, ThinkingLevel},
    agent_scope::{self, ContinuationSource, Handoff},
    agent_workspace::{self, GitBaseline, Workspace},
    ci_job::{CiJobMode, CiJobSelection},
    content as spec_content, grok,
    jobs::{JobContext, JobManager, JobRecord, PiPhase, lock_store},
    model::{
        Action, MutationRequest, QuestionKind, QuestionResponse, QuestionState, QuestionStatus,
        ResourceHealth, ResourceKind, ResponseKind, SpecMetadata, SpecRecord, Stage, TargetType,
        WorkbenchState,
    },
    progress_agent::{self, RoundBinding},
    sources,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{BuildInfo, FileDescriptor, MAX_ASSET, RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    borrow::Cow,
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex, OnceLock},
};

mod acceptance;
mod admission;
mod diff_hunks;
pub(super) use admission::start_async;
pub(super) mod activity;
pub(super) mod local_tasks;
pub(super) mod native_fork;
pub(super) mod queue;
pub(super) mod reads;

pub(super) const REVIEW_RECEIPT: &str = acceptance::RECEIPT;
const DELETED_SESSION: &str = "deletedAgentSession";

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AgentStartRequest {
    pub spec_id: String,
    pub prompt: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(super) host: Option<AgentHost>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    expected_runtime_identity: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    handoff: Option<Handoff>,
    #[serde(default)]
    pub project_ids: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub project_access: Option<Vec<ProjectAccess>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    expected_repository_keys: Option<BTreeMap<String, Option<Sha256>>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    continuation_source: Option<ContinuationSource>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    expected_continuation_digest: Option<Sha256>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    native_fork_source: Option<native_fork::NativeForkSource>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    ci_job: Option<CiJobSelection>,
    #[serde(default)]
    pub carry_files: BTreeMap<String, Vec<String>>,
    pub request_id: String,
    pub resume_run_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_job_id: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub citations: Vec<ReviewCitation>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub selected_model: Option<PiModel>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_model: Option<PiModel>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub thinking_level: Option<ThinkingLevel>,
    #[serde(default = "default_read_only")]
    pub read_only: bool,
    #[serde(default)]
    pub allow_commands: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(crate) command_execution: Option<CommandExecution>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    host_execution_confirmed: bool,
    #[serde(default)]
    pub lifecycle_stage: Option<Stage>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_goal_version: Option<String>,
}

impl AgentStartRequest {
    fn validate_handoff(&self, store: &mut Store, spec_id: &str, host: AgentHost) -> Result<()> {
        let Some(handoff) = &self.handoff else {
            return Ok(());
        };
        let missing = agent_scope::handoff_confirmation_required;
        agent_scope::validate_handoff(
            store,
            spec_id,
            handoff,
            agent_scope::HandoffBinding {
                host,
                runtime_identity: self
                    .expected_runtime_identity
                    .as_deref()
                    .ok_or_else(missing)?,
                expected_model: self.expected_model.as_ref().ok_or_else(missing)?,
                thinking_level: self.thinking_level,
                project_access: self.project_access.as_deref().ok_or_else(missing)?,
                expected_repository_keys: self.expected_repository_keys.as_ref(),
                command_execution: self.command_execution,
            },
        )
    }

    fn selected_host(&self, prior: Option<&AgentRun>) -> AgentHost {
        self.host
            .or_else(|| prior.map(|run| run.host))
            .unwrap_or(AgentHost::Pi)
    }

    fn verify_runtime_identity(&self, actual: Option<&str>, host: AgentHost) -> Result<()> {
        let expected = self.expected_runtime_identity.as_deref();
        if (host == AgentHost::Grok || self.handoff.is_some() || expected.is_some())
            && (expected.is_none() || expected != actual)
        {
            return Err(YssError::new(
                ErrorCode::Conflict,
                "本轮 Agent CLI 的运行配置已变化，请重新核对后发送",
            )
            .context("agentIssue", "runtimeChanged")
            .context("httpStatus", 409));
        }
        Ok(())
    }

    fn command_execution(&self) -> CommandExecution {
        self.command_execution.unwrap_or_default()
    }
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub(crate) enum CommandExecution {
    #[default]
    Sandbox,
    Host,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ProjectMode {
    Read,
    Edit,
    Execute,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectAccess {
    pub project_id: String,
    pub mode: ProjectMode,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_head: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub expected_root_path: Option<String>,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        deserialize_with = "deserialize_expected_branch"
    )]
    pub expected_branch: Option<Option<String>>,
}

fn deserialize_expected_branch<'de, D>(
    deserializer: D,
) -> std::result::Result<Option<Option<String>>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    // Preserve the distinction between an omitted pin and a detached checkout.
    Option::<String>::deserialize(deserializer).map(Some)
}

impl ProjectAccess {
    fn narrowed(&self, read_only: bool, allow_commands: bool) -> Self {
        Self {
            project_id: self.project_id.clone(),
            mode: match self.mode {
                _ if read_only => ProjectMode::Read,
                ProjectMode::Execute if !allow_commands => ProjectMode::Edit,
                mode => mode,
            },
            expected_head: None,
            expected_root_path: None,
            expected_branch: None,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReviewCitation {
    review_token: String,
    project_id: String,
    path: String,
    side: CitationSide,
    start_line: usize,
    end_line: usize,
    note: String,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum CitationSide {
    Initial,
    Result,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub(super) enum AgentHost {
    #[default]
    Codex,
    Pi,
    Grok,
}

impl AgentHost {
    pub(super) fn parse(value: Option<&str>) -> Result<Self> {
        match value {
            None | Some("pi") => Ok(Self::Pi),
            Some("grok") => Ok(Self::Grok),
            _ => Err(invalid("Agent CLI 无效")),
        }
    }

    fn current(self) -> bool {
        matches!(self, Self::Pi | Self::Grok)
    }
}
fn default_read_only() -> bool {
    true
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub(super) enum WorkspaceMode {
    #[default]
    Isolated,
    Direct,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentRun {
    pub id: String,
    pub spec_id: String,
    pub prompt: String,
    pub project_ids: Vec<String>,
    #[serde(default)]
    pub(super) session_title: String,
    pub status: String,
    pub job_id: String,
    #[serde(default)]
    pub binding_revision: u64,
    #[serde(default)]
    pub(super) host: AgentHost,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pi_session: Option<PiSession>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    grok_session: Option<grok::Session>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    handoff: Option<Handoff>,
    pub thread_id: Option<String>,
    pub turn_id: Option<String>,
    pub host_settings: Option<Value>,
    #[serde(default)]
    workspace_mode: WorkspaceMode,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    snapshot_error: Option<String>,
    #[serde(default)]
    pub(super) preparation_failed: bool,
    pub workspaces: Vec<Workspace>,
    pub answers: Vec<Value>,
    pub applied_files: Vec<Value>,
    pub created_at: String,
    pub updated_at: String,
    #[serde(default)]
    pub lifecycle_stage: Option<Stage>,
    #[serde(default)]
    pub goal_version: Option<String>,
    #[serde(default)]
    pub spec_digest: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    read_only: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub project_access: Option<Vec<ProjectAccess>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    command_execution: Option<CommandExecution>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    queue_project_access: Option<Vec<ProjectAccess>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    continuation_source: Option<ContinuationSource>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    native_fork_source: Option<ContinuationSource>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(super) ci_job: Option<Value>,
}

impl AgentRun {
    fn queue_access(&self) -> Option<&[ProjectAccess]> {
        self.queue_project_access
            .as_deref()
            .or(self.project_access.as_deref())
    }

    fn can_edit_queued_projects(&self) -> bool {
        self.host == AgentHost::Pi
            && self.workspace_mode == WorkspaceMode::Direct
            && self
                .queue_access()
                .is_none_or(|grants| grants.iter().any(|grant| grant.mode != ProjectMode::Read))
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiffFile {
    pub project_id: String,
    pub git_root: String,
    pub path: String,
    pub workspace_path: String,
    pub initial: Option<FileDescriptor>,
    pub baseline: Option<FileDescriptor>,
    pub result: Option<FileDescriptor>,
    pub current: Option<FileDescriptor>,
    pub conflict: bool,
    pub accepted: bool,
    pub content_matches: bool,
    pub pending: bool,
    pub reviewed_before: Option<String>,
    pub reviewed_before_descriptor: Option<FileDescriptor>,
    pub reviewed_before_known: bool,
    pub accepted_review_token: Option<String>,
    pub binary: bool,
    pub initial_text: Option<String>,
    pub result_text: Option<String>,
    pub current_text: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunDiff {
    pub run_id: String,
    pub job_id: String,
    pub historical: bool,
    workspace_mode: WorkspaceMode,
    pub review_token: String,
    pub git_baselines: BTreeMap<String, GitBaseline>,
    pub files: Vec<DiffFile>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AcceptFile {
    pub project_id: String,
    pub path: String,
    pub expected_current: Option<FileDescriptor>,
    pub expected_result: Option<FileDescriptor>,
    #[serde(default)]
    pub approve_conflict: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AcceptRequest {
    pub review_token: String,
    #[serde(default)]
    pub job_id: Option<String>,
    #[serde(default)]
    pub attempt_id: Option<String>,
    pub files: Vec<AcceptFile>,
}
fn invalid(message: impl Into<String>) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
fn load(store: &mut Store, id: &str) -> Result<AgentRun> {
    let mut run: AgentRun = store
        .record("agentRun", id)?
        .ok_or_else(|| invalid("Agent 运行不存在").context("httpStatus", 404))?;
    if run.id != id {
        return Err(invalid("运行记录 ID 不一致"));
    }
    let mut mapped = BTreeSet::new();
    for workspace in &run.workspaces {
        if !Path::new(&workspace.original_root).is_absolute() {
            return Err(invalid("运行原工程根必须是绝对路径"));
        }
        for project_id in &workspace.project_ids {
            if !run.project_ids.contains(project_id) || !mapped.insert(project_id) {
                return Err(invalid("运行工程映射不一致"));
            }
            let prefix = workspace
                .project_prefixes
                .get(project_id)
                .ok_or_else(|| invalid("运行工程缺少路径映射"))?;
            if !prefix.is_empty() {
                RelPath::new(prefix)?;
            }
            let project = sources::project(store.state(), project_id)?;
            if Path::new(&workspace.original_root).join(prefix) != Path::new(&project.root_path) {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "运行记录与当前登记工程路径不一致",
                )
                .context("resumeReason", "workspaceChanged"));
            }
        }
        for source in &workspace.resource_sources {
            if workspace.project_prefixes.get(&source.project_id) != Some(&source.project_prefix) {
                return Err(invalid("运行资源来源的工程路径映射不一致"));
            }
            RelPath::new(&source.source_path)?;
        }
    }
    if let Some(job) = store.record::<JobRecord>("job", &run.job_id)? {
        run.status = job.status;
    }
    Ok(run)
}
fn save_preparing_run(context: &JobContext, run: &mut AgentRun) -> Result<()> {
    let mut store = lock_store(&context.store)?;
    let latest = load(&mut store, &run.id)?;
    if latest.job_id != run.job_id || latest.binding_revision != run.binding_revision {
        return Err(stale_round(&latest));
    }
    let ci_evidence = (latest.ci_job.is_none())
        .then_some(run.ci_job.as_ref())
        .flatten()
        .map(|payload| super::sqlite::EventWrite {
            job_id: &context.id,
            kind: "ciEvidence",
            payload,
        });
    // An answer can arrive through the existing API while files are being captured.
    run.answers = latest.answers;
    store.put_record_with_event("agentRun", &run.id, run, ci_evidence)
}
pub fn result(store: &mut Store, run_id: &str) -> Result<AgentRun> {
    load(store, run_id)
}
fn session_project_cwd(project: &str, run: &AgentRun) -> Result<PathBuf> {
    run.workspaces
        .iter()
        .find_map(|workspace| {
            let prefix = workspace.project_prefixes.get(project)?;
            Some(Path::new(&workspace.worktree_root).join(prefix))
        })
        .ok_or_else(|| invalid("运行缺少主 Spec 工程"))
}
fn resume_error(reason: &str, message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
        .context("resumeReason", reason)
        .context("httpStatus", 409)
}

fn grok_scope(
    state: &WorkbenchState,
    spec: &SpecRecord,
    root: &str,
) -> Result<(Vec<PathBuf>, Sha256)> {
    let root_key = fs::alias_key(root.trim_end_matches('/'));
    let prefix = format!("{root_key}/");
    let mut excluded = BTreeMap::new();
    for project in &state.projects {
        let key = fs::alias_key(&project.root_path);
        if project.id != spec.project_id && (key == root_key || key.starts_with(&prefix)) {
            excluded.insert(key, PathBuf::from(&project.root_path));
        }
    }
    let digest = agent_scope::digest(&(
        "grok-project-scope.v1",
        &spec.project_id,
        &spec.source_path,
        &root_key,
        excluded.keys().collect::<Vec<_>>(),
    ))?;
    Ok((excluded.into_values().collect(), digest))
}

fn check_resume_session(state: &WorkbenchState, data_dir: &Path, run: &AgentRun) -> Result<()> {
    if !run.host.current() || run.workspace_mode != WorkspaceMode::Direct {
        return Err(resume_error(
            "legacySession",
            "该历史会话仅供审阅，可从原 Spec 新建会话",
        ));
    }
    if !terminal(&run.status) {
        return Err(resume_error("roundActive", "当前轮次仍在执行"));
    }
    let supported = match run.host {
        AgentHost::Pi => agent_protocol::supported(),
        AgentHost::Grok => cfg!(all(target_os = "macos", target_arch = "aarch64")),
        AgentHost::Codex => false,
    };
    if !supported {
        return Err(resume_error(
            "platformUnsupported",
            "当前平台无法续接此 Agent CLI 会话",
        ));
    }
    let workspace_changed = || {
        resume_error(
            "workspaceChanged",
            "原工作区已变化或不可用，请从 Spec 新建会话",
        )
    };
    let spec = sources::spec(state, &run.spec_id).map_err(|_| workspace_changed())?;
    if !spec.active
        || run
            .project_ids
            .iter()
            .any(|id| !sources::project(state, id).is_ok_and(|project| project.active))
    {
        return Err(workspace_changed());
    }
    let cwd = session_project_cwd(&spec.project_id, run).map_err(|_| workspace_changed())?;
    if !run.workspaces.iter().all(|workspace| {
        Root::open(Path::new(&workspace.worktree_root))
            .is_ok_and(|root| root.identity == workspace.worktree_identity)
    }) {
        return Err(workspace_changed());
    }
    if run.host == AgentHost::Grok {
        let session = run
            .grok_session
            .as_ref()
            .filter(|session| grok::session_available(data_dir, &run.id, session))
            .ok_or_else(|| {
                resume_error(
                    "grokSessionUnavailable",
                    "原 Grok 会话记录缺失，请从 Spec 新建会话",
                )
            })?;
        if session.git_scope_identity.is_none() {
            return Err(resume_error(
                "grokScopeChanged",
                "原 Grok 会话缺少可核对的 Git 边界记录，请从 Spec 新建会话",
            ));
        }
        let project = sources::project(state, &spec.project_id).map_err(|_| workspace_changed())?;
        let (_, scope) = grok_scope(state, spec, &project.root_path)?;
        if session.scope_identity != scope.as_str() {
            return Err(resume_error(
                "grokScopeChanged",
                "主工程授权范围已变化，请从 Spec 新建 Grok 会话",
            ));
        }
        return Ok(());
    }
    if !run
        .pi_session
        .as_ref()
        .is_some_and(|session| agent_protocol::session_available(data_dir, &run.id, &cwd, session))
    {
        return Err(resume_error(
            "piSessionUnavailable",
            "原 Pi 会话记录缺失或无效，请从 Spec 新建会话",
        ));
    }
    Ok(())
}

fn ensure_handoff_source_idle(store: &mut Store, id: &str) -> Result<()> {
    if store.records::<JobRecord>("job")?.iter().any(|job| {
        !job.terminal()
            && job
                .agent_handoff_source
                .as_ref()
                .is_some_and(|source| source.run_id == id)
            && job.handoff_submitted.is_none()
    }) {
        return Err(YssError::new(
            ErrorCode::Locked,
            "有待提交的跨 CLI 交接，请先处理该作业",
        ));
    }
    Ok(())
}

fn check_dispatch_outcome(store: &mut Store, run: &AgentRun) -> Result<()> {
    if store
        .record::<JobRecord>("job", &run.job_id)?
        .is_some_and(|job| job.terminal() && job.agent_dispatch_uncertain)
    {
        return Err(resume_error(
            "dispatchUncertain",
            "原轮次已尝试提交给 CLI，执行结果待核实，请从 Spec 新建独立任务",
        ));
    }
    Ok(())
}

fn recorded_model(run: &AgentRun) -> Result<PiModel> {
    let settings = run.host_settings.as_ref();
    settings
        .and_then(|value| PiModel::new(value["provider"].as_str()?, value["model"].as_str()?))
        .ok_or_else(|| {
            YssError::new(ErrorCode::NotReady, "原会话缺少模型记录，请选择本轮模型")
                .context("piIssue", "modelUnavailable")
        })
}

pub(crate) fn pi_protected_paths(store: &Store) -> Vec<PathBuf> {
    std::iter::once(store.data_dir().to_owned())
        .chain(
            store
                .state()
                .projects
                .iter()
                .map(|project| PathBuf::from(&project.root_path)),
        )
        .collect()
}

pub(crate) fn runtime_target(
    store: &mut Store,
    resume_id: Option<&str>,
    selected_model: Option<PiModel>,
) -> Result<(Vec<PathBuf>, Option<PiModel>)> {
    let (protected, model, _) =
        runtime_target_for(store, resume_id, selected_model, AgentHost::Pi)?;
    Ok((protected, model))
}

pub(super) fn runtime_target_for(
    store: &mut Store,
    resume_id: Option<&str>,
    selected_model: Option<PiModel>,
    host: AgentHost,
) -> Result<(Vec<PathBuf>, Option<PiModel>, Option<String>)> {
    if let Some(selected) = &selected_model {
        selected.validate_selection()?;
    }
    let mut model = selected_model;
    let mut resume_identity = None;
    if let Some(id) = resume_id {
        let run = load(store, id)?;
        if run.host != host {
            return Err(resume_error("hostChanged", "续接须使用原会话的 Agent CLI"));
        }
        check_dispatch_outcome(store, &run)?;
        check_resume_session(store.state(), store.data_dir(), &run)?;
        if model.is_none() {
            model = Some(recorded_model(&run)?);
        }
        resume_identity = run.grok_session.map(|session| session.identity);
    }
    Ok((pi_protected_paths(store), model, resume_identity))
}

pub fn view(shared: &Arc<Mutex<Store>>, run_id: &str, cancel: &Cancellation) -> Result<Value> {
    let mut store = lock_store(shared)?;
    let current = load(&mut store, run_id);
    let recovery = match &current {
        Ok(run) if terminal(&run.status) => {
            acceptance::recovery_block(&mut store, &acceptance::roots(run))?
        }
        _ => None,
    };
    let mut resume_issue = current
        .as_ref()
        .err()
        .cloned()
        .or_else(|| {
            recovery
                .clone()
                .map(|error| error.context("resumeReason", "recoveryRequired"))
        })
        .or_else(|| {
            current
                .as_ref()
                .ok()
                .and_then(|run| check_resume_session(store.state(), store.data_dir(), run).err())
        });
    let run = match current {
        Ok(run) => run,
        Err(error) if error.code == ErrorCode::ConcurrentChange => {
            store.record::<AgentRun>("agentRun", run_id)?.ok_or(error)?
        }
        Err(error) => return Err(error),
    };
    if resume_issue.is_none() {
        resume_issue = check_dispatch_outcome(&mut store, &run).err();
    }
    let mut native_issue = None;
    if resume_issue.is_none() {
        let jobs = store.records::<JobRecord>("job")?;
        if jobs
            .iter()
            .any(|job| !job.terminal() && job.session_run_id() == Some(run_id))
        {
            native_issue = Some(resume_error(
                "roundActive",
                "此会话已有准备或运行中的轮次或本机任务",
            ));
        } else if let Err(error) = queue::check_start(&mut store, Some(run_id), None) {
            if error.code != ErrorCode::Conflict {
                return Err(error);
            }
            native_issue = Some(error.context("resumeReason", "queuePending"));
        }
        if native_issue.is_none()
            && let Err(error) = ensure_handoff_source_idle(&mut store, run_id)
        {
            native_issue = Some(error.context("resumeReason", "handoffPending"));
        }
        if native_issue.is_none() {
            let roots = acceptance::roots(&run);
            if let Err(error) = super::jobs::check_roots(&jobs, &roots, None)
                .and_then(|_| ensure_roots_available(&mut store, &roots))
            {
                let reason = if error.code == ErrorCode::RecoveryRequired {
                    "commandRecoveryRequired"
                } else {
                    "workspaceBusy"
                };
                native_issue = Some(error.context("resumeReason", reason));
            }
        }
    }
    let rebindings = store
        .records::<Value>("agentRebind")?
        .into_iter()
        .filter(|item| item["runId"] == run_id)
        .collect::<Vec<_>>();
    drop(store);
    if resume_issue.is_none() {
        resume_issue = run
            .workspaces
            .iter()
            .find_map(|workspace| agent_workspace::validate_resume(workspace, cancel).err());
    }
    cancel.check()?;
    let snapshot_error = snapshot_issue(&run).map(str::to_owned);
    let can_edit_queued_projects = run.can_edit_queued_projects();
    let mut value = serde_json::to_value(run).map_err(|_| invalid("执行详情编码失败"))?;
    let fields = value.as_object_mut().expect("AgentRun is an object");
    fields.remove("piSession");
    fields.remove("grokSession");
    fields.remove("queueProjectAccess");
    fields.insert(
        "canEditQueuedProjects".into(),
        json!(can_edit_queued_projects),
    );
    fields.insert("canResume".into(), json!(resume_issue.is_none()));
    fields.insert(
        "resumeIssue".into(),
        json!(resume_issue.as_ref().map(|error| &error.message)),
    );
    fields.insert(
        "resumeReason".into(),
        json!(
            resume_issue
                .as_ref()
                .and_then(|error| error.context.get("resumeReason"))
        ),
    );
    let native_issue = resume_issue.as_ref().or(native_issue.as_ref());
    fields.insert(
        "actions".into(),
        json!({"resumeNative": action_availability(
            native_issue.map(|error| error.message.as_str()),
            native_issue.map(|error| error.context.get("resumeReason").map_or("workspaceChanged", String::as_str)),
        )}),
    );
    if let Some(error) = snapshot_error {
        fields.insert("snapshotError".into(), json!(error));
    }
    fields.insert(
        "recoveryJobId".into(),
        json!(
            recovery
                .as_ref()
                .and_then(|error| error.context.get("jobId"))
        ),
    );
    fields.insert(
        "recoveryRunId".into(),
        json!(
            recovery
                .as_ref()
                .and_then(|error| error.context.get("runId"))
        ),
    );
    fields.insert("rebindings".into(), json!(rebindings));
    Ok(value)
}

pub fn sessions(jobs: &JobManager, spec_id: &str) -> Result<Value> {
    let (spec, projects, source_error, runs) = {
        let mut store = lock_store(&jobs.store)?;
        let spec = sources::spec(store.state(), spec_id)?.clone();
        let source_error = sources::read_source(store.state(), spec_id)
            .err()
            .map(|error| error.message);
        let projects: Vec<_> = store.state().projects.iter().filter(|project| project.active || project.id == spec.project_id)
            .map(|project| json!({"id":project.id,"name":project.name,"rootPath":project.root_path,"active":project.active})).collect();
        let mut runs = Vec::new();
        for mut run in store
            .records::<AgentRun>("agentRun")?
            .into_iter()
            .filter(|run| run.host.current() && run.spec_id == spec_id)
        {
            let job: Option<JobRecord> = store.record("job", &run.job_id)?;
            if let Some(job) = &job {
                run.status = job.status.clone();
                if job.updated_at > run.updated_at {
                    run.updated_at.clone_from(&job.updated_at);
                }
            }
            let attempt = acceptance::summary(&mut store, &run);
            runs.push((run, job, attempt));
        }
        (spec, projects, source_error, runs)
    };
    let mut summaries = Vec::new();
    for (run, job, attempt) in runs {
        let mut attention = Vec::new();
        let mut review_error = None;
        let mut reports = Vec::new();
        match attempt {
            Ok(Some(attempt)) => {
                reports = attempt["projects"].as_array().cloned().unwrap_or_default();
            }
            Err(error) => review_error = Some(error.message),
            _ => {}
        }
        if run.status == "recoveryRequired"
            || reports.iter().any(|report| {
                report["disposition"]
                    .as_str()
                    .is_some_and(acceptance::requires_verification)
            })
        {
            attention.push(json!({"kind":"recovery"}));
        }
        if job.is_some() {
            attention.extend(super::activities::agent_attention(jobs, &run.job_id)?);
        }
        match run.status.as_str() {
            "interrupted" => attention.push(json!({"kind":"interrupted"})),
            "failed" => attention.push(json!({"kind":"failed"})),
            _ => {}
        }
        let mut targets = Vec::new();
        let pending = if reviewable(&run) {
            match pending_review_files(&run, &mut targets) {
                Ok(count) => (run.workspace_mode == WorkspaceMode::Isolated).then_some(count),
                Err(error) => {
                    review_error = Some(error.message);
                    targets.clear();
                    None
                }
            }
        } else {
            None
        };
        if pending.is_some_and(|count| count > 0) {
            attention.push(json!({"kind":"review","count":pending}));
        }
        let title = if run.session_title.is_empty() {
            &run.prompt
        } else {
            &run.session_title
        };
        let delete_issue = deletion_issue(&run.status, &reports);
        summaries.push((run.updated_at.clone(), run.id.clone(), json!({
            "id":run.id,"title":title,"jobId":run.job_id,"host":run.host,"status":run.status,"updatedAt":run.updated_at,
            "attention":attention, "pendingFiles":pending, "reviewError":review_error, "reviewTargets":targets,
            "hasSnapshot":reviewable(&run), "hasAcceptance":!reports.is_empty(), "deleteIssue":delete_issue,
            "workspaceMode":run.workspace_mode, "snapshotError":snapshot_issue(&run)
        })));
    }
    summaries.sort_unstable_by(|a, b| (&b.0, &b.1).cmp(&(&a.0, &a.1)));
    Ok(
        json!({"spec":spec,"sourceError":source_error,"projects":projects,
        "sessions":summaries.into_iter().map(|(_,_,item)|item).collect::<Vec<_>>() }),
    )
}

pub fn rounds(store: &mut Store, id: &str) -> Result<Value> {
    let run: Option<AgentRun> = store.record("agentRun", id)?;
    let Some(run) = run else {
        let job: JobRecord = store
            .record("job", id)?
            .filter(|job: &JobRecord| {
                job.id == id
                    && job.kind == "agent"
                    && job.agent_spec_id.is_some()
                    && job.agent_run_id.as_deref() == Some(id)
            })
            .ok_or_else(|| invalid("Agent 会话不存在").context("httpStatus", 404))?;
        let spec_id = job.agent_spec_id.as_deref().expect("job scope checked");
        let spec_active = sources::spec(store.state(), spec_id)
            .ok()
            .map(|spec| spec.active);
        let actions = round_actions(&job, spec_id, id, spec_active);
        return Ok(
            json!({"rounds": [job], "snapshotJobIds": [], "roundActions": {(&job.id): actions}}),
        );
    };
    if run.id != id {
        return Err(invalid("运行记录 ID 不一致"));
    }
    let bound = job_rounds(store, &run)?;
    let bound_ids: BTreeSet<_> = bound.iter().map(|job| job.id.as_str()).collect();
    let admitted_at = |job: &JobRecord| {
        time::OffsetDateTime::parse(
            &job.created_at,
            &time::format_description::well_known::Rfc3339,
        )
        .ok()
    };
    let mut reservations: Vec<_> = store
        .records::<JobRecord>("job")?
        .into_iter()
        .filter(|job| {
            (!job.terminal() || (job.agent_started_at.is_none() && job.status != "completed"))
                && !bound_ids.contains(job.id.as_str())
                && job.kind == "agent"
                && job.agent_run_id.as_deref() == Some(id)
                && job.agent_spec_id.as_deref() == Some(run.spec_id.as_str())
        })
        .map(|job| (admitted_at(&job), job))
        .collect();
    reservations.sort_by(|(left, a), (right, b)| (left, &a.id).cmp(&(right, &b.id)));
    let mut rounds = Vec::with_capacity(bound.len() + reservations.len());
    let mut reservations = reservations.into_iter().peekable();
    // Insert admitted attempts by time while preserving the lineage order of bound rounds.
    for job in bound {
        let at = admitted_at(&job);
        while let Some((_, reserved)) = reservations.next_if(|(time, _)| *time < at) {
            rounds.push(reserved);
        }
        rounds.push(job);
    }
    rounds.extend(reservations.map(|(_, job)| job));
    let mut snapshots = Vec::new();
    let mut round_ci_jobs = BTreeMap::new();
    let mut actions = BTreeMap::new();
    let spec_active = sources::spec(store.state(), &run.spec_id)
        .ok()
        .map(|spec| spec.active);
    for job in &rounds {
        actions.insert(&job.id, round_actions(job, &run.spec_id, id, spec_active));
        let (saved, evidence) = if job.id == run.job_id {
            (
                terminal(&job.status) && has_frozen_outcome(&run),
                run.ci_job.clone(),
            )
        } else {
            (
                store.has_record("agentRound", &job.id)?,
                store.ci_evidence(&job.id)?,
            )
        };
        if saved {
            snapshots.push(&job.id);
        }
        if let Some(evidence) = evidence {
            round_ci_jobs.insert(&job.id, evidence);
        }
    }
    Ok(
        json!({"rounds": rounds, "snapshotJobIds": snapshots, "roundCiJobs": round_ci_jobs,
        "roundActions": actions}),
    )
}

fn action_availability(reason: Option<&str>, code: Option<&str>) -> Value {
    match (reason, code) {
        (Some(reason), Some(code)) => {
            json!({"available": false, "reason": reason, "reasonCode": code})
        }
        (Some(reason), None) => json!({"available": false, "reason": reason}),
        (None, _) => json!({"available": true, "reason": null}),
    }
}

fn round_actions(job: &JobRecord, spec_id: &str, run_id: &str, spec_active: Option<bool>) -> Value {
    // A round reaches this projection only through the run's recorded lineage or an explicit
    // (Spec, run) pair on an admitted job that has yet to bind.
    let continuation = if let Err(error) = agent_scope::check_round_source(job, spec_id, run_id) {
        action_availability(Some(&error.message), Some("roundActive"))
    } else {
        match spec_active {
            Some(true) => action_availability(None, None),
            Some(false) => action_availability(Some("原 Spec 已归档"), Some("specArchived")),
            None => action_availability(Some("原 Spec 不可用"), Some("specUnavailable")),
        }
    };
    json!({"viewHistory": action_availability(None, None), "continueSameSpec": continuation})
}

fn job_rounds(store: &mut Store, run: &AgentRun) -> Result<Vec<JobRecord>> {
    let mut items = Vec::new();
    let mut seen = BTreeSet::new();
    let mut next = Some(run.job_id.clone());
    while let Some(job_id) = next.take() {
        if !seen.insert(job_id.clone()) {
            break;
        }
        let Some(job) = store.record::<JobRecord>("job", &job_id)? else {
            break;
        };
        if job.id != job_id || !agent_scope::job_matches_session(&job, &run.spec_id, &run.id) {
            break;
        }
        let event = store.run_event(&job_id)?.unwrap_or(Value::Null);
        if items.is_empty() || event["runId"] == run.id {
            next = event["previousJobId"].as_str().map(str::to_owned);
            items.push(job);
        }
    }
    items.reverse();
    Ok(items)
}

fn deletion_issue(status: &str, reports: &[Value]) -> Option<&'static str> {
    if !terminal(status)
        || reports
            .iter()
            .any(|report| report["disposition"] == "writing")
    {
        Some("会话仍有运行或接收操作，请结束后再删除")
    } else if status == "recoveryRequired"
        || reports.iter().any(|report| {
            report["disposition"]
                .as_str()
                .is_some_and(acceptance::requires_verification)
        })
    {
        Some("会话有待恢复或核实的结果，请处理后再删除")
    } else {
        None
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct DeleteSessionRequest {
    expected_job_id: String,
}

pub(crate) fn delete_session(
    jobs: &JobManager,
    id: &str,
    request: DeleteSessionRequest,
) -> Result<Value> {
    use super::sqlite::RecordWrite;

    let mut store = lock_store(&jobs.store)?;
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    super::command_recovery::ensure_history_available(&mut store, None, Some(id))?;
    let result = json!({"runId":id, "deleted":true});
    let Some(mut run) = store.record::<AgentRun>("agentRun", id)? else {
        if store.record::<String>(DELETED_SESSION, id)?.as_deref() == Some(&request.expected_job_id)
        {
            return Ok(result);
        }
        return Err(invalid("Pi 会话不存在").context("httpStatus", 404));
    };
    if run.id != id || !run.host.current() {
        return Err(invalid("Agent 会话不存在").context("httpStatus", 404));
    }
    ensure_handoff_source_idle(&mut store, id)?;
    if run.job_id != request.expected_job_id {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "会话已有新轮次，请刷新后重新确认删除",
        ));
    }
    if let Some(job) = store.record::<JobRecord>("job", &run.job_id)? {
        run.status = job.status;
    }
    let mut related: BTreeMap<_, _> = job_rounds(&mut store, &run)?
        .into_iter()
        .map(|job| (job.id.clone(), job))
        .collect();
    // Resume reservations and local project tasks can belong to the session without an agent round.
    for job in store
        .records::<JobRecord>("job")?
        .into_iter()
        .filter(|job| {
            job.session_run_id() == Some(id)
                && job
                    .agent_spec_id
                    .as_deref()
                    .is_none_or(|spec| spec == run.spec_id)
        })
    {
        related.insert(job.id.clone(), job);
    }
    if related.values().any(|job| !job.terminal()) || acceptance::lease_active(&mut store, id)? {
        return Err(YssError::new(
            ErrorCode::Locked,
            "会话仍有运行或接收操作，请结束后再删除",
        ));
    }
    let acceptance = acceptance::summary(&mut store, &run)?;
    let reports = acceptance
        .as_ref()
        .and_then(|value| value["projects"].as_array());
    if let Some(issue) = deletion_issue(&run.status, reports.map(Vec::as_slice).unwrap_or_default())
    {
        return Err(YssError::new(ErrorCode::Conflict, issue));
    }

    let mut job_ids: BTreeSet<_> = related.keys().cloned().collect();
    job_ids.insert(run.job_id.clone());
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    if super::command_recovery::unresolved(&mut store)?
        .iter()
        .any(|record| job_ids.contains(&record.job_id))
    {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "会话轮次仍有关联的命令暂存，请先处理恢复记录",
        ));
    }
    let remove = |kind: &str, id: &str| RecordWrite {
        kind: kind.into(),
        id: id.into(),
        payload: None,
    };
    let mut records = vec![
        RecordWrite {
            kind: DELETED_SESSION.into(),
            id: id.into(),
            payload: Some(json!(run.job_id).to_string()),
        },
        remove("agentRun", id),
        remove(queue::KIND, id),
        remove(acceptance::ATTEMPT, id),
        remove("agentAcceptance", id),
    ];
    for job in related.into_values() {
        records.push(remove("jobRequest", &job.request_id));
        records.push(remove("requestAck", &job.request_id));
        records.push(RecordWrite {
            kind: super::jobs::DELETED_REQUEST.into(),
            id: job.request_id,
            payload: Some("true".into()),
        });
    }
    for job in &job_ids {
        records.push(remove("job", job));
        records.push(remove("agentRound", job));
        records.push(remove(agent_protocol::NATIVE_FORK_ANCHOR_RECORD, job));
        records.push(remove(native_fork::SOURCE_ROUND, job));
    }
    for (key, rebind) in store.record_entries::<Value>("agentRebind")? {
        if rebind["runId"] == id {
            records.push(remove("agentRebind", &key));
        }
    }
    store.delete_agent_history(id, &job_ids, records)?;
    Ok(result)
}

fn pending_review_files(run: &AgentRun, targets: &mut Vec<(String, String)>) -> Result<usize> {
    let mut count = 0;
    for workspace in &run.workspaces {
        let root = (run.workspace_mode == WorkspaceMode::Isolated)
            .then(|| Root::open(Path::new(&workspace.original_root)))
            .transpose()?;
        if root
            .as_ref()
            .is_some_and(|root| root.identity != workspace.original_identity)
        {
            return Err(stale_round(run));
        }
        let result = workspace.result_snapshot.as_ref().expect("frozen review");
        for path in review_paths(run, workspace, result) {
            let receipt = last_accepted_file(run, workspace, path);
            let baseline = receipt
                .map(accepted_result)
                .transpose()?
                .unwrap_or_else(|| workspace.initial.get(path).cloned());
            let proposed = result.get(path);
            if receipt.is_some() || proposed != baseline.as_ref() {
                let (project, relative) = review_path(workspace, path)?;
                targets.push((project.to_owned(), relative.to_owned()));
            }
            if let Some(root) = &root
                && proposed != baseline.as_ref()
                && current_descriptor(root, path)?.as_ref() != proposed
            {
                count += 1;
            }
        }
    }
    Ok(count)
}

pub(super) fn review_path<'a>(
    workspace: &'a Workspace,
    path: &'a str,
) -> Result<(&'a str, &'a str)> {
    workspace
        .project_prefixes
        .iter()
        .filter_map(|(id, prefix)| {
            let relative = if prefix.is_empty() {
                path
            } else {
                path.strip_prefix(prefix)?.strip_prefix('/')?
            };
            Some((prefix.len(), id.as_str(), relative))
        })
        .max_by_key(|(depth, _, _)| *depth)
        .map(|(_, id, relative)| (id, relative))
        .ok_or_else(|| invalid("成果文件无法映射到已选择工程"))
}
fn validate_start_request(request: &AgentStartRequest, queued: bool) -> Result<()> {
    if request.host == Some(AgentHost::Codex) {
        return Err(invalid("历史 Codex 会话仅供审阅"));
    }
    if request.handoff.is_some()
        && (queued
            || request.host.is_none()
            || request.native_fork_source.is_some()
            || request.continuation_source.is_some()
            || request.expected_continuation_digest.is_some()
            || request.resume_run_id.is_some()
            || request.prompt.len().saturating_add(
                request
                    .handoff
                    .as_ref()
                    .map_or(0, |handoff| handoff.context.len()),
            ) > 128 * 1024)
    {
        return Err(invalid(
            "交接须选择明确的目标 CLI 并新建会话，交接与任务合计最多 128 KiB",
        ));
    }
    if request
        .expected_runtime_identity
        .as_ref()
        .is_some_and(|identity| {
            identity.len() != 64 || !identity.bytes().all(|byte| byte.is_ascii_hexdigit())
        })
    {
        return Err(invalid("Agent CLI 运行配置标识无效"));
    }
    if (request.host == Some(AgentHost::Grok) || request.handoff.is_some())
        && request.expected_repository_keys.is_none()
    {
        return Err(invalid("请核对本轮各工程的仓库标识后发送"));
    }
    if request.native_fork_source.is_some() {
        if queued
            || request.resume_run_id.is_some()
            || request.continuation_source.is_some()
            || request.expected_continuation_digest.is_some()
            || request.expected_job_id.is_some()
        {
            return Err(invalid("原生分叉只能单独发起新 Pi 会话"));
        }
        if !request.read_only
            || request.allow_commands
            || request.command_execution() != CommandExecution::Sandbox
            || request.host_execution_confirmed
            || request
                .project_access
                .as_ref()
                .is_some_and(|grants| grants.iter().any(|grant| grant.mode != ProjectMode::Read))
        {
            return Err(invalid("原生分叉只允许读取来源工程文件"));
        }
        if request.ci_job.is_some()
            || !request.citations.is_empty()
            || request.lifecycle_stage.is_some()
            || request.expected_goal_version.is_some()
        {
            return Err(invalid("原生分叉只使用本轮任务、原工程和 Pi 历史上下文"));
        }
    }
    if let Some(keys) = &request.expected_repository_keys
        && (queued
            || request.project_access.as_ref().is_none_or(|grants| {
                grants.len() != keys.len()
                    || grants
                        .iter()
                        .any(|grant| !keys.contains_key(&grant.project_id))
            }))
    {
        return Err(invalid("仓库标识必须与本轮选择的工程授权对应"));
    }
    if request.command_execution() == CommandExecution::Host {
        if queued {
            return Err(invalid("本机命令只能由用户手动发起本轮运行"));
        }
        if !request.host_execution_confirmed {
            return Err(invalid("请在运行设置中明确选择本轮本机直接执行"));
        }
        if !request.project_access.as_ref().is_some_and(|access| {
            access
                .iter()
                .any(|grant| grant.mode == ProjectMode::Execute)
        }) {
            return Err(invalid("本机命令需要显式授权至少一个工程运行命令"));
        }
    }
    if request.project_access.is_none() && request.read_only && request.allow_commands {
        return Err(invalid("只读模式不能运行隔离命令"));
    }
    if !queued
        && request.project_access.as_ref().is_some_and(|access| {
            access.iter().any(|grant| {
                grant.expected_head.is_none()
                    || grant.expected_branch.is_none()
                    || grant
                        .expected_root_path
                        .as_deref()
                        .is_none_or(|root| !Path::new(root).is_absolute())
            })
        })
    {
        return Err(invalid(
            "请重新核对各参与工程的登记路径、分支和 Git HEAD 后发送",
        ));
    }
    if request.project_access.as_ref().is_some_and(|access| {
        access.iter().any(|grant| {
            grant.expected_head.as_ref().is_some_and(|head| {
                !matches!(head.len(), 40 | 64)
                    || !head
                        .bytes()
                        .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
            })
        })
    }) {
        return Err(invalid("工程预期 HEAD 须为完整的 Git 提交 SHA"));
    }
    if request.command_execution() == CommandExecution::Host {
        agent_protocol::verify_host_commands_platform()?;
    }
    if request.prompt.trim().is_empty() || request.prompt.len() > 128 * 1024 {
        return Err(invalid("请填写 128 KiB 以内的续办要求"));
    }
    if let Some(selected) = &request.selected_model {
        selected.validate_selection()?;
    }
    if request.citations.len() > 12 {
        return Err(invalid("单轮最多可引用 12 段差异"));
    }
    if request.resume_run_id.is_some() && request.continuation_source.is_some() {
        return Err(invalid("接续来源仅用于新建会话"));
    }
    if request.expected_continuation_digest.is_some() && request.continuation_source.is_none() {
        return Err(invalid("接续摘要版本须对应本次新建会话的来源轮次"));
    }
    if request.resume_run_id.is_none()
        && (!request.citations.is_empty() || request.expected_job_id.is_some())
    {
        return Err(invalid("差异引用与来源轮次只用于同一会话的续接"));
    }
    Ok(())
}

fn validate_host_scope(
    request: &AgentStartRequest,
    host: AgentHost,
    spec: &SpecRecord,
    queued: bool,
) -> Result<()> {
    if host != AgentHost::Grok {
        return Ok(());
    }
    if queued
        || !request.read_only
        || request.allow_commands
        || request.command_execution.is_some()
        || request.host_execution_confirmed
        || request.native_fork_source.is_some()
        || request.continuation_source.is_some()
        || request.lifecycle_stage.is_some()
        || request.expected_goal_version.is_some()
        || request.ci_job.is_some()
        || !request.citations.is_empty()
        || request.project_ids.as_slice() != [spec.project_id.as_str()]
    {
        return Err(invalid("Grok Build 当前仅支持主 Spec 工程的只读会话"));
    }
    if request
        .project_access
        .as_ref()
        .is_none_or(|grants| grants.len() != 1 || grants[0].mode != ProjectMode::Read)
        || request.expected_model.is_none()
        || request.expected_runtime_identity.is_none()
        || request.expected_repository_keys.is_none()
    {
        return Err(invalid(
            "请核对 Grok Build 运行配置、模型和主工程只读授权后发送",
        ));
    }
    Ok(())
}

fn verify_repository_keys(
    request: &AgentStartRequest,
    host: AgentHost,
    workspaces: &[Workspace],
    cancel: &Cancellation,
) -> Result<()> {
    let Some((keys, grants)) = request
        .expected_repository_keys
        .as_ref()
        .zip(request.project_access.as_ref())
    else {
        return Ok(());
    };
    let verify_read_only = request.command_execution() == CommandExecution::Host
        || host == AgentHost::Grok
        || request.handoff.is_some();
    for workspace in workspaces {
        let mut checked = grants
            .iter()
            .filter(|grant| verify_read_only || grant.mode != ProjectMode::Read)
            .filter_map(|grant| {
                workspace
                    .project_prefixes
                    .get(&grant.project_id)
                    .map(|prefix| (&grant.project_id, prefix))
            })
            .peekable();
        let Some((first_id, _)) = checked.peek() else {
            continue;
        };
        let origin =
            super::integrations::checkout_origin(Path::new(&workspace.original_root), cancel)
                .map_err(|error| {
                    if error.code == ErrorCode::Cancelled {
                        error
                    } else {
                        error
                            .context("projectScopeChanged", true)
                            .context("projectId", first_id)
                    }
                })?;
        for (id, prefix) in checked {
            let current = super::integrations::repository_key(origin.as_deref(), prefix);
            if keys.get(id) != Some(&current) {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "工程仓库标识已变化，请重新核对本轮工程范围和权限",
                )
                .context("httpStatus", 409)
                .context("projectScopeChanged", true)
                .context("projectId", id));
            }
        }
    }
    Ok(())
}

pub fn start(manager: Arc<JobManager>, mut request: AgentStartRequest) -> Result<JobRecord> {
    validate_start_request(&request, false)?;
    let cancel = Cancellation::new();
    let (projects, prior, spec, title, mut protected, fork_target) = {
        let mut store = lock_store(&manager.store)?;
        let admission::Target {
            spec,
            projects,
            prior,
            protected,
        } = admission::capture_target(&mut store, &mut request)?;
        if request.selected_host(prior.as_ref()) != AgentHost::Pi || request.handoff.is_some() {
            return Err(invalid("此轮请使用支持异步受理的 Agent 工作区"));
        }
        if let Some(prior) = &prior {
            ensure_handoff_source_idle(&mut store, &prior.id)?;
        }
        super::agent_scope::check_expected(
            &mut store,
            &spec.id,
            request.continuation_source.as_ref(),
            request.expected_continuation_digest.as_ref(),
        )?;
        let fork = native_fork::capture_expected(&mut store, &request, &projects)?;
        let digest = Sha256::of(&serde_json::to_vec(&request).map_err(|e| invalid(e.to_string()))?);
        let title = format!("Spec 续办 · {} · {}", spec.title, &digest.as_str()[..8]);
        if fork.is_none() {
            sources::read_source(store.state(), &spec.id).map_err(|error| {
                YssError::new(
                    ErrorCode::NotReady,
                    format!("主 Spec 来源不可用：{}", error.message),
                )
                .context("httpStatus", 409)
            })?;
        }
        if request.carry_files.values().any(|paths| !paths.is_empty()) {
            return Err(invalid("Pi 直接读取原工程，请清空带入文件后重试"));
        }
        if let Some(prior) = &prior {
            check_resume_session(store.state(), store.data_dir(), prior)?;
            acceptance::ensure_recovered(&mut store, &acceptance::roots(prior))?;
        }
        (projects, prior, spec, title, protected, fork)
    };
    let fork = native_fork::verify_expected(fork_target, &request, &cancel)?;
    let roots: Vec<_> = projects
        .iter()
        .map(|(_, path)| {
            agent_workspace::agent_git_root(Path::new(path), &cancel)
                .map(|root| root.to_string_lossy().into_owned())
        })
        .collect::<Result<BTreeSet<_>>>()?
        .into_iter()
        .collect();
    protected.extend(roots.iter().map(PathBuf::from));
    let model = match (&request.selected_model, &prior, &fork) {
        (_, _, Some(fork)) => Some(fork.anchor.model.clone()),
        (None, Some(run), _) => Some(recorded_model(run)?),
        (selected, _, _) => selected.clone(),
    };
    let pi = agent_protocol::require_runtime(
        &cancel,
        &protected,
        model.as_ref(),
        request.allow_commands.then(|| request.command_execution()),
    )
    .map_err(|error| {
        if fork.is_some() {
            native_fork::runtime_unavailable(error)
        } else if request.selected_model.is_some() && error.code == ErrorCode::NotReady {
            // This request was rejected before creating a job.
            error.context("httpStatus", 409)
        } else {
            error
        }
    })?;
    if let Some(fork) = &fork {
        native_fork::verify_runtime(&pi, &fork.anchor)?;
    }
    pi.verify_expected(request.expected_model.as_ref())?;
    pi.verify_thinking(request.thinking_level)?;
    pi.verify_commands(request.allow_commands, request.command_execution())?;
    let citations = request.citations.clone();
    let verified_prior = prior
        .as_ref()
        .map(|run| (run.id.clone(), run.job_id.clone(), run.binding_revision));
    let verified_projects = projects.clone();
    let expected_spec = spec.clone();
    let lifecycle_stage = request.lifecycle_stage;
    let expected_goal_version = request.expected_goal_version.clone();
    let continuation_source = request.continuation_source.clone();
    let expected_continuation_digest = request.expected_continuation_digest.clone();
    let queued_run = request.resume_run_id.clone();
    manager.start_checked(
        "agent".into(),
        title,
        request.request_id.clone(),
        roots,
        |store, job| {
            queue::check_start(store, queued_run.as_deref(), None)?;
            super::agent_scope::check_expected(
                store,
                &spec.id,
                continuation_source.as_ref(),
                expected_continuation_digest.as_ref(),
            )?;
            if let Some(fork) = &fork {
                fork.recheck_source(store, &spec.id)?;
                job.agent_run_id = Some(job.id.clone());
                job.agent_spec_id = Some(spec.id.clone());
            }
            let current_spec = sources::spec(store.state(), &spec.id)?;
            if !current_spec.active
                || current_spec.source_path != spec.source_path
                || current_spec.project_id != spec.project_id
                || verified_projects.iter().any(|(id, root)| {
                    !sources::project(store.state(), id)
                        .is_ok_and(|p| p.active && p.root_path == *root)
                })
            {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "运行范围或 Spec 登记已变化，请刷新设置",
                ));
            }
            let verified_citations = if let Some((id, job_id, binding_revision)) = &verified_prior {
                let current = load(store, id)?;
                if current.job_id != *job_id || current.binding_revision != *binding_revision {
                    return Err(stale_round(&current));
                }
                ensure_handoff_source_idle(store, id)?;
                check_dispatch_outcome(store, &current)?;
                acceptance::ensure_recovered(store, &acceptance::roots(&current))?;
                check_resume_session(store.state(), store.data_dir(), &current)?;
                for workspace in &current.workspaces {
                    agent_workspace::validate_resume(workspace, &cancel)?;
                }
                job.agent_run_id = Some(id.clone());
                verify_citations(store, &current, &citations)?
            } else {
                Vec::new()
            };
            let binding = if fork.is_some() {
                None
            } else {
                progress_agent::select_binding(
                    store,
                    &spec.id,
                    lifecycle_stage,
                    expected_goal_version.as_deref(),
                    &cancel,
                )?
            };
            let phase_at = sources::now();
            job.pi_phase = Some(PiPhase::PrepareWorkspace);
            job.pi_phase_at = Some(phase_at.clone());
            job.updated_at = phase_at;
            Ok(PreparedRun {
                citations: verified_citations,
                binding,
                queued: false,
                fork,
            })
        },
        move |context, admission| {
            execute(
                context,
                request,
                projects,
                prior,
                AgentRuntime::Pi(pi),
                admission,
                expected_spec,
            )
        },
    )
}

fn stale_round(run: &AgentRun) -> YssError {
    YssError::new(
        ErrorCode::ConcurrentChange,
        "会话已有新轮次或工程绑定已变化，请打开最新轮并重新核对差异引用",
    )
    .context("httpStatus", 409)
    .context("jobId", &run.job_id)
}

fn verify_citations(
    store: &Store,
    run: &AgentRun,
    citations: &[ReviewCitation],
) -> Result<Vec<Value>> {
    verify_citation_contents(run, citations, &Cancellation::new(), &|descriptor| {
        frozen_bytes(store, descriptor)
    })
}

fn verify_citation_contents(
    run: &AgentRun,
    citations: &[ReviewCitation],
    cancel: &Cancellation,
    blob: &impl Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<Vec<Value>> {
    if citations.is_empty() {
        return Ok(Vec::new());
    }
    if !reviewable(run) {
        return Err(stale_round(run));
    }
    let preview = describe_diff(run, false, cancel)?;
    citations.iter().map(|citation| {
        if citation.review_token != preview.review_token {
            return Err(YssError::new(ErrorCode::ConcurrentChange, "差异版本已变化，请重新选行或移除旧引用").context("httpStatus", 409));
        }
        let mut file = preview.files.iter().find(|file| file.project_id == citation.project_id && file.path == citation.path).cloned()
            .ok_or_else(|| invalid("引用文件不在该工程的成果差异中"))?;
        hydrate_diff_file(run, false, &mut file, blob)?;
        let text = match citation.side {
            CitationSide::Initial => file.initial_text.as_deref(),
            CitationSide::Result => file.result_text.as_deref(),
        }.filter(|_| !file.binary).ok_or_else(|| invalid("引用侧没有可用文本"))?;
        let start = citation.start_line.checked_sub(1).ok_or_else(|| invalid("差异引用须从有效行号开始"))?;
        let lines: Vec<_> = text.lines().collect();
        let range = start..citation.end_line;
        if range.is_empty() || range.len() > 200 || citation.note.trim().is_empty() || citation.note.len() > 8 * 1024 {
            return Err(invalid("请选择 200 行以内的连续差异并填写 8 KiB 以内的调整意见"));
        }
        let snippet = lines.get(range).ok_or_else(|| invalid("差异引用行号超出文本"))?.join("\n");
        if snippet.len() > 24 * 1024 {
            return Err(invalid("所选差异文本过长，请缩小行范围"));
        }
        Ok(json!({
            "projectId": file.project_id, "path": file.path, "side": citation.side,
            "startLine": citation.start_line, "endLine": citation.end_line, "note": citation.note.trim(),
            "snippet": snippet, "runId": run.id, "jobId": run.job_id,
            "bindingRevision": run.binding_revision, "reviewToken": preview.review_token,
        }))
    }).collect()
}
fn include_run_response(records: &mut Vec<QuestionResponse>, response: QuestionResponse) {
    if records.iter().any(|record| record.id == response.id) {
        return;
    }
    let saved_at = |text: &str| {
        time::OffsetDateTime::parse(text, &time::format_description::well_known::Rfc3339).ok()
    };
    let incoming_at = saved_at(&response.created_at);
    // Accepting or resuming a run later must not replace a newer answer saved in the main workspace.
    let position = records
        .iter()
        .position(|record| {
            if record.spec_id != response.spec_id || record.question_id != response.question_id {
                return false;
            }
            match (incoming_at, saved_at(&record.created_at)) {
                (Some(incoming), Some(existing)) => incoming <= existing,
                _ => true,
            }
        })
        .unwrap_or(records.len());
    records.insert(position, response);
}

fn run_responses(run: &AgentRun) -> impl Iterator<Item = Result<QuestionResponse>> + '_ {
    run.answers
        .iter()
        .filter_map(|answer| answer.get("questionResponse"))
        .map(|value| serde_json::from_value(value.clone()).map_err(|e| invalid(e.to_string())))
}

fn mapped_state(store: &Store, run: &AgentRun) -> Result<WorkbenchState> {
    map_agent_state(store.state().clone(), run, |workspace| {
        view_root(store, &run.id, workspace, terminal(&run.status))
    })
}

fn map_agent_state(
    mut state: WorkbenchState,
    run: &AgentRun,
    root_for: impl Fn(&Workspace) -> Result<PathBuf>,
) -> Result<WorkbenchState> {
    for workspace in &run.workspaces {
        let root = root_for(workspace)?;
        for id in &workspace.project_ids {
            let project = state
                .projects
                .iter_mut()
                .find(|p| &p.id == id)
                .ok_or_else(|| invalid("运行工程记录已移除"))?;
            project.root_path = root
                .join(&workspace.project_prefixes[id])
                .to_string_lossy()
                .into();
        }
    }
    for response in run_responses(run) {
        include_run_response(&mut state.question_responses, response?);
    }
    Ok(state)
}

fn last_accepted_file<'a>(
    run: &'a AgentRun,
    workspace: &Workspace,
    path: &str,
) -> Option<&'a Value> {
    run.applied_files.iter().rev().find(|file| {
        file["worktreeRoot"].as_str() == Some(&workspace.worktree_root)
            && file["path"].as_str() == Some(path)
    })
}

fn accepted_result(record: &Value) -> Result<Option<FileDescriptor>> {
    serde_json::from_value(
        record
            .get("result")
            .ok_or_else(|| invalid("接收记录缺少文件版本"))?
            .clone(),
    )
    .map_err(|_| invalid("接收记录的文件版本无效"))
}

fn review_paths<'a>(
    run: &'a AgentRun,
    workspace: &'a Workspace,
    result: &'a agent_workspace::Snapshot,
) -> BTreeSet<&'a str> {
    workspace
        .initial
        .keys()
        .chain(result.keys())
        .map(String::as_str)
        .chain(run.applied_files.iter().filter_map(|file| {
            (file["worktreeRoot"].as_str() == Some(&workspace.worktree_root))
                .then(|| file["path"].as_str())
                .flatten()
        }))
        .filter(|path| agent_workspace::path_in_write_scope(workspace, path))
        .collect()
}

fn spec_workspace<'a>(run: &'a AgentRun, spec: &SpecRecord) -> Option<(&'a Workspace, String)> {
    let workspace = run
        .workspaces
        .iter()
        .find(|w| w.project_prefixes.contains_key(&spec.project_id))?;
    let prefix = &workspace.project_prefixes[&spec.project_id];
    let path = if prefix.is_empty() {
        spec.source_path.clone()
    } else {
        format!("{prefix}/{}", spec.source_path)
    };
    Some((workspace, path))
}

fn source_basis(run: &AgentRun, spec: &SpecRecord) -> Option<(&'static str, FileDescriptor)> {
    let (workspace, path) = spec_workspace(run, spec)?;
    if let Some(accepted) = last_accepted_file(run, workspace, &path) {
        let descriptor = serde_json::from_value(accepted.get("result")?.clone()).ok()?;
        return Some(("本次运行最近已接收的同一主 Spec", descriptor));
    }
    // The original checkout may have changed since this workspace was captured.
    if run.binding_revision != 0 {
        return None;
    }
    Some((
        "本次运行起点已带入的同一主 Spec",
        workspace.initial.get(&path)?.clone(),
    ))
}

fn source_basis_bytes(
    store: &Store,
    run: &AgentRun,
    descriptor: &FileDescriptor,
) -> Result<Vec<u8>> {
    frozen_bytes(store, descriptor)
        .or_else(|_| agent_workspace::initial_bytes(store.data_dir(), &run.id, descriptor))
}

fn material_versions(
    records: &super::materials::Records,
    state: &WorkbenchState,
    spec: &SpecRecord,
    workspace: &Workspace,
) -> Result<(String, Option<String>)> {
    let (view, assessment) = spec_content::inspect_record(state, &spec.id, records, false)?;
    let project = sources::project(state, &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path));
    let observed = sources::observe_spec(spec, &root);
    let included = |path: &str| {
        let prefix = &workspace.project_prefixes[&spec.project_id];
        if prefix.is_empty() {
            workspace.initial.contains_key(path)
        } else {
            workspace.initial.contains_key(&format!("{prefix}/{path}"))
        }
    };
    let mut resources = Vec::new();
    for resource in sources::resource_views(&observed.resources, &root) {
        let record = resource.record;
        let local = record.target_type == TargetType::Local;
        let path = local.then(|| RelPath::new(&record.target).ok()).flatten();
        let mut health = resource.health;
        if health == ResourceHealth::Available
            && (!included(&record.target)
                || (record.kind == ResourceKind::Prototype
                    && assessment.as_ref().is_some_and(|assessment| {
                        !assessment.observed_resources.contains_key(&record.target)
                    })))
        {
            health = ResourceHealth::Unreadable;
        }
        let mut entry = json!({"kind":record.kind,"path":path,"health":health});
        if record.kind == ResourceKind::Prototype && local {
            if health == ResourceHealth::Available {
                entry["preview"] = json!("有可打开的入口，浏览器交互待验证");
            } else {
                let src = record
                    .target
                    .strip_suffix("dist/index.html")
                    .filter(|parent| parent.is_empty() || parent.ends_with('/'))
                    .map(|parent| format!("{parent}index.html"))
                    .unwrap_or(record.target);
                let fallback = RelPath::new(&src).ok().filter(|path| {
                    included(path.as_str())
                        && root.as_ref().is_ok_and(|root| {
                            root.source_read(path, super::model::MAX_SOURCE)
                                .is_ok_and(|file| std::str::from_utf8(&file.bytes).is_ok())
                        })
                });
                entry["sourcePath"] = json!(fallback);
                entry["preview"] = json!("当前无可预览的构建产物，浏览器交互待验证");
            }
        }
        resources.push(entry);
    }
    let changes: Vec<_> = view
        .material_changes
        .iter()
        .filter_map(|change| {
            let path = RelPath::new(&change.target).ok()?;
            Some(json!({
                "target": path,
                "kind": change.kind,
                "confirmedDigest": change.confirmed_digest,
                "observedDigest": change.observed_digest,
            }))
        })
        .collect();
    let context = json!({
        "currentProjectResources": resources,
        "confirmedArtifactDigest": view.confirmed_version.as_ref().map(|record| &record.artifact_digest),
        "specDigest": view.artifact_digest,
        "observedArtifactDigest": view.observed_artifact_digest,
        "materialReviewDigest": view.material_review_digest,
        "materialChanges": changes,
        "metadataUnreadable": view.metadata_error.is_some(),
    });
    Ok((
        format!(
            "\n本轮原工程关联材料及版本\n{}\n按逐项可用状态选择材料。已有原型入口仍需实际在浏览器操作才能核验交互。材料确认沿用原决定和对应版本。\n",
            serde_json::to_string_pretty(&context).map_err(|_| invalid("关联材料版本编码失败"))?,
        ),
        view.artifact_digest,
    ))
}

fn check_spec_binding(current: &SpecRecord, expected: &SpecRecord) -> Result<()> {
    if !current.active
        || current.project_id != expected.project_id
        || current.source_path != expected.source_path
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "启动前主 Spec 登记已变化，请重新核对来源",
        ));
    }
    Ok(())
}

fn local_task_results(store: &mut Store, run_id: &str, source_job_id: &str) -> Result<Vec<Value>> {
    // A round that failed before Pi accepted its prompt did not receive these results.
    let mut source_rounds = BTreeSet::new();
    let mut previous = Some(source_job_id.to_owned());
    while let Some(id) = previous.take() {
        let Some(round) = store.record::<JobRecord>("job", &id)? else {
            break;
        };
        let round_session = round.agent_run_id.as_deref().unwrap_or(&round.id);
        if round.kind != "agent" || round_session != run_id || !source_rounds.insert(id.clone()) {
            break;
        }
        if round.agent_started_at.is_some() || round.status == "completed" {
            break;
        }
        if let Some(event) = store.run_event(&id)?
            && event["runId"] == run_id
        {
            previous = event["previousJobId"].as_str().map(str::to_owned);
        }
    }
    let mut jobs = store.records::<JobRecord>("job")?;
    jobs.retain(|job| {
        job.terminal()
            && job.local_project_task.as_ref().is_some_and(|task| {
                task.run_id == run_id && source_rounds.contains(&task.source_job_id)
            })
    });
    jobs.sort_unstable_by(|left, right| {
        (&right.created_at, &right.id).cmp(&(&left.created_at, &left.id))
    });
    Ok(jobs
        .into_iter()
        .take(10)
        .filter_map(|job| {
            let task = job.local_project_task?;
            let result = job.result.unwrap_or(Value::Null);
            Some(json!({
                "jobId": job.id, "projectId": task.project_id, "directory": task.directory,
                "action": task.action, "status": job.status, "exitCode": result["exitCode"],
                "errorCode": job.error.as_ref().and_then(|error| error["code"].as_str()),
                "terminationSignal": result["terminationSignal"], "artifactPaths": result["artifactPaths"],
            }))
        })
        .collect())
}

enum AgentRuntime {
    Pi(PiRuntime),
    Grok(grok::Runtime),
}

impl AgentRuntime {
    fn host(&self) -> AgentHost {
        match self {
            Self::Pi(_) => AgentHost::Pi,
            Self::Grok(_) => AgentHost::Grok,
        }
    }

    fn verify_fork(&self, fork: &native_fork::Verified) -> Result<()> {
        match self {
            Self::Pi(pi) => native_fork::verify_runtime(pi, &fork.anchor),
            Self::Grok(_) => Err(invalid("原生分叉只适用于 Pi")),
        }
    }
}

struct PreparedRun {
    citations: Vec<Value>,
    binding: Option<RoundBinding>,
    queued: bool,
    fork: Option<native_fork::Verified>,
}

fn execute(
    context: JobContext,
    request: AgentStartRequest,
    projects: Vec<(String, String)>,
    prior: Option<AgentRun>,
    runtime: AgentRuntime,
    admission: PreparedRun,
    expected_spec: SpecRecord,
) -> Result<Value> {
    let PreparedRun {
        citations,
        binding,
        queued,
        fork,
    } = admission;
    let command_execution = request.command_execution();
    let at = sources::now();
    let resumed = prior.is_some();
    let previous_job_id = prior.as_ref().map(|run| run.job_id.clone());
    let host = runtime.host();
    if let Some(fork) = &fork {
        runtime.verify_fork(fork)?;
    }
    let mut store = lock_store(&context.store)?;
    let spec = sources::spec(store.state(), &request.spec_id)?.clone();
    check_spec_binding(&spec, &expected_spec)?;
    if let Some(fork) = &fork {
        fork.recheck_source(&mut store, &spec.id)?;
    }
    request.validate_handoff(&mut store, &spec.id, host)?;
    let continuation = super::agent_scope::continuation(
        &mut store,
        &spec.id,
        request.continuation_source.as_ref(),
        request.expected_continuation_digest.as_ref(),
        &request.project_ids,
    )?;
    let job: JobRecord = store
        .record("job", &context.id)?
        .ok_or_else(|| invalid("执行记录不存在"))?;
    if !request.read_only {
        acceptance::ensure_recovered(&mut store, &job.roots)?;
    }
    let prior = if let Some(prior) = prior {
        let current = load(&mut store, &prior.id)?;
        // Runtime discovery happens before the job takes its workspace reservation.
        if current.job_id != prior.job_id || current.binding_revision != prior.binding_revision {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "续接前执行记录或工程绑定已变化，请打开最新执行详情后重试",
            )
            .context("jobId", current.job_id));
        }
        if reviewable(&current) {
            store.put_record("agentRound", &current.job_id, &current)?;
        } else {
            native_fork::archive_source_round(&mut store, &current)?;
        }
        Some(current)
    } else {
        None
    };
    let previous_grants = prior.as_ref().map(|run| {
        (
            run.read_only,
            run.project_access.clone(),
            run.queue_project_access.clone(),
            run.command_execution,
        )
    });
    let previous_snapshot_issue = prior.as_ref().and_then(snapshot_issue).map(str::to_owned);
    let mut run = prior.unwrap_or_else(|| AgentRun {
        id: context.id.clone(),
        spec_id: request.spec_id.clone(),
        prompt: request.prompt.clone(),
        project_ids: request.project_ids.clone(),
        session_title: request
            .prompt
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ")
            .chars()
            .take(96)
            .collect(),
        status: "preparing".into(),
        job_id: context.id.clone(),
        binding_revision: 0,
        host,
        pi_session: None,
        grok_session: None,
        handoff: None,
        thread_id: None,
        turn_id: None,
        host_settings: None,
        workspace_mode: WorkspaceMode::Direct,
        snapshot_error: None,
        preparation_failed: false,
        workspaces: Vec::new(),
        answers: Vec::new(),
        applied_files: Vec::new(),
        created_at: at.clone(),
        updated_at: at,
        lifecycle_stage: None,
        goal_version: None,
        spec_digest: None,
        read_only: None,
        project_access: None,
        command_execution: None,
        queue_project_access: None,
        continuation_source: request.continuation_source.clone(),
        native_fork_source: request
            .native_fork_source
            .as_ref()
            .map(|source| source.origin()),
        ci_job: None,
    });
    run.job_id = context.id.clone();
    run.handoff.clone_from(&request.handoff);
    run.prompt = request.prompt.clone();
    run.status = "preparing".into();
    run.updated_at = sources::now();
    run.read_only = Some(request.read_only);
    run.queue_project_access = if queued {
        run.queue_project_access
            .take()
            .or_else(|| run.project_access.clone())
            .filter(|access| Some(access) != request.project_access.as_ref())
    } else {
        None
    };
    run.project_access = request.project_access.clone();
    run.command_execution = request.allow_commands.then_some(command_execution);
    run.ci_job = None;
    run.snapshot_error = None;
    run.preparation_failed = false;
    let prior_workspaces = std::mem::take(&mut run.workspaces);
    let mut excluded_projects: Vec<_> = store
        .state()
        .projects
        .iter()
        .filter(|project| !request.project_ids.contains(&project.id))
        .map(|project| PathBuf::from(&project.root_path))
        .collect();
    let grok_scope_at_launch = if host == AgentHost::Grok {
        let project = sources::project(store.state(), &spec.project_id)?;
        let (excluded, identity) = grok_scope(store.state(), &spec, &project.root_path)?;
        excluded_projects = excluded;
        Some(identity)
    } else {
        None
    };
    if let Some(fork) = &fork {
        excluded_projects.extend(fork.excluded_paths()?);
    }

    progress_agent::apply_binding(&mut run, binding.as_ref());
    let mut run_event = json!({
        "runId": run.id, "resumed": resumed, "host": run.host, "workspaceMode": run.workspace_mode,
        "allowCommands": request.allow_commands, "readOnly": request.read_only, "prompt": request.prompt,
        "previousJobId": previous_job_id, "projectIds": run.project_ids,
        "goalVersion": run.goal_version, "specDigest": run.spec_digest, "lifecycleStage": run.lifecycle_stage,
        "citations": citations,
        "projectAccess": run.project_access, "continuationSource": run.continuation_source, "handoff":run.handoff
    });
    if let Some(mode) = run.command_execution {
        run_event["commandExecution"] = json!(mode);
    }
    if let Some(source) = &run.native_fork_source {
        run_event["nativeForkSource"] = json!(source);
    }
    store.put_record_with_event(
        "agentRun",
        &run.id,
        &run,
        Some(super::sqlite::EventWrite {
            job_id: &context.id,
            kind: "run",
            payload: &run_event,
        }),
    )?;
    drop(store);
    let mut captured_initial = false;
    let needs_session_check = match &runtime {
        AgentRuntime::Pi(pi) => pi.check_model_in_session || fork.is_some(),
        AgentRuntime::Grok(_) => true,
    };
    let mut session_checked = false;
    #[cfg(windows)]
    let mut boundary_pins = None;
    let work = (|| -> Result<Value> {
        let data_dir = {
            let store = lock_store(&context.store)?;
            store.data_dir().to_path_buf()
        };
        #[cfg(windows)]
        {
            let AgentRuntime::Pi(pi) = &runtime else {
                return Err(invalid("当前平台不支持 Grok Build"));
            };
            boundary_pins = Some(agent_workspace::pin_windows_project_boundaries(
                &data_dir,
                &projects,
                &excluded_projects,
                &pi.config_dir,
                &context.cancel,
            )?);
        }
        context.trace("workspaceStart");
        run.workspaces = agent_workspace::prepare_direct(
            &data_dir,
            &projects,
            resumed
                .then_some(prior_workspaces.as_slice())
                .or_else(|| fork.as_ref().map(|fork| fork.source_workspaces.as_slice())),
            &spec,
            &excluded_projects,
            request.project_access.as_deref().map_or(
                agent_workspace::DirectAccess::Legacy {
                    read_only: request.read_only,
                },
                agent_workspace::DirectAccess::PerProject,
            ),
            &context.cancel,
            #[cfg(windows)]
            boundary_pins
                .as_ref()
                .ok_or_else(|| invalid("Windows 工程边界未固定"))?,
        )?;
        if let Some(fork) = &fork {
            fork.verify_prepared(&run.workspaces)?;
        }
        let prepared_roots: BTreeSet<_> = run
            .workspaces
            .iter()
            .map(|workspace| &workspace.original_root)
            .collect();
        if !prepared_roots.into_iter().eq(job.roots.iter()) {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "准备期间运行工程的 Git 根已变化，请刷新工程范围",
            )
            .context("httpStatus", 409)
            .context("projectScopeChanged", true));
        }
        let ci_workspace = request
            .ci_job
            .as_ref()
            .map(|selection| {
                run.workspaces
                    .iter()
                    .find(|workspace| workspace.project_ids.contains(&selection.project_id))
                    .ok_or_else(|| invalid("CI 作业工程没有可用的运行基线"))
            })
            .transpose()?;
        if let Some((selection, workspace)) = request.ci_job.as_ref().zip(ci_workspace)
            && selection.mode == CiJobMode::Reproduce
            && !workspace.head.eq_ignore_ascii_case(&selection.expected_sha)
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "当前工程 HEAD 与所选 CI 作业提交不一致，请切换到对应提交或选择对照模式",
            )
            .context("httpStatus", 409)
            .context("projectId", &selection.project_id)
            .context("currentHead", &workspace.head)
            .context("jobSha", &selection.expected_sha));
        }
        let ci = request
            .ci_job
            .as_ref()
            .map(|selection| {
                super::ci_job::prepare(
                    &context.store,
                    &spec.id,
                    &projects,
                    selection,
                    &context.cancel,
                )
            })
            .transpose()?;
        run.ci_job = ci.as_ref().zip(ci_workspace).map(|(ci, workspace)| {
            let mut metadata = ci.metadata();
            metadata["localHead"] = json!(workspace.head);
            metadata["localBranch"] = json!(
                workspace
                    .direct
                    .as_ref()
                    .and_then(|direct| direct.baseline.branch_name())
            );
            metadata
        });
        for workspace in &run.workspaces {
            let blobs = agent_workspace::direct_initial_blobs(workspace, &context.cancel)?;
            lock_store(&context.store)?.write_extensions(&super::sqlite::CommitEffects {
                records: Vec::new(),
                blobs,
            })?;
        }
        save_preparing_run(&context, &mut run)?;
        context.trace("workspaceReady");
        captured_initial = true;
        let effective_access = projects
            .iter()
            .map(|(id, path)| {
                let direct = run
                    .workspaces
                    .iter()
                    .find(|workspace| workspace.project_prefixes.contains_key(id))
                    .and_then(|workspace| workspace.direct.as_ref())
                    .ok_or_else(|| invalid("参与工程缺少本轮 Git 基线"))?;
                let mode = direct
                    .project_modes
                    .as_ref()
                    .and_then(|modes| modes.get(id).copied())
                    .unwrap_or(if request.read_only {
                        ProjectMode::Read
                    } else if request.allow_commands {
                        ProjectMode::Execute
                    } else {
                        ProjectMode::Edit
                    });
                Ok(ProjectAccess {
                    project_id: id.clone(),
                    mode,
                    expected_head: Some(direct.baseline.head.clone()),
                    expected_root_path: Some(path.clone()),
                    expected_branch: Some(direct.baseline.branch_name().map(str::to_owned)),
                })
            })
            .collect::<Result<Vec<_>>>()?;
        let mut access_event = json!({"projectAccess": effective_access});
        if let Some(mode) = run.command_execution {
            access_event["commandExecution"] = json!(mode);
        }
        context.emit("runProjectAccess", access_event)?;
        for workspace in &run.workspaces {
            agent_workspace::validate(workspace)?;
        }
        let (mut instructions, cwd, primary_project) = if host == AgentHost::Grok {
            let store = lock_store(&context.store)?;
            let project = sources::project(store.state(), &spec.project_id)?;
            (
                format!(
                    "主 Spec 工程：{}\n主规格相对路径：{}\n",
                    project.name, spec.source_path
                ),
                project.root_path.clone(),
                spec.project_id.clone(),
            )
        } else {
            let (state, records) = {
                let mut store = lock_store(&context.store)?;
                (
                    store.state().clone(),
                    super::materials::Records::load(&mut store),
                )
            };
            let mapped = map_agent_state(state, &run, |workspace| {
                agent_workspace::validate(workspace)?;
                Ok(PathBuf::from(&workspace.worktree_root))
            })?;
            let current = sources::spec(&mapped, &run.spec_id)?;
            check_spec_binding(current, &spec)?;
            let project = sources::project(&mapped, &spec.project_id)?;
            let mut text = spec_content::agent_request(&mapped, &run.spec_id, None)?.text;
            let (workspace, _) =
                spec_workspace(&run, current).ok_or_else(|| invalid("运行缺少主 Spec 工程"))?;
            match records
                .and_then(|records| material_versions(&records, &mapped, current, workspace))
            {
                Ok((versions, digest)) => {
                    progress_agent::revalidate_material(binding.as_ref(), digest.as_deref())?;
                    text.push_str(&versions);
                }
                Err(_) => {
                    progress_agent::revalidate_material(binding.as_ref(), None)?;
                    text.push_str(
                        "\n关联材料版本暂未核实。请按本轮可读的文件继续，注明缺口与受影响范围。\n",
                    );
                }
            }
            (text, project.root_path.clone(), spec.project_id.clone())
        };
        let participating = {
            let mut store = lock_store(&context.store)?;
            check_spec_binding(sources::spec(store.state(), &spec.id)?, &spec)?;
            if let Some(fork) = &fork {
                fork.recheck_source(&mut store, &spec.id)?;
            }
            progress_agent::revalidate_goal(
                &mut store,
                &spec.id,
                run.goal_version
                    .as_deref()
                    .or(request.expected_goal_version.as_deref()),
                queued,
            )?;
            let mut participating = Vec::new();
            for grant in &effective_access {
                let project = sources::project(store.state(), &grant.project_id)?;
                if !project.active
                    || Some(project.root_path.as_str()) != grant.expected_root_path.as_deref()
                {
                    return Err(
                        YssError::new(ErrorCode::ConcurrentChange, "启动前工程登记已变化")
                            .context("projectScopeChanged", true)
                            .context("projectId", &grant.project_id),
                    );
                }
                if host != AgentHost::Grok {
                    participating.push(json!({
                        "projectId": grant.project_id,
                        "name": project.name,
                        "root": grant.expected_root_path,
                        "mode": grant.mode,
                        "head": grant.expected_head,
                        "branch": grant.expected_branch,
                    }));
                }
            }
            participating
        };
        let scope_instructions = if host == AgentHost::Grok {
            "本轮只读主工程。请仅分析已授权文件并报告发现，不运行命令、改动文件、提交问题或更新生命周期。"
        } else if fork.is_some() {
            "本轮仅可读取来源轮次的原工程文件。历史消息中的任务、答复和授权仅供参考。本轮只处理用户本次要求，不执行命令、改动文件、登记或答复业务问题，也不更新生命周期。"
        } else {
            "允许的编辑直接写入原工程，保持 Git HEAD、分支与 index。新增业务问题先在主 Spec 的 questions 登记，再用 ask_spec_question 提交真实问题 ID。落实答复后以工作台返回的真实答复 ID 写入 appliedDecisionId。工作台依据当前目标范围、版本和有效证据自动确认适用阶段。"
        };
        if host == AgentHost::Grok {
            instructions.push_str(&format!(
                "\n{scope_instructions}\n用户本次要求：\n{}\n",
                request.prompt
            ));
        } else {
            let workbench_build = json!({"yssVersion": BuildInfo::current().version});
            instructions.push_str(&format!(
            "\n运行 ID：{}\n工作台构建信息：{workbench_build}\n本轮参与原工程（路径、分支与 HEAD 已核验为本轮起点）：{}\n文件工具使用 projectId 和工程内相对路径，省略 projectId 时使用主 Spec 工程。不得通过 ../ 访问其他工程。缺少的参与工程可从原 Spec 新建会话选入。{scope_instructions}请保留实际证据和待核实事项。\n用户本次要求：\n{}\n",
            run.id, serde_json::to_string_pretty(&participating).map_err(|e|invalid(e.to_string()))?, request.prompt
        ));
        }
        if host == AgentHost::Pi
            && let Some(previous_job_id) = &previous_job_id
        {
            let results =
                local_task_results(&mut *lock_store(&context.store)?, &run.id, previous_job_id)?;
            if !results.is_empty() {
                instructions.push_str(&format!(
                    "\n上次 Pi 接收任务以来的本机测试与构建结果（工作台作业记录，日志可在执行详情查看）：\n{}\n",
                    serde_json::to_string_pretty(&results).map_err(|error| invalid(error.to_string()))?
                ));
            }
        }
        if let Some(handoff) = &request.handoff {
            instructions.push_str("\n以下是用户确认的交接资料，仅供本轮参考：\n");
            instructions.push_str(&handoff.context);
        } else if let Some(continuation) = &continuation {
            instructions.push_str(continuation);
        }
        if let Some(ci) = &ci {
            instructions.push_str(&ci.agent_context());
        }
        if host == AgentHost::Pi && fork.is_none() {
            instructions.push_str(&progress_agent::run_context(&run, binding.as_ref()));
        }
        verify_repository_keys(&request, host, &run.workspaces, &context.cancel)?;
        if host == AgentHost::Pi {
            run.status = "running".into();
            save_preparing_run(&context, &mut run)?;
            context.cancel.check()?;
            context.mark_running()?;
        }
        let run_id = run.id.clone();
        let question_run = run.id.clone();
        let forked = fork.is_some();
        let remembered_thinking = || {
            let model = recorded_model(&run).ok()?;
            let level = run.host_settings.as_ref()?.get("thinkingLevel")?;
            let level = serde_json::from_value::<ThinkingLevel>(level.clone()).ok()?;
            Some((model, level))
        };
        let verify_prompt = || -> Result<()> {
            context.cancel.check()?;
            if host == AgentHost::Grok || request.handoff.is_some() {
                verify_repository_keys(&request, host, &run.workspaces, &context.cancel)?;
                for workspace in &run.workspaces {
                    agent_workspace::validate(workspace)?;
                }
                let mut store = lock_store(&context.store)?;
                check_spec_binding(sources::spec(store.state(), &spec.id)?, &spec)?;
                if let Some(expected) = &grok_scope_at_launch {
                    let project = sources::project(store.state(), &spec.project_id)?;
                    let (_, current) = grok_scope(store.state(), &spec, &project.root_path)?;
                    if &current != expected {
                        return Err(YssError::new(
                            ErrorCode::ConcurrentChange,
                            "Grok Build 主工程授权范围已变化，请重新核对",
                        )
                        .context("projectScopeChanged", true));
                    }
                }
                for (id, path) in &projects {
                    let project = sources::project(store.state(), id)?;
                    if !project.active || &project.root_path != path {
                        return Err(YssError::new(ErrorCode::Conflict, "启动前的工程范围已变化")
                            .context("projectScopeChanged", true));
                    }
                }
                request.validate_handoff(&mut store, &spec.id, host)?;
            }
            Ok(())
        };
        match runtime {
            AgentRuntime::Pi(pi) => agent_protocol::run(
                RunRequest {
                    runtime: pi,
                    data_dir: &data_dir,
                    run_id: &run.id,
                    cwd: Path::new(&cwd),
                    primary_project: &primary_project,
                    workspaces: &run.workspaces,
                    unselected_projects: &excluded_projects,
                    prompt: &instructions,
                    citations: &citations,
                    resume: run.pi_session.as_ref(),
                    native_fork: fork.as_ref().map(|fork| &fork.anchor),
                    expected_model: fork
                        .as_ref()
                        .map(|fork| &fork.anchor.model)
                        .or(request.expected_model.as_ref()),
                    thinking_level: request.thinking_level,
                    remembered_thinking: remembered_thinking(),
                    read_only: request.read_only,
                    allow_commands: request.allow_commands,
                    command_execution,
                    explicit_access: request.project_access.is_some(),
                },
                &context,
                |session, settings| {
                    let mut store = lock_store(&context.store)?;
                    let mut latest = load(&mut store, &run_id)?;
                    if let Some(session) = session {
                        latest.pi_session = Some(session);
                    }
                    latest.host_settings = Some(settings);
                    latest.updated_at = sources::now();
                    store.put_record("agentRun", &run_id, &latest)?;
                    session_checked = true;
                    Ok(())
                },
                move |context, question_id| {
                    if forked {
                        return Err(invalid("原生分叉本轮不能提交业务问题"));
                    }
                    ask_question(context, &question_run, question_id)
                },
                |runtime| {
                    verify_prompt()?;
                    if request.expected_runtime_identity.is_some() {
                        request.verify_runtime_identity(runtime.identity().as_deref(), host)?;
                    }
                    Ok(())
                },
                request
                    .handoff
                    .as_ref()
                    .map(|handoff| handoff.context.as_str()),
            )?,
            AgentRuntime::Grok(grok) => grok::run(
                grok,
                &data_dir,
                &run.id,
                Path::new(&cwd),
                &excluded_projects,
                grok_scope_at_launch
                    .as_ref()
                    .expect("Grok project scope captured")
                    .as_str(),
                &instructions,
                &request.prompt,
                &spec.source_path,
                request
                    .handoff
                    .as_ref()
                    .map(|handoff| handoff.context.as_str()),
                run.grok_session.as_ref(),
                request.thinking_level.or_else(|| {
                    remembered_thinking().and_then(|(model, effort)| {
                        (request.expected_model.as_ref() == Some(&model)).then_some(effort)
                    })
                }),
                &context,
                |session, settings| {
                    let mut store = lock_store(&context.store)?;
                    let mut latest = load(&mut store, &run_id)?;
                    latest.grok_session = Some(session);
                    latest.host_settings = Some(settings.clone());
                    latest.status = "running".into();
                    latest.updated_at = sources::now();
                    store.put_record("agentRun", &run_id, &latest)?;
                    drop(store);
                    context.emit("grokConfiguration", settings)?;
                    session_checked = true;
                    Ok(())
                },
                verify_prompt,
            )?,
        }
        Ok(json!({"runId":run.id,"host":run.host,"status":"completed"}))
    })();
    let scope_changed = work
        .as_ref()
        .is_err_and(|error| error.context.contains_key("projectScopeChanged"));
    let can_freeze =
        captured_initial && !scope_changed && (!needs_session_check || session_checked);
    let finalizing = if can_freeze {
        context.set_pi_phase(PiPhase::FinalizeArtifacts)
    } else {
        Ok(())
    };
    context.trace("freezeStart");
    let frozen = can_freeze.then(|| {
        if fork.is_some() {
            native_fork::freeze_result(&context.store, &run)
        } else {
            freeze_shared(&context.store, &run.id, &Cancellation::new())
        }
    });
    let diff_available = frozen.as_ref().is_some_and(Result::is_ok);
    context.trace("freezeEnd");
    let mut store = lock_store(&context.store)?;
    let mut latest = load(&mut store, &run.id)?;
    if !can_freeze
        && let Some((read_only, project_access, queue_access, command_execution)) = previous_grants
    {
        latest.workspaces = prior_workspaces;
        for workspace in &mut latest.workspaces {
            workspace.result_snapshot = None;
        }
        latest.read_only = read_only;
        latest.project_access = project_access;
        latest.queue_project_access = queue_access;
        latest.command_execution = command_execution;
    }
    latest.status = match &work {
        Ok(_) => "completed",
        Err(error) if error.code == ErrorCode::Cancelled => "interrupted",
        Err(_) => "failed",
    }
    .into();
    latest.updated_at = sources::now();
    latest.preparation_failed = !can_freeze;
    latest.snapshot_error = match &frozen {
        Some(result) => result.as_ref().err().map(|error| error.message.clone()),
        None => previous_snapshot_issue,
    };
    store.put_record("agentRun", &run.id, &latest)?;
    let refresh = (diff_available && fork.is_none())
        .then(|| refresh_direct_spec(&mut store, &latest, &spec))
        .transpose();
    let reconciliation = if work.is_ok()
        && !request.read_only
        && !context.cancel.is_cancelled()
        && diff_available
        && refresh.is_ok()
        && latest.job_id == run.job_id
        && latest.binding_revision == run.binding_revision
    {
        reconcile_direct_progress(&mut store, &latest, &spec)
    } else {
        Ok(())
    };
    drop(store);
    #[cfg(windows)]
    drop(boundary_pins);
    if let Err(error) = refresh {
        let _ = context.emit("progressError", json!({
            "activity":"refreshSpec","runId":run.id,"message":"原工程已保留，请核对 Spec 刷新结果","error":error
        }));
    }
    if let Err(error) = reconciliation {
        let _ = context.emit("progressError", json!({
            "activity":"reconcileProgress","runId":run.id,"message":"本轮已结束，请核对生命周期同步结果","error":error
        }));
    }
    match work {
        Ok(mut value) => {
            finalizing?;
            value["diffAvailable"] = json!(diff_available);
            value["snapshotError"] = json!(latest.snapshot_error);
            Ok(value)
        }
        Err(error) => Err(match frozen {
            Some(Err(freeze)) => error.context("snapshotError", freeze.message),
            _ => error,
        }),
    }
}

fn refresh_direct_spec(store: &mut Store, run: &AgentRun, spec: &SpecRecord) -> Result<()> {
    let Some((workspace, path)) = spec_workspace(run, spec) else {
        return Ok(());
    };
    let Some(result) = workspace.result_snapshot.as_ref() else {
        return Ok(());
    };
    if result == &workspace.initial && run.answers.is_empty() {
        return Ok(());
    }
    let mut state = store.state().clone();
    check_spec_binding(sources::spec(&state, &spec.id)?, spec)?;
    let before = sources::read_source(&state, &spec.id)?;
    if !result
        .get(&path)
        .is_some_and(|file| file.sha256.as_str() == before.digest)
    {
        return Err(stale_round(run));
    }
    merge_registered_responses(&mut state, run, spec)?;
    super::registration::refresh(&mut state, &spec.id, &run.updated_at)?;
    if sources::read_source(&state, &spec.id)?.digest != before.digest {
        return Err(stale_round(run));
    }
    store.commit(store.state().revision, state)?;
    Ok(())
}

fn reconcile_direct_progress(store: &mut Store, run: &AgentRun, spec: &SpecRecord) -> Result<()> {
    let Some(goal_version) = run.goal_version.as_deref() else {
        return Ok(());
    };
    if super::progress::load_goal_version(store, &spec.id)?.as_deref() != Some(goal_version) {
        return Ok(());
    }
    check_spec_binding(sources::spec(store.state(), &spec.id)?, spec)?;
    for workspace in &run.workspaces {
        agent_workspace::validate(workspace)?;
        for (id, prefix) in &workspace.project_prefixes {
            let project = sources::project(store.state(), id)?;
            let round_root = Path::new(&workspace.original_root).join(prefix);
            if !project.active || Path::new(&project.root_path) != round_root {
                return Err(stale_round(run));
            }
        }
    }
    let expected_revision = store.state().revision;
    super::service::mutate(
        store,
        MutationRequest {
            expected_revision,
            expected_target: None,
            request_id: None,
            action: Action::ReconcileProgress {
                spec_id: spec.id.clone(),
            },
        },
    )?;
    Ok(())
}

fn frozen_bytes(store: &Store, descriptor: &FileDescriptor) -> Result<Vec<u8>> {
    let bytes = store.get_blob(descriptor.sha256.as_str())?;
    if Sha256::of(&bytes) != descriptor.sha256 || bytes.len() as u64 != descriptor.size {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "运行对象摘要或长度不一致",
        ));
    }
    Ok(bytes)
}
struct CapturedWorkspace {
    snapshot: agent_workspace::Snapshot,
    blobs: Vec<(String, Vec<u8>)>,
}
fn capture_workspace(
    workspace: &Workspace,
    cancel: &Cancellation,
) -> Result<Option<CapturedWorkspace>> {
    if !Path::new(&workspace.worktree_root).exists() && workspace.result_snapshot.is_some() {
        return Ok(None);
    }
    cancel.check()?;
    agent_workspace::validate(workspace)?;
    let snapshot = agent_workspace::working_snapshot(workspace, cancel)?;
    let root = Root::open(Path::new(&workspace.worktree_root))?;
    let mut blobs = BTreeMap::new();
    for (path, descriptor) in &snapshot {
        cancel.check()?;
        let file = root.read_checked(&RelPath::new(path)?, MAX_ASSET)?;
        if file.snapshot.descriptor() != Some(descriptor) {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "冻结成果期间文件改变").at(path),
            );
        }
        blobs
            .entry(descriptor.sha256.as_str().to_owned())
            .or_insert(file.bytes);
    }
    if agent_workspace::working_snapshot(workspace, cancel)? != snapshot {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "冻结成果期间工作区改变",
        ));
    }
    Ok(Some(CapturedWorkspace {
        snapshot,
        blobs: blobs.into_iter().collect(),
    }))
}
pub fn freeze(store: &mut Store, run_id: &str) -> Result<()> {
    let mut run = load(store, run_id)?;
    for workspace in &mut run.workspaces {
        if let Some(captured) = capture_workspace(workspace, &Cancellation::new())? {
            store.write_extensions(&super::sqlite::CommitEffects {
                records: Vec::new(),
                blobs: captured.blobs,
            })?;
            workspace.result_snapshot = Some(captured.snapshot);
        }
    }
    run.updated_at = sources::now();
    store.put_record("agentRun", &run.id, &run)
}
fn check_freeze_base(store: &mut Store, base: &AgentRun) -> Result<AgentRun> {
    let latest = load(store, &base.id)?;
    if latest.job_id != base.job_id
        || latest.workspaces.len() != base.workspaces.len()
        || latest
            .workspaces
            .iter()
            .zip(&base.workspaces)
            .any(|(current, prior)| {
                current.original_root != prior.original_root
                    || current.worktree_root != prior.worktree_root
                    || current.initial != prior.initial
                    || current.result_snapshot != prior.result_snapshot
            })
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "冻结期间运行版本已变化，请重新准备",
        ));
    }
    Ok(latest)
}
pub fn freeze_shared(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    cancel: &Cancellation,
) -> Result<()> {
    match freeze_attempt(shared, run_id, cancel) {
        Err(error) if error.code == ErrorCode::ConcurrentChange => {
            freeze_attempt(shared, run_id, cancel)
        }
        result => result,
    }
}
fn freeze_attempt(shared: &Arc<Mutex<Store>>, run_id: &str, cancel: &Cancellation) -> Result<()> {
    let base = {
        let mut store = lock_store(shared)?;
        load(&mut store, run_id)?
    };
    let mut snapshots = Vec::new();
    for workspace in &base.workspaces {
        let captured = capture_workspace(workspace, cancel)?;
        if let Some(captured) = captured {
            let mut store = lock_store(shared)?;
            check_freeze_base(&mut store, &base)?;
            store.write_extensions(&super::sqlite::CommitEffects {
                records: Vec::new(),
                blobs: captured.blobs,
            })?;
            snapshots.push(Some(captured.snapshot));
        } else {
            snapshots.push(workspace.result_snapshot.clone());
        }
    }
    cancel.check()?;
    let mut store = lock_store(shared)?;
    let mut latest = check_freeze_base(&mut store, &base)?;
    for (workspace, snapshot) in latest.workspaces.iter_mut().zip(snapshots) {
        workspace.result_snapshot = snapshot;
    }
    latest.updated_at = sources::now();
    store.put_record("agentRun", &latest.id, &latest)
}
pub fn freeze_all_shared(shared: &Arc<Mutex<Store>>, cancel: &Cancellation) -> Result<()> {
    let runs = lock_store(shared)?.records::<AgentRun>("agentRun")?;
    for run in runs {
        cancel.check()?;
        if run.workspace_mode == WorkspaceMode::Direct || run.workspaces.is_empty() {
            continue;
        }
        let current = {
            let mut store = lock_store(shared)?;
            result(&mut store, &run.id)?
        };
        if terminal(&current.status)
            && current
                .workspaces
                .iter()
                .all(|workspace| workspace.result_snapshot.is_some())
        {
            continue;
        }
        freeze_shared(shared, &run.id, cancel)?;
    }
    Ok(())
}

pub fn freeze_all(store: &mut Store) -> Result<()> {
    for run in store.records::<AgentRun>("agentRun")? {
        if run.workspace_mode == WorkspaceMode::Direct || run.workspaces.is_empty() {
            continue;
        }
        let current = result(store, &run.id)?;
        if matches!(
            current.status.as_str(),
            "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
        ) && current
            .workspaces
            .iter()
            .all(|workspace| workspace.result_snapshot.is_some())
        {
            continue;
        }
        freeze(store, &run.id)?;
    }
    Ok(())
}
fn terminal(status: &str) -> bool {
    matches!(
        status,
        "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
    )
}
fn snapshot_issue(run: &AgentRun) -> Option<&str> {
    run.snapshot_error.as_deref().or_else(|| {
        (run.workspace_mode == WorkspaceMode::Direct
            && terminal(&run.status)
            && !run.preparation_failed
            && !has_frozen_outcome(run))
        .then_some("本轮没有冻结成果，请核对原工程和本轮日志")
    })
}
fn reviewable(run: &AgentRun) -> bool {
    terminal(&run.status) && has_frozen_outcome(run)
}
fn has_frozen_outcome(run: &AgentRun) -> bool {
    !run.workspaces.is_empty()
        && run
            .workspaces
            .iter()
            .all(|workspace| workspace.result_snapshot.is_some())
}
fn view_root(
    store: &Store,
    run_id: &str,
    workspace: &Workspace,
    prefer_frozen: bool,
) -> Result<std::path::PathBuf> {
    if Path::new(&workspace.worktree_root).exists()
        && (!prefer_frozen || (workspace.direct.is_none() && workspace.result_snapshot.is_none()))
    {
        agent_workspace::validate(workspace)?;
        return Ok(Path::new(&workspace.worktree_root).to_path_buf());
    }
    let snapshot = workspace
        .result_snapshot
        .as_ref()
        .ok_or_else(|| invalid("运行工作区缺失且没有冻结成果"))?;
    let digest = Sha256::of(&serde_json::to_vec(snapshot).map_err(|e| invalid(e.to_string()))?);
    let relative = RelPath::new(format!("agent-previews/{run_id}/{}", digest.as_str()))?;
    let data = Root::open(store.data_dir())?;
    for parent in relative.parents().chain(std::iter::once(relative.as_str())) {
        let rel = RelPath::new(parent)?;
        match data.inspect(&rel)? {
            FileSnapshot::Missing => {
                data.create_dir(&rel)?;
            }
            FileSnapshot::Directory { .. } => {}
            _ => {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "冻结预览目录含受保护对象").at(parent),
                );
            }
        }
    }
    let path = store.data_dir().join(relative.as_str());
    let root = Root::open(&path)?;
    for (file, descriptor) in snapshot {
        let rel = RelPath::new(file)?;
        for parent in rel.parents() {
            let parent = RelPath::new(parent)?;
            match root.inspect(&parent)? {
                FileSnapshot::Missing => {
                    root.create_dir(&parent)?;
                }
                FileSnapshot::Directory { .. } => {}
                _ => {
                    return Err(
                        YssError::new(ErrorCode::UnsafePath, "冻结预览父目录不安全").at(parent)
                    );
                }
            }
        }
        match root.inspect(&rel)? {
            FileSnapshot::Missing => {
                root.create_file(&rel, &frozen_bytes(store, descriptor)?, descriptor.mode)?;
            }
            FileSnapshot::File {
                descriptor: current,
                ..
            } if current == *descriptor => {}
            _ => {
                return Err(
                    YssError::new(ErrorCode::ConcurrentChange, "冻结预览文件已被修改").at(file),
                );
            }
        }
    }
    root.revalidate()?;
    Ok(path)
}
pub fn artifact_state(
    store: &mut Store,
    run_id: &str,
    job_id: Option<&str>,
    spec_id: &str,
) -> Result<WorkbenchState> {
    let (run, historical) = match load_round(store, run_id, job_id) {
        Ok(value) => value,
        Err(error) if error.code == ErrorCode::ConcurrentChange => {
            let run = rebind_run(store, run_id)?;
            if job_id.is_some_and(|id| id != run.job_id) {
                return Err(error);
            }
            (run, false)
        }
        Err(error) => return Err(error),
    };
    if run.spec_id != spec_id {
        return Err(invalid("Spec 与运行不匹配"));
    }
    if run.workspace_mode == WorkspaceMode::Direct && terminal(&run.status) && !reviewable(&run) {
        return Err(invalid("本轮没有可审阅的冻结成果").context("httpStatus", 404));
    }
    let mut state = mapped_state(store, &run)?;
    if reviewable(&run) {
        let spec = state
            .specs
            .iter_mut()
            .find(|spec| spec.id == spec_id)
            .ok_or_else(|| invalid("Spec 未登记"))?;
        let source = run
            .workspaces
            .iter()
            .flat_map(|w| &w.resource_sources)
            .find(|source| source.project_id == spec.project_id);
        if let Some(source) = source {
            spec.source_path.clone_from(&source.source_path);
            spec.resources.clone_from(&source.personal_resources);
            spec.inferred_resources
                .clone_from(&source.inferred_resources);
            spec.source_resource_targets.clear();
        } else if historical {
            return Err(invalid("旧轮未留存主 Spec 的来源"));
        }
    }
    if historical {
        let ids: BTreeSet<_> = run_responses(&run)
            .map(|result| result.map(|response| response.id))
            .collect::<Result<_>>()?;
        state
            .question_responses
            .retain(|response| response.spec_id != spec_id || ids.contains(&response.id));
    }
    Ok(state)
}
pub fn respond(
    store: &mut Store,
    run_id: &str,
    response: QuestionResponse,
) -> Result<QuestionResponse> {
    save_response(store, run_id, response, None)
}
fn save_response(
    store: &mut Store,
    run_id: &str,
    response: QuestionResponse,
    input: Option<(&str, &str)>,
) -> Result<QuestionResponse> {
    let mut run = load(store, run_id)?;
    if response.spec_id != run.spec_id {
        return Err(invalid("Spec 与运行不匹配"));
    }
    ensure_response_available(store, &run.spec_id, &response.question_id)?;
    let mut mapped = mapped_state(store, &run)?;
    spec_content::respond(&mut mapped, response, &sources::now())?;
    let response = mapped
        .question_responses
        .last()
        .cloned()
        .ok_or_else(|| invalid("答复保存失败"))?;
    let mut answer = json!({"id":response.id,"questionResponse":response});
    run.updated_at = sources::now();
    if let Some((job_id, request_id)) = input {
        if run.job_id != job_id {
            return Err(invalid("答复的执行记录已变化，请重新核对"));
        }
        answer["jobId"] = json!(job_id);
        answer["requestId"] = json!(request_id);
        run.answers.push(answer);
        // The answer and its replay event must survive interruptions together.
        store.put_record_with_event(
            "agentRun",
            &run.id,
            &run,
            Some(super::sqlite::EventWrite {
                job_id,
                kind: "piQuestionResult",
                payload: &json!({
                    "requestId": request_id, "responseId": response.id, "answer": response.answer
                }),
            }),
        )?;
    } else {
        run.answers.push(answer);
        if run.workspace_mode == WorkspaceMode::Direct && terminal(&run.status) {
            let mut state = store.state().clone();
            let spec = sources::spec(&state, &run.spec_id)?.clone();
            merge_registered_responses(&mut state, &run, &spec)?;
            let effects = super::sqlite::CommitEffects {
                records: vec![super::sqlite::RecordWrite {
                    kind: "agentRun".into(),
                    id: run.id.clone(),
                    payload: Some(serde_json::to_string(&run).map_err(|e| invalid(e.to_string()))?),
                }],
                blobs: Vec::new(),
            };
            store.commit_with_effects(state.revision, state, &effects)?;
        } else {
            store.put_record("agentRun", &run.id, &run)?;
        }
    }
    Ok(response)
}
fn ask_question(context: &JobContext, run_id: &str, question_id: &str) -> Result<String> {
    let (spec_id, question) = {
        let mut store = lock_store(&context.store)?;
        let run = load(&mut store, run_id)?;
        let question = spec_content::read_view(&mapped_state(&store, &run)?, &run.spec_id)?
            .questions
            .into_iter()
            .find(|question| question.question.id == question_id)
            .filter(|question| question.question.kind == QuestionKind::Decision)
            .ok_or_else(|| invalid("请先在主 Spec 登记此业务问题，再按实际 ID 提问"))?;
        (run.spec_id, question)
    };
    let answer_text = |response: &QuestionResponse| {
        format!(
            "用户答复：{}\n工作台已保存答复 ID {}。落实正文及产物后，以该 ID 写入 appliedDecisionId。",
            response.answer, response.id
        )
    };
    if let Some(saved) = &question.response {
        if saved.kind == ResponseKind::Defer {
            let usage = if question.state == QuestionState::Deferred {
                "已回写，引用原安排即可。"
            } else {
                "仅回写延期安排，保留其影响范围、日期和条件。"
            };
            return Ok(format!(
                "用户已为当前问题保存延期安排。{usage}按条件复查的延期须核实触发事实。\n{}",
                serde_json::to_string_pretty(saved).map_err(|_| invalid("延期安排无法编码"))?
            ));
        }
        if question.state == QuestionState::Resolved {
            return Ok(format!(
                "此答复已回写，请引用原结论。\n答复 ID {}\n用户答复：{}\n来源结论：{}",
                saved.id, saved.answer, question.question.resolution
            ));
        }
        return Ok(answer_text(saved));
    }
    let mut details = json!({ "id": question_id, "question": question.question.title });
    if !question.question.recommendation.trim().is_empty() {
        details["options"] = json!([{ "label": question.question.recommendation }]);
    }
    let request_id = fs::unique_id()?;
    let reply = context.request_input(
        &request_id,
        json!({
            "protocolMethod": "pi/askSpecQuestion",
            "params": { "reason": question.question.impact, "questions": [details] },
        }),
    )?;
    let saved = (|| -> Result<QuestionResponse> {
        let answer = reply["answers"][question_id]["answers"]
            .as_array()
            .filter(|answers| answers.len() == 1)
            .and_then(|answers| answers[0].as_str())
            .map(str::trim)
            .filter(|answer| !answer.is_empty())
            .ok_or_else(|| invalid("业务问题答复为空或格式无效"))?;
        context.cancel.check()?;
        let mut store = lock_store(&context.store)?;
        save_response(
            &mut store,
            run_id,
            QuestionResponse {
                id: String::new(),
                spec_id,
                question_id: question_id.into(),
                kind: ResponseKind::Answer,
                answer: answer.into(),
                reason: String::new(),
                impact: String::new(),
                excluded_scope: String::new(),
                review_at: String::new(),
                review_when: String::new(),
                review_utc_offset_minutes: Some(0),
                question_digest: question.digest,
                created_at: String::new(),
            },
            Some((&context.id, &request_id)),
        )
    })();
    match saved {
        Ok(saved) => Ok(answer_text(&saved)),
        Err(error) => {
            if error.code != ErrorCode::Cancelled {
                let _ = context.emit(
                    "piQuestionResult",
                    json!({"requestId": request_id, "error": error.message}),
                );
            }
            Err(error)
        }
    }
}
fn text(bytes: Option<Vec<u8>>) -> Option<String> {
    bytes
        .filter(|b| b.len() <= 128 * 1024 && !b.contains(&0))
        .and_then(|b| String::from_utf8(b).ok())
}
fn bytes(root: &Root, path: &str, expected: &Option<FileDescriptor>) -> Result<Option<Vec<u8>>> {
    if expected.is_none() {
        return Ok(None);
    }
    let file = root.read_checked(&RelPath::new(path)?, MAX_ASSET)?;
    if file.snapshot.descriptor() != expected.as_ref() {
        return Err(YssError::new(ErrorCode::ConcurrentChange, "读取差异期间文件改变").at(path));
    }
    Ok(Some(file.bytes))
}
pub fn diff(store: &mut Store, run_id: &str) -> Result<RunDiff> {
    let run = result(store, run_id)?;
    diff_run(&run, &Cancellation::new(), &|descriptor| {
        frozen_bytes(store, descriptor)
    })
}
pub fn round_diff(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    job_id: Option<&str>,
) -> Result<RunDiff> {
    let (run, historical) = load_round(&mut *lock_store(shared)?, run_id, job_id)?;
    if (job_id.is_some() || run.workspace_mode == WorkspaceMode::Direct) && !reviewable(&run) {
        return Err(invalid("本轮没有可审阅的冻结成果").context("httpStatus", 404));
    }
    project_diff(&run, historical, &Cancellation::new(), &|descriptor| {
        frozen_bytes(&*lock_store(shared)?, descriptor)
    })
}

pub(super) fn compact_round_diff(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    job_id: Option<&str>,
    cancel: &Cancellation,
) -> Result<Value> {
    let (run, historical) = load_round(&mut *lock_store(shared)?, run_id, job_id)?;
    if (job_id.is_some() || run.workspace_mode == WorkspaceMode::Direct) && !reviewable(&run) {
        return Err(invalid("本轮没有可审阅的冻结成果").context("httpStatus", 404));
    }
    let summary = describe_diff(&run, historical, cancel)?;
    #[derive(Default, Serialize)]
    struct GroupCounts {
        total: usize,
        pending: usize,
        conflicts: usize,
    }
    let mut groups = BTreeMap::<&str, GroupCounts>::new();
    for file in &summary.files {
        let counts = groups.entry(&file.project_id).or_default();
        counts.total += 1;
        counts.pending += usize::from(file.pending);
        counts.conflicts += usize::from(file.conflict);
    }
    let mut value = serde_json::to_value(&summary).map_err(|_| invalid("差异摘要编码失败"))?;
    value["bindingRevision"] = json!(run.binding_revision);
    value["groupCounts"] = json!(groups);
    value["total"] = json!(summary.files.len());
    for file in value["files"].as_array_mut().expect("RunDiff files") {
        diff_hunks::remove_text(file);
        file.as_object_mut()
            .expect("DiffFile is an object")
            .remove("binary");
    }
    Ok(value)
}

pub(super) struct FileDiffQuery<'a> {
    pub(super) job_id: &'a str,
    pub(super) project_id: &'a str,
    pub(super) path: &'a str,
    pub(super) review_token: &'a str,
    pub(super) full: bool,
}

pub(super) fn round_diff_file(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    query: FileDiffQuery<'_>,
    cancel: &Cancellation,
) -> Result<Value> {
    RelPath::new(query.path)?;
    let (run, historical) = load_round(&mut *lock_store(shared)?, run_id, Some(query.job_id))?;
    if !reviewable(&run) {
        return Err(stale_round(&run));
    }
    let summary = describe_diff(&run, historical, cancel)?;
    if summary.review_token != query.review_token {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "本轮差异已变化，请重新读取文件列表",
        )
        .context("httpStatus", 409)
        .context("jobId", &summary.job_id)
        .context("reviewToken", &summary.review_token));
    }
    let mut file = summary
        .files
        .into_iter()
        .find(|file| file.project_id == query.project_id && file.path == query.path)
        .ok_or_else(|| invalid("文件不在该工程的本轮成果差异中").context("httpStatus", 404))?;
    cancel.check()?;
    hydrate_diff_file(&run, historical, &mut file, &|descriptor| {
        frozen_bytes(&*lock_store(shared)?, descriptor)
    })?;
    diff_hunks::file_preview(file, run.workspace_mode, historical, query.full)
}
fn load_round(store: &mut Store, run_id: &str, job_id: Option<&str>) -> Result<(AgentRun, bool)> {
    let mut latest = store
        .record::<AgentRun>("agentRun", run_id)?
        .filter(|run| run.id == run_id)
        .ok_or_else(|| invalid("Agent 运行不存在").context("httpStatus", 404))?;
    let job_id = job_id.unwrap_or(&latest.job_id);
    if job_id == latest.job_id {
        if latest.workspace_mode != WorkspaceMode::Direct {
            return Ok((load(store, run_id)?, false));
        }
        if let Some(job) = store.record::<JobRecord>("job", job_id)? {
            latest.status = job.status;
        }
        return Ok((latest, false));
    }
    let archived = store
        .record::<AgentRun>("agentRound", job_id)?
        .filter(|run| run.id == run_id && run.job_id == job_id && reviewable(run))
        .ok_or_else(|| {
            invalid("旧轮没有留存可审阅的成果快照，请查看最新成果").context("httpStatus", 404)
        })?;
    Ok((archived, true))
}
fn diff_run(
    run: &AgentRun,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<RunDiff> {
    project_diff(run, false, cancel, read_blob)
}
fn project_diff(
    run: &AgentRun,
    historical: bool,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<RunDiff> {
    let mut diff = describe_diff(run, historical, cancel)?;
    for file in &mut diff.files {
        cancel.check()?;
        hydrate_diff_file(run, historical, file, read_blob)?;
    }
    Ok(diff)
}

fn describe_diff(run: &AgentRun, historical: bool, cancel: &Cancellation) -> Result<RunDiff> {
    let run_id = &run.id;
    let direct = run.workspace_mode == WorkspaceMode::Direct;
    if direct && !reviewable(run) {
        return Err(invalid("本轮没有可审阅的冻结成果").context("httpStatus", 404));
    }
    let mut files = Vec::new();
    let mut git_baselines = BTreeMap::new();
    let mut manifests = BTreeMap::new();
    for workspace in &run.workspaces {
        let root = Path::new(&workspace.original_root);
        let use_frozen = terminal(&run.status) && workspace.result_snapshot.is_some();
        let final_state = if use_frozen {
            Cow::Borrowed(workspace.result_snapshot.as_ref().unwrap())
        } else {
            agent_workspace::validate(workspace)?;
            Cow::Owned(agent_workspace::working_snapshot(workspace, cancel)?)
        };
        let original = (!historical && !direct)
            .then(|| Root::open(root))
            .transpose()?;
        if original
            .as_ref()
            .is_some_and(|original| original.identity != workspace.original_identity)
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "原工程目录身份已改变",
            ));
        }
        let git_baseline = (!historical && !direct)
            .then(|| agent_workspace::git_baseline(root, cancel))
            .transpose()?;
        for path in review_paths(run, workspace, &final_state) {
            cancel.check()?;
            let initial = workspace.initial.get(path).cloned();
            let result = final_state.get(path).cloned();
            let receipt = last_accepted_file(run, workspace, path);
            let baseline = receipt
                .map(accepted_result)
                .transpose()?
                .unwrap_or_else(|| initial.clone());
            if receipt.is_none() && baseline == result {
                continue;
            }
            let (project_id, relative) = review_path(workspace, path)?;
            let reviewed = receipt.filter(|receipt| {
                baseline == result && receipt["jobId"].as_str() == Some(&run.job_id)
            });
            let current = if let Some(original) = &original {
                current_descriptor(original, path)?
            } else {
                None
            };
            files.push(DiffFile {
                project_id: project_id.to_owned(),
                git_root: workspace.original_root.clone(),
                path: relative.to_owned(),
                workspace_path: path.to_owned(),
                pending: !direct && !historical && baseline != result && current != result,
                conflict: !direct
                    && !historical
                    && baseline != result
                    && current != baseline
                    && current != result,
                accepted: receipt.is_some() && baseline == result,
                content_matches: !direct && !historical && current == result,
                accepted_review_token: receipt
                    .filter(|_| baseline == result)
                    .and_then(|receipt| receipt["reviewToken"].as_str())
                    .map(str::to_owned),
                reviewed_before: reviewed
                    .and_then(|receipt| receipt["reviewedBefore"].as_str())
                    .map(str::to_owned),
                reviewed_before_descriptor: reviewed
                    .and_then(|receipt| serde_json::from_value(receipt["before"].clone()).ok())
                    .flatten(),
                reviewed_before_known: reviewed
                    .is_some_and(|receipt| receipt.get("before").is_some()),
                baseline,
                initial,
                result,
                current,
                binary: false,
                initial_text: None,
                result_text: None,
                current_text: None,
            });
        }
        manifests.insert(workspace.original_root.clone(), final_state);
        if let Some(metadata) = &workspace.direct {
            git_baselines.insert(workspace.original_root.clone(), metadata.baseline.clone());
        }
        if let Some(baseline) = git_baseline {
            agent_workspace::check_baseline(root, &baseline, cancel)?;
            git_baselines.insert(workspace.original_root.clone(), baseline);
        }
    }
    let reviewed_files: Vec<_> = files
        .iter()
        .map(|file| {
            (
                &file.project_id,
                &file.path,
                &file.initial,
                &file.baseline,
                &file.result,
                &file.current,
            )
        })
        .collect();
    let token_bytes = serde_json::to_vec(&(
        &run.id,
        &run.job_id,
        run.binding_revision,
        &manifests,
        &git_baselines,
        reviewed_files,
    ))
    .map_err(|error| invalid(error.to_string()))?;
    Ok(RunDiff {
        run_id: run_id.into(),
        job_id: run.job_id.clone(),
        historical,
        workspace_mode: run.workspace_mode,
        review_token: Sha256::of(&token_bytes).as_str().to_owned(),
        git_baselines,
        files,
    })
}

fn hydrate_diff_file(
    run: &AgentRun,
    historical: bool,
    file: &mut DiffFile,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<()> {
    let workspace = run
        .workspaces
        .iter()
        .find(|workspace| workspace.original_root == file.git_root)
        .ok_or_else(|| invalid("成果文件缺少绑定的工程"))?;
    let initial = file.initial.as_ref().map(read_blob).transpose()?;
    let result = if terminal(&run.status) && workspace.result_snapshot.is_some() {
        file.result.as_ref().map(read_blob).transpose()?
    } else {
        let worktree = Root::open(Path::new(&workspace.worktree_root))?;
        if worktree.identity != workspace.worktree_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "运行工作区目录身份已改变",
            ));
        }
        bytes(&worktree, &file.workspace_path, &file.result)?
    };
    let current = if !historical && run.workspace_mode == WorkspaceMode::Isolated {
        let original = Root::open(Path::new(&workspace.original_root))?;
        if original.identity != workspace.original_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "原工程目录身份已改变",
            ));
        }
        bytes(&original, &file.workspace_path, &file.current)?
    } else {
        None
    };
    file.binary = [&initial, &result, &current]
        .into_iter()
        .flatten()
        .any(|bytes| bytes.contains(&0) || std::str::from_utf8(bytes).is_err());
    file.initial_text = text(initial);
    file.result_text = text(result);
    file.current_text = text(current);
    Ok(())
}
fn current_descriptor(root: &Root, path: &str) -> Result<Option<FileDescriptor>> {
    let snapshot = root.inspect(&RelPath::new(path)?)?;
    if matches!(
        snapshot,
        FileSnapshot::Dangerous | FileSnapshot::Directory { .. }
    ) {
        return Err(YssError::new(ErrorCode::UnsafePath, "原工程成果路径含受保护对象").at(path));
    }
    Ok(snapshot.descriptor().cloned())
}
pub fn accept(
    store: &mut Store,
    run_id: &str,
    request: AcceptRequest,
    cancel: &Cancellation,
) -> Result<Value> {
    if let Some(receipt) = acceptance::replay(store, run_id, &request)? {
        return Ok(receipt);
    }
    let mut run = result(store, run_id)?;
    validate_acceptance(&run, &request)?;
    acceptance::ensure_recovered(store, &selected_roots(&run, &request)?)?;
    if run
        .workspaces
        .iter()
        .any(|workspace| workspace.result_snapshot.is_none())
    {
        freeze(store, run_id)?;
        run = result(store, run_id)?;
    }
    let preview = describe_diff(&run, false, cancel)?;
    validate_selection(&preview, &request)?;
    let blobs = selected_result_blobs(&preview, &request, cancel, &|descriptor| {
        frozen_bytes(store, descriptor)
    })?;
    writeback_questions(store, &run, &preview, &request, &|d| {
        captured_blob(&blobs, d)
    })?;
    acceptance::Attempt::begin(store, &run, &request)?;
    let outcome = apply_reviewed(&run, &preview, request, cancel, &|descriptor| {
        captured_blob(&blobs, descriptor)
    })?;
    commit_acceptance(store, run_id, &run.job_id, outcome)
}
fn reject_direct_acceptance(run: &AgentRun) -> Result<()> {
    if run.workspace_mode == WorkspaceMode::Direct {
        return Err(invalid("本轮直接写入原工程，成果无需接收"));
    }
    Ok(())
}

fn validate_acceptance(run: &AgentRun, request: &AcceptRequest) -> Result<()> {
    reject_direct_acceptance(run)?;
    if request.job_id.as_ref().is_some_and(|id| id != &run.job_id) {
        return Err(stale_round(run));
    }
    if !terminal(&run.status) {
        return Err(YssError::new(
            ErrorCode::Locked,
            "运行仍在执行，请结束后接收",
        ));
    }
    if request.files.is_empty() {
        return Err(invalid("请选择需要接收的文件"));
    }
    Ok(())
}

fn selected_roots(run: &AgentRun, request: &AcceptRequest) -> Result<Vec<String>> {
    let roots = request
        .files
        .iter()
        .map(|file| {
            run.workspaces
                .iter()
                .find(|workspace| workspace.project_prefixes.contains_key(&file.project_id))
                .map(|workspace| workspace.original_root.clone())
                .ok_or_else(|| invalid("接收文件不属于本次运行的工程"))
        })
        .collect::<Result<BTreeSet<_>>>()?;
    Ok(roots.into_iter().collect())
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct AcceptanceLease {
    id: String,
    owner: String,
    run_id: String,
    job_id: String,
    roots: Vec<String>,
    #[serde(default)]
    spec_id: String,
    #[serde(default)]
    writeback_questions: BTreeSet<String>,
}
fn lease_owner() -> &'static str {
    static OWNER: OnceLock<String> = OnceLock::new();
    OWNER.get_or_init(|| {
        format!(
            "{}-{}",
            std::process::id(),
            time::OffsetDateTime::now_utc().unix_timestamp_nanos()
        )
    })
}
pub fn ensure_roots_available(store: &mut Store, roots: &[String]) -> Result<()> {
    super::jobs::ensure_grok_roots_available(store, roots)?;
    // Until per-root durable locks exist, conservatively protect registered
    // scratch from all absolute project writes. Synthetic external-read keys
    // do not address the filesystem and can continue to refresh.
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    if roots.iter().any(|root| Path::new(root).is_absolute()) {
        super::command_recovery::ensure_none(store)?;
    }
    for lease in store.records::<AcceptanceLease>("agentAcceptance")? {
        if lease.owner == lease_owner() && super::jobs::roots_overlap(&lease.roots, roots)? {
            return Err(
                YssError::new(ErrorCode::Locked, "目标工程正在接收已审阅成果，请稍后重试")
                    .context("runId", lease.run_id),
            );
        }
    }
    Ok(())
}
fn spec_acceptance(store: &mut Store, spec_id: &str) -> Result<Option<AcceptanceLease>> {
    Ok(store
        .records::<AcceptanceLease>("agentAcceptance")?
        .into_iter()
        .find(|lease| lease.owner == lease_owner() && lease.spec_id == spec_id))
}
pub(super) fn ensure_source_path_available(store: &mut Store, spec_id: &str) -> Result<()> {
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    super::command_recovery::ensure_none(store)?;
    if spec_acceptance(store, spec_id)?.is_some() {
        return Err(YssError::new(
            ErrorCode::Locked,
            "此 Spec 正在接收已审阅成果，请在接收后更改主文件路径",
        ));
    }
    Ok(())
}
pub(super) fn ensure_response_available(
    store: &mut Store,
    spec_id: &str,
    question_id: &str,
) -> Result<()> {
    if spec_acceptance(store, spec_id)?
        .is_some_and(|lease| lease.writeback_questions.contains(question_id))
    {
        return Err(YssError::new(
            ErrorCode::Locked,
            "此问题正在接收已复核的回写，请在接收后保存",
        ));
    }
    Ok(())
}
struct AcceptanceGuard {
    store: Arc<Mutex<Store>>,
    run_id: String,
    id: String,
}
impl Drop for AcceptanceGuard {
    fn drop(&mut self) {
        if let Ok(mut store) = lock_store(&self.store)
            && let Ok(Some(lease)) =
                store.record::<AcceptanceLease>("agentAcceptance", &self.run_id)
            && lease.id == self.id
        {
            let _ = store.delete_record("agentAcceptance", &self.run_id);
        }
    }
}
fn acquire_acceptance(
    shared: &Arc<Mutex<Store>>,
    store: &mut Store,
    run_id: &str,
    request: &AcceptRequest,
) -> Result<(AgentRun, AcceptanceGuard)> {
    let run = result(store, run_id)?;
    validate_acceptance(&run, request)?;
    acceptance::ensure_recovered(store, &selected_roots(&run, request)?)?;
    let roots = run
        .workspaces
        .iter()
        .map(|workspace| workspace.original_root.clone())
        .collect::<Vec<_>>();
    ensure_roots_available(store, &roots)?;
    for job in store.records::<JobRecord>("job")? {
        if !job.terminal() && super::jobs::roots_overlap(&job.roots, &roots)? {
            return Err(YssError::new(
                ErrorCode::Locked,
                "目标工程已有运行中的操作，请完成后重新审阅",
            )
            .context("jobId", job.id));
        }
    }
    let id = fs::unique_id()?;
    store.put_record(
        "agentAcceptance",
        run_id,
        &AcceptanceLease {
            id: id.clone(),
            owner: lease_owner().into(),
            run_id: run_id.into(),
            job_id: run.job_id.clone(),
            roots,
            spec_id: run.spec_id.clone(),
            writeback_questions: BTreeSet::new(),
        },
    )?;
    Ok((
        run,
        AcceptanceGuard {
            store: shared.clone(),
            run_id: run_id.into(),
            id,
        },
    ))
}
fn selected_result_blobs(
    preview: &RunDiff,
    request: &AcceptRequest,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<BTreeMap<String, Vec<u8>>> {
    let selected: BTreeSet<_> = request
        .files
        .iter()
        .map(|file| (file.project_id.as_str(), file.path.as_str()))
        .collect();
    let mut blobs = BTreeMap::new();
    for file in &preview.files {
        if file.pending
            && selected.contains(&(file.project_id.as_str(), file.path.as_str()))
            && let Some(descriptor) = &file.result
            && let std::collections::btree_map::Entry::Vacant(entry) =
                blobs.entry(descriptor.sha256.as_str().to_owned())
        {
            cancel.check()?;
            entry.insert(read_blob(descriptor)?);
        }
    }
    Ok(blobs)
}
fn captured_blob(
    blobs: &BTreeMap<String, Vec<u8>>,
    descriptor: &FileDescriptor,
) -> Result<Vec<u8>> {
    let bytes = blobs
        .get(descriptor.sha256.as_str())
        .ok_or_else(|| invalid("冻结成果字节对象缺失"))?;
    if Sha256::of(bytes) != descriptor.sha256 || bytes.len() as u64 != descriptor.size {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "冻结成果字节对象校验失败",
        ));
    }
    Ok(bytes.clone())
}
pub fn accept_shared(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    request: AcceptRequest,
    cancel: &Cancellation,
) -> Result<Value> {
    let timing = std::env::var_os("YSS_PI_TIMINGS").map(|_| std::time::Instant::now());
    let (mut run, _lease, request_id) = {
        let mut store = lock_store(shared)?;
        if let Some(receipt) = acceptance::replay(&mut store, run_id, &request)? {
            return Ok(receipt);
        }
        let (run, lease) = acquire_acceptance(shared, &mut store, run_id, &request)?;
        let request_id = timing
            .and_then(|_| store.record::<JobRecord>("job", &run.job_id).ok().flatten())
            .map(|job| job.request_id);
        (run, lease, request_id)
    };
    if run
        .workspaces
        .iter()
        .any(|workspace| workspace.result_snapshot.is_none())
    {
        freeze_shared(shared, run_id, cancel)?;
        run = {
            let mut store = lock_store(shared)?;
            result(&mut store, run_id)?
        };
    }
    let preview = describe_diff(&run, false, cancel)?;
    validate_selection(&preview, &request)?;
    let blobs = selected_result_blobs(&preview, &request, cancel, &|descriptor| {
        frozen_bytes(&*lock_store(shared)?, descriptor)
    })?;
    {
        let mut store = lock_store(shared)?;
        let latest = load(&mut store, run_id)?;
        let questions = writeback_questions(&store, &latest, &preview, &request, &|d| {
            captured_blob(&blobs, d)
        })?;
        if !questions.is_empty() {
            let mut lease = store
                .record::<AcceptanceLease>("agentAcceptance", run_id)?
                .ok_or_else(|| invalid("接收期间缺少工程锁"))?;
            lease.writeback_questions = questions;
            store.put_record("agentAcceptance", run_id, &lease)?;
        }
        acceptance::Attempt::begin(&mut store, &run, &request)?;
    }
    let outcome = apply_reviewed(&run, &preview, request, cancel, &|descriptor| {
        captured_blob(&blobs, descriptor)
    })?;
    let mut store = lock_store(shared)?;
    let response = commit_acceptance(&mut store, run_id, &run.job_id, outcome).map_err(|error| {
        YssError::new(
            ErrorCode::RecoveryRequired,
            "接收结果待核实，请刷新并核对文件与事务",
        )
        .context("acceptanceUnverified", true)
        .context("reason", error.message)
    });
    if let Some(started) = timing {
        eprintln!(
            "{}",
            json!({"jobId":run.job_id,"requestId":request_id,"phase":"acceptanceCompleted","elapsedUs":started.elapsed().as_micros()})
        );
    }
    response
}

pub fn acceptance_view(
    shared: &Arc<Mutex<Store>>,
    run_id: &str,
    expected_job_id: Option<&str>,
    attempt_id: Option<&str>,
    verify: bool,
) -> Result<Value> {
    let mut store = lock_store(shared)?;
    // Reconcile the recorded checkout even if the current project registration has moved.
    let run: AgentRun = store
        .record("agentRun", run_id)?
        .filter(|run: &AgentRun| run.id == run_id)
        .ok_or_else(|| invalid("Agent 运行不存在").context("httpStatus", 404))?;
    reject_direct_acceptance(&run)?;
    if expected_job_id.is_some_and(|job_id| job_id != run.job_id) {
        return Err(stale_round(&run));
    }
    Ok(acceptance::view(&mut store, &run, verify, attempt_id)?.unwrap_or(Value::Null))
}

struct AcceptanceOutcome {
    reports: Vec<Value>,
    successful_projects: BTreeSet<String>,
    accepted_paths: BTreeMap<String, BTreeSet<String>>,
    applied_files: Vec<Value>,
}
fn source_metadata(bytes: &[u8]) -> Result<Option<SpecMetadata>> {
    if bytes.len() as u64 > super::model::MAX_SOURCE {
        return Err(invalid("主 Spec 超过 2 MiB 读取上限"));
    }
    let text = std::str::from_utf8(bytes).map_err(|_| invalid("主 Spec 不是 UTF-8"))?;
    spec_content::metadata(text)
}

fn writeback_questions(
    store: &Store,
    run: &AgentRun,
    preview: &RunDiff,
    request: &AcceptRequest,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<BTreeSet<String>> {
    let state = store.state();
    let spec = sources::spec(state, &run.spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let source = Path::new(&project.root_path).join(&spec.source_path);
    let mut questions = BTreeSet::new();
    let file =
        preview.files.iter().find(|file| {
            request.files.iter().any(|selected| {
                selected.project_id == file.project_id && selected.path == file.path
            }) && sources::project(state, &file.project_id)
                .is_ok_and(|p| Path::new(&p.root_path).join(&file.path) == source)
        });
    let Some(file) = file.filter(|file| file.pending) else {
        return Ok(questions);
    };
    let result = file
        .result
        .as_ref()
        .ok_or_else(|| invalid("成果删除了本次主 Spec，无法登记回写结果").at(&file.path))?;
    let proposed = source_metadata(&read_blob(result)?).map_err(|mut error| {
        let field = error
            .path
            .as_ref()
            .map(|path| format!("（{path}）"))
            .unwrap_or_default();
        error.message = format!("主 Spec 成果无法登记{field}：{}", error.message);
        error.at(&file.path)
    })?;
    let conflict = |question_id: &str| {
        YssError::new(ErrorCode::ConcurrentChange,
            format!("问题 {question_id} 的来源、答复或延期安排已变化，无法接收此回写；请依据当前决定更新成果。")).at(&file.path)
    };
    let root = Root::open(Path::new(&project.root_path))?;
    let current = bytes(&root, &spec.source_path, &file.current)?.unwrap_or_default();
    // A valid result may repair an existing malformed source.
    let current_metadata = source_metadata(&current).ok().flatten();
    let basis = source_basis(run, spec);
    let same_source = match &basis {
        Some((_, descriptor)) => Some(descriptor) == file.current.as_ref(),
        // Rebinding changes the checkout identity. An exact match still proves the original bytes.
        None => run.binding_revision != 0 && file.initial.is_some() && file.initial == file.current,
    };
    let saved_metadata = if same_source {
        None
    } else {
        basis
            .and_then(|(_, d)| source_basis_bytes(store, run, &d).ok())
            .map(|bytes| source_metadata(&bytes).ok().flatten())
    };
    let basis_metadata = if same_source {
        Some(&current_metadata)
    } else {
        saved_metadata.as_ref()
    };
    let Some(proposed) = proposed else {
        if current_metadata.is_some() || basis_metadata.is_some_and(Option::is_some) {
            return Err(
                invalid("成果移除了已有的 yss-spec 元数据，请保留主 Spec 的登记信息")
                    .at(&file.path),
            );
        }
        return Ok(questions);
    };
    if !proposed
        .questions
        .iter()
        .any(|q| q.applied_decision_id.is_some() && q.status != QuestionStatus::Open)
    {
        return Ok(questions);
    }
    let index = |metadata: &Option<SpecMetadata>| -> BTreeMap<_, _> {
        metadata
            .iter()
            .flat_map(|m| &m.questions)
            .map(|q| (q.id.clone(), q.clone()))
            .collect()
    };
    let basis_questions = basis_metadata.map(index);
    let current_questions = index(&current_metadata);
    let mut records: Vec<_> = state
        .question_responses
        .iter()
        .filter(|r| r.spec_id == spec.id)
        .cloned()
        .collect();
    for response in run_responses(run) {
        let response = response?;
        if response.spec_id == spec.id {
            include_run_response(&mut records, response);
        }
    }
    let mut by_question = BTreeMap::<&str, Vec<&QuestionResponse>>::new();
    for response in &records {
        by_question
            .entry(&response.question_id)
            .or_default()
            .push(response);
    }
    for question in proposed
        .questions
        .iter()
        .filter(|q| q.applied_decision_id.is_some() && q.status != QuestionStatus::Open)
    {
        let original = current_questions.get(&question.id);
        if original == Some(question) {
            continue;
        }
        if !basis_questions
            .as_ref()
            .is_some_and(|known| original == known.get(&question.id))
        {
            return Err(conflict(&question.id));
        }
        let matching = by_question.remove(question.id.as_str()).unwrap_or_default();
        // A frozen result must not turn a condition already reopened in the source into a valid deferral.
        if let Some(original) = original
            && original.applied_decision_id == question.applied_decision_id
            && matches!(
                spec_content::question_view(original, matching.clone())?.state,
                QuestionState::Overdue | QuestionState::NeedsReview
            )
        {
            return Err(conflict(&question.id));
        }
        let view = spec_content::question_view(question, matching)?;
        if !matches!(
            view.state,
            QuestionState::Resolved | QuestionState::Deferred
        ) || view.response.as_ref().map(|r| &r.id) != question.applied_decision_id.as_ref()
        {
            return Err(conflict(&question.id));
        }
        questions.insert(question.id.clone());
    }
    Ok(questions)
}

fn apply_reviewed(
    run: &AgentRun,
    preview: &RunDiff,
    request: AcceptRequest,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<AcceptanceOutcome> {
    validate_selection(preview, &request)?;
    let mut selected = BTreeMap::<String, Vec<(&DiffFile, Option<FileDescriptor>)>>::new();
    for file in request.files {
        let candidate = preview
            .files
            .iter()
            .find(|f| f.project_id == file.project_id && f.path == file.path)
            .expect("selection validated");
        selected
            .entry(file.project_id)
            .or_default()
            .push((candidate, file.expected_current));
    }
    let mut reports = Vec::new();
    let mut applied_files = Vec::new();
    let mut successful_projects = BTreeSet::new();
    let mut accepted_paths = BTreeMap::<String, BTreeSet<String>>::new();
    let mut blocked_roots = BTreeSet::new();
    for (project_id, files) in &selected {
        let workspace = run
            .workspaces
            .iter()
            .find(|workspace| workspace.project_ids.contains(project_id))
            .ok_or_else(|| invalid("运行工程不存在"))?;
        let root = &workspace.original_root;
        if blocked_roots.contains(root.as_str()) {
            reports.push(
                json!({"projectIds":[project_id],"projectId":project_id,"root":root,
                "status":"failed","disposition":"noWrite",
                "error":{"message":"同一 Git 工程有接收事务待恢复或清理，本项尚未写入"}}),
            );
            continue;
        }
        let pending: Vec<_> = files
            .iter()
            .filter(|(file, _)| file.pending)
            .map(|(file, expected)| {
                (
                    file.workspace_path.clone(),
                    expected.clone(),
                    file.result.clone(),
                )
            })
            .collect();
        if pending.is_empty() {
            accepted_paths
                .entry(workspace.worktree_root.clone())
                .or_default()
                .extend(
                    files
                        .iter()
                        .filter(|(file, _)| file.accepted && file.content_matches)
                        .map(|(file, _)| file.workspace_path.clone()),
                );
            let recorded = files
                .iter()
                .all(|(file, _)| file.accepted && file.content_matches);
            reports.push(
                json!({"projectIds":[project_id],"projectId":project_id,"root":root,
                "status": if recorded {"alreadyApplied"} else {"unchanged"},
                "disposition": if recorded {"applied"} else {"noWrite"}}),
            );
            continue;
        }
        let report = match agent_workspace::apply(
            workspace,
            &preview.git_baselines[root],
            &pending,
            read_blob,
            cancel,
        ) {
            Ok(report) => report,
            Err(error) => {
                let disposition = if error.context.get("transaction").map(String::as_str)
                    == Some("committed")
                {
                    "committedPendingCleanup"
                } else if error.code == ErrorCode::RecoveryRequired {
                    "recoveryRequired"
                } else if error.context.get("rollback").map(String::as_str) == Some("rolled-back") {
                    "rolledBack"
                } else {
                    "noWrite"
                };
                reports.push(
                    json!({"projectIds":[project_id],"projectId":project_id,"root":root,
                    "status":"failed","disposition":disposition,"error":error}),
                );
                if acceptance::requires_verification(disposition) {
                    blocked_roots.insert(root.as_str());
                }
                continue;
            }
        };
        if report.cleanup == "pending" {
            blocked_roots.insert(root.as_str());
        }
        successful_projects.insert(project_id.clone());
        let accepted = accepted_paths
            .entry(workspace.worktree_root.clone())
            .or_default();
        for (file, _) in files.iter().filter(|(file, _)| file.pending) {
            accepted.insert(file.workspace_path.clone());
            applied_files.push(json!({
                "projectId":project_id,"worktreeRoot":workspace.worktree_root,"path":file.workspace_path,
                "jobId":run.job_id,"result":file.result,"before":file.current,"reviewedBefore":file.current_text,
                "reviewToken":preview.review_token,"acceptedAt":sources::now(),"transactionId":report.id
            }));
        }
        reports.push(json!({"projectIds":[project_id],"projectId":project_id,"root":root,"status":"applied",
            "disposition":if report.cleanup == "pending" {"committedPendingCleanup"} else {"applied"},"transaction":report}));
    }
    Ok(AcceptanceOutcome {
        reports,
        successful_projects,
        accepted_paths,
        applied_files,
    })
}

fn validate_selection(preview: &RunDiff, request: &AcceptRequest) -> Result<()> {
    if request.review_token != preview.review_token {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "成果版本或原工程 Git 基线已变化，请刷新差异并重新审阅",
        )
        .context("httpStatus", 409));
    }
    let mut seen = BTreeSet::new();
    for file in &request.files {
        if !seen.insert((file.project_id.clone(), file.path.clone())) {
            return Err(invalid("接收文件重复"));
        }
        let candidate = preview
            .files
            .iter()
            .find(|f| f.project_id == file.project_id && f.path == file.path)
            .ok_or_else(|| invalid("文件已不在当前成果差异中，请刷新"))?;
        if candidate.current != file.expected_current {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "原工程已变化，请重新预览")
                    .at(&file.path),
            );
        }
        if candidate.result != file.expected_result {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "被审阅的成果文件版本已变化，请重新审阅",
            )
            .at(&file.path)
            .context("httpStatus", 409));
        }
        if candidate.conflict && !file.approve_conflict {
            return Err(invalid("请明确确认采用与原工程冲突的隔离结果").at(&file.path));
        }
    }
    Ok(())
}
fn merge_registered_responses(
    state: &mut WorkbenchState,
    run: &AgentRun,
    spec: &SpecRecord,
) -> Result<()> {
    // Only the registered source, after file acceptance, can establish which answers belong here.
    let Ok(Some(metadata)) = sources::read_source(state, &spec.id)
        .and_then(|source| spec_content::metadata(&source.text))
    else {
        return Ok(());
    };
    let mut questions = BTreeMap::new();
    for question in metadata.questions {
        let digest = spec_content::question_digest(&question)?;
        questions.insert(question.id, (digest, question.applied_decision_id));
    }
    for response in run_responses(run) {
        let response = response?;
        if response.spec_id == spec.id
            && questions
                .get(&response.question_id)
                .is_some_and(|(digest, applied)| {
                    response.question_digest == *digest || applied.as_ref() == Some(&response.id)
                })
        {
            include_run_response(&mut state.question_responses, response);
        }
    }
    Ok(())
}

fn commit_acceptance(
    store: &mut Store,
    run_id: &str,
    job_id: &str,
    outcome: AcceptanceOutcome,
) -> Result<Value> {
    let mut run = load(store, run_id)?;
    if run.job_id != job_id {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "接收期间运行代次已变化，请核对已应用文件",
        )
        .context("projects", json!(outcome.reports)));
    }
    let mut state = store.state().clone();
    let at = sources::now();
    let spec = sources::spec(&state, &run.spec_id)?.clone();
    // The same path can be reported under a child project, including on a retry.
    let source_accepted = spec_workspace(&run, &spec).is_some_and(|(workspace, path)| {
        outcome
            .accepted_paths
            .get(&workspace.worktree_root)
            .is_some_and(|paths| paths.contains(&path))
    });
    run.applied_files.extend(outcome.applied_files);
    let refresh = if source_accepted || outcome.successful_projects.contains(&spec.project_id) {
        merge_registered_responses(&mut state, &run, &spec)?;
        super::registration::refresh(&mut state, &run.spec_id, &at).map(|_| ())
    } else {
        Ok(())
    };
    let revision = store.state().revision;
    run.updated_at = at;
    let mut effects = super::sqlite::CommitEffects {
        records: vec![super::sqlite::RecordWrite {
            kind: "agentRun".into(),
            id: run.id.clone(),
            payload: Some(serde_json::to_string(&run).map_err(|error| invalid(error.to_string()))?),
        }],
        blobs: Vec::new(),
    };
    let mut attempt = acceptance::Attempt::load(store, run_id)?
        .filter(|attempt| attempt.job_id == job_id)
        .ok_or_else(|| invalid("接收缺少对应轮次的请求记录"))?;
    let reports = attempt.complete(outcome.reports).to_vec();
    let next_revision = revision
        .checked_add(1)
        .ok_or_else(|| invalid("revision 已溢出"))?;
    let response = json!({"runId":run_id,"specId":run.spec_id,"jobId":job_id,
        "attemptId":attempt.id(),"projects":reports,"refreshError":refresh.err(),"revision":next_revision});
    effects.records.push(attempt.record(run_id)?);
    effects
        .records
        .extend(attempt.receipt(run_id, response.clone())?);
    store.commit_with_effects(revision, state, &effects)?;
    Ok(response)
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RebindBinding {
    pub project_ids: Vec<String>,
    pub previous_root: String,
    pub root: String,
    pub root_identity: crate::fs::RootIdentity,
    pub project_prefixes: BTreeMap<String, String>,
    pub git_baseline: GitBaseline,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RebindPreview {
    pub run_id: String,
    pub preview_token: String,
    pub bindings: Vec<RebindBinding>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct RebindRequest {
    pub preview_token: String,
}
fn rebind_run(store: &mut Store, run_id: &str) -> Result<AgentRun> {
    let mut run: AgentRun = store
        .record("agentRun", run_id)?
        .ok_or_else(|| invalid("Agent 运行不存在").context("httpStatus", 404))?;
    if run.id != run_id {
        return Err(invalid("运行记录 ID 不一致"));
    }
    if run.workspace_mode == WorkspaceMode::Direct {
        return Err(invalid("原工程绑定发生变化，请从 Spec 新建会话"));
    }
    if let Some(job) = store.record::<JobRecord>("job", &run.job_id)? {
        run.status = job.status;
    }
    if !terminal(&run.status) {
        return Err(YssError::new(
            ErrorCode::Locked,
            "运行仍在执行，请结束后重新绑定",
        ));
    }
    if run.workspaces.is_empty()
        || run
            .workspaces
            .iter()
            .any(|workspace| workspace.result_snapshot.is_none())
    {
        return Err(YssError::new(
            ErrorCode::NotReady,
            "仅支持已完整冻结初始版本与成果的终态运行",
        ));
    }
    Ok(run)
}
fn verify_rebind_blobs(shared: &Arc<Mutex<Store>>, run: &AgentRun) -> Result<()> {
    let descriptors: BTreeMap<_, _> = run
        .workspaces
        .iter()
        .flat_map(|workspace| {
            workspace.initial.values().chain(
                workspace
                    .result_snapshot
                    .as_ref()
                    .expect("validated frozen result")
                    .values(),
            )
        })
        .map(|descriptor| (descriptor.sha256.as_str().to_owned(), descriptor.size))
        .collect();
    for (hash, size) in descriptors {
        let bytes = lock_store(shared)?.get_blob(&hash)?;
        if bytes.len() as u64 != size || Sha256::of(&bytes).as_str() != hash {
            return Err(YssError::new(
                ErrorCode::DigestMismatch,
                "重新绑定前发现冻结字节对象不完整",
            ));
        }
    }
    Ok(())
}
fn prepare_rebind(shared: &Arc<Mutex<Store>>, run_id: &str) -> Result<(AgentRun, RebindPreview)> {
    let (run, state) = {
        let mut store = lock_store(shared)?;
        (rebind_run(&mut store, run_id)?, store.state().clone())
    };
    verify_rebind_blobs(shared, &run)?;
    let mut bindings = Vec::new();
    let mut projects_seen = BTreeSet::new();
    for workspace in &run.workspaces {
        let mut proposed_root = None;
        let mut project_prefixes = BTreeMap::new();
        for project_id in &workspace.project_ids {
            if !run.project_ids.contains(project_id) || !projects_seen.insert(project_id) {
                return Err(invalid("运行工程分组无效"));
            }
            let project = sources::project(&state, project_id)?;
            let path = std::fs::canonicalize(&project.root_path)?;
            let root = agent_workspace::git_root(&path)?;
            if proposed_root
                .as_ref()
                .is_some_and(|proposed| proposed != &root)
            {
                return Err(invalid("同一运行工作区的工程须重新绑定到同一 Git 根"));
            }
            let prefix = path
                .strip_prefix(&root)
                .map_err(|_| invalid("工程根超出 Git 根"))?
                .to_string_lossy()
                .replace('\\', "/");
            if workspace.project_prefixes.get(project_id) != Some(&prefix) {
                return Err(invalid(
                    "当前登记工程的 Git 相对路径与运行原布局不同，请核对工程路径",
                ));
            }
            if !prefix.is_empty() {
                RelPath::new(&prefix)?;
            }
            project_prefixes.insert(project_id.clone(), prefix);
            proposed_root = Some(root);
        }
        let root = proposed_root.ok_or_else(|| invalid("运行工作区没有工程映射"))?;
        let root_identity = Root::open(&root)?.identity;
        let git_baseline = agent_workspace::git_baseline(&root, &Cancellation::new())?;
        bindings.push(RebindBinding {
            project_ids: workspace.project_ids.clone(),
            previous_root: workspace.original_root.clone(),
            root: root.to_string_lossy().into(),
            root_identity,
            project_prefixes,
            git_baseline,
        });
    }
    if projects_seen.len() != run.project_ids.len() {
        return Err(invalid("运行工程映射不完整"));
    }
    let bytes = serde_json::to_vec(&(
        "agent.rebind.v1",
        &run.id,
        &run.job_id,
        run.binding_revision,
        &run.workspaces,
        &bindings,
    ))
    .map_err(|error| invalid(error.to_string()))?;
    Ok((
        run,
        RebindPreview {
            run_id: run_id.into(),
            preview_token: Sha256::of(&bytes).as_str().into(),
            bindings,
        },
    ))
}
pub fn rebind_preview(shared: &Arc<Mutex<Store>>, run_id: &str) -> Result<RebindPreview> {
    prepare_rebind(shared, run_id).map(|(_, preview)| preview)
}
pub fn rebind(shared: &Arc<Mutex<Store>>, run_id: &str, request: RebindRequest) -> Result<Value> {
    let (base, preview) = prepare_rebind(shared, run_id).map_err(|error| {
        if matches!(
            error.code,
            ErrorCode::InvalidInput | ErrorCode::Io | ErrorCode::ConcurrentChange
        ) {
            error.context("httpStatus", 409)
        } else {
            error
        }
    })?;
    if request.preview_token != preview.preview_token {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "工程路径、Git 基线或运行版本已变化，请重新核对绑定",
        )
        .context("httpStatus", 409));
    }
    let mut store = lock_store(shared)?;
    let mut run = rebind_run(&mut store, run_id)?;
    if run.job_id != base.job_id
        || run.binding_revision != base.binding_revision
        || run.workspaces != base.workspaces
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "运行绑定版本已变化，请重新核对",
        )
        .context("httpStatus", 409));
    }
    let roots: Vec<_> = preview
        .bindings
        .iter()
        .map(|binding| binding.root.clone())
        .chain(
            base.workspaces
                .iter()
                .map(|workspace| workspace.original_root.clone()),
        )
        .collect();
    ensure_roots_available(&mut store, &roots)?;
    acceptance::ensure_recovered(&mut store, &roots)?;
    for job in store.records::<JobRecord>("job")? {
        if !job.terminal() && super::jobs::roots_overlap(&job.roots, &roots)? {
            return Err(YssError::new(
                ErrorCode::Locked,
                "目标工程存在运行中的操作，请结束后重新绑定",
            )
            .context("jobId", job.id));
        }
    }
    for binding in &preview.bindings {
        for project_id in &binding.project_ids {
            let project = sources::project(store.state(), project_id)?;
            if Path::new(&binding.root).join(&binding.project_prefixes[project_id])
                != Path::new(&project.root_path)
            {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "登记工程路径已变化，请重新核对绑定",
                )
                .context("httpStatus", 409));
            }
        }
    }
    for (workspace, binding) in run.workspaces.iter_mut().zip(&preview.bindings) {
        workspace.original_root = binding.root.clone();
        workspace.original_identity = binding.root_identity.clone();
        workspace.project_prefixes = binding.project_prefixes.clone();
    }
    run.binding_revision = run
        .binding_revision
        .checked_add(1)
        .ok_or_else(|| invalid("运行绑定版本已溢出"))?;
    let previous_applied_files = std::mem::take(&mut run.applied_files);
    run.updated_at = sources::now();
    let rebind_id = fs::unique_id()?;
    let history = json!({"id":rebind_id,"runId":run_id,"jobId":run.job_id,"previousBindings":base.workspaces.iter().map(|workspace|json!({"root":workspace.original_root,"identity":workspace.original_identity,"projectPrefixes":workspace.project_prefixes})).collect::<Vec<_>>(),"bindings":preview.bindings,"previousAppliedFiles":previous_applied_files,"createdAt":run.updated_at});
    store.write_extensions(&super::sqlite::CommitEffects {
        records: vec![
            super::sqlite::RecordWrite {
                kind: "agentRun".into(),
                id: run.id.clone(),
                payload: Some(
                    serde_json::to_string(&run).map_err(|error| invalid(error.to_string()))?,
                ),
            },
            super::sqlite::RecordWrite {
                kind: "agentRebind".into(),
                id: rebind_id.clone(),
                payload: Some(
                    serde_json::to_string(&history).map_err(|error| invalid(error.to_string()))?,
                ),
            },
        ],
        blobs: Vec::new(),
    })?;
    Ok(json!({"runId":run_id,"status":"rebound","bindings":preview.bindings,"rebindId":rebind_id}))
}
