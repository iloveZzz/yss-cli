use super::*;
use crate::workbench::{jobs::DeferredAgent, read_model};

pub(super) struct Target {
    pub(super) spec: SpecRecord,
    pub(super) projects: Vec<(String, String)>,
    pub(super) prior: Option<AgentRun>,
    pub(super) protected: Vec<PathBuf>,
}

pub(in crate::workbench) fn start_async(
    manager: Arc<JobManager>,
    request: AgentStartRequest,
    fingerprint: String,
) -> Result<JobRecord> {
    start(manager, request, fingerprint, false)
}

pub(super) fn start_queued(
    manager: Arc<JobManager>,
    request: AgentStartRequest,
    fingerprint: String,
) -> Result<JobRecord> {
    start(manager, request, fingerprint, true)
}

fn start(
    manager: Arc<JobManager>,
    request: AgentStartRequest,
    fingerprint: String,
    queued: bool,
) -> Result<JobRecord> {
    validate_start_request(&request, queued)?;
    if request.carry_files.values().any(|paths| !paths.is_empty()) {
        return Err(invalid("Pi 直接读取原工程，请清空带入文件后重试"));
    }
    let spec_id = request.spec_id.clone();
    let run_id = request.resume_run_id.clone();
    let dispatch_id = queued.then(|| request.request_id.clone());
    let expected_goal_version = request.expected_goal_version.clone();
    let worker = manager.clone();
    manager.start_preparing(
        "Pi 会话续办".into(),
        request.request_id.clone(),
        DeferredAgent {
            spec_id: spec_id.clone(),
            run_id: request.resume_run_id.clone(),
            fingerprint,
        },
        move |store, job| {
            queue::check_start(store, run_id.as_deref(), dispatch_id.as_deref())?;
            if queued {
                progress_agent::revalidate_goal(
                    store,
                    &spec_id,
                    expected_goal_version.as_deref(),
                    true,
                )?;
            }
            let spec = sources::spec(store.state(), &spec_id)?;
            if !spec.active {
                return Err(invalid("主 Spec 已归档"));
            }
            job.title = format!("Spec 续办 · {}", spec.title);
            Ok(())
        },
        move |context| prepare(&worker, context, request, queued),
    )
}

pub(super) fn capture_target(store: &mut Store, request: &mut AgentStartRequest) -> Result<Target> {
    let spec = sources::spec(store.state(), &request.spec_id)?.clone();
    check_spec_binding(&spec, &spec)?;
    let prior = request
        .resume_run_id
        .as_ref()
        .map(|id| {
            let prior = load(store, id)?;
            if prior.spec_id != request.spec_id {
                return Err(invalid("续接必须使用原 Spec 与原工程范围"));
            }
            if !terminal(&prior.status) {
                return Err(YssError::new(ErrorCode::Locked, "此运行仍在执行"));
            }
            if request
                .expected_job_id
                .as_ref()
                .is_some_and(|id| id != &prior.job_id)
            {
                return Err(stale_round(&prior));
            }
            if prior.host != AgentHost::Pi || prior.workspace_mode != WorkspaceMode::Direct {
                return Err(invalid("历史会话仅支持审阅，请从原 Spec 新建 Pi 会话"));
            }
            Ok(prior)
        })
        .transpose()?;
    if let Some(entries) = request.project_access.as_mut() {
        entries.sort_by(|left, right| left.project_id.cmp(&right.project_id));
        let ids: Vec<_> = entries
            .iter()
            .map(|entry| entry.project_id.clone())
            .collect();
        if ids.windows(2).any(|pair| pair[0] == pair[1]) || !ids.contains(&spec.project_id) {
            return Err(invalid(
                "工程授权须明确包含主 Spec 工程，且每个工程只能指定一次",
            ));
        }
        if entries.iter().any(|entry| {
            entry.expected_head.as_ref().is_some_and(|head| {
                !matches!(head.len(), 40 | 64)
                    || !head
                        .bytes()
                        .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
            })
        }) {
            return Err(invalid("工程预期 HEAD 须为完整的 Git 提交 SHA"));
        }
        if !request.project_ids.is_empty() {
            request.project_ids.sort();
            request.project_ids.dedup();
            if request.project_ids != ids {
                return Err(invalid("工程授权与旧格式工程列表不一致"));
            }
        }
        request.project_ids = ids;
    } else {
        if request.project_ids.is_empty()
            && let Some(prior) = &prior
            && prior.project_access.is_some()
        {
            request.project_ids.clone_from(&prior.project_ids);
        }
        request.project_ids.push(spec.project_id.clone());
        request.project_ids.sort();
        request.project_ids.dedup();
        request.project_access = prior
            .as_ref()
            .and_then(|prior| prior.project_access.as_ref())
            .map(|entries| {
                entries
                    .iter()
                    .map(|entry| entry.narrowed(request.read_only, request.allow_commands))
                    .collect()
            });
    }
    if prior
        .as_ref()
        .is_some_and(|prior| prior.project_ids != request.project_ids)
    {
        return Err(invalid("续接必须使用原 Spec 与原工程范围"));
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    if request.project_access.is_none() && request.allow_commands && !request.read_only {
        request.project_access = Some(
            request
                .project_ids
                .iter()
                .map(|id| ProjectAccess {
                    project_id: id.clone(),
                    mode: ProjectMode::Execute,
                    expected_head: None,
                    expected_root_path: None,
                    expected_branch: None,
                })
                .collect(),
        );
    }
    if let Some(entries) = &request.project_access {
        request.read_only = entries.iter().all(|entry| entry.mode == ProjectMode::Read);
        request.allow_commands = entries
            .iter()
            .any(|entry| entry.mode == ProjectMode::Execute);
    }
    let projects = request
        .project_ids
        .iter()
        .map(|id| {
            let project = sources::project(store.state(), id)?;
            if !project.active {
                return Err(invalid("运行工程已归档"));
            }
            let expected_root = request
                .project_access
                .as_ref()
                .and_then(|entries| entries.iter().find(|entry| &entry.project_id == id))
                .and_then(|grant| grant.expected_root_path.as_deref());
            if let Some(expected_root) = expected_root
                && expected_root != project.root_path
            {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "工程登记路径已变化，请刷新本轮工程范围",
                )
                .context("httpStatus", 409)
                .context("projectId", id)
                .context("expectedRootPath", expected_root)
                .context("currentRootPath", &project.root_path));
            }
            Ok((id.clone(), project.root_path.clone()))
        })
        .collect::<Result<Vec<_>>>()?;
    Ok(Target {
        spec,
        projects,
        prior,
        protected: pi_protected_paths(store),
    })
}

