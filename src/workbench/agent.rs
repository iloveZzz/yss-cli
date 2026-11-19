use super::{
    agent_protocol::{self, PiSession, RunRequest},
    agent_workspace::{self, GitBaseline, Workspace},
    content as spec_content,
    jobs::{JobContext, JobManager, JobRecord, lock_store},
    model::{
        ContentView, QuestionKind, QuestionResponse, ResponseKind, SourceDocument, Stage,
        WorkbenchState,
    },
    progress_agent::{self, Activity},
    sources,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{FileDescriptor, MAX_ASSET, RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex, OnceLock},
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AgentStartRequest {
    pub spec_id: String,
    pub prompt: String,
    #[serde(default)]
    pub project_ids: Vec<String>,
    #[serde(default)]
    pub carry_files: BTreeMap<String, Vec<String>>,
    pub request_id: String,
    pub resume_run_id: Option<String>,
    #[serde(default)]
    pub allow_commands: bool,
    #[serde(default)]
    pub lifecycle_stage: Option<Stage>,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
enum AgentHost {
    #[default]
    Codex,
    Pi,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentRun {
    pub id: String,
    pub spec_id: String,
    pub prompt: String,
    pub project_ids: Vec<String>,
    pub status: String,
    pub job_id: String,
    #[serde(default)]
    pub binding_revision: u64,
    #[serde(default)]
    host: AgentHost,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pi_session: Option<PiSession>,
    pub thread_id: Option<String>,
    pub turn_id: Option<String>,
    pub host_settings: Option<Value>,
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
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiffFile {
    pub project_id: String,
    pub path: String,
    pub workspace_path: String,
    pub initial: Option<FileDescriptor>,
    pub result: Option<FileDescriptor>,
    pub current: Option<FileDescriptor>,
    pub conflict: bool,
    pub binary: bool,
    pub initial_text: Option<String>,
    pub result_text: Option<String>,
    pub current_text: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunDiff {
    pub run_id: String,
    pub review_token: String,
    pub git_baselines: BTreeMap<String, GitBaseline>,
    pub files: Vec<DiffFile>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AcceptFile {
    pub project_id: String,
    pub path: String,
    pub expected_current: Option<FileDescriptor>,
    pub expected_result: Option<FileDescriptor>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AcceptRequest {
    pub review_token: String,
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
                ));
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
fn save(context: &JobContext, run: &AgentRun) -> Result<()> {
    lock_store(&context.store)?.put_record("agentRun", &run.id, run)
}
pub fn result(store: &mut Store, run_id: &str) -> Result<AgentRun> {
    load(store, run_id)
}
fn session_cwd(store: &Store, run: &AgentRun) -> Result<PathBuf> {
    let project = &sources::spec(store.state(), &run.spec_id)?.project_id;
    run.workspaces
        .iter()
        .find_map(|workspace| {
            let prefix = workspace.project_prefixes.get(project)?;
            Some(Path::new(&workspace.worktree_root).join(prefix))
        })
        .ok_or_else(|| invalid("运行缺少主 Spec 工程"))
}
fn can_resume(store: &Store, run: &AgentRun) -> bool {
    agent_protocol::supported()
        && run.host == AgentHost::Pi
        && terminal(&run.status)
        && run.pi_session.as_ref().is_some_and(|session| {
            session_cwd(store, run).is_ok_and(|cwd| {
                run.workspaces.iter().all(|workspace| {
                    Root::open(Path::new(&workspace.worktree_root))
                        .is_ok_and(|root| root.identity == workspace.worktree_identity)
                }) && agent_protocol::session_available(store.data_dir(), &run.id, &cwd, session)
            })
        })
}
pub fn view(store: &mut Store, run_id: &str) -> Result<Value> {
    let run = load(store, run_id)?;
    let resume = can_resume(store, &run);
    let mut value = serde_json::to_value(run).map_err(|_| invalid("执行详情编码失败"))?;
    let fields = value.as_object_mut().expect("AgentRun is an object");
    fields.remove("piSession");
    fields.insert("canResume".into(), json!(resume));
    Ok(value)
}
pub fn start(manager: Arc<JobManager>, mut request: AgentStartRequest) -> Result<JobRecord> {
    if request.prompt.trim().is_empty() || request.prompt.len() > 128 * 1024 {
        return Err(invalid("请填写 128 KiB 以内的续办要求"));
    }
    let (projects, prior, spec) = {
        let mut store = lock_store(&manager.store)?;
        let spec = sources::spec(store.state(), &request.spec_id)?.clone();
        if !spec.active {
            return Err(invalid("主 Spec 已归档"));
        }
        if request.project_ids.is_empty() {
            request.project_ids.push(spec.project_id.clone());
        }
        if !request.project_ids.contains(&spec.project_id) {
            request.project_ids.push(spec.project_id.clone());
        }
        request.project_ids.sort();
        request.project_ids.dedup();
        if request
            .carry_files
            .keys()
            .any(|id| !request.project_ids.contains(id))
        {
            return Err(invalid("带入文件引用了运行范围外的工程"));
        }
        for paths in request.carry_files.values() {
            for path in paths {
                let rel = RelPath::new(path)?;
                if !agent_workspace::allowed(&rel) {
                    return Err(invalid("不能带入受保护的控制文件"));
                }
            }
        }
        let projects = request
            .project_ids
            .iter()
            .map(|id| {
                let p = sources::project(store.state(), id)?;
                if !p.active {
                    return Err(invalid("运行工程已归档"));
                }
                Ok((id.clone(), p.root_path.clone()))
            })
            .collect::<Result<Vec<_>>>()?;
        let prior = if let Some(id) = &request.resume_run_id {
            let prior = result(&mut store, id)?;
            if prior.spec_id != request.spec_id || prior.project_ids != request.project_ids {
                return Err(invalid("续接必须使用原 Spec 与原工程范围"));
            }
            if !matches!(
                prior.status.as_str(),
                "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
            ) {
                return Err(YssError::new(ErrorCode::Locked, "此运行仍在执行"));
            }
            if prior.host != AgentHost::Pi {
                return Err(invalid("此运行使用 Codex，请从 Spec 发起新的 Pi 运行"));
            }
            if !can_resume(&store, &prior) {
                return Err(invalid("Pi 会话或工作区不可用，请从 Spec 重新发起"));
            }
            if !request.carry_files.is_empty() {
                return Err(invalid("续接沿用原运行初始快照，请勿再次带入文件"));
            }
            for workspace in &prior.workspaces {
                agent_workspace::validate(workspace)?;
            }
            Some(prior)
        } else {
            None
        };
        (projects, prior, spec)
    };
    let cancel = Cancellation::new();
    let roots = projects
        .iter()
        .map(|(id, path)| {
            let path = Path::new(path);
            let root = agent_workspace::agent_git_root(path, &cancel)?;
            if prior.is_none()
                && id == &spec.project_id
                && !request
                    .carry_files
                    .get(id)
                    .is_some_and(|paths| paths.contains(&spec.source_path))
            {
                agent_workspace::check_spec_at_head(&root, path, &spec.source_path, &cancel)?;
            }
            Ok(root.to_string_lossy().into_owned())
        })
        .collect::<Result<BTreeSet<_>>>()?
        .into_iter()
        .collect();
    let pi = agent_protocol::require_runtime(&cancel)?;
    let digest = Sha256::of(&serde_json::to_vec(&request).map_err(|e| invalid(e.to_string()))?);
    let title = format!("Spec 续办 · {} · {}", spec.title, &digest.as_str()[..8]);
    manager.start(
        "agent".into(),
        title,
        request.request_id.clone(),
        roots,
        move |context| execute(context, request, projects, prior, &pi),
    )
}
fn mapped_state(store: &Store, run: &AgentRun) -> Result<WorkbenchState> {
    let mut state = store.state().clone();
    for workspace in &run.workspaces {
        let root = view_root(store, &run.id, workspace, terminal(&run.status))?;
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
    for value in &run.answers {
        if let Some(response) = value.get("questionResponse") {
            let response: QuestionResponse =
                serde_json::from_value(response.clone()).map_err(|e| invalid(e.to_string()))?;
            if !state.question_responses.iter().any(|r| r.id == response.id) {
                state.question_responses.push(response);
            }
        }
    }
    Ok(state)
}
fn execute(
    context: JobContext,
    request: AgentStartRequest,
    projects: Vec<(String, String)>,
    prior: Option<AgentRun>,
    pi: &Path,
) -> Result<Value> {
    let at = sources::now();
    let resumed = prior.is_some();
    let previous_job_id = prior.as_ref().map(|run| run.job_id.clone());
    let mut store = lock_store(&context.store)?;
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
        Some(current)
    } else {
        None
    };
    let mut run = prior.unwrap_or_else(|| AgentRun {
        id: context.id.clone(),
        spec_id: request.spec_id.clone(),
        prompt: request.prompt.clone(),
        project_ids: request.project_ids.clone(),
        status: "preparing".into(),
        job_id: context.id.clone(),
        binding_revision: 0,
        host: AgentHost::Pi,
        pi_session: None,
        thread_id: None,
        turn_id: None,
        host_settings: None,
        workspaces: Vec::new(),
        answers: Vec::new(),
        applied_files: Vec::new(),
        created_at: at.clone(),
        updated_at: at,
        lifecycle_stage: None,
        goal_version: None,
        spec_digest: None,
    });
    run.job_id = context.id.clone();
    run.prompt = request.prompt.clone();
    run.status = "preparing".into();
    run.updated_at = sources::now();
    let binding = progress_agent::bind_run(&mut store, &mut run, request.lifecycle_stage);
    store.put_record_with_event(
        "agentRun",
        &run.id,
        &run,
        Some(super::sqlite::EventWrite {
            job_id: &context.id,
            kind: "run",
            payload: &json!({
                "runId": run.id, "resumed": resumed, "host": run.host,
                "allowCommands": request.allow_commands, "prompt": request.prompt,
                "previousJobId": previous_job_id
            }),
        }),
    )?;
    drop(store);
    if let Err(error) = binding {
        let _ = context.emit(
            "progressError",
            json!({"runId":run.id,"activity":"bind","error":error}),
        );
    }
    progress_agent::note(&context, &run, Activity::Started);
    let work = (|| -> Result<Value> {
        let (data_dir, spec) = {
            let store = lock_store(&context.store)?;
            (
                store.data_dir().to_path_buf(),
                sources::spec(store.state(), &run.spec_id)?.clone(),
            )
        };
        if !resumed {
            run.workspaces = agent_workspace::prepare_for_spec(
                &data_dir,
                &run.id,
                &projects,
                &request.carry_files,
                &spec,
                &context.cancel,
            )?;
            let mut store = lock_store(&context.store)?;
            for workspace in &run.workspaces {
                let mut blobs = BTreeMap::new();
                for descriptor in workspace.initial.values() {
                    let bytes = agent_workspace::initial_bytes(&data_dir, &run.id, descriptor)?;
                    blobs
                        .entry(descriptor.sha256.as_str().to_owned())
                        .or_insert(bytes);
                }
                store.write_extensions(&super::sqlite::CommitEffects {
                    records: Vec::new(),
                    blobs: blobs.into_iter().collect(),
                })?;
            }
            store.put_record("agentRun", &run.id, &run)?;
        } else {
            for workspace in &mut run.workspaces {
                agent_workspace::bind_spec(workspace, &spec);
                workspace.result_snapshot = None;
            }
            save(&context, &run)?;
        }
        for workspace in &run.workspaces {
            agent_workspace::validate(workspace)?;
        }
        let (mut instructions, cwd, primary_project) = {
            let store = lock_store(&context.store)?;
            let mapped = mapped_state(&store, &run)?;
            let spec = sources::spec(&mapped, &run.spec_id)?;
            let project = sources::project(&mapped, &spec.project_id)?;
            if Root::open(Path::new(&project.root_path))?
                .inspect(&RelPath::new(&spec.source_path)?)?
                == FileSnapshot::Missing
            {
                return Err(YssError::new(
                    ErrorCode::NotReady,
                    "隔离工作区缺少主 Spec，请在发起时选择带入该文件",
                )
                .at(&spec.source_path));
            }
            let mut text = spec_content::agent_request(&mapped, &run.spec_id, &data_dir)?.text;
            if let Some(index) = text.find("此工作台的所有后续命令") {
                text.truncate(index);
            }
            (text, project.root_path.clone(), spec.project_id.clone())
        };
        instructions.push_str(&format!("\n运行 ID：{}\n工作区映射：{}\n在列出的隔离工作区完成此任务。所有文件修改限定在这些工作区内，保持 Git HEAD、分支与 index。新增业务问题先写入主 Spec 的 questions，再用 ask_spec_question 提交该问题的 ID。工作台将保存实际答复并在工具结果中返回答复 ID，落实答复后以该 ID 写入 appliedDecisionId。运行结束后由用户在工作台接收文件并刷新原 Spec 记录。\n用户本次要求：\n{}\n",run.id,serde_json::to_string_pretty(&run.workspaces.iter().map(|w|json!({"projectIds":w.project_ids,"worktreeRoot":w.worktree_root,"projectPrefixes":w.project_prefixes})).collect::<Vec<_>>()).map_err(|e|invalid(e.to_string()))?,request.prompt));
        match lock_store(&context.store)
            .and_then(|mut store| progress_agent::run_context(&mut store, &run))
        {
            Ok(progress_instructions) => instructions.push_str(&progress_instructions),
            Err(error) => {
                let _ = context.emit(
                    "progressError",
                    json!({"runId":run.id,"activity":"context","error":error}),
                );
            }
        }
        run.status = "running".into();
        save(&context, &run)?;
        let run_id = run.id.clone();
        let question_run = run.id.clone();
        agent_protocol::run(
            RunRequest {
                executable: pi,
                data_dir: &data_dir,
                run_id: &run.id,
                cwd: Path::new(&cwd),
                primary_project: &primary_project,
                workspaces: &run.workspaces,
                prompt: &instructions,
                resume: run.pi_session.as_ref(),
                allow_commands: request.allow_commands,
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
                store.put_record("agentRun", &run_id, &latest)
            },
            move |context, question_id| ask_question(context, &question_run, question_id),
        )?;
        let mut store = lock_store(&context.store)?;
        let mut latest = load(&mut store, &run.id)?;
        latest.status = "completed".into();
        latest.updated_at = sources::now();
        store.put_record("agentRun", &latest.id, &latest)?;
        Ok(
            json!({"runId": latest.id, "host": latest.host, "status": "completed", "diffAvailable": true}),
        )
    })();
    if let Err(error) = &work
        && let Ok(mut store) = lock_store(&context.store)
        && let Ok(mut latest) = load(&mut store, &run.id)
    {
        latest.status = if error.code == ErrorCode::Cancelled {
            "interrupted"
        } else {
            "failed"
        }
        .into();
        latest.updated_at = sources::now();
        let _ = store.put_record("agentRun", &latest.id, &latest);
    }
    let frozen = freeze_shared(&context.store, &run.id, &Cancellation::new());
    progress_agent::note(
        &context,
        &run,
        if work.is_ok() && frozen.is_ok() {
            Activity::Completed
        } else {
            Activity::Interrupted
        },
    );
    match (work, frozen) {
        (Ok(value), Ok(())) => Ok(value),
        (Err(error), Ok(())) => Err(error),
        (Ok(_), Err(error)) => Err(error),
        (Err(error), Err(freeze)) => Err(error.context("snapshotError", freeze.message)),
    }
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
        if run.workspaces.is_empty() {
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
        if run.workspaces.is_empty() {
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
fn view_root(
    store: &Store,
    run_id: &str,
    workspace: &Workspace,
    prefer_frozen: bool,
) -> Result<std::path::PathBuf> {
    if Path::new(&workspace.worktree_root).exists()
        && (!prefer_frozen || workspace.result_snapshot.is_none())
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
pub fn project_root(
    store: &mut Store,
    run_id: &str,
    project_id: &str,
) -> Result<std::path::PathBuf> {
    let run = load(store, run_id)?;
    let workspace = run
        .workspaces
        .iter()
        .find(|w| w.project_ids.iter().any(|id| id == project_id))
        .ok_or_else(|| invalid("运行没有关联此工程"))?;
    Ok(view_root(store, run_id, workspace, terminal(&run.status))?
        .join(&workspace.project_prefixes[project_id]))
}
pub fn document(
    store: &mut Store,
    run_id: &str,
    spec_id: &str,
    path: &str,
) -> Result<SourceDocument> {
    let run = load(store, run_id)?;
    if run.spec_id != spec_id {
        return Err(invalid("Spec 与运行不匹配"));
    }
    super::registration::read_document(&mapped_state(store, &run)?, spec_id, path)
}
pub fn source(store: &mut Store, run_id: &str, spec_id: &str) -> Result<SourceDocument> {
    let run = load(store, run_id)?;
    if run.spec_id != spec_id {
        return Err(invalid("Spec 与运行不匹配"));
    }
    sources::read_source(&mapped_state(store, &run)?, spec_id)
}
pub fn content(store: &mut Store, run_id: &str, spec_id: &str) -> Result<ContentView> {
    let run = load(store, run_id)?;
    if spec_id != run.spec_id {
        return Err(invalid("Spec 与运行不匹配"));
    }
    spec_content::read_view(&mapped_state(store, &run)?, spec_id)
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
        store.put_record("agentRun", &run.id, &run)?;
    }
    Ok(response)
}
fn ask_question(context: &JobContext, run_id: &str, question_id: &str) -> Result<String> {
    let (spec_id, question) = {
        let mut store = lock_store(&context.store)?;
        let run = load(&mut store, run_id)?;
        let question = content(&mut store, run_id, &run.spec_id)?
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
    if let Some(saved) = &question.response
        && saved.question_digest == question.digest
        && saved.kind == ResponseKind::Answer
    {
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
                review_utc_offset_minutes: 0,
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
pub fn diff_shared(shared: &Arc<Mutex<Store>>, run_id: &str) -> Result<RunDiff> {
    let run = {
        let mut store = lock_store(shared)?;
        result(&mut store, run_id)?
    };
    let cancel = Cancellation::new();
    let blobs = capture_blobs(shared, &run, &cancel)?;
    diff_run(&run, &cancel, &|descriptor| {
        captured_blob(&blobs, descriptor)
    })
}
fn diff_run(
    run: &AgentRun,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<RunDiff> {
    let run_id = &run.id;
    let mut files = Vec::new();
    let mut git_baselines = BTreeMap::new();
    let mut manifests = BTreeMap::new();
    for workspace in &run.workspaces {
        let baseline = agent_workspace::git_baseline(Path::new(&workspace.original_root), cancel)?;
        let use_frozen = matches!(
            run.status.as_str(),
            "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
        ) && workspace.result_snapshot.is_some();
        let final_state = if use_frozen {
            workspace.result_snapshot.as_ref().unwrap().clone()
        } else {
            agent_workspace::validate(workspace)?;
            agent_workspace::working_snapshot(workspace, cancel)?
        };
        manifests.insert(workspace.original_root.clone(), final_state.clone());
        let original = Root::open(Path::new(&workspace.original_root))?;
        if original.identity != workspace.original_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "原工程目录身份已改变",
            ));
        }
        let paths: BTreeSet<_> = workspace.initial.keys().chain(final_state.keys()).collect();
        for path in paths {
            cancel.check()?;
            let initial = workspace.initial.get(path).cloned();
            let result = final_state.get(path).cloned();
            if initial == result {
                continue;
            }
            let (project_id, prefix) = workspace
                .project_prefixes
                .iter()
                .filter(|(_, prefix)| prefix.is_empty() || path.starts_with(&format!("{prefix}/")))
                .max_by_key(|(_, prefix)| prefix.len())
                .ok_or_else(|| invalid("成果文件无法映射到已选择工程"))?;
            let relative = if prefix.is_empty() {
                path.clone()
            } else {
                path[prefix.len() + 1..].into()
            };
            let snapshot = original.inspect(&RelPath::new(path)?)?;
            if matches!(
                snapshot,
                crate::fs::FileSnapshot::Dangerous | crate::fs::FileSnapshot::Directory { .. }
            ) {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "原工程成果路径含受保护对象").at(path),
                );
            }
            let current = snapshot.descriptor().cloned();
            let initial_data = initial.as_ref().map(read_blob).transpose()?;
            let result_data = if use_frozen {
                result.as_ref().map(read_blob).transpose()?
            } else {
                bytes(
                    &Root::open(Path::new(&workspace.worktree_root))?,
                    path,
                    &result,
                )?
            };
            let current_data = bytes(&original, path, &current)?;
            let binary = initial_data
                .as_ref()
                .into_iter()
                .chain(result_data.as_ref())
                .chain(current_data.as_ref())
                .any(|b| b.contains(&0) || std::str::from_utf8(b).is_err());
            files.push(DiffFile {
                project_id: project_id.clone(),
                path: relative,
                workspace_path: path.clone(),
                conflict: current != initial && current != result,
                initial,
                result,
                current,
                binary,
                initial_text: text(initial_data),
                result_text: text(result_data),
                current_text: text(current_data),
            });
        }
        agent_workspace::check_baseline(Path::new(&workspace.original_root), &baseline, cancel)?;
        git_baselines.insert(workspace.original_root.clone(), baseline);
    }
    let reviewed_files: Vec<_> = files
        .iter()
        .map(|file| {
            (
                &file.project_id,
                &file.path,
                &file.initial,
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
        review_token: Sha256::of(&token_bytes).as_str().to_owned(),
        git_baselines,
        files,
    })
}
pub fn accept(
    store: &mut Store,
    run_id: &str,
    request: AcceptRequest,
    cancel: &Cancellation,
) -> Result<Value> {
    let mut run = result(store, run_id)?;
    validate_acceptance(&run, &request)?;
    if run
        .workspaces
        .iter()
        .any(|workspace| workspace.result_snapshot.is_none())
    {
        freeze(store, run_id)?;
        run = result(store, run_id)?;
    }
    let preview = diff_run(&run, cancel, &|descriptor| frozen_bytes(store, descriptor))?;
    let outcome = apply_reviewed(&run, &preview, request, cancel, &|descriptor| {
        frozen_bytes(store, descriptor)
    })?;
    commit_acceptance(store, run_id, &run.job_id, outcome)
}
fn validate_acceptance(run: &AgentRun, request: &AcceptRequest) -> Result<()> {
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
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct AcceptanceLease {
    id: String,
    owner: String,
    run_id: String,
    job_id: String,
    roots: Vec<String>,
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
fn overlapping(left: &[String], right: &[String]) -> bool {
    left.iter().any(|root| {
        right
            .iter()
            .any(|other| Path::new(root).starts_with(other) || Path::new(other).starts_with(root))
    })
}
pub fn ensure_roots_available(store: &mut Store, roots: &[String]) -> Result<()> {
    if let Some(lease) = store
        .records::<AcceptanceLease>("agentAcceptance")?
        .into_iter()
        .find(|lease| lease.owner == lease_owner() && overlapping(&lease.roots, roots))
    {
        return Err(
            YssError::new(ErrorCode::Locked, "目标工程正在接收已审阅成果，请稍后重试")
                .context("runId", lease.run_id),
        );
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
    run_id: &str,
    request: &AcceptRequest,
) -> Result<(AgentRun, AcceptanceGuard)> {
    let mut store = lock_store(shared)?;
    let run = result(&mut store, run_id)?;
    validate_acceptance(&run, request)?;
    let roots = run
        .workspaces
        .iter()
        .map(|workspace| workspace.original_root.clone())
        .collect::<Vec<_>>();
    ensure_roots_available(&mut store, &roots)?;
    if let Some(job) = store
        .records::<JobRecord>("job")?
        .into_iter()
        .find(|job| !job.terminal() && overlapping(&job.roots, &roots))
    {
        return Err(YssError::new(
            ErrorCode::Locked,
            "目标工程已有运行中的操作，请完成后重新审阅",
        )
        .context("jobId", job.id));
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
fn capture_blobs(
    shared: &Arc<Mutex<Store>>,
    run: &AgentRun,
    cancel: &Cancellation,
) -> Result<BTreeMap<String, Vec<u8>>> {
    let mut hashes = BTreeSet::new();
    for workspace in &run.workspaces {
        let result = workspace.result_snapshot.as_ref();
        if !terminal(&run.status) || result.is_none() {
            hashes.extend(
                workspace
                    .initial
                    .values()
                    .map(|descriptor| descriptor.sha256.as_str().to_owned()),
            );
            continue;
        }
        let result = result.expect("checked frozen result");
        for path in workspace.initial.keys().chain(result.keys()) {
            let before = workspace.initial.get(path);
            let after = result.get(path);
            if before != after {
                for descriptor in before.into_iter().chain(after) {
                    hashes.insert(descriptor.sha256.as_str().to_owned());
                }
            }
        }
    }
    hashes
        .into_iter()
        .map(|hash| {
            cancel.check()?;
            let bytes = lock_store(shared)?.get_blob(&hash)?;
            Ok((hash, bytes))
        })
        .collect()
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
    let (mut run, _lease) = acquire_acceptance(shared, run_id, &request)?;
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
    let blobs = capture_blobs(shared, &run, cancel)?;
    cancel.check()?;
    let preview = diff_run(&run, cancel, &|descriptor| {
        captured_blob(&blobs, descriptor)
    })?;
    let outcome = apply_reviewed(&run, &preview, request, cancel, &|descriptor| {
        captured_blob(&blobs, descriptor)
    })?;
    let mut store = lock_store(shared)?;
    commit_acceptance(&mut store, run_id, &run.job_id, outcome)
}
struct AcceptanceOutcome {
    reports: Vec<Value>,
    successful_projects: BTreeSet<String>,
    applied_files: Vec<Value>,
}
fn apply_reviewed(
    run: &AgentRun,
    preview: &RunDiff,
    request: AcceptRequest,
    cancel: &Cancellation,
    read_blob: &dyn Fn(&FileDescriptor) -> Result<Vec<u8>>,
) -> Result<AcceptanceOutcome> {
    if request.review_token != preview.review_token {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "成果版本或原工程 Git 基线已变化，请刷新差异并重新审阅",
        )
        .context("httpStatus", 409));
    }
    let mut selected =
        BTreeMap::<String, Vec<(String, Option<FileDescriptor>, Option<FileDescriptor>)>>::new();
    let mut seen = BTreeSet::new();
    for file in request.files {
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
        if !run
            .workspaces
            .iter()
            .any(|workspace| workspace.project_ids.contains(&file.project_id))
        {
            return Err(invalid("运行工程不存在"));
        }
        selected.entry(file.project_id.clone()).or_default().push((
            candidate.workspace_path.clone(),
            file.expected_current,
            candidate.result.clone(),
        ));
    }
    let mut reports = Vec::new();
    let mut applied_files = Vec::new();
    let mut successful_projects = BTreeSet::new();
    for (project_id, files) in &selected {
        let workspace = run
            .workspaces
            .iter()
            .find(|workspace| workspace.project_ids.contains(project_id))
            .ok_or_else(|| invalid("运行工程不存在"))?;
        if files.iter().all(|(path, expected, result)| {
            expected == result
                && run.applied_files.iter().any(|record| {
                    record.get("worktreeRoot").and_then(Value::as_str)
                        == Some(&workspace.worktree_root)
                        && record.get("path").and_then(Value::as_str) == Some(path)
                })
        }) {
            reports.push(
                json!({"projectIds":[project_id],"projectId":project_id,"status":"alreadyApplied"}),
            );
            continue;
        }
        match agent_workspace::apply(
            workspace,
            &preview.git_baselines[&workspace.original_root],
            files,
            read_blob,
            cancel,
        ) {
            Ok(report) => {
                successful_projects.insert(project_id.clone());
                for (path, _, result) in files {
                    applied_files.push(json!({"projectId":project_id,"worktreeRoot":workspace.worktree_root,"path":path,"result":result,"acceptedAt":sources::now(),"transactionId":report.id}));
                }
                reports.push(json!({"projectIds":[project_id],"projectId":project_id,"status":"applied","transaction":report}));
            }
            Err(error) => reports
                .push(json!({"projectIds":[project_id],"projectId":project_id,"status":"failed","error":error})),
        }
    }
    Ok(AcceptanceOutcome {
        reports,
        successful_projects,
        applied_files,
    })
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
    run.applied_files.extend(outcome.applied_files);
    let mut state = store.state().clone();
    let at = sources::now();
    for answer in &run.answers {
        if let Some(value) = answer.get("questionResponse") {
            let response: QuestionResponse =
                serde_json::from_value(value.clone()).map_err(|e| invalid(e.to_string()))?;
            let spec = sources::spec(&state, &response.spec_id)?;
            if outcome.successful_projects.contains(&spec.project_id)
                && !state.question_responses.iter().any(|r| r.id == response.id)
            {
                state.question_responses.push(response);
            }
        }
    }
    let spec = sources::spec(&state, &run.spec_id)?.clone();
    let refresh = if outcome.successful_projects.contains(&spec.project_id) {
        super::registration::refresh(&mut state, &run.spec_id, &at).map(|_| ())
    } else {
        Ok(())
    };
    let revision = store.state().revision;
    run.updated_at = at;
    let effects = super::sqlite::CommitEffects {
        records: vec![super::sqlite::RecordWrite {
            kind: "agentRun".into(),
            id: run.id.clone(),
            payload: Some(serde_json::to_string(&run).map_err(|error| invalid(error.to_string()))?),
        }],
        blobs: Vec::new(),
    };
    store.commit_with_effects(revision, state, &effects)?;
    if let Err(error) = progress_agent::record(store, &run, Activity::Accepted) {
        let _ = store.append_event(
            job_id,
            "progressError",
            &json!({"runId":run.id,"activity":"accepted","error":error}),
        );
    }
    Ok(
        json!({"runId":run_id,"specId":run.spec_id,"projects":outcome.reports,"refreshError":refresh.err(),"revision":store.state().revision}),
    )
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
    if let Some(job) = store
        .records::<JobRecord>("job")?
        .into_iter()
        .find(|job| !job.terminal() && overlapping(&job.roots, &roots))
    {
        return Err(YssError::new(
            ErrorCode::Locked,
            "目标工程存在运行中的操作，请结束后重新绑定",
        )
        .context("jobId", job.id));
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
