use super::*;
use crate::workbench::{
    agent_protocol::{
        NATIVE_FORK_ANCHOR_RECORD, NativeForkAnchor, native_fork_supported,
        native_fork_version_supported, verify_native_fork_source,
    },
    agent_scope::{SourceRound, digest},
};

pub(super) const SOURCE_ROUND: &str = "agentNativeForkSourceRound";

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct NativeForkSource {
    run_id: String,
    job_id: String,
    expected_fork_digest: Sha256,
}

impl NativeForkSource {
    pub(super) fn origin(&self) -> ContinuationSource {
        ContinuationSource {
            run_id: self.run_id.clone(),
            job_id: self.job_id.clone(),
        }
    }
}

#[derive(Serialize)]
#[serde(
    tag = "state",
    rename_all = "lowercase",
    rename_all_fields = "camelCase"
)]
pub(in crate::workbench) enum Offer {
    Available {
        digest: Sha256,
        model: PiModel,
        project_ids: Vec<String>,
    },
    Blocked {
        reason_code: String,
        message: String,
    },
    Unsupported {
        reason_code: String,
        message: String,
    },
}

pub(in crate::workbench) struct Target {
    anchor: NativeForkAnchor,
    spec: SpecRecord,
    projects: Vec<(String, String)>,
    workspaces: Vec<Workspace>,
    data_dir: PathBuf,
    cwd: PathBuf,
}

pub(super) struct Verified {
    pub(super) anchor: NativeForkAnchor,
    pub(super) source_workspaces: Vec<Workspace>,
    baselines: BTreeMap<String, GitBaseline>,
    digest: Sha256,
}

fn issue(code: ErrorCode, reason: &str, message: &str) -> YssError {
    YssError::new(code, message)
        .context("nativeForkIssue", reason)
        .context("httpStatus", 409)
}

fn workspace_changed() -> YssError {
    issue(
        ErrorCode::ConcurrentChange,
        "workspaceChanged",
        "来源工程的登记或 Git 检出已变化，请核对后重试",
    )
    .context("projectScopeChanged", true)
}

fn source_unavailable() -> YssError {
    issue(
        ErrorCode::NotReady,
        "sourceUnavailable",
        "无法核对该轮与原 Pi 会话的归属",
    )
}

fn recovery_required() -> YssError {
    issue(
        ErrorCode::RecoveryRequired,
        "recoveryRequired",
        "来源工程有待恢复或核实的接收结果",
    )
}

fn anchor_changed() -> YssError {
    issue(
        ErrorCode::ConcurrentChange,
        "anchorChanged",
        "来源 Pi 会话记录已变化或不可用，请重新查看可用的接续方式",
    )
}

fn preview_changed() -> YssError {
    issue(
        ErrorCode::ConcurrentChange,
        "previewChanged",
        "原生分叉预览已变化，请重新核对后发送",
    )
}

fn preserve_cancellation(error: YssError, failure: impl FnOnce() -> YssError) -> YssError {
    if error.code == ErrorCode::Cancelled {
        error
    } else {
        failure()
    }
}

fn session_matches(run: &AgentRun, anchor: &NativeForkAnchor) -> bool {
    run.pi_session.as_ref() == Some(&anchor.session)
}

