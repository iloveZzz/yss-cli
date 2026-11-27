//! Input and output mapping for a Windows command job.
//!
//! This does not launch commands or apply changes. The caller must register the
//! scratch directory durably outside that directory before copying, retain it on
//! every error, and restore its private ACL before inspecting command output.
//! A recovered record must be revalidated against the current job and grants;
//! it must never be treated as authority to delete a directory or write a file.

use super::workspace::Projects;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, FileSnapshot, Root},
    model::{MAX_ASSET, MAX_MANIFEST, RelPath},
    workbench::agent::ProjectMode,
};
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, path::PathBuf};

// Bounds include ignored files: hitting a bound refuses the command rather than
// creating a deceptively incomplete project. No Git ignore filter is consulted.
const MAX_ENTRIES: usize = 32_768;
const MAX_PATH_BYTES: usize = 2 * 1024 * 1024;
const MAX_TOTAL: u64 = 512 * 1024 * 1024;
const MAX_DEPTH: usize = 64;

fn denied(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message).context("piIssue", "commandsUnavailable")
}

fn changed(message: &str) -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, message)
}

fn opaque_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

/// Credentials and YSS/Git control paths never enter a command scratch tree.
/// Matching is deliberately conservative; excluded entries are recorded and
/// output at those paths is rejected, never interpreted as an original deletion.
fn sensitive(path: &RelPath) -> bool {
    path.as_str().split('/').any(|part| {
        let name = part.to_ascii_lowercase();
        matches!(
            name.as_str(),
            ".git"
                | ".gitmodules"
                | ".yss"
                | ".ssh"
                | ".aws"
                | ".azure"
                | ".gnupg"
                | ".kube"
                | ".pi"
                | ".codex"
                | ".config"
                | ".docker"
                | ".m2"
                | ".npmrc"
                | ".pypirc"
                | ".netrc"
                | ".env"
                | ".git-credentials"
                | "credentials"
                | "credentials.json"
                | "credentials.toml"
        ) || name.starts_with(".env.")
            || name.ends_with(".pem")
            || name.ends_with(".p12")
            || name.ends_with(".pfx")
            || name.ends_with(".key")
    })
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct Binding {
    pub job_id: String,
    pub project_id: String,
    pub mode: ProjectMode,
    pub source_path: PathBuf,
    pub source_identity: FileIdentity,
    pub scratch_path: PathBuf,
    pub scratch_identity: FileIdentity,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct InputEntry {
    pub source: FileSnapshot,
    pub copied: FileSnapshot,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct Manifest {
    pub binding: Binding,
    pub entries: BTreeMap<RelPath, InputEntry>,
    /// Each item is a protected path excluded before opening or reading it.
    pub excluded: Vec<RelPath>,
}

/// Proof that both trees were fully re-enumerated in this process. Construction
/// is private to this module; it is not persistent and does not replace the
/// per-file guards required at launch or writeback.
pub(crate) struct VerifiedManifest<'a> {
    manifest: Manifest,
    // Retain both authorization pins and the scratch handle until proof use.
    // These borrows cannot prevent external changes to file contents.
    _projects: &'a Projects,
    _scratch: &'a Root,
}
impl VerifiedManifest<'_> {
    pub(crate) fn manifest(&self) -> &Manifest {
        &self.manifest
    }

    /// Re-enumerate both trees while retaining the original authorization pins.
    /// Call this immediately before publishing Ready and again before launch.
    /// Filesystem changes can still race the database commit or process start;
    /// callers must therefore keep the candidate disabled until that window is
    /// closed and must retain transaction guards at writeback.
    pub(crate) fn revalidate(&self, cancel: &Cancellation) -> Result<()> {
        revalidate_source(self._projects, &self.manifest, self._scratch, cancel)?;
        #[cfg(windows)]
        self._scratch.verify_private_dir()?;
        let source = self
            ._projects
            .project_root(&self.manifest.binding.project_id)?;
        let scope = ProjectScope {
            projects: self._projects,
            id: &self.manifest.binding.project_id,
            root: source,
            mode: self.manifest.binding.mode,
        };
        verify_copy(&scope, &self.manifest, self._scratch, cancel)
    }
}

/// A proposed change is only a review input. The consumer must re-check the
/// project grant, Git baseline, source snapshot, and copied file snapshot under
/// the existing transaction lock immediately before any writeback.
pub(crate) enum Change {
    CreateDir {
        path: RelPath,
    },
    CreateFile {
        path: RelPath,
        output: FileSnapshot,
    },
    ReplaceFile {
        path: RelPath,
        before: FileSnapshot,
        output: FileSnapshot,
    },
    DeleteFile {
        path: RelPath,
        before: FileSnapshot,
    },
}

struct Budget {
    entries: usize,
    path_bytes: usize,
    bytes: u64,
}
impl Budget {
    fn new() -> Self {
        Self {
            entries: 0,
            path_bytes: 0,
            bytes: 0,
        }
    }
    fn item(&mut self, path: &RelPath) -> Result<()> {
        self.entries = self
            .entries
            .checked_add(1)
            .ok_or_else(|| denied("工程条目数量溢出"))?;
        self.path_bytes = self
            .path_bytes
            .checked_add(path.as_str().len())
            .ok_or_else(|| denied("工程路径长度溢出"))?;
        if self.entries > MAX_ENTRIES || self.path_bytes > MAX_PATH_BYTES {
            return Err(denied("工程内容超过隔离映射上限；未运行命令").at(path));
        }
        Ok(())
    }
    fn bytes(&mut self, size: u64, path: &RelPath) -> Result<()> {
        self.bytes = self
            .bytes
            .checked_add(size)
            .ok_or_else(|| denied("工程内容大小溢出"))?;
        if self.bytes > MAX_TOTAL {
            return Err(denied("工程内容超过隔离映射大小上限；未运行命令").at(path));
        }
        Ok(())
    }
}

trait Scope {
    fn source(&self) -> &Root;
    fn project_id(&self) -> &str;
    fn mode(&self) -> ProjectMode;
    fn allows(&self, path: &RelPath) -> Result<bool>;
    fn validate(&self, cancel: &Cancellation) -> Result<()>;
}

struct ProjectScope<'a> {
    projects: &'a Projects,
    id: &'a str,
    root: &'a Root,
    mode: ProjectMode,
}
impl Scope for ProjectScope<'_> {
    fn source(&self) -> &Root {
        self.root
    }
    fn project_id(&self) -> &str {
        self.id
    }
    fn mode(&self) -> ProjectMode {
        self.mode
    }
    fn allows(&self, path: &RelPath) -> Result<bool> {
        if !self.projects.includes(self.id, path) {
            return Ok(false);
        }
        let location = self.projects.locate(Some(self.id), path.as_str())?;
        Ok(location.project_id == self.id && location.mode() == self.mode)
    }
    fn validate(&self, cancel: &Cancellation) -> Result<()> {
        self.root.revalidate()?;
        self.projects.check_baselines(cancel)
    }
}

