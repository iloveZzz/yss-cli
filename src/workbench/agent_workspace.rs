use super::model::{ResourceKind, ResourceRef, SpecRecord, TargetType};
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
fn mkdirs(root: &Root, path: &RelPath) -> Result<()> {
    for parent in path.parents() {
        let rel = RelPath::new(parent)?;
        match root.inspect(&rel)? {
            FileSnapshot::Missing => {
                root.create_dir(&rel)?;
            }
            FileSnapshot::Directory { .. } => {}
            _ => {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "文件父目录包含受保护对象").at(parent),
                );
            }
        }
    }
    Ok(())
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
    cancel: &Cancellation,
) -> Result<()> {
    for source in sources {
        cancel.check()?;
        if !source.project_prefix.is_empty() {
            RelPath::new(&source.project_prefix)?;
        }
        RelPath::new(&source.source_path)?;
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
        for resource in source.personal_resources.iter().chain(declared) {
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
pub fn prepare(
    data_dir: &Path,
    run_id: &str,
    projects: &[(String, String)],
    carry: &BTreeMap<String, Vec<String>>,
    cancel: &Cancellation,
) -> Result<Vec<Workspace>> {
    prepare_workspaces(data_dir, run_id, projects, carry, None, cancel)
}

pub fn prepare_for_spec(
    data_dir: &Path,
    run_id: &str,
    projects: &[(String, String)],
    carry: &BTreeMap<String, Vec<String>>,
    spec: &SpecRecord,
    cancel: &Cancellation,
) -> Result<Vec<Workspace>> {
    prepare_workspaces(data_dir, run_id, projects, carry, Some(spec), cancel)
}

fn prepare_workspaces(
    data_dir: &Path,
    run_id: &str,
    projects: &[(String, String)],
    carry: &BTreeMap<String, Vec<String>>,
    spec: Option<&SpecRecord>,
    cancel: &Cancellation,
) -> Result<Vec<Workspace>> {
    let data = Root::open(data_dir)?;
    let run_dir = RelPath::new(format!("agent-workspaces/{run_id}/sentinel"))?;
    mkdirs(&data, &run_dir)?;
    let mut grouped = BTreeMap::<PathBuf, Vec<(String, PathBuf)>>::new();
    for (id, path) in projects {
        cancel.check()?;
        let actual = std::fs::canonicalize(path)?;
        grouped
            .entry(git_root_cancel(&actual, cancel)?)
            .or_default()
            .push((id.clone(), actual));
    }
    let mut workspaces = Vec::new();
    for (index, (original, group)) in grouped.into_iter().enumerate() {
        cancel.check()?;
        let root = Root::open(&original)?;
        let head = git_text_cancel(&original, &["rev-parse", "--verify", "HEAD"], cancel)?;
        let branch = format!("codex/yss-workbench-{run_id}-{index}");
        let target = data_dir
            .join("agent-workspaces")
            .join(run_id)
            .join(index.to_string());
        let target_text = target
            .to_str()
            .ok_or_else(|| invalid("工作区路径不是 UTF-8"))?;
        git_cancel(
            &original,
            &["worktree", "add", "-b", &branch, "--", target_text, &head],
            cancel,
            Duration::from_secs(300),
        )?;
        let work = Root::open(&target)?;
        let mut prefixes = BTreeMap::new();
        let mut carried = BTreeSet::new();
        for (id, project) in &group {
            let prefix = project
                .strip_prefix(&original)
                .map_err(|_| invalid("工程根越界"))?
                .to_string_lossy()
                .replace('\\', "/");
            prefixes.insert(id.clone(), prefix.clone());
            for selected in carry.get(id).into_iter().flatten() {
                cancel.check()?;
                let rel = RelPath::new(if prefix.is_empty() {
                    selected.clone()
                } else {
                    format!("{prefix}/{selected}")
                })?;
                if !allowed(&rel) {
                    return Err(YssError::new(
                        ErrorCode::UnsafePath,
                        "不能带入 Git 或 YSS 控制文件",
                    )
                    .at(&rel));
                }
                carried.insert(rel.as_str().to_owned());
                let before = work.inspect(&rel)?;
                let current = root.inspect(&rel)?;
                if matches!(
                    before,
                    FileSnapshot::Directory { .. } | FileSnapshot::Dangerous
                ) || matches!(
                    current,
                    FileSnapshot::Directory { .. } | FileSnapshot::Dangerous
                ) {
                    return Err(YssError::new(
                        ErrorCode::UnsafePath,
                        "带入路径须为普通文件或已删除文件",
                    )
                    .at(&rel));
                }
                let bytes = if let FileSnapshot::File { .. } = current {
                    Some(root.read_checked(&rel, MAX_ASSET)?)
                } else {
                    None
                };
                if root.inspect(&rel)? != current {
                    return Err(
                        YssError::new(ErrorCode::ConcurrentChange, "带入期间原文件改变").at(&rel),
                    );
                }
                if before != FileSnapshot::Missing {
                    work.remove(&rel, &before)?;
                }
                if let Some(file) = bytes {
                    mkdirs(&work, &rel)?;
                    work.create_file(&rel, &file.bytes, file.snapshot.descriptor().unwrap().mode)?;
                }
            }
        }
        let mut initial = snapshot(&target, cancel)?;
        for path in carried {
            if let FileSnapshot::File { descriptor, .. } = work.inspect(&RelPath::new(&path)?)? {
                initial.insert(path, descriptor);
            }
        }
        let resource_sources = spec
            .filter(|spec| prefixes.contains_key(&spec.project_id))
            .map(|spec| vec![resource_source(spec, &prefixes[&spec.project_id])])
            .unwrap_or_default();
        referenced_files(&work, &resource_sources, &mut initial, cancel)?;
        validate_snapshot(&initial)?;
        let object_dir = RelPath::new(format!("agent-workspaces/{run_id}/objects/sentinel"))?;
        mkdirs(&data, &object_dir)?;
        for (path, descriptor) in &initial {
            cancel.check()?;
            let object = RelPath::new(format!(
                "agent-workspaces/{run_id}/objects/{}",
                descriptor.sha256
            ))?;
            if data.inspect(&object)? == FileSnapshot::Missing {
                let file = work.read_checked(&RelPath::new(path)?, MAX_ASSET)?;
                if file.snapshot.descriptor() != Some(descriptor) {
                    return Err(
                        YssError::new(ErrorCode::ConcurrentChange, "初始快照期间文件改变").at(path),
                    );
                }
                data.create_file(&object, &file.bytes, 0o600)?;
            }
        }
        workspaces.push(Workspace {
            project_ids: group.iter().map(|(id, _)| id.clone()).collect(),
            project_prefixes: prefixes,
            original_root: original.to_string_lossy().into(),
            original_identity: root.identity,
            worktree_root: target_text.into(),
            worktree_identity: work.identity,
            branch,
            head,
            initial,
            resource_sources,
            result_snapshot: None,
        });
    }
    Ok(workspaces)
}
pub fn working_snapshot(workspace: &Workspace, cancel: &Cancellation) -> Result<Snapshot> {
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
    referenced_files(&root, &workspace.resource_sources, &mut result, cancel)?;
    validate_snapshot(&result)?;
    Ok(result)
}
pub fn validate(workspace: &Workspace) -> Result<()> {
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