pub(in crate::workbench) fn capture(
    store: &mut Store,
    spec_id: &str,
    source: &ContinuationSource,
    round: SourceRound,
) -> Result<Target> {
    if !native_fork_supported() {
        return Err(issue(
            ErrorCode::UnsupportedPlatform,
            "platformUnsupported",
            "当前平台无法使用 Pi 原生分叉",
        ));
    }
    if round.job.status != "completed" {
        return Err(issue(
            ErrorCode::NotReady,
            "sourceIncomplete",
            "原生分叉需要已成功结束的 Pi 轮次",
        ));
    }
    let spec = sources::spec(store.state(), spec_id)?.clone();
    if !spec.active {
        return Err(workspace_changed());
    }
    let latest = round.latest.as_ref().ok_or_else(source_unavailable)?;
    for job in store
        .records::<JobRecord>("job")?
        .into_iter()
        .filter(|job| {
            job.id == latest.job_id || job.session_run_id() == Some(source.run_id.as_str())
        })
    {
        if !job.terminal() {
            return Err(issue(
                ErrorCode::Locked,
                "sourceActive",
                "来源会话仍有准备或执行中的作业，请等待结束",
            ));
        }
        if job.id == latest.job_id && job.status == "recoveryRequired" {
            return Err(recovery_required());
        }
    }
    let anchor = store
        .record::<NativeForkAnchor>(NATIVE_FORK_ANCHOR_RECORD, &source.job_id)
        .map_err(|error| {
            if error.code == ErrorCode::InvalidInput {
                anchor_changed()
            } else {
                error
            }
        })?
        .ok_or_else(|| {
            issue(
                ErrorCode::UnsupportedFormat,
                "anchorMissing",
                "该轮未保留可用于原生分叉的 Pi 锚点",
            )
        })?;
    if anchor.parent_id.is_none() {
        return Err(issue(
            ErrorCode::UnsupportedFormat,
            "anchorHasNoParent",
            "该轮的 Pi 锚点没有可继承的历史消息",
        ));
    }
    if anchor.run_id != source.run_id
        || anchor.job_id != source.job_id
        || !session_matches(latest, &anchor)
    {
        return Err(source_unavailable());
    }
    let run = match round.into_run() {
        Some(run) => run,
        None => store
            .record::<AgentRun>(SOURCE_ROUND, &source.job_id)?
            .filter(|run| {
                run.id == source.run_id && run.spec_id == spec_id && run.job_id == source.job_id
            })
            .ok_or_else(source_unavailable)?,
    };
    if run.host != AgentHost::Pi
        || run.workspace_mode != WorkspaceMode::Direct
        || run.preparation_failed
        || !session_matches(&run, &anchor)
        || recorded_model(&run).ok().as_ref() != Some(&anchor.model)
    {
        return Err(source_unavailable());
    }
    let mut projects = BTreeMap::new();
    for workspace in &run.workspaces {
        if workspace.project_ids.len() != workspace.project_prefixes.len() {
            return Err(workspace_changed());
        }
        for id in &workspace.project_ids {
            let prefix = workspace
                .project_prefixes
                .get(id)
                .ok_or_else(workspace_changed)?;
            if !prefix.is_empty() {
                RelPath::new(prefix).map_err(|_| workspace_changed())?;
            }
            let project = sources::project(store.state(), id).map_err(|_| workspace_changed())?;
            let root = Path::new(&workspace.original_root).join(prefix);
            if !project.active
                || root != Path::new(&project.root_path)
                || projects
                    .insert(id.clone(), project.root_path.clone())
                    .is_some()
            {
                return Err(workspace_changed());
            }
        }
    }
    let ids = run.project_ids.iter().collect::<BTreeSet<_>>();
    if ids.len() != run.project_ids.len()
        || !projects.keys().eq(ids)
        || !run.workspaces.iter().any(|workspace| {
            workspace.resource_sources.iter().any(|material| {
                material.project_id == spec.project_id && material.source_path == spec.source_path
            })
        })
    {
        return Err(workspace_changed());
    }
    let roots = acceptance::roots(&run);
    if acceptance::recovery_block(store, &roots)?.is_some() {
        return Err(recovery_required());
    }
    ensure_roots_available(store, &roots).map_err(|error| {
        preserve_cancellation(error, || {
            issue(
                ErrorCode::Locked,
                "sourceActive",
                "来源工程正在接收成果，请等待结束",
            )
        })
    })?;
    let cwd = session_project_cwd(&spec.project_id, &run).map_err(|_| workspace_changed())?;
    let mut workspaces = run.workspaces;
    strip_file_snapshots(&mut workspaces);
    Ok(Target {
        anchor,
        spec,
        projects: projects.into_iter().collect(),
        workspaces,
        data_dir: store.data_dir().to_path_buf(),
        cwd,
    })
}

fn strip_file_snapshots(workspaces: &mut [Workspace]) {
    for workspace in workspaces {
        workspace.initial.clear();
        workspace.result_snapshot = None;
    }
}

pub(super) fn archive_source_round(store: &mut Store, run: &AgentRun) -> Result<()> {
    if run.status == "completed"
        && !run.preparation_failed
        && store.has_record(NATIVE_FORK_ANCHOR_RECORD, &run.job_id)?
    {
        let mut source = run.clone();
        strip_file_snapshots(&mut source.workspaces);
        store.put_record(SOURCE_ROUND, &source.job_id, &source)?;
    }
    Ok(())
}

