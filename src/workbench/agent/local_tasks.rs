use super::{AgentHost, AgentRun, Workspace, WorkspaceMode, invalid, load, stale_round};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root, RootIdentity},
    model::{MAX_MANIFEST, RelPath},
    workbench::{
        agent_workspace,
        jobs::{
            JobContext, JobFailure, JobManager, JobRecord, LocalProjectAction,
            LocalProjectCheckout, LocalProjectTask, lock_store,
        },
        pnpm::{corepack_hash_matches, integrity_hash},
        process, sources,
        store::Store,
    },
};
use semver::{BuildMetadata, Version};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    env,
    ffi::OsString,
    path::{Path, PathBuf},
    process::{Command, ExitStatus},
    sync::Arc,
    time::{Duration, Instant, SystemTime},
};

const PROBE_TIMEOUT: Duration = Duration::from_secs(10);
const TASK_TIMEOUT: Duration = Duration::from_secs(600);
const LOG_LIMIT: usize = 256 * 1024;
const DIST_ENTRY_LIMIT: usize = 4096;
const COMMAND_ID: &str = "local-project-command";

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(in crate::workbench) struct Request {
    project_id: String,
    directory: String,
    action: LocalProjectAction,
    expected_job_id: String,
    expected_checkout: Option<LocalProjectCheckout>,
    request_id: String,
}

struct Selection {
    revision: u64,
    project: PathBuf,
    cwd: PathBuf,
    workspace: Workspace,
}

impl Selection {
    fn matches(&self, current: &Self) -> bool {
        self.revision == current.revision
            && self.project == current.project
            && self.cwd == current.cwd
            && self.workspace.original_root == current.workspace.original_root
            && self.workspace.original_identity == current.workspace.original_identity
            && self.workspace.direct == current.workspace.direct
    }
}

#[derive(PartialEq, Eq)]
struct Package {
    identity: RootIdentity,
    head: String,
    branch: Option<String>,
    manifest: FileSnapshot,
    version: Version,
}

pub(in crate::workbench) fn list(store: &mut Store, run_id: &str) -> Result<Value> {
    let _ = store
        .record::<AgentRun>("agentRun", run_id)?
        .filter(|run| run.id == run_id && run.host == AgentHost::Pi)
        .ok_or_else(|| sources::missing("Pi 会话不存在"))?;
    let mut items: Vec<_> = store
        .records::<JobRecord>("job")?
        .into_iter()
        .filter(|job| {
            job.local_project_task
                .as_ref()
                .is_some_and(|task| task.run_id == run_id)
        })
        .collect();
    items.sort_by(|a, b| b.created_at.cmp(&a.created_at).then(b.id.cmp(&a.id)));
    Ok(json!({ "items": items }))
}

pub(in crate::workbench) fn start(
    jobs: Arc<JobManager>,
    run_id: &str,
    request: Request,
) -> Result<JobRecord> {
    let task = LocalProjectTask {
        run_id: run_id.into(),
        source_job_id: request.expected_job_id,
        project_id: request.project_id,
        directory: if request.directory == "." {
            String::new()
        } else {
            request.directory
        },
        action: request.action,
        expected_checkout: request.expected_checkout,
    };
    let selected = {
        let mut store = lock_store(&jobs.store)?;
        if let Some(existing) =
            JobManager::existing_local_project_task(&mut store, &request.request_id, &task)?
        {
            return Ok(existing);
        }
        selection(&mut store, &task)?
    };
    let (package, _) = read_package(&selected, &task, &Cancellation::new())?;
    let git_root = selected.workspace.original_root.clone();
    let task_for_admission = task.clone();
    let selected = Arc::new(selected);
    let expected = selected.clone();
    jobs.start_local_project_task(
        task.clone(),
        request.request_id,
        git_root,
        move |store| {
            let current = selection(store, &task_for_admission)?;
            if !expected.matches(&current) {
                return Err(target_changed());
            }
            super::acceptance::ensure_recovered(store, &[current.workspace.original_root])?;
            Ok(())
        },
        move |context| run(context, task, &selected, &package),
    )
}

