mod bridge;
mod rpc;
mod sandbox;
mod workspace;

use super::{PiSession, RunRequest, RuntimeStatus};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root},
    model::RelPath,
    workbench::{jobs::JobContext, process},
};
use serde_json::{Value, json};
use std::{
    env, fs,
    io::{BufRead, BufReader, Read},
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    process::Command,
    time::Duration,
};

const SUPPORTED_VERSION: semver::Version = semver::Version::new(1, 5, 2);
const SESSION_DIR: &str = "pi-sessions";

fn not_ready(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
}

fn locate_pi() -> Result<PathBuf> {
    let pi = if let Some(path) = env::var_os("YSS_PI_EXECUTABLE") {
        let path = PathBuf::from(path);
        if !path.is_absolute() {
            return Err(not_ready("YSS_PI_EXECUTABLE 须使用 Pi 的绝对路径"));
        }
        Some(path)
    } else {
        env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute())
            .map(|path| path.join("pi"))
            .find(|path| {
                fs::metadata(path)
                    .is_ok_and(|metadata| metadata.is_file() && metadata.mode() & 0o111 != 0)
            })
    };
    pi.and_then(|path| fs::canonicalize(path).ok())
        .filter(|path| {
            fs::metadata(path)
                .is_ok_and(|metadata| metadata.is_file() && metadata.mode() & 0o111 != 0)
        })
        .ok_or_else(|| not_ready("未找到本机 Pi，请安装 Pi 1.5.2 或设置 YSS_PI_EXECUTABLE"))
}

fn pi_command(executable: &Path, protected: &[&Path]) -> Result<Command> {
    if protected.iter().any(|root| executable.starts_with(root)) {
        return Err(not_ready("Pi 可执行文件须在本次工程和工作台数据目录之外"));
    }
    let path = env::join_paths(
        env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute())
            .filter_map(|path| fs::canonicalize(path).ok())
            .filter(|path| path.is_dir() && !protected.iter().any(|root| path.starts_with(root))),
    )
    .map_err(|_| not_ready("Pi 运行环境的 PATH 无效"))?;
    let mut command = Command::new(executable);
    command.env("PATH", path);
    for variable in [
        "NODE_OPTIONS",
        "NODE_PATH",
        "BASH_ENV",
        "ENV",
        "DYLD_INSERT_LIBRARIES",
    ] {
        command.env_remove(variable);
    }
    Ok(command)
}

pub(super) fn check_runtime(cancel: &Cancellation, status: &mut RuntimeStatus) -> Result<PathBuf> {
    let pi = locate_pi()?;
    let mut probe = pi_command(&pi, &[])?;
    // Probe outside of any checkout so project-local resources cannot influence discovery.
    probe.current_dir("/private/tmp").arg("--version");
    let (exit, stdout, _) = process::capture_command(probe, cancel, Duration::from_secs(5))?;
    let version = std::str::from_utf8(&stdout)
        .ok()
        .and_then(|text| semver::Version::parse(text.trim()).ok())
        .filter(|_| exit.success())
        .ok_or_else(|| not_ready("Pi 版本探测失败，请检查可执行文件和运行环境"))?;
    let compatible = version == SUPPORTED_VERSION;
    status.version = Some(version);
    if !compatible {
        return Err(not_ready("工作台当前支持 Pi 1.5.2，请核对本机 Pi 版本"));
    }
    sandbox::probe(cancel)?;
    status.sandbox_ready = true;
    Ok(pi)
}

fn session_directory(data_dir: &Path, run_id: &str, create: bool) -> Result<PathBuf> {
    let id = RelPath::new(run_id)?;
    if id.as_str().contains('/') {
        return Err(not_ready("Pi 运行编号无效"));
    }
    Root::open(data_dir)?;
    let mut directory = data_dir.to_owned();
    for part in [SESSION_DIR, id.as_str()] {
        directory.push(part);
        if create
            && fs::symlink_metadata(&directory).is_err()
            && let Err(error) = sandbox::private_dir(&directory)
            && fs::symlink_metadata(&directory).is_err()
        {
            return Err(error);
        }
        let metadata = fs::symlink_metadata(&directory)
            .map_err(|_| not_ready("Pi 会话目录缺失，请从 Spec 重新发起运行"))?;
        if !metadata.is_dir()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
        {
            return Err(not_ready("Pi 会话目录必须是当前用户专有的普通目录"));
        }
    }
    Ok(directory)
}