impl Target {
    fn verify(&self, cancel: &Cancellation) -> Result<BTreeMap<String, GitBaseline>> {
        cancel.check()?;
        let mut baselines = BTreeMap::new();
        for workspace in &self.workspaces {
            let (_, baseline) = agent_workspace::native_fork_checkout(workspace, cancel)
                .map_err(|error| preserve_cancellation(error, workspace_changed))?;
            if baselines
                .insert(workspace.original_root.clone(), baseline)
                .is_some()
            {
                return Err(workspace_changed());
            }
        }
        Root::open(&self.cwd)
            .and_then(|root| {
                sources::text(&root, &self.spec.source_path)?;
                root.revalidate()
            })
            .map_err(|_| {
                issue(
                    ErrorCode::NotReady,
                    "specUnavailable",
                    "主 Spec 来源当前不可读取",
                )
            })?;
        verify_native_fork_source(&self.data_dir, &self.cwd, &self.anchor, cancel)
            .map_err(|error| preserve_cancellation(error, anchor_changed))?;
        Ok(baselines)
    }

    fn digest(&self, baselines: &BTreeMap<String, GitBaseline>) -> Result<Sha256> {
        digest(&(
            "agent-native-fork.v2",
            &self.spec.id,
            &self.spec.project_id,
            &self.spec.source_path,
            &self.anchor,
            &self.projects,
            &self.workspaces,
            baselines,
        ))
    }

    fn check_project_pins(
        &self,
        request: &AgentStartRequest,
        baselines: &BTreeMap<String, GitBaseline>,
    ) -> Result<()> {
        if let Some(grants) = &request.project_access {
            for grant in grants {
                let baseline = self
                    .workspaces
                    .iter()
                    .find(|workspace| workspace.project_prefixes.contains_key(&grant.project_id))
                    .and_then(|workspace| baselines.get(&workspace.original_root))
                    .ok_or_else(workspace_changed)?;
                if grant.expected_head.as_deref() != Some(baseline.head.as_str())
                    || grant
                        .expected_branch
                        .as_ref()
                        .map(|branch| branch.as_deref())
                        != Some(baseline.branch_name())
                {
                    return Err(workspace_changed());
                }
            }
        }
        Ok(())
    }
}

pub(in crate::workbench) fn preview(
    target: Result<Target>,
    mut protected: Vec<PathBuf>,
    cancel: &Cancellation,
) -> Result<Offer> {
    let result: Result<Offer> = (|| {
        cancel.check()?;
        let target = target?;
        let baselines = target.verify(cancel)?;
        protected.extend(baselines.keys().map(PathBuf::from));
        let runtime =
            agent_protocol::require_runtime(cancel, &protected, Some(&target.anchor.model), None)
                .map_err(runtime_unavailable)?;
        verify_runtime(&runtime, &target.anchor)?;
        Ok(Offer::Available {
            digest: target.digest(&baselines)?,
            model: target.anchor.model,
            project_ids: target.projects.into_iter().map(|(id, _)| id).collect(),
        })
    })();
    match result {
        Ok(offer) => Ok(offer),
        Err(error) if error.code == ErrorCode::Cancelled => Err(error),
        Err(error) => {
            let Some(reason_code) = error.context.get("nativeForkIssue").cloned() else {
                return Ok(Offer::Blocked {
                    reason_code: "sourceUnavailable".into(),
                    message: "暂时无法核对原生分叉来源，可使用摘要续办".into(),
                });
            };
            let message = error.message;
            if matches!(
                error.code,
                ErrorCode::UnsupportedPlatform | ErrorCode::UnsupportedFormat
            ) {
                Ok(Offer::Unsupported {
                    reason_code,
                    message,
                })
            } else {
                Ok(Offer::Blocked {
                    reason_code,
                    message,
                })
            }
        }
    }
}

pub(super) fn capture_expected(
    store: &mut Store,
    request: &AgentStartRequest,
    projects: &[(String, String)],
) -> Result<Option<Target>> {
    let Some(fork) = &request.native_fork_source else {
        return Ok(None);
    };
    let target = load(store, &request.spec_id, &fork.origin())?;
    if target.projects != projects {
        return Err(issue(
            ErrorCode::InvalidInput,
            "projectScopeChanged",
            "原生分叉必须使用来源轮次的全部原工程",
        ));
    }
    Ok(Some(target))
}

