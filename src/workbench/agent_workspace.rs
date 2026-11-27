use super::{
    agent::{ProjectAccess, ProjectMode},
    model::{ResourceKind, ResourceRef, SpecRecord, TargetType},
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root, RootIdentity},
    model::{FileDescriptor, MAX_ASSET, RelPath, RenderedFile},
    transaction::{self, Operation},
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    time::Duration,
};

pub type Snapshot = BTreeMap<String, FileDescriptor>;
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SnapshotSource {
    pub project_id: String,
    pub project_prefix: String,
    pub source_path: String,
    pub personal_resources: Vec<ResourceRef>,
    pub inferred_resources: Vec<ResourceRef>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Workspace {
    pub project_ids: Vec<String>,
    pub project_prefixes: BTreeMap<String, String>,
    pub original_root: String,
    pub original_identity: RootIdentity,
    pub worktree_root: String,
    pub worktree_identity: RootIdentity,
    pub branch: String,
    pub head: String,
    pub initial: Snapshot,
    #[serde(default)]
    pub resource_sources: Vec<SnapshotSource>,
    #[serde(default)]
    pub result_snapshot: Option<Snapshot>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direct: Option<DirectWorkspace>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DirectWorkspace {
    pub baseline: GitBaseline,
    pub read_only: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub project_modes: Option<BTreeMap<String, ProjectMode>>,
    pub project_identities: BTreeMap<String, RootIdentity>,
    pub excluded_prefixes: BTreeSet<String>,
}
fn invalid(message: impl Into<String>) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
pub fn git(root: &Path, arguments: &[&str]) -> Result<Vec<u8>> {
    git_cancel(
        root,
        arguments,
        &Cancellation::new(),
        Duration::from_secs(30),
    )
}
fn git_cancel(
    root: &Path,
    arguments: &[&str],
    cancel: &Cancellation,
    timeout: Duration,
) -> Result<Vec<u8>> {
    let mut command = crate::fs::git::command(root);
    command.args(arguments);
    super::process::run_command(command, cancel, timeout).map_err(|mut error| {
        if error.code != ErrorCode::Cancelled {
            error.message = format!("Git 操作失败：{}", error.message);
        }
        error.context("operation", arguments.first().unwrap_or(&""))
    })
}
fn git_text(root: &Path, arguments: &[&str]) -> Result<String> {
    git_text_cancel(root, arguments, &Cancellation::new())
}
fn git_text_cancel(root: &Path, arguments: &[&str], cancel: &Cancellation) -> Result<String> {
    String::from_utf8(git_cancel(
        root,
        arguments,
        cancel,
        Duration::from_secs(30),
    )?)
    .map(|s| s.trim_end_matches(['\n', '\r']).into())
    .map_err(|_| invalid("Git 路径不是 UTF-8"))
}
pub fn git_root(project: &Path) -> Result<PathBuf> {
    git_root_cancel(project, &Cancellation::new())
}
pub(super) fn agent_git_root(project: &Path, cancel: &Cancellation) -> Result<PathBuf> {
    let root = git_root_cancel(project, cancel).map_err(|error| {
        if error.code == ErrorCode::Cancelled {
            return error;
        }
        invalid(format!(
            "无法定位工程 {} 的 Git 工作树，请确认 Git 已安装并核对工程登记",
            project.display()
        ))
        .context("cause", error.message)
    })?;
    git_text_cancel(&root, &["rev-parse", "--verify", "HEAD^{commit}"], cancel).map_err(
        |error| {
            if error.code == ErrorCode::Cancelled {
                return error;
            }
            invalid(format!(
                "工程 {} 没有可用的 Git HEAD 提交，无法记录本轮 Git 基线",
                project.display()
            ))
            .context("cause", error.message)
        },
    )?;
    Ok(root)
}

fn git_root_cancel(project: &Path, cancel: &Cancellation) -> Result<PathBuf> {
    let actual = std::fs::canonicalize(project)?;
    let root = PathBuf::from(git_text_cancel(
        &actual,
        &["rev-parse", "--show-toplevel"],
        cancel,
    )?);
    let root = std::fs::canonicalize(root)?;
    if !actual.starts_with(&root) {
        return Err(invalid("工程不在 Git 根内"));
    }
    Root::open(&root)?;
    Ok(root)
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GitBaseline {
    pub head: String,
    pub branch: Option<String>,
    pub index_digest: String,
    pub git_dir: String,
    pub git_dir_identity: RootIdentity,
    pub common_dir: String,
    pub common_dir_identity: RootIdentity,
}
impl GitBaseline {
    pub(super) fn branch_name(&self) -> Option<&str> {
        self.branch
            .as_deref()
            .map(|name| name.strip_prefix("refs/heads/").unwrap_or(name))
    }
}

pub fn git_baseline(root: &Path, cancel: &Cancellation) -> Result<GitBaseline> {
    let head = git_text_cancel(root, &["rev-parse", "--verify", "HEAD"], cancel)?;
    let symbolic = git_text_cancel(root, &["rev-parse", "--symbolic-full-name", "HEAD"], cancel)?;
    let branch = if symbolic == "HEAD" {
        None
    } else {
        Some(symbolic)
    };
    let index = git_cancel(
        root,
        &["ls-files", "--stage", "-z"],
        cancel,
        Duration::from_secs(30),
    )?;
    let git_dir = std::fs::canonicalize(git_text_cancel(
        root,
        &["rev-parse", "--absolute-git-dir"],
        cancel,
    )?)?;
    let common_dir = std::fs::canonicalize(git_text_cancel(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
        cancel,
    )?)?;
    let git_dir_identity = Root::open(&git_dir)?.identity;
    let common_dir_identity = Root::open(&common_dir)?.identity;
    Ok(GitBaseline {
        head,
        branch,
        index_digest: crate::model::Sha256::of(&index).as_str().into(),
        git_dir: git_dir.to_string_lossy().into(),
        git_dir_identity,
        common_dir: common_dir.to_string_lossy().into(),
        common_dir_identity,
    })
}
pub fn check_baseline(root: &Path, expected: &GitBaseline, cancel: &Cancellation) -> Result<()> {
    if &git_baseline(root, cancel)? != expected {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "原工程 Git HEAD、分支、暂存区或目录身份已变化，请重新审阅差异",
        ));
    }
    Ok(())
}
pub fn allowed(path: &RelPath) -> bool {
    !path
        .as_str()
        .split('/')
        .any(|p| matches!(p, ".git" | ".yss"))
}
pub fn snapshot(path: &Path, cancel: &Cancellation) -> Result<Snapshot> {
    let root = Root::open(path)?;
    let names = git_cancel(
        path,
        &[
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
        ],
        cancel,
        Duration::from_secs(30),
    )?;
    let mut result = BTreeMap::new();
    let mut total = 0u64;
    for name in names.split(|b| *b == 0).filter(|b| !b.is_empty()) {
        cancel.check()?;
        if result.len() >= 200_000 {
            return Err(invalid("工程文件数量超过快照限制"));
        }
        let name = std::str::from_utf8(name).map_err(|_| invalid("工程包含非 UTF-8 路径"))?;
        let rel = RelPath::new(name)?;
        if !allowed(&rel) {
            continue;
        }
        match root.inspect(&rel)? {
            FileSnapshot::Missing => {}
            FileSnapshot::File { descriptor, .. } => {
                total = total
                    .checked_add(descriptor.size)
                    .ok_or_else(|| invalid("工程快照大小溢出"))?;
                if total > MAX_ASSET {
                    return Err(invalid("工程快照超过 256 MiB 限制"));
                }
                result.insert(name.into(), descriptor);
            }
            _ => {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "工程包含符号链接、子模块或其他受保护对象，无法生成完整文件快照",
                )
                .at(name));
            }
        }
    }
    root.revalidate()?;
    Ok(result)
}
fn resource_source(spec: &SpecRecord, prefix: &str) -> SnapshotSource {
    SnapshotSource {
        project_id: spec.project_id.clone(),
        project_prefix: prefix.into(),
        source_path: spec.source_path.clone(),
        personal_resources: spec
            .resources
            .iter()
            .filter(|resource| {
                !spec
                    .source_resource_targets
                    .contains(&super::content::resource_key(resource))
            })
            .cloned()
            .collect(),
        inferred_resources: spec.inferred_resources.clone(),
    }
}
pub fn bind_spec(workspace: &mut Workspace, spec: &SpecRecord) {
    if let Some(prefix) = workspace.project_prefixes.get(&spec.project_id) {
        let source = resource_source(spec, prefix);
        workspace
            .resource_sources
            .retain(|source| source.project_id != spec.project_id);
        workspace.resource_sources.push(source);
    }
}
fn validate_snapshot(snapshot: &Snapshot) -> Result<()> {
    if snapshot.len() > 200_000
        || snapshot
            .values()
            .map(|descriptor| descriptor.size)
            .sum::<u64>()
            > MAX_ASSET
    {
        return Err(invalid("工程快照超过 256 MiB 或 20 万文件限制"));
    }
    Ok(())
}
fn referenced_files(
    root: &Root,
    sources: &[SnapshotSource],
    snapshot: &mut Snapshot,
    direct: Option<&Workspace>,
    cancel: &Cancellation,
) -> Result<()> {
    for source in sources {
        cancel.check()?;
        if !source.project_prefix.is_empty() {
            RelPath::new(&source.project_prefix)?;
        }
        let source_file = project_file(&source.project_prefix, &source.source_path)?;
        if let Some(workspace) = direct {
            if !direct_path_allowed(workspace, &source_file) {
                continue;
            }
            record_direct_file(root, workspace, source_file.clone(), snapshot)?;
            if !snapshot.contains_key(source_file.as_str()) {
                continue;
            }
        }
        if !source.project_prefix.is_empty()
            && root.inspect(&RelPath::new(&source.project_prefix)?)? == FileSnapshot::Missing
        {
            continue;
        }
        let project = Root::open(&root.path.join(&source.project_prefix))?;
        let metadata = super::sources::text(&project, &source.source_path)
            .and_then(|text| super::content::metadata(&text))
            .ok()
            .flatten();
        let declared = metadata
            .as_ref()
            .map(|metadata| &metadata.resources)
            .unwrap_or(&source.inferred_resources);
        let resources: Vec<_> = source
            .personal_resources
            .iter()
            .chain(declared)
            .cloned()
            .collect();
        if let Some(workspace) = direct {
            for resource in &resources {
                if resource.target_type == TargetType::Local
                    && resource.kind == ResourceKind::Prototype
                {
                    // Keep the declared source entry even when preview automatically chooses dist.
                    let fallback = resource
                        .target
                        .strip_suffix("dist/index.html")
                        .filter(|prefix| prefix.is_empty() || prefix.ends_with('/'))
                        .map_or_else(
                            || resource.target.clone(),
                            |prefix| format!("{prefix}index.html"),
                        );
                    let fallback = project_file(&source.project_prefix, &fallback)?;
                    record_direct_file(root, workspace, fallback, snapshot)?;
                }
            }
        }
        let resources =
            super::content::resolve_local_resources(&project, &source.source_path, resources);
        for resource in &resources {
            cancel.check()?;
            if resource.target_type != TargetType::Local {
                continue;
            }
            let target = RelPath::new(&resource.target)?;
            if !allowed(&target)
                || target
                    .as_str()
                    .split('/')
                    .any(|part| matches!(part, "node_modules" | ".pnpm-store" | ".yarn" | ".venv"))
            {
                continue;
            }
            if let Some(workspace) = direct {
                let file = project_file(&source.project_prefix, target.as_str())?;
                record_direct_file(root, workspace, file.clone(), snapshot)?;
                if snapshot.contains_key(file.as_str())
                    && resource.kind == ResourceKind::Prototype
                    && super::preview::check_entry(&project, target.as_str()).is_ok()
                {
                    let files = super::preview::served_files_with_cancel(
                        &project,
                        target.as_str(),
                        cancel,
                    )?;
                    for (path, bytes) in files {
                        let path = project_file(&source.project_prefix, &path)?;
                        if !direct_path_allowed(workspace, &path) {
                            continue;
                        }
                        record_direct_file(root, workspace, path.clone(), snapshot)?;
                        if let Some(descriptor) = snapshot.get(path.as_str())
                            && crate::model::FileDescriptor::new(&bytes, descriptor.mode)
                                != *descriptor
                        {
                            return Err(YssError::new(
                                ErrorCode::ConcurrentChange,
                                "读取原型资源期间文件改变",
                            )
                            .at(path));
                        }
                    }
                    validate_snapshot(snapshot)?;
                }
                continue;
            }
            let current = project.inspect(&target)?;
            if current == FileSnapshot::Missing {
                continue;
            }
            let FileSnapshot::File { descriptor, .. } = current else {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "运行引用资源须为普通文件").at(&target),
                );
            };
            let paths = if resource.kind == ResourceKind::Prototype
                && matches!(
                    target
                        .as_str()
                        .rsplit('.')
                        .next()
                        .map(str::to_ascii_lowercase)
                        .as_deref(),
                    Some("html" | "htm")
                ) {
                let files =
                    super::preview::served_files_with_cancel(&project, target.as_str(), cancel)?;
                let mut descriptors = BTreeMap::new();
                for (path, bytes) in files {
                    let current = project.inspect(&RelPath::new(&path)?)?;
                    let current = current.descriptor().ok_or_else(|| {
                        YssError::new(ErrorCode::ConcurrentChange, "读取原型资源期间文件改变")
                            .at(&path)
                    })?;
                    if crate::model::FileDescriptor::new(&bytes, current.mode) != *current {
                        return Err(YssError::new(
                            ErrorCode::ConcurrentChange,
                            "读取原型资源期间文件改变",
                        )
                        .at(&path));
                    }
                    descriptors.insert(path, current.clone());
                }
                descriptors
            } else {
                BTreeMap::from([(resource.target.clone(), descriptor)])
            };
            for (path, descriptor) in paths {
                let path = if source.project_prefix.is_empty() {
                    path
                } else {
                    format!("{}/{path}", source.project_prefix)
                };
                snapshot.insert(path, descriptor);
            }
            validate_snapshot(snapshot)?;
        }
    }
    Ok(())
}
fn project_file(prefix: &str, path: &str) -> Result<RelPath> {
    let path = RelPath::new(path)?;
    if prefix.is_empty() {
        Ok(path)
    } else {
        RelPath::new(prefix)?.join(path.as_str())
    }
}