fn target_changed() -> YssError {
    YssError::new(
        ErrorCode::ConcurrentChange,
        "本机任务的工程版本、工作目录或 package.json 已变化，请重新核对",
    )
}

fn selection(store: &mut Store, task: &LocalProjectTask) -> Result<Selection> {
    let run = load(store, &task.run_id)?;
    if run.job_id != task.source_job_id {
        return Err(stale_round(&run));
    }
    if run.host != AgentHost::Pi || run.workspace_mode != WorkspaceMode::Direct {
        return Err(invalid("本机任务仅支持使用原工程的 Pi 会话"));
    }
    let source: JobRecord = store
        .record("job", &run.job_id)?
        .filter(|job: &JobRecord| {
            job.kind == "agent" && job.agent_run_id.as_ref().is_none_or(|id| id == &run.id)
        })
        .ok_or_else(|| invalid("Pi 轮次缺少执行记录"))?;
    if !source.terminal() {
        return Err(
            YssError::new(ErrorCode::Locked, "请等待此 Pi 轮次结束").context("jobId", &run.job_id)
        );
    }
    let project = sources::project(store.state(), &task.project_id)?;
    if !project.active || !run.project_ids.contains(&task.project_id) {
        return Err(invalid("请选择本轮范围内的有效登记工程"));
    }
    let project_path = PathBuf::from(&project.root_path);
    if task
        .expected_checkout
        .as_ref()
        .is_some_and(|expected| expected.root_path != project.root_path)
    {
        return Err(target_changed());
    }
    let directory = if task.directory.is_empty() {
        None
    } else {
        let path = RelPath::new(&task.directory)?;
        if fs::alias_key(path.as_str())
            .split('/')
            .any(|part| matches!(part, ".git" | ".gitmodules" | ".yss"))
        {
            return Err(YssError::new(
                ErrorCode::UnsafePath,
                "本机任务目录经过受保护路径",
            ));
        }
        Some(path)
    };
    let cwd = directory.as_ref().map_or_else(
        || project_path.clone(),
        |path| project_path.join(path.as_str()),
    );
    let workspace = run
        .workspaces
        .into_iter()
        .find(|workspace| workspace.project_ids.contains(&task.project_id))
        .ok_or_else(|| invalid("本轮没有此工程的原目录绑定"))?;
    let root = Path::new(&workspace.original_root);
    let direct = workspace
        .direct
        .as_ref()
        .ok_or_else(|| invalid("本轮没有原工程身份记录"))?;
    if cwd.starts_with(store.data_dir())
        || direct
            .excluded_prefixes
            .iter()
            .any(|prefix| cwd.starts_with(root.join(prefix)))
        || store.state().projects.iter().any(|other| {
            let other = Path::new(&other.root_path);
            other != project_path && other.starts_with(&project_path) && cwd.starts_with(other)
        })
    {
        return Err(YssError::new(
            ErrorCode::UnsafePath,
            "本机任务目录超出所选工程边界",
        ));
    }
    Ok(Selection {
        revision: run.binding_revision,
        project: project_path,
        cwd,
        workspace,
    })
}

