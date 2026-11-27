use crate::{
    error::{ErrorCode, Result, YssError},
    model::{MAX_MANIFEST, RelPath, Sha256},
};
use std::{
    collections::BTreeSet,
    io::{self, Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    thread,
};

const MAX_GIT_STDERR: u64 = 16 * 1024;

fn diagnostic(reason: &str, message: &str, hint: &str) -> YssError {
    YssError::new(ErrorCode::UnsafePath, message)
        .context("reason", reason)
        .context("hint", hint)
}

enum GitFailure {
    Missing,
    Launch,
    DeveloperTools,
    Ownership,
    Permission,
    Repository,
    Index,
    Config,
    ConfigInput,
    OutputLimit,
    Other,
}

impl GitFailure {
    fn classify(stderr: &[u8]) -> Self {
        // Git can print paths and configuration values. Return only a known diagnosis.
        let stderr = String::from_utf8_lossy(stderr);
        for line in stderr.lines() {
            if line.starts_with("xcrun: error: invalid active developer path")
                || line.starts_with("xcode-select: note: No developer tools were found")
                || line.starts_with("xcode-select: error: tool 'git' requires Xcode")
            {
                return Self::DeveloperTools;
            }
            let Some(detail) = line
                .strip_prefix("fatal: ")
                .or_else(|| line.strip_prefix("error: "))
            else {
                continue;
            };
            if detail.starts_with("detected dubious ownership in repository at ")
                || (detail.starts_with("unsafe repository (")
                    && detail.ends_with(" is owned by someone else)"))
            {
                return Self::Ownership;
            }
            if [": Permission denied", ": Access is denied."]
                .iter()
                .any(|suffix| detail.ends_with(suffix))
            {
                return Self::Permission;
            }
            if detail == "this operation must be run in a work tree"
                || [
                    "not a git repository",
                    "invalid gitfile format:",
                    "no path in gitfile:",
                    "gitfile does not point to a valid repository:",
                ]
                .iter()
                .any(|prefix| detail.starts_with(prefix))
            {
                return Self::Repository;
            }
            if [
                "index file corrupt",
                "corrupt index file",
                "bad index version",
                "unknown index version",
                "bad index file sha1 signature",
                "unknown index entry format",
                "unable to read index file",
            ]
            .iter()
            .any(|prefix| detail.starts_with(prefix))
                || [
                    ": index file open failed",
                    ": index file smaller than expected",
                ]
                .iter()
                .any(|suffix| detail.ends_with(suffix))
            {
                return Self::Index;
            }
            if detail.starts_with("bad config line ") {
                return if detail.ends_with(" in standard input") {
                    Self::ConfigInput
                } else {
                    Self::Config
                };
            }
        }
        Self::Other
    }

    fn error(self) -> YssError {
        let (reason, message, hint) = match self {
            Self::Missing => (
                "git_not_found",
                "找不到 Git",
                "安装 Git，并确认启动 YSS 的环境可以执行 git --version。",
            ),
            Self::Launch => (
                "git_launch_failed",
                "无法启动 Git",
                "确认当前用户有权执行 Git，并在启动 YSS 的环境运行 git --version。",
            ),
            Self::DeveloperTools => (
                "git_developer_tools_unavailable",
                "macOS 命令行工具不可用，无法运行 Git",
                "检查 Apple 命令行工具的安装状态。未安装时运行 xcode-select --install，确认 git --version 正常后重新打开工作台。",
            ),
            Self::Ownership => (
                "git_dubious_ownership",
                "Git 拒绝访问所有者不匹配的工作树",
                "核实目录所有者和仓库来源。使用拥有此工作树的账户，或确认可信后仅将此仓库加入 Git 的 safe.directory。",
            ),
            Self::Permission => (
                "git_permission_denied",
                "Git 无权访问工作树或仓库文件",
                "检查运行 YSS 的账户是否能读取工作树、.git 及其指向的仓库目录。",
            ),
            Self::Repository => (
                "git_worktree_invalid",
                "无法识别 Git 工作树",
                "检查所选目录及 .git 指向的仓库能否访问。若移动过链接工作树，可在主仓库运行 git worktree repair，或改选有效的本地检出目录。",
            ),
            Self::Index => (
                "git_index_unreadable",
                "Git 无法读取索引",
                "在所选目录运行 git status 检查索引。修复前备份未提交的修改，也可改用有效的检出目录。",
            ),
            Self::Config | Self::ConfigInput => (
                "git_config_invalid",
                "Git 配置格式无效",
                "在所选目录运行 git status，检查并修复 Git 报告的配置文件。",
            ),
            Self::OutputLimit => (
                "git_output_limit",
                "Git 输出超出读取上限",
                "检查仓库是否包含大量跟踪或未跟踪的文件，或选择文件较少的仓库后重试。",
            ),
            Self::Other => (
                "git_failed",
                "Git 操作失败",
                "在启动 YSS 的环境进入所选目录，运行 git status 检查工作树和 Git 配置后重试。",
            ),
        };
        diagnostic(reason, message, hint)
    }
}

fn worktree_path_error() -> YssError {
    diagnostic(
        "git_worktree_path_unverified",
        "无法核验 Git 工作树路径",
        "选择实际的本地工作树目录，并检查其上级目录可访问。",
    )
}

fn submodule_path_error() -> YssError {
    diagnostic(
        "git_submodule_path_invalid",
        "Git 子模块路径无效",
        "检查 Git 索引和 .gitmodules 的子模块路径，使用工作树内可识别的相对路径。",
    )
}

fn index_path_error() -> YssError {
    diagnostic(
        "git_index_path_unverified",
        "Git 索引路径无法核验",
        "检查索引中的文件名是否可用 UTF-8 表示，或改选路径有效的本地检出目录。",
    )
}

fn boundary_error() -> YssError {
    diagnostic(
        "git_boundary_crossed",
        "路径穿越 Git 或子模块边界",
        "选择当前工作树中的普通目录或文件。若要操作子模块，请单独选择它自己的工作树。",
    )
}

fn modules_error() -> YssError {
    diagnostic(
        "gitmodules_invalid",
        ".gitmodules 无法核验",
        "确认 .gitmodules 是工作树内可读的普通文件，检查其格式和子模块路径后重试。",
    )
}

pub fn command(root: &Path) -> Command {
    let mut command = Command::new("git");
    for (key, _) in std::env::vars_os() {
        if key.to_string_lossy().starts_with("GIT_") {
            command.env_remove(key);
        }
    }
    command
        .args([
            "-c",
            "core.fsmonitor=false",
            "-c",
            "core.hooksPath=/dev/null",
            "-C",
        ])
        .arg(root);
    command.env("GIT_OPTIONAL_LOCKS", "0");
    command
}
pub fn output(
    root: &Path,
    args: &[&str],
    input: Option<&[u8]>,
    allow_one: bool,
) -> Result<Vec<u8>> {
    run(root, args, input, allow_one).map_err(GitFailure::error)
}

fn run(
    root: &Path,
    args: &[&str],
    input: Option<&[u8]>,
    allow_one: bool,
) -> std::result::Result<Vec<u8>, GitFailure> {
    thread::scope(|scope| {
        let (mut stderr, child_stderr) = io::pipe().map_err(|_| GitFailure::Other)?;
        let stderr = thread::Builder::new()
            .spawn_scoped(scope, move || -> io::Result<Vec<u8>> {
                let mut diagnostic = Vec::new();
                (&mut stderr)
                    .take(MAX_GIT_STDERR)
                    .read_to_end(&mut diagnostic)?;
                // Keep draining so warnings cannot block Git after the capture limit.
                io::copy(&mut stderr, &mut io::sink())?;
                Ok(diagnostic)
            })
            .map_err(|_| GitFailure::Other)?;
        let (child_stdin, writer) = if let Some(input) = input {
            let (child_stdin, mut stdin) = io::pipe().map_err(|_| GitFailure::Other)?;
            let writer = thread::Builder::new()
                .spawn_scoped(scope, move || stdin.write_all(input))
                .map_err(|_| GitFailure::Other)?;
            (Stdio::from(child_stdin), Some(writer))
        } else {
            (Stdio::null(), None)
        };

        let mut child = command(root)
            .args(args)
            .env("LC_ALL", "C")
            .env("LANGUAGE", "C")
            .stdin(child_stdin)
            .stdout(Stdio::piped())
            .stderr(child_stderr)
            .spawn()
            .map_err(|error| match error.kind() {
                io::ErrorKind::NotFound => GitFailure::Missing,
                _ => GitFailure::Launch,
            })?;
        let mut bytes = Vec::new();
        let read = child
            .stdout
            .take()
            .ok_or(GitFailure::Other)
            .and_then(|stdout| {
                stdout
                    .take(MAX_MANIFEST + 1)
                    .read_to_end(&mut bytes)
                    .map_err(|_| GitFailure::Other)
            });
        if read.is_err() || bytes.len() as u64 > MAX_MANIFEST {
            let _ = child.kill();
            let _ = child.wait();
            read?;
            return Err(GitFailure::OutputLimit);
        }
        let status = child.wait().map_err(|_| GitFailure::Other)?;
        let stderr = stderr
            .join()
            .map_err(|_| GitFailure::Other)?
            .map_err(|_| GitFailure::Other)?;
        if !status.success() && !(allow_one && status.code() == Some(1)) {
            return Err(GitFailure::classify(&stderr));
        }
        if let Some(writer) = writer {
            writer
                .join()
                .map_err(|_| GitFailure::Other)?
                .map_err(|_| GitFailure::Other)?;
        }
        Ok(bytes)
    })
}

#[derive(Clone)]
pub struct Boundary {
    pub root: PathBuf,
    pub blocked: BTreeSet<String>,
    pub fingerprint: Sha256,
}
impl Boundary {
    pub fn probe(root: &Path) -> Result<Self> {
        let mut selected = None;
        for parent in root.ancestors() {
            if std::fs::symlink_metadata(parent.join(".git")).is_ok()
                || std::fs::symlink_metadata(parent.join(".gitmodules")).is_ok()
            {
                selected = Some(parent);
                break;
            }
        }
        let mut blocked = BTreeSet::new();
        let mut fingerprint = Vec::new();
        if let Some(repository) = selected {
            let top = output(repository, &["rev-parse", "--show-toplevel"], None, false)?;
            let top = PathBuf::from(
                std::str::from_utf8(&top)
                    .map_err(|_| worktree_path_error())?
                    .trim_end_matches(['\r', '\n']),
            );
            #[cfg(unix)]
            let prefix = {
                let prefix = root.strip_prefix(&top).map_err(|_| worktree_path_error())?;
                super::alias_key(
                    &prefix
                        .to_str()
                        .ok_or_else(worktree_path_error)?
                        .replace('\\', "/"),
                )
            };
            #[cfg(windows)]
            let prefix = {
                let current = std::fs::canonicalize(root).map_err(|_| worktree_path_error())?;
                let top_canonical =
                    std::fs::canonicalize(&top).map_err(|_| worktree_path_error())?;
                let top_dir = super::open_absolute(&top_canonical)?;
                let top_identity =
                    super::platform::identity_dir(&top_dir).map_err(|_| worktree_path_error())?;
                let mut suffix = None;
                for ancestor in current.ancestors() {
                    let directory = super::open_absolute(ancestor)?;
                    if super::platform::identity_dir(&directory)
                        .map_err(|_| worktree_path_error())?
                        == top_identity
                    {
                        suffix = Some(
                            current
                                .strip_prefix(ancestor)
                                .map_err(|_| worktree_path_error())?
                                .to_owned(),
                        );
                        break;
                    }
                }
                suffix
                    .ok_or_else(worktree_path_error)?
                    .to_str()
                    .ok_or_else(worktree_path_error)?
                    .replace('\\', "/")
            };
            let index = output(repository, &["ls-files", "--stage", "-z"], None, false)?;
            let mut paths = BTreeSet::new();
            for entry in index.split(|b| *b == 0).filter(|s| !s.is_empty()) {
                let entry = std::str::from_utf8(entry).map_err(|_| index_path_error())?;
                let (metadata, path) = entry.split_once('\t').ok_or_else(index_path_error)?;
                if metadata.split_whitespace().next() == Some("160000") {
                    paths.insert(RelPath::new(path).map_err(|_| submodule_path_error())?);
                }
            }
            let modules = top.join(".gitmodules");
            if let Ok(metadata) = std::fs::symlink_metadata(&modules) {
                if !metadata.is_file()
                    || metadata.file_type().is_symlink()
                    || metadata.len() > MAX_MANIFEST
                {
                    return Err(modules_error());
                }
                let bytes = super::open_absolute(&top)
                    .and_then(|dir| super::read_in_dir(&dir, ".gitmodules", MAX_MANIFEST))
                    .map_err(|error| {
                        error.context("reason", "gitmodules_unreadable").context(
                            "hint",
                            "检查 .gitmodules 是否是工作树内可读的普通文件，确认文件未被同时修改后重试。",
                        )
                    })?
                    .bytes;
                let values = run(
                    &top,
                    &[
                        "config",
                        "--null",
                        "--file",
                        "-",
                        "--get-regexp",
                        "^submodule\\..*\\.path$",
                    ],
                    Some(&bytes),
                    true,
                )
                .map_err(|failure| match failure {
                    GitFailure::ConfigInput => modules_error(),
                    failure => failure.error(),
                })?;
                for entry in values.split(|b| *b == 0).filter(|s| !s.is_empty()) {
                    let entry = std::str::from_utf8(entry).map_err(|_| modules_error())?;
                    let (_, path) = entry.split_once('\n').ok_or_else(modules_error)?;
                    paths.insert(RelPath::new(path).map_err(|_| submodule_path_error())?);
                }
            }
            for path in paths {
                fingerprint.extend_from_slice(path.as_str().as_bytes());
                fingerprint.push(0);
                #[cfg(unix)]
                let path = super::alias_key(path.as_str());
                #[cfg(windows)]
                let path = path.as_str().to_owned();
                #[cfg(unix)]
                if prefix.is_empty() {
                    blocked.insert(path);
                } else if path == prefix || prefix.starts_with(&format!("{path}/")) {
                    return Err(boundary_error());
                } else if let Some(path) = path.strip_prefix(&format!("{prefix}/")) {
                    blocked.insert(path.to_owned());
                }
                #[cfg(windows)]
                if prefix.is_empty() {
                    blocked.insert(path);
                } else if super::windows_path_under(&prefix, &path)
                    .ok_or_else(submodule_path_error)?
                {
                    return Err(boundary_error());
                } else if super::windows_path_under(&path, &prefix)
                    .ok_or_else(submodule_path_error)?
                {
                    let suffix = path
                        .split('/')
                        .skip(prefix.split('/').count())
                        .collect::<Vec<_>>()
                        .join("/");
                    if suffix.is_empty() {
                        return Err(submodule_path_error());
                    }
                    blocked.insert(suffix);
                }
            }
            fingerprint.extend_from_slice(top.as_os_str().as_encoded_bytes());
        }
        Ok(Self {
            root: root.to_owned(),
            blocked,
            fingerprint: Sha256::of(&fingerprint),
        })
    }
    pub fn check(&self, path: &RelPath) -> Result<()> {
        #[cfg(unix)]
        let key = super::alias_key(path.as_str());
        #[cfg(unix)]
        let blocked = self
            .blocked
            .iter()
            .any(|p| &key == p || key.starts_with(&format!("{p}/")));
        #[cfg(windows)]
        let blocked = self
            .blocked
            .iter()
            .any(|p| super::windows_path_under(path.as_str(), p).unwrap_or(true));
        if path
            .as_str()
            .split('/')
            .any(|p| p.eq_ignore_ascii_case(".git"))
            || blocked
        {
            return Err(boundary_error().at(path));
        }
        Ok(())
    }
    pub fn revalidate(&self) -> Result<()> {
        if Self::probe(&self.root)?.fingerprint != self.fingerprint {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "Git 边界在操作期间改变")
                    .context("reason", "git_boundary_changed")
                    .context("hint", "等待当前 Git 或子模块操作结束后重试。"),
            );
        }
        Ok(())
    }
}