#[cfg(unix)]
fn path_under(path: &str, prefix: &str) -> bool {
    prefix.is_empty()
        || path == prefix
        || path
            .strip_prefix(prefix)
            .is_some_and(|rest| rest.starts_with('/'))
}

pub(super) fn direct_path_allowed(workspace: &Workspace, path: &RelPath) -> bool {
    let Some(direct) = &workspace.direct else {
        return false;
    };
    #[cfg(unix)]
    let key = crate::fs::alias_key(path.as_str());
    #[cfg(windows)]
    let key = path.as_str().to_owned();
    if key.split('/').any(|part| {
        [".git", ".gitmodules", ".yss"]
            .iter()
            .any(|protected| part.eq_ignore_ascii_case(protected))
    }) {
        return false;
    }
    let selected = matching_project(workspace, &key);
    selected.is_some_and(|(depth, _)| {
        #[cfg(unix)]
        let excluded = direct.excluded_prefixes.iter().any(|prefix| {
            prefix.split('/').count() >= depth && path_under(&key, &crate::fs::alias_key(prefix))
        });
        #[cfg(windows)]
        let excluded = direct.excluded_prefixes.iter().any(|prefix| {
            prefix.split('/').count() >= depth
                && crate::fs::windows_path_under(&key, prefix).unwrap_or(true)
        });
        !excluded
    })
}