fn path_for(base: Option<&RelPath>, name: &str) -> Result<RelPath> {
    match base {
        Some(base) => base.join(name),
        None => RelPath::new(name),
    }
}

#[cfg(windows)]
fn same_or_within(path: &str, prefix: &str) -> Result<bool> {
    crate::fs::windows_path_under(path, prefix).ok_or_else(|| denied("无法核对 Windows 路径别名"))
}

#[cfg(not(windows))]
fn same_or_within(path: &str, prefix: &str) -> Result<bool> {
    let path = crate::fs::alias_key(path);
    let prefix = crate::fs::alias_key(prefix);
    Ok(path == prefix
        || path
            .strip_prefix(&prefix)
            .is_some_and(|rest| rest.starts_with('/')))
}

fn list_unique(
    root: &Root,
    base: Option<&RelPath>,
    remaining: usize,
    cancel: &Cancellation,
) -> Result<Vec<String>> {
    // Read at most one entry past the remaining allowance, including ignored
    // entries. `Root::list` is intentionally not used for an untrusted tree.
    let mut limit = remaining.saturating_add(1);
    let (entries, complete) = root.source_entries(base, &mut limit, cancel)?;
    if !complete || entries.len() > remaining {
        return Err(denied("目录条目超过隔离映射上限"));
    }
    let names = entries
        .into_iter()
        .map(|(name, _)| name)
        .collect::<Vec<_>>();
    #[cfg(windows)]
    {
        use std::{cell::Cell, cmp::Ordering};
        use windows_sys::Win32::Globalization::{
            CSTR_EQUAL, CSTR_GREATER_THAN, CSTR_LESS_THAN, CompareStringOrdinal,
        };
        let failed = Cell::new(false);
        let mut ordered = names.clone();
        ordered.sort_by(|left, right| {
            let left: Vec<u16> = left.encode_utf16().collect();
            let right: Vec<u16> = right.encode_utf16().collect();
            let (Ok(left_len), Ok(right_len)) =
                (i32::try_from(left.len()), i32::try_from(right.len()))
            else {
                failed.set(true);
                return Ordering::Equal;
            };
            match unsafe {
                CompareStringOrdinal(left.as_ptr(), left_len, right.as_ptr(), right_len, 1)
            } {
                CSTR_LESS_THAN => Ordering::Less,
                CSTR_EQUAL => Ordering::Equal,
                CSTR_GREATER_THAN => Ordering::Greater,
                _ => {
                    failed.set(true);
                    Ordering::Equal
                }
            }
        });
        if failed.get() {
            return Err(denied("无法核对 Windows 路径别名"));
        }
        for pair in ordered.windows(2) {
            if same_or_within(&pair[0], &pair[1])? && same_or_within(&pair[1], &pair[0])? {
                return Err(denied("目录包含 Windows 大小写或路径别名冲突"));
            }
        }
    }
    #[cfg(not(windows))]
    {
        let mut keys = std::collections::BTreeSet::new();
        if names
            .iter()
            .any(|name| !keys.insert(crate::fs::alias_key(name)))
        {
            return Err(denied("目录包含路径别名冲突"));
        }
    }
    Ok(names)
}