fn prepare(
    manager: &JobManager,
    context: JobContext,
    mut request: AgentStartRequest,
    queued: bool,
) -> Result<Value> {
    context.cancel.check()?;
    let (mut target, state, data_dir) = {
        let mut store = lock_store(&context.store)?;
        context.trace("firstLockAcquired");
        let target = capture_target(&mut store, &mut request)?;
        (
            target,
            store.state().clone(),
            store.data_dir().to_path_buf(),
        )
    };
    sources::read_source(&state, &target.spec.id).map_err(|error| {
        YssError::new(
            ErrorCode::NotReady,
            format!("主 Spec 来源不可用：{}", error.message),
        )
        .context("httpStatus", 409)
    })?;
    let roots: Vec<String> = target
        .projects
        .iter()
        .map(|(_, path)| {
            agent_workspace::agent_git_root(Path::new(path), &context.cancel)
                .map(|root| root.to_string_lossy().into_owned())
        })
        .collect::<Result<BTreeSet<_>>>()?
        .into_iter()
        .collect();
    context.trace("gitReady");
    target.protected.extend(roots.iter().map(PathBuf::from));
    let model = match (&request.selected_model, &target.prior) {
        (None, Some(run)) => Some(recorded_model(run)?),
        (selected, _) => selected.clone(),
    };
    let pi = agent_protocol::require_runtime(&context.cancel, &target.protected, model.as_ref())?;
    pi.verify_expected(request.expected_model.as_ref())?;
    pi.verify_thinking(request.thinking_level)?;
    pi.verify_commands(request.allow_commands)?;
    context.trace("runtimeReady");

    let citations = if let Some(prior) = &target.prior {
        let cwd = session_project_cwd(&target.spec.project_id, prior)?;
        if !prior.pi_session.as_ref().is_some_and(|session| {
            agent_protocol::session_available(&data_dir, &prior.id, &cwd, session)
        }) {
            return Err(
                YssError::new(ErrorCode::NotReady, "原 Pi 会话不可用，请从 Spec 新建会话")
                    .context("httpStatus", 409),
            );
        }
        for workspace in &prior.workspaces {
            agent_workspace::validate_resume(workspace, &context.cancel)?;
        }
        verify_citation_contents(prior, &request.citations, &context.cancel, &|descriptor| {
            frozen_bytes(&*lock_store(&context.store)?, descriptor)
        })?
    } else {
        Vec::new()
    };
    let inputs = read_model::capture(&mut *lock_store(&context.store)?)?;
    let stamp = inputs.stamp;
    let snapshot = read_model::build_snapshot(inputs, Some(&target.spec.id), &context.cancel)?;
    if queued
        && snapshot
            .progress
            .get(&target.spec.id)
            .and_then(|summary| summary.goal_version.as_deref())
            != request.expected_goal_version.as_deref()
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "队列目标版本已变化，请核对后重试",
        )
        .context("progressBinding", true));
    }
    let binding = progress_agent::select_snapshot_binding(
        &snapshot,
        &target.spec.id,
        request.lifecycle_stage,
        request.expected_goal_version.as_deref(),
    )?;
    context.trace("materialsReady");
    manager.reserve_agent_roots(&context, roots, |store| {
        context.trace("reservationLockAcquired");
        check_spec_binding(sources::spec(store.state(), &target.spec.id)?, &target.spec)?;
        if store.projection_stamp() != stamp
            || target.projects.iter().any(|(id, root)| {
                !sources::project(store.state(), id).is_ok_and(|p| p.active && p.root_path == *root)
            })
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "准备期间的目标或登记已变化，请核对后重试",
            )
            .context("progressBinding", true));
        }
        if let Some(prior) = &target.prior {
            let current = load(store, &prior.id)?;
            if current.job_id != prior.job_id
                || current.binding_revision != prior.binding_revision
                || !terminal(&current.status)
            {
                return Err(stale_round(&current));
            }
            acceptance::ensure_recovered(store, &acceptance::roots(&current))?;
        }
        Ok(())
    })?;
    context.trace("rootsReserved");
    context.cancel.check()?;
    execute(
        context,
        request,
        target.projects,
        target.prior,
        &pi,
        (citations, binding, queued),
        target.spec,
    )
}