#[cfg(unix)]
fn matching_project<'a>(workspace: &'a Workspace, path_key: &str) -> Option<(usize, &'a str)> {
    workspace
        .project_prefixes
        .iter()
        .filter(|(_, prefix)| path_under(path_key, &crate::fs::alias_key(prefix)))
        .map(|(id, prefix)| {
            (
                prefix.split('/').filter(|part| !part.is_empty()).count(),
                id.as_str(),
            )
        })
        .max_by_key(|(depth, _)| *depth)
}

#[cfg(windows)]
fn matching_project<'a>(workspace: &'a Workspace, path: &str) -> Option<(usize, &'a str)> {
    let mut best = None;
    for (id, prefix) in &workspace.project_prefixes {
        if crate::fs::windows_path_under(path, prefix)? {
            let depth = prefix.split('/').filter(|part| !part.is_empty()).count();
            if best.is_none_or(|(current, _)| depth > current) {
                best = Some((depth, id.as_str()));
            }
        }
    }
    best
}

pub(super) fn path_in_write_scope(workspace: &Workspace, path: &str) -> bool {
    workspace
        .direct
        .as_ref()
        .and_then(|direct| direct.project_modes.as_ref())
        .is_none_or(|modes| {
            #[cfg(unix)]
            let matched = matching_project(workspace, &crate::fs::alias_key(path));
            #[cfg(windows)]
            let matched = matching_project(workspace, path);
            matched
                .and_then(|(_, id)| modes.get(id))
                .is_some_and(|mode| *mode != ProjectMode::Read)
        })
}