fn disjoint(source: &Root, scratch: &Root) -> Result<()> {
    #[cfg(windows)]
    let overlap = source.relative_windows_path(&scratch.path)?.is_some()
        || scratch.relative_windows_path(&source.path)?.is_some();
    #[cfg(not(windows))]
    let overlap = source.path.starts_with(&scratch.path) || scratch.path.starts_with(&source.path);
    if overlap {
        return Err(denied("隔离目录不能位于工程或其祖先中"));
    }
    Ok(())
}

fn reject_case_renames<'a>(paths: impl IntoIterator<Item = &'a RelPath>) -> Result<()> {
    let mut names = paths.into_iter().map(RelPath::as_str).collect::<Vec<_>>();
    #[cfg(windows)]
    {
        use std::{cell::Cell, cmp::Ordering};
        use windows_sys::Win32::Globalization::{
            CSTR_EQUAL, CSTR_GREATER_THAN, CSTR_LESS_THAN, CompareStringOrdinal,
        };
        let failed = Cell::new(false);
        names.sort_by(|left, right| {
            let left: Vec<u16> = left.encode_utf16().collect();
            let right: Vec<u16> = right.encode_utf16().collect();
            let (Ok(left_len), Ok(right_len)) =
                (i32::try_from(left.len()), i32::try_from(right.len()))
            else {
                failed.set(true);
                return Ordering::Equal;
            };
            match unsafe {
                CompareStringOrdinal(left.as_ptr(), left_len, right.as_ptr(), right_len, 1)
            } {
                CSTR_LESS_THAN => Ordering::Less,
                CSTR_EQUAL => Ordering::Equal,
                CSTR_GREATER_THAN => Ordering::Greater,
                _ => {
                    failed.set(true);
                    Ordering::Equal
                }
            }
        });
        if failed.get() {
            return Err(denied("无法核对 Windows 路径别名"));
        }
    }
    #[cfg(not(windows))]
    names.sort_by_key(|name| crate::fs::alias_key(name));
    for pair in names.windows(2) {
        if pair[0] != pair[1]
            && same_or_within(pair[0], pair[1])?
            && same_or_within(pair[1], pair[0])?
        {
            return Err(denied("命令输出包含仅大小写不同的路径；暂存内容已保留"));
        }
    }
    Ok(())
}