fn read_package(
    selection: &Selection,
    task: &LocalProjectTask,
    cancel: &Cancellation,
) -> Result<(Package, Root)> {
    cancel.check()?;
    let (git, baseline) =
        agent_workspace::resume_checkout(&selection.workspace, cancel).map_err(directory_error)?;
    if task.expected_checkout.as_ref().is_some_and(|expected| {
        expected.head != baseline.head || expected.branch.as_deref() != baseline.branch_name()
    }) {
        return Err(target_changed());
    }
    let rel = selection
        .cwd
        .strip_prefix(&git.path)
        .map_err(|_| target_changed())?;
    let cwd = if rel.as_os_str().is_empty() {
        git
    } else {
        let rel = rel
            .to_str()
            .ok_or_else(|| invalid("本机任务目录不是 UTF-8"))?;
        let dir = git
            .open_dir(&RelPath::new(rel.replace(std::path::MAIN_SEPARATOR, "/"))?)
            .map_err(directory_error)?;
        let cwd = Root::open(&selection.cwd).map_err(directory_error)?;
        if fs::platform::identity_dir(&dir)? != cwd.identity {
            return Err(target_changed());
        }
        cwd
    };
    let manifest = RelPath::new("package.json")?;
    if cwd.regular_file_metadata(&manifest)?.is_none() {
        return Err(invalid("所选目录需要可读取的普通 package.json 文件").at(manifest));
    }
    let file = cwd
        .read_checked(&manifest, MAX_MANIFEST)
        .map_err(directory_error)?;
    let value: Value = serde_json::from_slice(&file.bytes)
        .map_err(|_| invalid("package.json 格式无效").at(&manifest))?;
    let version = value["packageManager"]
        .as_str()
        .and_then(|pin| pin.strip_prefix("pnpm@"))
        .and_then(|version| Version::parse(version).ok())
        .ok_or_else(|| {
            invalid("package.json 必须通过 packageManager 声明确切 pnpm 版本").at(&manifest)
        })?;
    if value["scripts"][task.action.as_str()]
        .as_str()
        .is_none_or(|script| script.trim().is_empty())
    {
        return Err(
            invalid(format!("package.json 缺少 {} 脚本", task.action.as_str())).at(&manifest),
        );
    }
    Ok((
        Package {
            identity: cwd.identity.clone(),
            branch: baseline.branch_name().map(str::to_owned),
            head: baseline.head,
            manifest: file.snapshot,
            version,
        },
        cwd,
    ))
}

fn directory_error(error: YssError) -> YssError {
    if error.code == ErrorCode::Io
        && error
            .context
            .get("kind")
            .is_some_and(|kind| matches!(kind.as_str(), "NotFound" | "NotADirectory"))
    {
        return invalid("所选工作目录或 package.json 不存在，或已被移除");
    }
    error
}

struct HostEnvironment {
    path: OsString,
}

impl HostEnvironment {
    fn load() -> Result<Self> {
        let path = env::var_os("PATH").ok_or_else(|| invalid("无法读取本机 PATH"))?;
        let path = env::join_paths(env::split_paths(&path).filter(|entry| entry.is_absolute()))
            .map_err(|_| invalid("无法解析本机 PATH"))?;
        if path.is_empty() {
            return Err(invalid("本机 PATH 没有绝对目录"));
        }
        Ok(Self { path })
    }

    fn program(&self, name: &str) -> Option<PathBuf> {
        process::system_tool_path(name, &self.path)
    }

    fn command(&self, program: &Path, cwd: &Path) -> Command {
        // Keep the installed toolchain and user's package-manager cache available while
        // excluding API keys, tokens, debugging options and runtime preload settings.
        const HOST_VARIABLES: &[&str] = &[
            "HOME",
            "USERPROFILE",
            "APPDATA",
            "LOCALAPPDATA",
            "SYSTEMROOT",
            "COMSPEC",
            "PATHEXT",
            "TMPDIR",
            "TMP",
            "TEMP",
            "XDG_CACHE_HOME",
            "XDG_CONFIG_HOME",
            "XDG_DATA_HOME",
            "COREPACK_HOME",
            "PNPM_HOME",
            "LANG",
            "LC_ALL",
            "LC_CTYPE",
        ];
        let mut command = Command::new(program);
        command
            .env_clear()
            .envs(
                HOST_VARIABLES
                    .iter()
                    .filter_map(|key| env::var_os(key).map(|value| (*key, value))),
            )
            .env("PATH", &self.path)
            .env("NO_COLOR", "1")
            .env("COREPACK_ENABLE_NETWORK", "0")
            .env("COREPACK_DEFAULT_TO_LATEST", "0")
            .env("COREPACK_ENABLE_AUTO_PIN", "0")
            .env("COREPACK_ENV_FILE", "0")
            .env("npm_config_manage_package_manager_versions", "false")
            .env("pnpm_config_pm_on_fail", "ignore")
            .current_dir(cwd);
        command
    }
}