fn concurrent(message: &str) -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, message)
}

fn direct_root(workspace: &Workspace, direct: &DirectWorkspace) -> Result<Root> {
    let root = Root::open(Path::new(&workspace.original_root))?;
    if workspace.original_root != workspace.worktree_root
        || root.identity != workspace.original_identity
        || root.identity != workspace.worktree_identity
        || direct.project_identities.len() != workspace.project_prefixes.len()
    {
        return Err(concurrent("原工程绑定或目录身份已改变"));
    }
    for (id, prefix) in &workspace.project_prefixes {
        let project_path = if prefix.is_empty() {
            root.path.clone()
        } else {
            root.path.join(RelPath::new(prefix)?.as_str())
        };
        let project = Root::open(&project_path)?;
        if direct.project_identities.get(id) != Some(&project.identity) {
            return Err(concurrent("登记工程目录身份已改变").at(prefix));
        }
    }
    Ok(root)
}

fn check_resume_baseline(prior: &GitBaseline, current: &GitBaseline) -> Result<()> {
    if prior.branch != current.branch || (prior.branch.is_none() && prior.head != current.head) {
        return Err(
            concurrent("原工程 Git 分支或分离 HEAD 已改变，请从原 Spec 新建会话")
                .context("resumeReason", "checkoutChanged"),
        );
    }
    check_git_directory_identity(prior, current)
}

fn check_git_directory_identity(prior: &GitBaseline, current: &GitBaseline) -> Result<()> {
    if prior.git_dir != current.git_dir
        || prior.git_dir_identity != current.git_dir_identity
        || prior.common_dir != current.common_dir
        || prior.common_dir_identity != current.common_dir_identity
    {
        return Err(
            concurrent("原工程 Git 目录身份已改变，请从原 Spec 新建会话")
                .context("resumeReason", "workspaceChanged"),
        );
    }
    Ok(())
}

pub(crate) fn validate_resume(workspace: &Workspace, cancel: &Cancellation) -> Result<()> {
    resume_checkout(workspace, cancel)
        .and_then(|(root, _)| check_project_git_roots(workspace, &root, cancel))
        .map_err(|error| {
            if error.code == ErrorCode::Cancelled || error.context.contains_key("resumeReason") {
                return error;
            }
            concurrent("原工作区或 Git 检出不可用，请从原 Spec 新建会话")
                .context("resumeReason", "workspaceChanged")
                .context("cause", error.message)
        })
}

pub(super) fn resume_checkout(
    workspace: &Workspace,
    cancel: &Cancellation,
) -> Result<(Root, GitBaseline)> {
    let (root, current, prior) = direct_checkout(workspace, cancel)?;
    check_resume_baseline(prior, &current)?;
    Ok((root, current))
}

pub(super) fn native_fork_checkout(
    workspace: &Workspace,
    cancel: &Cancellation,
) -> Result<(Root, GitBaseline)> {
    let (root, current, prior) = direct_checkout(workspace, cancel)?;
    check_git_directory_identity(prior, &current)?;
    check_project_git_roots(workspace, &root, cancel)?;
    Ok((root, current))
}

fn check_project_git_roots(
    workspace: &Workspace,
    root: &Root,
    cancel: &Cancellation,
) -> Result<()> {
    for prefix in workspace
        .project_prefixes
        .values()
        .filter(|prefix| !prefix.is_empty())
    {
        if git_root_cancel(&root.path.join(prefix), cancel)? != root.path {
            return Err(
                concurrent("登记工程所在 Git 工作树已变化，请从原 Spec 新建会话")
                    .context("resumeReason", "workspaceChanged"),
            );
        }
    }
    Ok(())
}

fn direct_checkout<'a>(
    workspace: &'a Workspace,
    cancel: &Cancellation,
) -> Result<(Root, GitBaseline, &'a GitBaseline)> {
    let direct = workspace
        .direct
        .as_ref()
        .ok_or_else(|| invalid("历史隔离会话请从原 Spec 开始新会话"))?;
    let root = direct_root(workspace, direct)?;
    let current = git_baseline(&root.path, cancel)?;
    Ok((root, current, &direct.baseline))
}