/// The caller must durably register the scratch identity before preparing it
/// and keep its directory/ancestor pins throughout execution.
pub(super) fn prepare(
    projects: &Projects,
    project_id: &str,
    job_id: &str,
    scratch: &Root,
    cancel: &Cancellation,
) -> Result<Manifest> {
    if !opaque_id(job_id) {
        return Err(denied("隔离作业标识无效"));
    }
    let location = projects.locate(Some(project_id), ".")?;
    if location.project_id != project_id {
        return Err(denied("工程未获本轮授权"));
    }
    let root = projects.project_root(project_id)?;
    let scope = ProjectScope {
        projects,
        id: project_id,
        root,
        mode: location.mode(),
    };
    prepare_scope(&scope, job_id, scratch, cancel)
}

fn prepare_scope(
    scope: &impl Scope,
    job_id: &str,
    scratch: &Root,
    cancel: &Cancellation,
) -> Result<Manifest> {
    cancel.check()?;
    if !scratch
        .path
        .file_name()
        .and_then(|name| name.to_str())
        .is_some_and(opaque_id)
    {
        return Err(denied("隔离目录名称无效；原内容已保留"));
    }
    scope.validate(cancel)?;
    scratch.revalidate()?;
    #[cfg(windows)]
    scratch.verify_private_dir()?;
    disjoint(scope.source(), scratch)?;
    if !list_unique(scratch, None, 0, cancel)?.is_empty() {
        return Err(denied("隔离目录并非空目录，原内容已保留"));
    }
    let binding = Binding {
        job_id: job_id.to_owned(),
        project_id: scope.project_id().to_owned(),
        mode: scope.mode(),
        source_path: scope.source().path.clone(),
        source_identity: scope.source().identity.clone(),
        scratch_path: scratch.path.clone(),
        scratch_identity: scratch.identity.clone(),
    };
    let mut manifest = Manifest {
        binding,
        entries: BTreeMap::new(),
        excluded: Vec::new(),
    };
    let mut budget = Budget::new();
    copy_directory(scope, scratch, None, 0, &mut budget, &mut manifest, cancel)?;
    verify_source(scope, &manifest, cancel)?;
    verify_copy(scope, &manifest, scratch, cancel)?;
    scope.validate(cancel)?;
    scratch.revalidate()?;
    // Store implementations have bounded records; fail before claiming Prepared.
    if serde_json::to_vec(&manifest)
        .map_err(|_| denied("无法编码隔离映射"))?
        .len() as u64
        > MAX_MANIFEST
    {
        return Err(denied("隔离映射记录超过上限；暂存内容已保留"));
    }
    Ok(manifest)
}

/// Recheck the entire allowed source, including ignored files, immediately
/// before command launch. Callers must still use source snapshots as transaction
/// guards at writeback because the user can edit the project afterwards.
pub(super) fn revalidate_source(
    projects: &Projects,
    manifest: &Manifest,
    scratch: &Root,
    cancel: &Cancellation,
) -> Result<()> {
    let location = projects.locate(Some(&manifest.binding.project_id), ".")?;
    if location.project_id != manifest.binding.project_id
        || location.mode() != manifest.binding.mode
    {
        return Err(denied("工程离线命令授权已变化"));
    }
    let root = projects.project_root(&manifest.binding.project_id)?;
    if root.identity != manifest.binding.source_identity
        || root.path != manifest.binding.source_path
        || scratch.identity != manifest.binding.scratch_identity
        || scratch.path != manifest.binding.scratch_path
    {
        return Err(changed("工程或隔离目录身份已变化；暂存内容已保留"));
    }
    let scope = ProjectScope {
        projects,
        id: &manifest.binding.project_id,
        root,
        mode: manifest.binding.mode,
    };
    verify_source(&scope, manifest, cancel)
}