struct PackageManager {
    program: PathBuf,
    locator: Option<String>,
}

impl PackageManager {
    fn command(&self, host: &HostEnvironment, cwd: &Path) -> Command {
        let mut command = host.command(&self.program, cwd);
        if let Some(locator) = &self.locator {
            command.arg(locator);
        }
        command
    }
}

fn probe(mut command: Command, cancel: &Cancellation) -> Result<Option<String>> {
    command
        .env("npm_config_offline", "true")
        .env("pnpm_config_offline", "true")
        .arg("--version");
    match process::capture_command(command, cancel, PROBE_TIMEOUT) {
        Ok((status, output, _)) if status.success() => Ok(std::str::from_utf8(&output)
            .ok()
            .map(|text| text.trim().to_owned())),
        Err(error) if error.code == ErrorCode::Cancelled => Err(error),
        _ => Ok(None),
    }
}

fn integrity_unavailable(pin: &Version) -> YssError {
    YssError::new(
        ErrorCode::NotReady,
        format!("本机没有可核对完整性摘要的 pnpm@{pin}，请检查 Corepack 缓存"),
    )
}

fn toolchain(
    host: &HostEnvironment,
    cwd: &Path,
    pin: &Version,
    cancel: &Cancellation,
) -> Result<(PackageManager, Version, Version)> {
    let node = host
        .program("node")
        .ok_or_else(|| YssError::new(ErrorCode::NotReady, "本机 PATH 缺少可用的 Node.js"))?;
    let node = probe(host.command(&node, cwd), cancel)?
        .as_deref()
        .and_then(|version| version.strip_prefix('v'))
        .and_then(|version| Version::parse(version).ok())
        .ok_or_else(|| YssError::new(ErrorCode::NotReady, "本机 PATH 缺少可用的 Node.js"))?;
    let mut expected = pin.clone();
    // Corepack's optional integrity suffix is carried in its locator. pnpm --version
    // prints the package version without that suffix.
    let integrity = integrity_hash(pin);
    if integrity.is_some() {
        expected.build = BuildMetadata::EMPTY;
    }
    let corepack = host
        .program("corepack")
        .filter(|_| integrity.is_none_or(|hash| corepack_hash_matches(&expected, hash)));
    let pnpm = integrity.is_none().then(|| host.program("pnpm")).flatten();
    for manager in [
        corepack.map(|program| PackageManager {
            program,
            locator: Some(format!("pnpm@{pin}")),
        }),
        pnpm.map(|program| PackageManager {
            program,
            locator: None,
        }),
    ]
    .into_iter()
    .flatten()
    {
        if let Some(version) = probe(manager.command(host, cwd), cancel)?
            .and_then(|version| Version::parse(&version).ok())
            && version == expected
        {
            return Ok((manager, node, version));
        }
    }
    if integrity.is_some() {
        return Err(integrity_unavailable(pin));
    }
    Err(YssError::new(
        ErrorCode::NotReady,
        format!("本机没有可用的 pnpm@{pin}，请核对 Corepack 缓存或本机 pnpm 版本"),
    ))
}

#[derive(Default)]
struct Output {
    text: String,
    partial: [Vec<u8>; 2],
    published: usize,
    last_publish: Option<Instant>,
    truncated: bool,
}