fn direct_metadata(root: &Root, workspace: &Workspace, path: &RelPath) -> Result<bool> {
    if !direct_path_allowed(workspace, path) {
        return Ok(false);
    }
    let before = workspace.initial.contains_key(path.as_str());
    let metadata = match root.source_metadata(path) {
        Ok(metadata) => metadata,
        Err(error)
            if error
                .context
                .get("kind")
                .is_some_and(|kind| kind == "NotFound") =>
        {
            return Ok(false);
        }
        // Gitlinks, links and nested repositories are outside the selected file scope.
        Err(error) if error.code == ErrorCode::UnsafePath && !before => return Ok(false),
        Err(error) => return Err(error),
    };
    #[cfg(unix)]
    let single_link = {
        use cap_std::fs::MetadataExt;
        metadata.nlink() == 1
    };
    #[cfg(windows)]
    let single_link = if metadata.is_file() {
        crate::fs::platform::has_single_link(&root.open_streamed(path)?)?
    } else {
        false
    };
    #[cfg(not(any(unix, windows)))]
    let single_link = false;
    if metadata.is_file() && single_link {
        Ok(true)
    } else if before {
        Err(concurrent("原工程文件已被受保护对象替换").at(path))
    } else {
        Ok(false)
    }
}

fn record_direct_file(
    root: &Root,
    workspace: &Workspace,
    path: RelPath,
    snapshot: &mut Snapshot,
) -> Result<()> {
    if !snapshot.contains_key(path.as_str()) && direct_metadata(root, workspace, &path)? {
        let file = root.source_read_single_link(&path, MAX_ASSET)?;
        if !direct_metadata(root, workspace, &path)? {
            return Err(concurrent("读取原工程文件期间发生变化").at(path));
        }
        if let FileSnapshot::File { descriptor, .. } = file.snapshot {
            snapshot.insert(path.into(), descriptor);
        }
    }
    Ok(())
}

fn direct_snapshot(workspace: &Workspace, cancel: &Cancellation) -> Result<Snapshot> {
    let direct = workspace
        .direct
        .as_ref()
        .expect("direct snapshot requires metadata");
    let root = direct_root(workspace, direct)?;
    check_baseline(&root.path, &direct.baseline, cancel)?;
    let mut snapshot = BTreeMap::new();
    if !direct.read_only {
        let writable: Vec<_> = workspace
            .project_prefixes
            .iter()
            .filter(|(id, _)| {
                direct.project_modes.as_ref().is_none_or(|modes| {
                    modes
                        .get(*id)
                        .is_some_and(|mode| *mode != ProjectMode::Read)
                })
            })
            .map(|(_, prefix)| prefix)
            .collect();
        let scopes: Vec<_> = writable
            .iter()
            .filter(|prefix| !prefix.is_empty())
            .map(|prefix| format!(":(literal){prefix}"))
            .collect();
        let mut args = vec![
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
            "--",
        ];
        if scopes.len() == writable.len() {
            args.extend(scopes.iter().map(String::as_str));
        }
        let names = git_cancel(&root.path, &args, cancel, Duration::from_secs(30))?;
        for name in names
            .split(|byte| *byte == 0)
            .filter(|name| !name.is_empty())
        {
            cancel.check()?;
            let name = std::str::from_utf8(name).map_err(|_| invalid("工程包含非 UTF-8 路径"))?;
            if !name.ends_with('/') {
                let path = RelPath::new(name)?;
                if path_in_write_scope(workspace, path.as_str()) {
                    record_direct_file(&root, workspace, path, &mut snapshot)?;
                }
            }
            if snapshot.len() > 200_000 {
                return Err(invalid("工程文件数量超过快照限制"));
            }
        }
    }
    for path in workspace.initial.keys() {
        cancel.check()?;
        record_direct_file(&root, workspace, RelPath::new(path)?, &mut snapshot)?;
    }
    referenced_files(
        &root,
        &workspace.resource_sources,
        &mut snapshot,
        Some(workspace),
        cancel,
    )?;
    validate_snapshot(&snapshot)?;
    root.revalidate()?;
    check_baseline(&root.path, &direct.baseline, cancel)?;
    Ok(snapshot)
}

pub(super) enum DirectAccess<'a> {
    Legacy { read_only: bool },
    PerProject(&'a [ProjectAccess]),
}

/// Keep selected and excluded directory identities stable through snapshot and execution.
#[cfg(windows)]
pub(super) struct WindowsBoundaryPins {
    _pins: Vec<std::fs::File>,
    git_roots: BTreeMap<PathBuf, GitBaseline>,
}

#[cfg(windows)]
pub(super) fn pin_windows_project_boundaries(
    data_dir: &Path,
    projects: &[(String, String)],
    excluded: &[PathBuf],
    pi_config: &Path,
    cancel: &Cancellation,
) -> Result<WindowsBoundaryPins> {
    let mut pins = Vec::new();
    let mut identities = Vec::new();
    let mut roots = Vec::new();
    let managed_tools = super::managed_tools::protected_directory()?;
    let mut paths: Vec<PathBuf> = projects
        .iter()
        .map(|(_, path)| PathBuf::from(path))
        .chain(excluded.iter().cloned())
        .chain([data_dir.to_owned(), pi_config.to_owned()])
        .chain(managed_tools.iter().cloned())
        .collect();
    let mut git_roots = BTreeMap::new();
    for (_, project) in projects {
        cancel.check()?;
        let git_root = agent_git_root(Path::new(project), cancel)?;
        if !git_roots.contains_key(&git_root) {
            let root = Root::open(&git_root)?;
            if let Some(marker) = root.pin_git_marker()? {
                pins.push(marker);
            }
            let baseline = git_baseline(&git_root, cancel)?;
            paths.extend([
                git_root.clone(),
                PathBuf::from(&baseline.git_dir),
                PathBuf::from(&baseline.common_dir),
            ]);
            git_roots.insert(git_root, baseline);
        }
    }
    for path in paths.iter().map(PathBuf::as_path) {
        cancel.check()?;
        let original = match Root::open(path) {
            Ok(root) => root,
            Err(_)
                if (path == pi_config || managed_tools.as_deref() == Some(path))
                    && std::fs::symlink_metadata(path)
                        .is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound) =>
            {
                continue;
            }
            Err(error) => return Err(error),
        };
        let mut current = std::fs::canonicalize(path)?;
        loop {
            if current.file_name().is_none() {
                break;
            }
            let root = Root::open(&current)?;
            if !identities.contains(&root.identity) {
                pins.push(root.pin_directory()?);
                identities.push(root.identity.clone());
            }
            current = current
                .parent()
                .ok_or_else(|| invalid("工程祖先目录无效"))?
                .to_owned();
        }
        roots.push(original);
    }
    for root in roots {
        root.revalidate()?;
    }
    for (root, baseline) in &git_roots {
        check_baseline(root, baseline, cancel)?;
    }
    Ok(WindowsBoundaryPins {
        _pins: pins,
        git_roots,
    })
}

