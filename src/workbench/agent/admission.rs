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
    let spec_id = request.spec_id.clone();
    let run_id = request.resume_run_id.clone();
    let dispatch_id = queued.then(|| request.request_id.clone());
    let validation = request.clone();
    let worker = manager.clone();
    let host = request.selected_host(None);
    let selection = request.expected_model.as_ref().map(|model| {
        json!({
            "provider":model.provider, "model":model.id, "thinkingLevel":request.thinking_level,
        })
    });
    manager.start_preparing(
        "Agent 会话续办".into(),
        request.request_id.clone(),
        DeferredAgent {
            spec_id: spec_id.clone(),
            run_id: request.resume_run_id.clone(),
            fingerprint,
            host,
            selection,
        },
        move |store, job| {
            validate_start_request(&validation, queued)?;
            let prior = run_id.as_deref().map(|id| load(store, id)).transpose()?;
            let host = validation.selected_host(prior.as_ref());
            if let Some(prior) = &prior {
                if prior.host != host || !host.current() {
                    return Err(invalid("续接须使用原会话的 Agent CLI"));
                }
                ensure_handoff_source_idle(store, &prior.id)?;
                check_dispatch_outcome(store, prior)?;
            }
            if let Some(handoff) = &validation.handoff {
                validation.validate_handoff(store, &spec_id, host)?;
                job.agent_handoff_source = Some(handoff.source.clone());
            }
            job.agent_host = Some(host);
            if validation
                .carry_files
                .values()
                .any(|paths| !paths.is_empty())
            {
                return Err(invalid("Agent 直接核对原工程，请清空带入文件后重试"));
            }
            queue::check_start(store, run_id.as_deref(), dispatch_id.as_deref())?;
            super::super::agent_scope::check_expected(
                store,
                &spec_id,
                validation.continuation_source.as_ref(),
                validation.expected_continuation_digest.as_ref(),
            )?;
            let spec = sources::spec(store.state(), &spec_id)?;
            if !spec.active {
                return Err(invalid("主 Spec 已归档"));
            }
            job.title = format!("Spec 续办 · {}", spec.title);
            if queued || validation.goal_expectation.is_some() {
                validation.verify_goal(store, None, queued)?;
            }
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
            if !prior.host.current()
                || request.selected_host(Some(&prior)) != prior.host
                || prior.workspace_mode != WorkspaceMode::Direct
            {
                return Err(invalid(
                    "请沿原会话的 Agent CLI 续接，历史会话可从原 Spec 新建",
                ));
            }
            check_dispatch_outcome(store, &prior)?;
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
                .context("projectScopeChanged", true)
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
    let (mut target, state, data_dir, fork_target) = {
        let mut store = lock_store(&context.store)?;
        context.trace("firstLockAcquired");
        if queued || request.goal_expectation.is_some() {
            request.verify_goal(&mut store, None, queued)?;
        }
        let target = capture_target(&mut store, &mut request)?;
        let fork = native_fork::capture_expected(&mut store, &request, &target.projects)?;
        (
            target,
            store.state().clone(),
            store.data_dir().to_path_buf(),
            fork,
        )
    };
    let host = request.selected_host(target.prior.as_ref());
    validate_host_scope(&request, host, &target.spec, queued)?;
    let fork = native_fork::verify_expected(fork_target, &request, &context.cancel)?;
    if let Some(prior) = &target.prior {
        check_resume_session(&state, &data_dir, prior)?;
        for workspace in &prior.workspaces {
            agent_workspace::validate_resume(workspace, &context.cancel)?;
        }
    }
    if fork.is_none() {
        sources::read_source(&state, &target.spec.id).map_err(|error| {
            YssError::new(
                ErrorCode::NotReady,
                format!("主 Spec 来源不可用：{}", error.message),
            )
            .context("httpStatus", 409)
        })?;
    }
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
    let model = match (&request.selected_model, &target.prior, &fork) {
        (_, _, Some(fork)) => Some(fork.anchor.model.clone()),
        (None, Some(run), _) => Some(recorded_model(run)?),
        (selected, _, _) => selected.clone(),
    };
    let runtime = match host {
        AgentHost::Pi => {
            context.set_pi_phase(PiPhase::CheckRuntime)?;
            let pi = agent_protocol::prepare_runtime(
                &context.cancel,
                &target.protected,
                model.as_ref(),
                request.allow_commands.then(|| request.command_execution()),
            )
            .map_err(|error| {
                if fork.is_some() {
                    native_fork::runtime_unavailable(error)
                } else {
                    error
                }
            })?;
            if let Some(fork) = &fork {
                native_fork::verify_runtime(&pi, &fork.anchor)?;
            }
            pi.verify_expected(request.expected_model.as_ref())?;
            pi.verify_commands(request.allow_commands, request.command_execution())?;
            if request.handoff.is_some() || request.expected_runtime_identity.is_some() {
                request.verify_runtime_identity(pi.identity().as_deref(), host)?;
            }
            AgentRuntime::Pi(pi)
        }
        AgentHost::Grok => {
            let (expected_model, identity) = request
                .expected_model
                .as_ref()
                .zip(request.expected_runtime_identity.as_deref())
                .ok_or_else(|| invalid("请核对本轮 Grok 模型和运行配置后发送"))?;
            AgentRuntime::Grok(grok::prepare_run(
                &context.cancel,
                &target.protected,
                model.as_ref(),
                expected_model,
                identity,
            )?)
        }
        AgentHost::Codex => return Err(invalid("历史 Codex 会话仅供审阅")),
    };
    if host == AgentHost::Pi {
        context.trace("runtimeReady");
        context.set_pi_phase(PiPhase::PrepareWorkspace)?;
    } else {
        context.trace("grokInstallationReady");
    }
    let citations = if let Some(prior) = &target.prior {
        verify_citation_contents(prior, &request.citations, &context.cancel, &|descriptor| {
            frozen_bytes(&*lock_store(&context.store)?, descriptor)
        })?
    } else {
        Vec::new()
    };
    let (stamp, binding) = if host == AgentHost::Grok {
        let mut store = lock_store(&context.store)?;
        store.capture_read_records(&[])?;
        (store.projection_stamp(), None)
    } else {
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
        let binding = if fork.is_some() {
            None
        } else {
            progress_agent::select_snapshot_binding(
                &snapshot,
                &target.spec.id,
                request.lifecycle_stage,
                request.expected_goal_version.as_deref(),
                request.goal_expectation.as_ref(),
            )?
        };
        (stamp, binding)
    };
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
        if let Some(fork) = &fork {
            fork.recheck_source(store, &target.spec.id)?;
        }
        request.validate_handoff(store, &target.spec.id, host)?;
        Ok(())
    })?;
    context.trace("rootsReserved");
    context.cancel.check()?;
    execute(
        context,
        request,
        target.projects,
        target.prior,
        runtime,
        PreparedRun {
            citations,
            binding,
            queued,
            fork,
        },
        target.spec,
    )
}