impl Output {
    fn write(&mut self, context: &JobContext, chunk: Option<(bool, &[u8])>) -> Result<()> {
        if !self.truncated
            && let Some((stdout, bytes)) = chunk
        {
            let partial = &mut self.partial[usize::from(!stdout)];
            partial.extend_from_slice(bytes);
            let complete = (partial.len().saturating_sub(3)..partial.len())
                .find(|&start| {
                    std::str::from_utf8(&partial[start..])
                        .is_err_and(|error| error.valid_up_to() == 0 && error.error_len().is_none())
                })
                .unwrap_or(partial.len());
            let text = String::from_utf8_lossy(&partial[..complete]);
            let written = text.floor_char_boundary(LOG_LIMIT - self.text.len());
            self.text.push_str(&text[..written]);
            self.truncated = written < text.len();
            partial.drain(..complete);
        }
        if self.text.len() > self.published
            && self
                .last_publish
                .is_none_or(|at| at.elapsed() >= Duration::from_millis(100))
        {
            context.emit(
                "item/commandExecution/outputDelta",
                json!({
                    "itemId": COMMAND_ID, "delta": &self.text[self.published..]
                }),
            )?;
            self.published = self.text.len();
            self.last_publish = Some(Instant::now());
        }
        Ok(())
    }

    fn finish(mut self, error: Option<&YssError>) -> String {
        for partial in self.partial {
            if !self.truncated && !partial.is_empty() {
                let text = String::from_utf8_lossy(&partial);
                let written = text.floor_char_boundary(LOG_LIMIT - self.text.len());
                self.text.push_str(&text[..written]);
                self.truncated = written < text.len();
            }
        }
        if self.truncated {
            self.text.push_str("\n[日志已截断，保留前 256 KiB]\n");
        }
        if let Some(error) = error {
            if !self.text.is_empty() && !self.text.ends_with('\n') {
                self.text.push('\n');
            }
            self.text.push_str(&error.message);
        }
        self.text
    }
}

fn termination_signal(_status: ExitStatus) -> Option<Value> {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        let number = _status.signal()?;
        let name = match number {
            libc::SIGKILL => "SIGKILL",
            libc::SIGABRT => "SIGABRT",
            libc::SIGTERM => "SIGTERM",
            libc::SIGINT => "SIGINT",
            _ => return Some(json!({ "number": number })),
        };
        Some(json!({ "number": number, "name": name }))
    }
    #[cfg(not(unix))]
    None
}

#[derive(PartialEq, Eq)]
struct DistSnapshot {
    identity: RootIdentity,
    modified: SystemTime,
    entries: Option<Vec<(RelPath, u64, SystemTime)>>,
}

fn dist_snapshot(cwd: &Root, cancel: &Cancellation) -> Result<Option<DistSnapshot>> {
    let path = RelPath::new("dist")?;
    let Some(metadata) = cwd.entry_metadata(&path)? else {
        return Ok(None);
    };
    if !metadata.is_dir() || fs::platform::dangerous(&metadata) {
        return Ok(None);
    }
    let dir = cwd.open_dir(&path)?;
    let identity = fs::platform::identity_dir(&dir)?;
    let modified = dir.dir_metadata()?.modified()?.into_std();
    let mut pending = vec![path];
    let mut entries = Vec::new();
    let mut budget = DIST_ENTRY_LIMIT;
    let mut complete = true;
    while let Some(path) = pending.pop() {
        let (children, directory_complete) =
            cwd.source_entries(Some(&path), &mut budget, cancel)?;
        for (name, metadata) in children {
            let child = path.join(&name)?;
            if metadata.is_dir() && !fs::platform::dangerous(&metadata) {
                pending.push(child.clone());
            }
            entries.push((child, metadata.len(), metadata.modified()?.into_std()));
        }
        if !directory_complete {
            complete = false;
            break;
        }
    }
    Ok(Some(DistSnapshot {
        identity,
        modified,
        entries: complete.then_some(entries),
    }))
}