pub(super) fn prepare_direct(
    data_dir: &Path,
    projects: &[(String, String)],
    prior: Option<&[Workspace]>,
    spec: &SpecRecord,
    excluded_projects: &[PathBuf],
    access: DirectAccess<'_>,
    cancel: &Cancellation,
    #[cfg(windows)] boundary_pins: &WindowsBoundaryPins,
) -> Result<Vec<Workspace>> {
    let (read_only, grants) = match access {
        DirectAccess::Legacy { read_only } => (read_only, None),
        DirectAccess::PerProject(grants) => (true, Some(grants)),
    };
    let managed_tools = super::managed_tools::protected_directory()?;
    let mut grouped = BTreeMap::<PathBuf, Vec<(&str, Root)>>::new();
    for (id, path) in projects {
        cancel.check()?;
        let project = Root::open(Path::new(path))?;
        #[cfg(unix)]
        let in_data = project.path.starts_with(data_dir);
        #[cfg(windows)]
        let in_data = Root::open(data_dir)?
            .relative_windows_path(&project.path)?
            .is_some();
        if in_data {
            return Err(invalid("工作台数据目录不能作为 Pi 工程"));
        }
        if let Some(protected) = &managed_tools {
            #[cfg(unix)]
            let inside = project.path.starts_with(protected);
            #[cfg(windows)]
            let inside = match Root::open(protected) {
                Ok(root) => root.relative_windows_path(&project.path)?.is_some(),
                Err(_)
                    if std::fs::symlink_metadata(protected)
                        .is_err_and(|error| error.kind() == std::io::ErrorKind::NotFound) =>
                {
                    false
                }
                Err(error) => return Err(error),
            };
            if inside {
                return Err(invalid("工作台受管工具目录不能作为 Pi 工程"));
            }
        }
        let git_root = agent_git_root(&project.path, cancel)?;
        grouped.entry(git_root).or_default().push((id, project));
    }
    if prior.is_some_and(|previous| previous.len() != grouped.len()) {
        return Err(concurrent("续接工程范围已改变，请从原 Spec 开始新会话"));
    }
    let mut workspaces = Vec::new();
    for (path, projects) in grouped {
        cancel.check()?;
        let root = Root::open(&path)?;
        let baseline = git_baseline(&path, cancel)?;
        #[cfg(windows)]
        if boundary_pins.git_roots.get(&path) != Some(&baseline) {
            return Err(concurrent("Git 工作树或受保护目录在准备期间变化"));
        }
        let root_path = path.to_str().ok_or_else(|| invalid("Git 根不是 UTF-8"))?;
        let mut prefixes = BTreeMap::new();
        let mut identities = BTreeMap::new();
        let mut project_modes = grants.map(|_| BTreeMap::new());
        let mut unique_prefixes = BTreeSet::new();
        for (id, project) in &projects {
            if let Some((grants, modes)) = grants.zip(project_modes.as_mut()) {
                let grant = grants
                    .iter()
                    .find(|grant| &grant.project_id == id)
                    .ok_or_else(|| invalid("运行工程缺少授权"))?;
                if grant
                    .expected_head
                    .as_ref()
                    .is_some_and(|head| head != &baseline.head)
                {
                    return Err(concurrent("原工程 HEAD 已变化，请刷新本轮工程范围")
                        .context("httpStatus", 409)
                        .context("projectScopeChanged", true)
                        .context("projectId", id)
                        .context(
                            "expectedHead",
                            grant.expected_head.as_deref().unwrap_or_default(),
                        )
                        .context("currentHead", &baseline.head));
                }
                if let Some(expected_branch) = &grant.expected_branch
                    && expected_branch.as_deref() != baseline.branch_name()
                {
                    return Err(concurrent("原工程 Git 分支已变化，请刷新本轮工程范围")
                        .context("httpStatus", 409)
                        .context("projectScopeChanged", true)
                        .context("projectId", id)
                        .context(
                            "expectedBranch",
                            expected_branch.as_deref().unwrap_or("HEAD"),
                        )
                        .context("currentBranch", baseline.branch_name().unwrap_or("HEAD")));
                }
                modes.insert((*id).to_owned(), grant.mode);
            }
            #[cfg(unix)]
            let prefix = project
                .path
                .strip_prefix(&path)
                .map_err(|_| invalid("登记工程不在 Git 根内"))?;
            #[cfg(unix)]
            let prefix = prefix
                .to_str()
                .ok_or_else(|| invalid("工程路径不是 UTF-8"))?
                .replace(std::path::MAIN_SEPARATOR, "/");
            #[cfg(windows)]
            let prefix = root
                .relative_windows_path(&project.path)?
                .ok_or_else(|| invalid("登记工程不在 Git 根内"))?;
            if !prefix.is_empty() {
                RelPath::new(&prefix)?;
                if crate::fs::alias_key(&prefix)
                    .split('/')
                    .any(|part| matches!(part, ".git" | ".gitmodules" | ".yss"))
                {
                    return Err(invalid("受保护的 Git 或 YSS 目录不能作为运行工程"));
                }
            }
            if grants.is_some() && !unique_prefixes.insert(crate::fs::alias_key(&prefix)) {
                return Err(invalid("同一路径的登记工程只能选择一个"));
            }
            #[cfg(windows)]
            if grants.is_some()
                && identities
                    .values()
                    .any(|identity| identity == &project.identity)
            {
                return Err(invalid("同一目录的登记工程只能选择一个"));
            }
            prefixes.insert((*id).to_owned(), prefix);
            identities.insert((*id).to_owned(), project.identity.clone());
        }
        if let Some(previous) = prior {
            let old = previous
                .iter()
                .find(|w| w.original_root == root_path)
                .ok_or_else(|| concurrent("续接工程范围已改变，请从原 Spec 开始新会话"))?;
            let direct = old
                .direct
                .as_ref()
                .ok_or_else(|| invalid("历史隔离会话请从原 Spec 开始新会话"))?;
            if old.original_identity != root.identity
                || old.project_prefixes != prefixes
                || direct.project_identities != identities
            {
                return Err(concurrent("续接工程绑定已改变，请从原 Spec 开始新会话"));
            }
            check_resume_baseline(&direct.baseline, &baseline)?;
        }
        let mut excluded_prefixes = BTreeSet::new();
        #[cfg(unix)]
        for excluded in excluded_projects
            .iter()
            .map(PathBuf::as_path)
            .chain([data_dir])
            .chain(managed_tools.as_deref())
        {
            if projects
                .iter()
                .any(|(_, project)| excluded != project.path && excluded.starts_with(&project.path))
            {
                let relative = excluded
                    .strip_prefix(&path)
                    .map_err(|_| invalid("排除路径不在 Git 根内"))?;
                let relative = relative
                    .to_str()
                    .ok_or_else(|| invalid("排除路径不是 UTF-8"))?
                    .replace(std::path::MAIN_SEPARATOR, "/");
                excluded_prefixes.insert(RelPath::new(relative)?.into());
            }
        }
        #[cfg(windows)]
        for excluded in excluded_projects
            .iter()
            .map(PathBuf::as_path)
            .chain([data_dir])
            .chain(managed_tools.as_deref())
        {
            let mut inside = false;
            for (_, project) in &projects {
                if let Some(relative) = project.relative_windows_path(excluded)? {
                    if relative.is_empty() {
                        return Err(invalid("未选择工程与授权工程指向同一目录"));
                    }
                    inside = true;
                }
            }
            if inside {
                let relative = root
                    .relative_windows_path(excluded)?
                    .ok_or_else(|| invalid("排除路径不在 Git 根内"))?;
                excluded_prefixes.insert(RelPath::new(relative)?.into());
            }
        }
        let mut workspace = Workspace {
            project_ids: prefixes.keys().cloned().collect(),
            project_prefixes: prefixes,
            original_root: root_path.into(),
            original_identity: root.identity.clone(),
            worktree_root: root_path.into(),
            worktree_identity: root.identity.clone(),
            branch: baseline
                .branch
                .as_deref()
                .and_then(|branch| branch.strip_prefix("refs/heads/"))
                .unwrap_or("HEAD")
                .into(),
            head: baseline.head.clone(),
            initial: BTreeMap::new(),
            resource_sources: Vec::new(),
            result_snapshot: None,
            direct: Some(DirectWorkspace {
                baseline,
                read_only: project_modes.as_ref().map_or(read_only, |modes| {
                    modes.values().all(|mode| *mode == ProjectMode::Read)
                }),
                project_modes,
                project_identities: identities,
                excluded_prefixes,
            }),
        };
        bind_spec(&mut workspace, spec);
        workspace.initial = direct_snapshot(&workspace, cancel)?;
        if let Some(prefix) = workspace.project_prefixes.get(&spec.project_id) {
            let source = project_file(prefix, &spec.source_path)?;
            if !workspace.initial.contains_key(source.as_str()) {
                return Err(
                    YssError::new(ErrorCode::NotReady, "原工程主 Spec 不是可读的普通文件")
                        .at(source),
                );
            }
        }
        workspaces.push(workspace);
    }
    Ok(workspaces)
}

