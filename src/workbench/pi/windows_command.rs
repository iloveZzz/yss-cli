use super::{
    sandbox::{Sandbox, StoppedCommand, pin_directory_chain},
    windows_mapping::{self, Manifest, VerifiedManifest},
    workspace::{Location, Projects},
};
use crate::{
    error::{ErrorCode, Result, YssError},
    fs::{Root, unique_id},
    model::RelPath,
    workbench::{
        agent::{self, ProjectMode},
        command_recovery::{self, Binding, CommandSpec, ProjectSource},
        jobs::{JobContext, JobRecord, lock_store},
    },
};
use std::{fs::File, process::ExitStatus};

struct Copy {
    root: Root,
    manifest: Manifest,
    _pin: File,
}

fn recovery_error(id: &str, error: YssError) -> YssError {
    YssError::new(
        ErrorCode::RecoveryRequired,
        format!("离线命令现场需要恢复：{}", error.message),
    )
    .context("scratchId", id)
}

fn verified_inputs<'a>(
    projects: &'a Projects,
    copies: &'a [Copy],
    context: &JobContext,
) -> Result<Vec<VerifiedManifest<'a>>> {
    copies
        .iter()
        .map(|copy| {
            windows_mapping::revalidate_initial(
                projects,
                &copy.manifest,
                &copy.root,
                &context.cancel,
            )
        })
        .collect()
}

fn prepare_copies(
    projects: &Projects,
    context: &JobContext,
    binding: &Binding,
    command: &CommandSpec<'_>,
) -> Result<(Vec<Copy>, Vec<File>)> {
    let scratch = {
        let store = lock_store(&context.store)?;
        command_recovery::scratch_root(&store, binding)?
    };
    let pins = pin_directory_chain(&scratch.path)?;
    scratch.revalidate()?;
    let mut copies = Vec::with_capacity(binding.projects.len());
    for project in &binding.projects {
        let root = Root::open(&scratch.path.join(project.scratch_child.as_str()))?;
        let pin = root.pin_directory()?;
        if root.identity != project.scratch_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "命令副本的目录身份已变化",
            ));
        }
        // The outer recovery record already binds all private children before
        // copying. Publish the complete set of verified manifests together.
        let manifest = windows_mapping::prepare(
            projects,
            &project.project_id,
            &context.id,
            &root,
            &context.cancel,
        )?;
        copies.push(Copy {
            root,
            manifest,
            _pin: pin,
        });
    }
    let inputs = verified_inputs(projects, &copies, context)?;
    {
        let mut store = lock_store(&context.store)?;
        command_recovery::mark_ready(&mut store, binding, command, inputs, &context.cancel)?;
    }
    Ok((copies, pins))
}