/// Validate both complete trees before marking the durable record Ready.
/// It is not valid after the command has modified scratch.
pub(super) fn revalidate_initial<'a>(
    projects: &'a Projects,
    manifest: &Manifest,
    scratch: &'a Root,
    cancel: &Cancellation,
) -> Result<VerifiedManifest<'a>> {
    revalidate_source(projects, manifest, scratch, cancel)?;
    #[cfg(windows)]
    scratch.verify_private_dir()?;
    let source = projects.project_root(&manifest.binding.project_id)?;
    let scope = ProjectScope {
        projects,
        id: &manifest.binding.project_id,
        root: source,
        mode: manifest.binding.mode,
    };
    verify_copy(&scope, manifest, scratch, cancel)?;
    Ok(VerifiedManifest {
        manifest: manifest.clone(),
        _projects: projects,
        _scratch: scratch,
    })
}

fn verify_copy(
    scope: &impl Scope,
    manifest: &Manifest,
    scratch: &Root,
    cancel: &Cancellation,
) -> Result<()> {
    let mut output = BTreeMap::new();
    collect_output(
        scope,
        scratch,
        manifest,
        None,
        0,
        &mut Budget::new(),
        &mut output,
        cancel,
    )?;
    if output.len() != manifest.entries.len()
        || output.iter().any(|(path, snapshot)| {
            manifest
                .entries
                .get(path)
                .is_none_or(|entry| &entry.copied != snapshot)
        })
    {
        return Err(changed("隔离目录内容已变化；暂存内容已保留"));
    }
    scratch.revalidate()
}

fn verify_source(scope: &impl Scope, manifest: &Manifest, cancel: &Cancellation) -> Result<()> {
    scope.validate(cancel)?;
    let mut found = BTreeMap::new();
    let mut excluded = Vec::new();
    let mut budget = Budget::new();
    collect_source(
        scope,
        None,
        0,
        &mut budget,
        &mut found,
        &mut excluded,
        cancel,
    )?;
    if found.len() != manifest.entries.len()
        || excluded != manifest.excluded
        || found.iter().any(|(path, snapshot)| {
            manifest
                .entries
                .get(path)
                .is_none_or(|entry| &entry.source != snapshot)
        })
    {
        return Err(changed("工程文件或排除范围已变化；暂存内容已保留"));
    }
    scope.validate(cancel)
}

fn collect_source(
    scope: &impl Scope,
    base: Option<&RelPath>,
    depth: usize,
    budget: &mut Budget,
    found: &mut BTreeMap<RelPath, FileSnapshot>,
    excluded: &mut Vec<RelPath>,
    cancel: &Cancellation,
) -> Result<()> {
    if depth > MAX_DEPTH {
        return Err(denied("工程目录层级超过隔离映射上限"));
    }
    let names = list_unique(
        scope.source(),
        base,
        MAX_ENTRIES.saturating_sub(budget.entries),
        cancel,
    )?;
    for name in &names {
        cancel.check()?;
        let path = path_for(base, name)?;
        budget.item(&path)?;
        if sensitive(&path) || !scope.allows(&path)? {
            excluded.push(path);
            continue;
        }
        let metadata = scope.source().source_metadata(&path)?;
        if metadata.is_dir() {
            let snapshot = scope.source().inspect(&path)?;
            if !matches!(snapshot, FileSnapshot::Directory { .. }) {
                return Err(changed("工程目录已变化").at(path));
            }
            found.insert(path.clone(), snapshot.clone());
            collect_source(
                scope,
                Some(&path),
                depth + 1,
                budget,
                found,
                excluded,
                cancel,
            )?;
            if scope.source().inspect(&path)? != snapshot {
                return Err(changed("核对时工程目录已变化").at(path));
            }
        } else if metadata.is_file() {
            let file = scope.source().read_checked_single_link(&path, MAX_ASSET)?;
            budget.bytes(file.bytes.len() as u64, &path)?;
            found.insert(path, file.snapshot);
        } else {
            return Err(denied("工程包含链接或特殊文件，无法安全映射").at(path));
        }
    }
    if list_unique(scope.source(), base, MAX_ENTRIES, cancel)? != names {
        return Err(changed("核对时工程目录内容已变化"));
    }
    Ok(())
}