pub(super) fn verify_expected(
    target: Option<Target>,
    request: &AgentStartRequest,
    cancel: &Cancellation,
) -> Result<Option<Verified>> {
    let Some(fork) = &request.native_fork_source else {
        return Ok(None);
    };
    let target = target.ok_or_else(source_unavailable)?;
    let baselines = target.verify(cancel)?;
    let digest = target.digest(&baselines)?;
    if digest != fork.expected_fork_digest {
        return Err(preview_changed());
    }
    target.check_project_pins(request, &baselines)?;
    if [&request.selected_model, &request.expected_model]
        .into_iter()
        .filter_map(Option::as_ref)
        .any(|model| model != &target.anchor.model)
    {
        return Err(issue(
            ErrorCode::Conflict,
            "modelChanged",
            "原生分叉必须使用来源轮次的 Pi 模型",
        ));
    }
    let mut source_workspaces = target.workspaces;
    for workspace in &mut source_workspaces {
        let baseline = baselines
            .get(&workspace.original_root)
            .ok_or_else(workspace_changed)?;
        let direct = workspace.direct.as_mut().ok_or_else(workspace_changed)?;
        direct.baseline.clone_from(baseline);
    }
    Ok(Some(Verified {
        anchor: target.anchor,
        source_workspaces,
        baselines,
        digest,
    }))
}

fn load(store: &mut Store, spec_id: &str, source: &ContinuationSource) -> Result<Target> {
    let round = SourceRound::load(store, spec_id, source).map_err(|error| {
        if error.code == ErrorCode::InvalidInput {
            source_unavailable()
        } else {
            error
        }
    })?;
    capture(store, spec_id, source, round)
}

impl Verified {
    pub(super) fn verify_prepared(&self, workspaces: &[Workspace]) -> Result<()> {
        if self.baselines.len() != workspaces.len()
            || workspaces.iter().any(|workspace| {
                !workspace.direct.as_ref().is_some_and(|direct| {
                    direct.read_only
                        && self.baselines.get(&workspace.original_root) == Some(&direct.baseline)
                })
            })
        {
            return Err(workspace_changed());
        }
        Ok(())
    }

    pub(super) fn excluded_paths(&self) -> Result<Vec<PathBuf>> {
        self.source_workspaces
            .iter()
            .flat_map(|workspace| {
                workspace
                    .direct
                    .iter()
                    .flat_map(|direct| &direct.excluded_prefixes)
                    .map(|prefix| {
                        let relative = RelPath::new(prefix).map_err(|_| workspace_changed())?;
                        Ok(Path::new(&workspace.original_root).join(relative.as_str()))
                    })
            })
            .collect()
    }

    pub(super) fn recheck_source(&self, store: &mut Store, spec_id: &str) -> Result<()> {
        let source = ContinuationSource {
            run_id: self.anchor.run_id.clone(),
            job_id: self.anchor.job_id.clone(),
        };
        if load(store, spec_id, &source)?.digest(&self.baselines)? != self.digest {
            return Err(preview_changed());
        }
        Ok(())
    }
}

pub(super) fn runtime_unavailable(error: YssError) -> YssError {
    preserve_cancellation(error, || {
        issue(
            ErrorCode::NotReady,
            "runtimeUnavailable",
            "无法核验当前 Pi 运行环境和来源轮次的模型",
        )
    })
}

pub(super) fn verify_runtime(runtime: &PiRuntime, anchor: &NativeForkAnchor) -> Result<()> {
    if !native_fork_version_supported(&runtime.version) {
        return Err(issue(
            ErrorCode::UnsupportedFormat,
            "runtimeUnsupported",
            "当前 Pi 版本无法使用原生分叉",
        ));
    }
    runtime.verify_native_fork(anchor).map_err(|error| {
        preserve_cancellation(error, || {
            issue(
                ErrorCode::ConcurrentChange,
                "runtimeChanged",
                "Pi 版本、配置目录或模型与来源轮次不一致",
            )
        })
    })
}

pub(super) fn freeze_result(shared: &Arc<Mutex<Store>>, run: &AgentRun) -> Result<()> {
    for workspace in &run.workspaces {
        agent_workspace::validate(workspace)?;
    }
    let mut store = lock_store(shared)?;
    let mut latest = check_freeze_base(&mut store, run)?;
    // Read-only forks have no project writes to attribute to this round.
    for workspace in &mut latest.workspaces {
        workspace.result_snapshot = Some(workspace.initial.clone());
    }
    latest.updated_at = sources::now();
    store.put_record("agentRun", &latest.id, &latest)
}