fn session_name(file: &str) -> Result<RelPath> {
    let file = RelPath::new(file)?;
    if file.as_str().contains('/') || !file.as_str().ends_with(".jsonl") {
        return Err(not_ready("Pi 会话文件路径无效"));
    }
    Ok(file)
}

pub(super) fn verify_session(
    data_dir: &Path,
    run_id: &str,
    cwd: &Path,
    session: &PiSession,
) -> Result<PathBuf> {
    let dir = session_directory(data_dir, run_id, false)?;
    let relative = session_name(&session.file)?;
    let root = Root::open(&dir)?;
    let file = root
        .open_streamed(&relative)
        .map_err(|_| not_ready("Pi 会话文件缺失，请从 Spec 重新发起运行"))?;
    let metadata = file.metadata()?;
    if metadata.nlink() != 1 || metadata.uid() != unsafe { libc::geteuid() } {
        return Err(not_ready("Pi 会话文件身份无效"));
    }
    let mut header = Vec::new();
    BufReader::new(file)
        .take(16 * 1024 + 1)
        .read_until(b'\n', &mut header)?;
    if header.len() > 16 * 1024 || header.last() != Some(&b'\n') {
        return Err(not_ready("Pi 会话文件头不完整"));
    }
    let header: Value =
        serde_json::from_slice(&header).map_err(|_| not_ready("Pi 会话文件头无效"))?;
    if header["type"] != "session"
        || header["id"].as_str() != Some(&session.id)
        || header["cwd"].as_str().map(Path::new) != Some(cwd)
    {
        return Err(not_ready("Pi 会话与原运行工作区不匹配"));
    }
    Ok(dir.join(relative.as_str()))
}

fn session_ref(dir: &Path, state: &Value) -> Result<Option<PiSession>> {
    let Some(file) = state["sessionFile"].as_str() else {
        return Ok(None);
    };
    let id = state["sessionId"]
        .as_str()
        .filter(|id| !id.is_empty() && id.len() <= 128)
        .ok_or_else(|| not_ready("Pi 会话 ID 无效"))?;
    let relative = Path::new(file)
        .strip_prefix(dir)
        .ok()
        .and_then(Path::to_str)
        .ok_or_else(|| not_ready("Pi 会话文件未写入工作台私有目录"))?;
    let file = session_name(relative)?.as_str().to_owned();
    Ok(Some(PiSession {
        id: id.to_owned(),
        file,
    }))
}

fn project_context(request: &RunRequest<'_>) -> Result<(String, Vec<PathBuf>)> {
    let mut rules = String::new();
    let mut skills = Vec::new();
    for workspace in request.workspaces {
        let root = Root::open(Path::new(&workspace.worktree_root))?;
        for (project, prefix) in &workspace.project_prefixes {
            let in_project = |name: &str| {
                RelPath::new(if prefix.is_empty() {
                    name.to_owned()
                } else {
                    format!("{prefix}/{name}")
                })
            };
            let rel = in_project("AGENTS.md")?;
            if matches!(root.inspect(&rel)?, FileSnapshot::File { .. }) {
                let file = root.read_checked(&rel, 64 * 1024)?;
                let text = std::str::from_utf8(&file.bytes)
                    .map_err(|_| not_ready("工程规则须为 UTF-8"))?;
                rules.push_str(&format!("\n工程 {project} 的 AGENTS.md\n{text}\n"));
            }
            let rel = in_project(".agents/skills")?;
            if let FileSnapshot::Directory { .. } = root.inspect(&rel)? {
                let skill_dir = root.path.join(rel.as_str());
                if project == request.primary_project {
                    let mut limit = 257;
                    let (entries, complete) =
                        root.source_entries(Some(&rel), &mut limit, &Cancellation::new())?;
                    if !complete {
                        return Err(not_ready("主工程技能数量超过 256 个"));
                    }
                    for (name, metadata) in entries {
                        if metadata.is_dir() {
                            let path = RelPath::new(format!("{}/{name}/SKILL.md", rel.as_str()))?;
                            if matches!(root.inspect(&path)?, FileSnapshot::File { .. }) {
                                skills.push(root.path.join(path.as_str()));
                            }
                        }
                    }
                } else {
                    rules.push_str(&format!(
                        "工程 {project} 的技能目录为 {}，按需读取其 SKILL.md 与引用材料。\n",
                        skill_dir.display()
                    ));
                }
            }
        }
    }
    if rules.len() > 256 * 1024 {
        return Err(not_ready("所选工程规则超过 256 KiB，请缩小工程范围"));
    }
    Ok((rules, skills))
}