fn copy_directory(
    scope: &impl Scope,
    scratch: &Root,
    base: Option<&RelPath>,
    depth: usize,
    budget: &mut Budget,
    manifest: &mut Manifest,
    cancel: &Cancellation,
) -> Result<()> {
    if depth > MAX_DEPTH {
        return Err(denied("工程目录层级超过隔离映射上限"));
    }
    cancel.check()?;
    let names = list_unique(
        scope.source(),
        base,
        MAX_ENTRIES.saturating_sub(budget.entries),
        cancel,
    )?;
    if names.len().saturating_add(budget.entries) > MAX_ENTRIES {
        return Err(denied("工程条目数量超过隔离映射上限"));
    }
    for name in &names {
        cancel.check()?;
        let path = path_for(base, name)?;
        budget.item(&path)?;
        if sensitive(&path) || !scope.allows(&path)? {
            manifest.excluded.push(path);
            continue;
        }
        // Metadata does not read file contents; single-link is checked on the
        // same handle before any bytes are read below.
        let metadata = scope.source().source_metadata(&path)?;
        if metadata.is_dir() {
            let before = scope.source().inspect(&path)?;
            if !matches!(before, FileSnapshot::Directory { .. }) {
                return Err(changed("工程目录已变化").at(&path));
            }
            let copied = FileSnapshot::Directory {
                identity: scratch.create_dir(&path)?,
            };
            manifest.entries.insert(
                path.clone(),
                InputEntry {
                    source: before.clone(),
                    copied,
                },
            );
            copy_directory(
                scope,
                scratch,
                Some(&path),
                depth + 1,
                budget,
                manifest,
                cancel,
            )?;
            if scope.source().inspect(&path)? != before {
                return Err(changed("复制时工程目录已变化").at(path));
            }
        } else if metadata.is_file() {
            let input = scope.source().read_checked_single_link(&path, MAX_ASSET)?;
            budget.bytes(input.bytes.len() as u64, &path)?;
            let mode = input
                .snapshot
                .descriptor()
                .ok_or_else(|| denied("工程文件状态无效"))?
                .mode;
            let copied = scratch.create_file(&path, &input.bytes, mode)?;
            if scope
                .source()
                .read_checked_single_link(&path, MAX_ASSET)?
                .snapshot
                != input.snapshot
            {
                return Err(changed("复制时工程文件已变化").at(path));
            }
            manifest.entries.insert(
                path,
                InputEntry {
                    source: input.snapshot,
                    copied,
                },
            );
        } else {
            return Err(denied("工程包含链接或特殊文件，无法安全映射").at(path));
        }
    }
    if list_unique(scope.source(), base, MAX_ENTRIES, cancel)? != names {
        return Err(changed("复制时工程目录内容已变化"));
    }
    Ok(())
}