pub(super) fn run(
    sandbox: &Sandbox,
    projects: &Projects,
    target: &Location<'_>,
    context: &JobContext,
    command: &str,
    output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<(ExitStatus, Vec<u8>, Vec<u8>)> {
    let (run, data_dir) = {
        let mut store = lock_store(&context.store)?;
        let job: JobRecord = store
            .record("job", &context.id)?
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "找不到离线命令所属作业"))?;
        let run_id = job
            .agent_run_id
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "找不到离线命令所属会话"))?;
        (
            agent::result(&mut store, &run_id)?,
            store.data_dir().to_path_buf(),
        )
    };
    let grants = run
        .project_access
        .as_ref()
        .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "离线命令需要本轮明确的工程授权"))?;
    if !grants
        .iter()
        .any(|grant| grant.project_id == target.project_id && grant.mode == ProjectMode::Execute)
    {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "此工程未获本轮离线命令授权",
        ));
    }
    let sources = grants
        .iter()
        .map(|grant| {
            let source = projects.project_root(&grant.project_id)?;
            Ok(ProjectSource {
                project_id: grant.project_id.clone(),
                source_path: source.path.clone(),
                source_identity: source.identity.clone(),
                scratch_child: RelPath::new(unique_id()?)?,
            })
        })
        .collect::<Result<Vec<_>>>()?;
    let selected = sources
        .iter()
        .find(|entry| entry.project_id == target.project_id)
        .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "此工程未获本轮离线命令授权"))?;
    let id = unique_id()?;
    let planned_root = Root::open(&data_dir)?
        .path
        .join("command-scratch")
        .join(&id);
    let planned = planned_root.join(selected.scratch_child.as_str());
    let mapping = sources
        .iter()
        .map(|source| {
            (
                source.project_id.as_str(),
                planned_root.join(source.scratch_child.as_str()),
            )
        })
        .collect::<Vec<_>>();
    let paths = mapping
        .iter()
        .map(|(id, path)| (*id, path.as_path()))
        .collect::<Vec<_>>();
    let launch = sandbox.binding(&planned, &paths, target.relative_path(), command)?;
    let spec = CommandSpec {
        project_id: target.project_id,
        cwd: target.relative_path(),
        command,
        executable_digest: &launch.0,
        environment_digest: &launch.1,
        policy_digest: &launch.2,
    };
    let binding = command_recovery::create_and_begin_copying(
        &context.store,
        &context.id,
        &run.id,
        &id,
        sources,
        &spec,
        &context.cancel,
    )?;

    let invocation = (|| -> Result<(StoppedCommand, Vec<Copy>, Vec<File>)> {
        let (copies, pins) = prepare_copies(projects, context, &binding, &spec)
            .map_err(|error| error.context("cleanupVerified", true))?;
        context.trace("windowsCommandSnapshotReady");
        let selected = copies
            .iter()
            .find(|copy| copy.manifest.binding.project_id == target.project_id)
            .ok_or_else(|| {
                YssError::new(ErrorCode::ConcurrentChange, "命令未找到已授权副本")
                    .context("cleanupVerified", true)
            })?;
        if selected.root.path != planned {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "命令的隔离目录已变化")
                    .context("cleanupVerified", true),
            );
        }
        let roots = copies
            .iter()
            .map(|copy| (copy.manifest.binding.project_id.as_str(), &copy.root))
            .collect::<Vec<_>>();
        let paths = roots
            .iter()
            .map(|(id, root)| (*id, root.path.as_path()))
            .collect::<Vec<_>>();
        let stopped = sandbox.run(
            &selected.root,
            &roots,
            spec.cwd,
            command,
            &context.cancel,
            || {
                if sandbox.binding(&selected.root.path, &paths, spec.cwd, command)? != launch {
                    return Err(YssError::new(
                        ErrorCode::ConcurrentChange,
                        "隔离命令工具链或环境已变化",
                    ));
                }
                verified_inputs(projects, &copies, context)?;
                let mut store = lock_store(&context.store)?;
                command_recovery::begin_launch(&mut store, &binding, &spec, &context.cancel)
            },
            || {
                let mut store = lock_store(&context.store)?;
                command_recovery::mark_stopped(&mut store, &binding, &spec)
            },
            output,
        )?;
        Ok((stopped, copies, pins))
    })();

    match invocation {
        Ok((stopped, copies, pins)) => {
            let result = settle(
                &binding, &spec, &launch, &planned, &stopped, projects, &copies, context,
            );
            drop(copies);
            drop(pins);
            match result {
                Ok(wrote_back) => {
                    let finished = if wrote_back {
                        command_recovery::finish_after_writeback(&context.store, &binding)
                    } else {
                        command_recovery::finish_without_writeback(&context.store, &binding)
                    };
                    finished.map_err(|error| recovery_error(&id, error))?;
                    Ok(stopped.parts())
                }
                Err(error) if error.code == ErrorCode::RecoveryRequired => Err(error),
                Err(error) => {
                    if let Err(cleanup) =
                        command_recovery::finish_without_writeback(&context.store, &binding)
                    {
                        return Err(
                            recovery_error(&id, cleanup).context("commandError", error.message)
                        );
                    }
                    Err(error)
                }
            }
        }
        Err(error) => {
            if error
                .context
                .get("cleanupVerified")
                .is_some_and(|value| value == "true")
            {
                command_recovery::finish_verified_failure(&context.store, &binding, &error)
                    .map_err(|error| recovery_error(&id, error))?;
                Err(error)
            } else {
                Err(recovery_error(&id, error))
            }
        }
    }
}

fn settle(
    binding: &Binding,
    spec: &CommandSpec<'_>,
    launch: &(
        crate::model::Sha256,
        crate::model::Sha256,
        crate::model::Sha256,
    ),
    planned: &std::path::Path,
    stopped: &StoppedCommand,
    projects: &Projects,
    copies: &[Copy],
    context: &JobContext,
) -> Result<bool> {
    let copy = copies
        .iter()
        .find(|copy| copy.manifest.binding.project_id == spec.project_id)
        .ok_or_else(|| YssError::new(ErrorCode::RecoveryRequired, "缺少已绑定的隔离副本"))?;
    if stopped.scratch_path() != planned
        || stopped.scratch_identity() != &copy.root.identity
        || stopped.binding() != launch
    {
        return Err(recovery_error(
            &binding.id,
            YssError::new(ErrorCode::ConcurrentChange, "进程退出证明与命令绑定不一致"),
        ));
    }
    if !stopped.status().success() {
        return Ok(false);
    }
    let changes = windows_mapping::diff(projects, &copy.manifest, &copy.root, &context.cancel)?;
    if changes.is_empty() {
        return Ok(false);
    }
    command_recovery::apply_stopped(
        &context.store,
        binding,
        spec,
        stopped,
        changes,
        &context.cancel,
    )?;
    Ok(true)
}