pub(super) fn run(
    request: RunRequest<'_>,
    context: &JobContext,
    mut save_session: impl FnMut(Option<PiSession>, Value) -> Result<()>,
    ask: impl Fn(&JobContext, &str) -> Result<String> + Send + 'static,
) -> Result<()> {
    context.cancel.check()?;
    let directory = session_directory(request.data_dir, request.run_id, request.resume.is_none())?;
    let resume = request
        .resume
        .map(|session| verify_session(request.data_dir, request.run_id, request.cwd, session))
        .transpose()?;
    let worktrees = workspace::Worktrees::new(request.workspaces, request.primary_project)?;
    let (rules, skills) = project_context(&request)?;
    let private = sandbox::PrivateDir::new()?;
    let extension = private.path().join("workbench.ts");
    fs::write(&extension, include_bytes!("tools.mjs"))?;
    let sandbox = request
        .allow_commands
        .then(|| {
            sandbox::Sandbox::new(
                &private,
                request.data_dir,
                request.workspaces,
                &context.cancel,
            )
        })
        .transpose()?;
    let bridge = bridge::Bridge::start(private.path(), context, worktrees, sandbox, ask)?;
    let protected = std::iter::once(request.data_dir)
        .chain(
            request
                .workspaces
                .iter()
                .map(|workspace| Path::new(&workspace.original_root)),
        )
        .collect::<Vec<_>>();
    let mut command = pi_command(request.executable, &protected)?;
    command
        .current_dir(request.cwd)
        .args([
            "--mode",
            "rpc",
            "--offline",
            "--no-builtin-tools",
            "--no-extensions",
            "--no-mcp",
            "--no-approve",
            "--no-prompt-templates",
            "--no-context-files",
            "--no-themes",
            "--no-skills",
        ])
        .arg("--extension")
        .arg(&extension)
        .arg("--session-dir")
        .arg(&directory)
        .env("PI_TELEMETRY", "0");
    if let Some(file) = &resume {
        command.arg("--session").arg(file);
    }
    for path in &skills {
        command.arg("--skill").arg(path);
    }
    bridge.configure(&mut command, request.allow_commands);
    let mut pi = rpc::Session::open(command, &context.cancel)?;
    let state = pi.state(&context.cancel)?;
    bridge.await_ready()?;
    let session = session_ref(&directory, &state)?;
    if let Some(original) = request.resume
        && session
            .as_ref()
            .is_none_or(|opened| opened.id != original.id || opened.file != original.file)
    {
        return Err(not_ready("Pi 打开的会话与原运行不匹配"));
    }
    let metadata = json!({
        "host": "pi", "version": SUPPORTED_VERSION,
        "model": state.pointer("/model/id").and_then(Value::as_str),
        "provider": state.pointer("/model/provider").and_then(Value::as_str),
        "thinkingLevel": state["thinkingLevel"].as_str(),
        "allowCommands": request.allow_commands
    });
    save_session(session, metadata.clone())?;
    context.emit("piStatus", json!({ "message": "已连接 Pi 会话" }))?;
    let commands = if request.allow_commands {
        "命令沙箱断网。Maven 与 pnpm 构建使用本机已准备的离线依赖。"
    } else {
        "本次未授权命令工具，使用文件工具。"
    };
    let prompt = format!("{}\n{commands}\n{rules}", request.prompt);
    let result = pi.prompt(&prompt, context);
    // Some Pi configurations only report the session file after the first message is persisted.
    if let Ok(state) = pi.state(&context.cancel)
        && let Some(session) = session_ref(&directory, &state)?
    {
        save_session(Some(session), metadata)?;
    }
    result
}