fn artifact(
    task: &LocalProjectTask,
    cwd: &Root,
    before: Option<&DistSnapshot>,
    cancel: &Cancellation,
) -> Option<String> {
    cwd.revalidate().ok()?;
    let after = dist_snapshot(cwd, cancel).ok()??;
    let changed = before.is_none_or(|before| {
        before.identity != after.identity
            || before.modified != after.modified
            || before
                .entries
                .as_ref()
                .zip(after.entries.as_ref())
                .is_some_and(|(before, after)| before != after)
    });
    changed.then(|| {
        if task.directory.is_empty() {
            "dist".to_owned()
        } else {
            format!("{}/dist", task.directory)
        }
    })
}

fn verified_target(
    context: &JobContext,
    task: &LocalProjectTask,
    selected: &Selection,
    package: &Package,
) -> Result<Root> {
    let current = selection(&mut *lock_store(&context.store)?, task)?;
    if !selected.matches(&current) {
        return Err(target_changed());
    }
    let (manifest, cwd) = read_package(&current, task, &context.cancel)?;
    if &manifest != package {
        return Err(target_changed());
    }
    Ok(cwd)
}

fn run(
    context: JobContext,
    task: LocalProjectTask,
    selected: &Selection,
    package: &Package,
) -> std::result::Result<Value, JobFailure> {
    let mut item = json!({
        "id": COMMAND_ID, "type": "commandExecution",
        "command": format!("pnpm run {}", task.action.as_str()), "cwd": selected.cwd,
        "projectId": task.project_id
    });
    context.emit("item/started", json!({ "item": item }))?;
    let mut output = Output::default();
    let mut result = json!({ "exitCode": null, "artifactPaths": [] });
    let execution = (|| -> Result<()> {
        let host = HostEnvironment::load()?;
        let cwd = verified_target(&context, &task, selected, package)?;
        let (manager, node, pnpm) = toolchain(&host, &cwd.path, &package.version, &context.cancel)?;
        let dist_before = (task.action == LocalProjectAction::Build)
            .then(|| dist_snapshot(&cwd, &context.cancel).ok())
            .flatten();
        let cwd = verified_target(&context, &task, selected, package)?;
        if let Some(hash) = integrity_hash(&package.version)
            && !corepack_hash_matches(&pnpm, hash)
        {
            return Err(integrity_unavailable(&package.version));
        }
        result["nodeVersion"] = json!(node);
        result["pnpmVersion"] = json!(pnpm);
        let mut command = manager.command(&host, &cwd.path);
        command.args(["run", task.action.as_str()]);
        let status =
            process::stream_command_output(command, &context.cancel, TASK_TIMEOUT, |chunk| {
                output.write(&context, chunk)
            })?;
        result["exitCode"] = json!(status.code());
        item["exitCode"] = result["exitCode"].clone();
        if let Some(signal) = termination_signal(status) {
            result["terminationSignal"] = signal.clone();
            item["terminationSignal"] = signal;
        }
        if !status.success() {
            return Err(YssError::new(ErrorCode::Io, "本机项目脚本执行失败"));
        }
        if let Some(before) = dist_before
            && let Some(artifact) = artifact(&task, &cwd, before.as_ref(), &context.cancel)
        {
            result["artifactPaths"] = json!([artifact]);
        }
        Ok(())
    })();
    let error = execution.err();
    item["status"] = json!(match error.as_ref().map(|error| error.code) {
        Some(ErrorCode::Cancelled) => "canceled",
        Some(_) => "failed",
        None => "completed",
    });
    item["aggregatedOutput"] = json!(output.finish(error.as_ref()));
    let published = context.emit("item/completed", json!({ "item": item }));
    if let Some(error) = error.or_else(|| published.err()) {
        return Err(JobFailure {
            error,
            result: Some(result),
        });
    }
    Ok(result)
}