pub(super) fn direct_initial_blobs(
    workspace: &Workspace,
    cancel: &Cancellation,
) -> Result<Vec<(String, Vec<u8>)>> {
    let direct = workspace
        .direct
        .as_ref()
        .ok_or_else(|| invalid("缺少原工程运行基线"))?;
    let root = direct_root(workspace, direct)?;
    check_baseline(&root.path, &direct.baseline, cancel)?;
    let mut blobs = BTreeMap::new();
    for (path, descriptor) in &workspace.initial {
        cancel.check()?;
        let path = RelPath::new(path)?;
        let file = root.source_read_single_link(&path, MAX_ASSET)?;
        if file.snapshot.descriptor() != Some(descriptor)
            || !direct_metadata(&root, workspace, &path)?
        {
            return Err(concurrent("记录运行前文件期间原工程内容已变化").at(path));
        }
        blobs
            .entry(descriptor.sha256.as_str().to_owned())
            .or_insert(file.bytes);
    }
    root.revalidate()?;
    check_baseline(&root.path, &direct.baseline, cancel)?;
    Ok(blobs.into_iter().collect())
}

pub fn working_snapshot(workspace: &Workspace, cancel: &Cancellation) -> Result<Snapshot> {
    if workspace.direct.is_some() {
        return direct_snapshot(workspace, cancel);
    }
    let mut result = snapshot(Path::new(&workspace.worktree_root), cancel)?;
    let root = Root::open(Path::new(&workspace.worktree_root))?;
    for path in workspace.initial.keys() {
        if result.contains_key(path) {
            continue;
        }
        cancel.check()?;
        match root.inspect(&RelPath::new(path)?)? {
            FileSnapshot::Missing => {}
            FileSnapshot::File { descriptor, .. } => {
                result.insert(path.clone(), descriptor);
            }
            _ => {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "运行文件已被受保护对象替换").at(path),
                );
            }
        }
    }
    referenced_files(
        &root,
        &workspace.resource_sources,
        &mut result,
        None,
        cancel,
    )?;
    validate_snapshot(&result)?;
    Ok(result)
}
pub fn validate(workspace: &Workspace) -> Result<()> {
    if let Some(direct) = &workspace.direct {
        let root = direct_root(workspace, direct)?;
        return check_baseline(&root.path, &direct.baseline, &Cancellation::new());
    }
    if Root::open(Path::new(&workspace.original_root))?.identity != workspace.original_identity
        || Root::open(Path::new(&workspace.worktree_root))?.identity != workspace.worktree_identity
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "运行工作区或原工程目录身份已改变",
        ));
    }
    if git_text(
        Path::new(&workspace.worktree_root),
        &["symbolic-ref", "--short", "HEAD"],
    )? != workspace.branch
        || git_text(Path::new(&workspace.worktree_root), &["rev-parse", "HEAD"])? != workspace.head
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "运行工作区分支或 HEAD 已改变",
        ));
    }
    Ok(())
}
pub fn initial_bytes(data: &Path, run_id: &str, descriptor: &FileDescriptor) -> Result<Vec<u8>> {
    let rel = RelPath::new(format!(
        "agent-workspaces/{run_id}/objects/{}",
        descriptor.sha256
    ))?;
    let bytes = Root::open(data)?.read_checked(&rel, MAX_ASSET)?.bytes;
    if crate::model::Sha256::of(&bytes) != descriptor.sha256 {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "运行初始对象摘要不一致",
        ));
    }
    Ok(bytes)
}
pub fn apply(
    workspace: &Workspace,
    baseline: &GitBaseline,
    selected: &[(String, Option<FileDescriptor>, Option<FileDescriptor>)],
    source: impl Fn(&FileDescriptor) -> Result<Vec<u8>>,
    cancel: &Cancellation,
) -> Result<transaction::TransactionReport> {
    let original = Root::open(Path::new(&workspace.original_root))?;
    if original.identity != workspace.original_identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "原工程目录身份已改变",
        ));
    }
    check_baseline(Path::new(&workspace.original_root), baseline, cancel)?;
    let mut operations = Vec::new();
    let mut directories = BTreeSet::new();
    let mut guards = BTreeMap::new();
    for (path, expected, result) in selected {
        let rel = RelPath::new(path)?;
        if !allowed(&rel) {
            return Err(YssError::new(ErrorCode::UnsafePath, "不能接收控制文件").at(path));
        }
        let before = original.inspect(&rel)?;
        if before.descriptor() != expected.as_ref()
            || matches!(
                before,
                FileSnapshot::Dangerous | FileSnapshot::Directory { .. }
            )
        {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "原工程文件已改变，请重新预览").at(path),
            );
        }
        guards.insert(rel.clone(), before.clone());
        if expected == result {
            continue;
        }
        if result.is_some() {
            for parent in rel.parents() {
                let parent = RelPath::new(parent)?;
                match original.inspect(&parent)? {
                    FileSnapshot::Missing => {
                        directories.insert(parent);
                    }
                    FileSnapshot::Directory { .. } => {}
                    _ => {
                        return Err(
                            YssError::new(ErrorCode::UnsafePath, "接收路径父目录不安全").at(parent)
                        );
                    }
                }
            }
            let descriptor = result.as_ref().unwrap();
            let bytes = source(descriptor)?;
            if crate::model::Sha256::of(&bytes) != descriptor.sha256
                || bytes.len() as u64 != descriptor.size
            {
                return Err(YssError::new(
                    ErrorCode::DigestMismatch,
                    "冻结成果对象摘要或长度不一致",
                )
                .at(path));
            }
            let file = RenderedFile {
                bytes,
                mode: descriptor.mode,
            };
            operations.push(if before == FileSnapshot::Missing {
                Operation::Create { path: rel, file }
            } else {
                Operation::Replace {
                    path: rel,
                    before,
                    file,
                }
            });
        } else if before != FileSnapshot::Missing {
            operations.push(Operation::Delete { path: rel, before });
        }
    }
    let mut directory_ops: Vec<_> = directories
        .into_iter()
        .map(|path| Operation::CreateDir { path })
        .collect();
    directory_ops.extend(operations);
    transaction::apply_files_checked(
        Path::new(&workspace.original_root),
        original.identity,
        directory_ops,
        guards,
        cancel,
        &|| check_baseline(Path::new(&workspace.original_root), baseline, cancel),
    )
}