/// Verify the result after the complete process tree has stopped and the scratch
/// ACL has been restored. No original files are changed and scratch is retained.
pub(super) fn diff(
    projects: &Projects,
    manifest: &Manifest,
    scratch: &Root,
    cancel: &Cancellation,
) -> Result<Vec<Change>> {
    let location = projects.locate(Some(&manifest.binding.project_id), ".")?;
    if location.project_id != manifest.binding.project_id
        || manifest.binding.mode != ProjectMode::Execute
        || location.mode() != manifest.binding.mode
    {
        return Err(denied("工程离线命令授权已变化"));
    }
    let source = projects.project_root(&manifest.binding.project_id)?;
    if source.identity != manifest.binding.source_identity
        || source.path != manifest.binding.source_path
        || scratch.identity != manifest.binding.scratch_identity
        || scratch.path != manifest.binding.scratch_path
    {
        return Err(changed("工程或隔离目录身份已变化；暂存内容已保留"));
    }
    #[cfg(windows)]
    scratch.verify_private_dir()?;
    let scope = ProjectScope {
        projects,
        id: &manifest.binding.project_id,
        root: source,
        mode: manifest.binding.mode,
    };
    verify_source(&scope, manifest, cancel)?;
    let mut output = BTreeMap::new();
    let mut budget = Budget::new();
    collect_output(
        &scope,
        scratch,
        manifest,
        None,
        0,
        &mut budget,
        &mut output,
        cancel,
    )?;
    reject_case_renames(manifest.entries.keys().chain(output.keys()))?;
    scope.validate(cancel)?;
    scratch.revalidate()?;
    let mut dirs = Vec::new();
    let mut files = Vec::new();
    for (path, after) in &output {
        match (manifest.entries.get(path).map(|entry| &entry.source), after) {
            (None, FileSnapshot::Directory { .. }) => {
                dirs.push(Change::CreateDir { path: path.clone() })
            }
            (None, FileSnapshot::File { .. }) => files.push(Change::CreateFile {
                path: path.clone(),
                output: after.clone(),
            }),
            (Some(FileSnapshot::Directory { .. }), FileSnapshot::Directory { .. }) => {}
            (Some(before @ FileSnapshot::File { .. }), FileSnapshot::File { .. }) => {
                if before.descriptor() != after.descriptor() {
                    files.push(Change::ReplaceFile {
                        path: path.clone(),
                        before: before.clone(),
                        output: after.clone(),
                    });
                }
            }
            _ => return Err(denied("命令改变了目录与文件的类型；暂存内容已保留").at(path)),
        }
    }
    for (path, entry) in &manifest.entries {
        if output.contains_key(path) {
            continue;
        }
        match &entry.source {
            FileSnapshot::File { .. } => files.push(Change::DeleteFile {
                path: path.clone(),
                before: entry.source.clone(),
            }),
            FileSnapshot::Directory { .. } => {
                return Err(denied("命令删除了原目录，暂不支持安全写回；暂存内容已保留").at(path));
            }
            _ => return Err(denied("隔离映射记录无效")),
        }
    }
    dirs.extend(files);
    Ok(dirs)
}

fn collect_output(
    scope: &impl Scope,
    scratch: &Root,
    manifest: &Manifest,
    base: Option<&RelPath>,
    depth: usize,
    budget: &mut Budget,
    output: &mut BTreeMap<RelPath, FileSnapshot>,
    cancel: &Cancellation,
) -> Result<()> {
    if depth > MAX_DEPTH {
        return Err(denied("命令输出目录层级超过上限"));
    }
    let names = list_unique(
        scratch,
        base,
        MAX_ENTRIES.saturating_sub(budget.entries),
        cancel,
    )?;
    if names.len().saturating_add(budget.entries) > MAX_ENTRIES {
        return Err(denied("命令输出条目超过上限"));
    }
    for name in &names {
        cancel.check()?;
        let path = path_for(base, name)?;
        budget.item(&path)?;
        if sensitive(&path)
            || !scope.allows(&path)?
            || manifest
                .excluded
                .iter()
                .any(|excluded| same_or_within(path.as_str(), excluded.as_str()).unwrap_or(true))
        {
            return Err(denied("命令输出包含未授权或受保护路径；暂存内容已保留").at(path));
        }
        let metadata = scratch.source_metadata(&path)?;
        if metadata.is_dir() {
            let before = scratch.inspect(&path)?;
            if !matches!(before, FileSnapshot::Directory { .. }) {
                return Err(changed("命令输出目录已变化").at(path));
            }
            output.insert(path.clone(), before.clone());
            collect_output(
                scope,
                scratch,
                manifest,
                Some(&path),
                depth + 1,
                budget,
                output,
                cancel,
            )?;
            if scratch.inspect(&path)? != before {
                return Err(changed("读取时命令输出目录已变化").at(path));
            }
        } else if metadata.is_file() {
            let file = scratch.read_checked_single_link(&path, MAX_ASSET)?;
            budget.bytes(file.bytes.len() as u64, &path)?;
            output.insert(path, file.snapshot);
        } else {
            return Err(denied("命令输出包含链接或特殊文件；暂存内容已保留").at(path));
        }
    }
    if list_unique(scratch, base, MAX_ENTRIES, cancel)? != names {
        return Err(changed("读取时命令输出目录已变化"));
    }
    Ok(())
}
