use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root, alias_key, git},
    model::{FileDescriptor, RelPath, RenderedFile, Sha256},
    transaction::{self, Operation},
    workbench::agent_workspace::{self, GitBaseline, Workspace},
};
use cap_std::fs::MetadataExt;
use std::{
    cell::RefCell,
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
};

const MAX_FILE: u64 = 512 * 1024;
const MAX_READ: usize = 128 * 1024;

pub(super) struct Projects {
    repositories: Vec<Repository>,
    projects: BTreeMap<String, Project>,
    primary: String,
    protected: BTreeSet<PathBuf>,
    observed: RefCell<BTreeMap<PathBuf, FileDescriptor>>,
}

struct Repository {
    path: PathBuf,
    baseline: GitBaseline,
    recorded_paths: BTreeSet<String>,
}

struct Project {
    root: Root,
    repository: usize,
    blocked: BTreeSet<String>,
}

pub(super) struct Location<'a> {
    project: &'a Project,
    repository: &'a Repository,
    path: Option<RelPath>,
    observed: &'a RefCell<BTreeMap<PathBuf, FileDescriptor>>,
}

fn unsafe_path(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsafePath, message)
}

fn outside_scope() -> YssError {
    unsafe_path("路径不在本轮所选工程内").context("piErrorCode", "path_outside_scope")
}

fn missing_path() -> YssError {
    YssError::new(ErrorCode::Io, "所选工程内没有该文件或目录")
        .context("piErrorCode", "missing_path")
}

fn missing(error: &YssError) -> bool {
    error
        .context
        .get("kind")
        .is_some_and(|kind| kind == "NotFound")
}

pub(super) fn tool_path(path: &RelPath) -> bool {
    !path.as_str().split('/').any(|part| {
        [".git", ".gitmodules", ".yss"]
            .iter()
            .any(|name| part.eq_ignore_ascii_case(name))
    })
}

fn relative_input(path: &str) -> Result<Option<RelPath>> {
    let path = path.strip_prefix("./").unwrap_or(path);
    let path = path.strip_suffix('/').unwrap_or(path);
    if path.is_empty() || path == "." {
        return Ok(None);
    }
    if path.split('/').any(|part| part == "..") {
        return Err(outside_scope());
    }
    RelPath::new(path)
        .map(Some)
        .map_err(|error| error.context("piErrorCode", "invalid_path"))
}

fn absolute_suffix<'a>(root: &Path, input: &'a str) -> Option<&'a str> {
    let rest = input.strip_prefix(root.to_str()?)?;
    if rest.is_empty() {
        Some(rest)
    } else {
        rest.strip_prefix('/')
    }
}

impl Project {
    fn includes(&self, path: &RelPath) -> bool {
        let key = alias_key(path.as_str());
        tool_path(path)
            && !self.blocked.iter().any(|p| {
                key == *p
                    || key
                        .strip_prefix(p)
                        .is_some_and(|rest| rest.starts_with('/'))
            })
    }
}

impl Projects {
    pub fn review_location(&self, target: &Location<'_>) -> Option<(String, String)> {
        let absolute = target.display();
        let (path, id, _) = self.absolute_project(absolute.to_str()?)?;
        (!path.is_empty()).then(|| (id.to_owned(), path.to_owned()))
    }

    pub fn new(
        workspaces: &[Workspace],
        primary: &str,
        data_dir: &Path,
        pi_config: &Path,
    ) -> Result<Self> {
        let mut repositories = Vec::new();
        let mut projects = BTreeMap::new();
        let mut protected = BTreeSet::from([data_dir.to_owned(), pi_config.to_owned()]);
        let mut excluded = BTreeSet::new();
        for workspace in workspaces {
            let direct = workspace
                .direct
                .as_ref()
                .ok_or_else(|| YssError::new(ErrorCode::NotReady, "请从原 Spec 发起直接运行"))?;
            let git_root = Path::new(&workspace.original_root);
            protected.extend([
                PathBuf::from(&direct.baseline.git_dir),
                PathBuf::from(&direct.baseline.common_dir),
            ]);
            excluded.extend(
                direct
                    .excluded_prefixes
                    .iter()
                    .map(|path| git_root.join(path)),
            );
            for (id, prefix) in &workspace.project_prefixes {
                let root = Root::open(&git_root.join(prefix))?;
                if direct.project_identities.get(id) != Some(&root.identity) {
                    return Err(YssError::new(
                        ErrorCode::ConcurrentChange,
                        "Pi 工程目录身份已改变",
                    ));
                }
                projects.insert(
                    id.clone(),
                    Project {
                        root,
                        repository: repositories.len(),
                        blocked: BTreeSet::new(),
                    },
                );
            }
            repositories.push(Repository {
                path: git_root.to_owned(),
                baseline: direct.baseline.clone(),
                recorded_paths: workspace.initial.keys().cloned().collect(),
            });
        }
        for project in projects.values_mut() {
            if protected
                .iter()
                .any(|path| project.root.path.starts_with(path))
            {
                return Err(unsafe_path("Pi 工程与受保护目录重叠"));
            }
            project.blocked.extend(
                protected
                    .iter()
                    .chain(&excluded)
                    .filter_map(|path| path.strip_prefix(&project.root.path).ok())
                    .filter(|path| !path.as_os_str().is_empty())
                    .filter_map(Path::to_str)
                    .map(alias_key),
            );
        }
        if !projects.contains_key(primary) {
            return Err(outside_scope());
        }
        protected.extend(excluded);
        Ok(Self {
            repositories,
            projects,
            primary: primary.to_owned(),
            protected,
            observed: RefCell::new(BTreeMap::new()),
        })
    }

    pub fn roots(&self) -> impl Iterator<Item = &Root> {
        self.projects.values().map(|project| &project.root)
    }

    pub fn repository_paths(&self) -> impl Iterator<Item = &Path> {
        self.repositories.iter().map(|repo| repo.path.as_path())
    }

    pub fn project_root(&self, id: &str) -> Result<&Root> {
        let root = &self.projects.get(id).ok_or_else(outside_scope)?.root;
        root.revalidate()?;
        Ok(root)
    }

    pub fn includes(&self, id: &str, path: &RelPath) -> bool {
        self.projects
            .get(id)
            .is_some_and(|project| project.includes(path))
    }

    pub fn check_baselines(&self, cancel: &Cancellation) -> Result<()> {
        for repository in &self.repositories {
            agent_workspace::check_baseline(&repository.path, &repository.baseline, cancel)?;
        }
        Ok(())
    }

    pub fn protected(&self) -> &BTreeSet<PathBuf> {
        &self.protected
    }

    fn absolute_project<'a, 'b>(
        &'a self,
        path: &'b str,
    ) -> Option<(&'b str, &'a str, &'a Project)> {
        self.projects
            .iter()
            .filter_map(|(id, project)| {
                let rest = absolute_suffix(&project.root.path, path)?;
                Some((rest, id.as_str(), project))
            })
            .min_by_key(|(rest, _, _)| rest.len())
    }

    pub fn locate(&self, project: Option<&str>, path: &str) -> Result<Location<'_>> {
        let absolute = Path::new(path).is_absolute();
        let (project, relative) = if absolute && project.is_none() {
            let (relative, _, project) = self.absolute_project(path).ok_or_else(outside_scope)?;
            (project, relative)
        } else {
            let project = self
                .projects
                .get(project.unwrap_or(&self.primary))
                .ok_or_else(outside_scope)?;
            let relative = if absolute {
                absolute_suffix(&project.root.path, path).ok_or_else(outside_scope)?
            } else {
                path
            };
            (project, relative)
        };
        let path = relative_input(relative)?;
        if let Some(path) = &path {
            if !tool_path(path) {
                return Err(unsafe_path("Pi 工具不能访问 Git 或 YSS 控制文件"));
            }
            if !project.includes(path) {
                return Err(outside_scope());
            }
        }
        project.root.revalidate()?;
        Ok(Location {
            project,
            repository: &self.repositories[project.repository],
            path,
            observed: &self.observed,
        })
    }

    pub fn error_display_path(&self, project: Option<&str>, path: &str) -> String {
        if path.len() > 4096 {
            return "路径未显示".into();
        }
        let relative = if Path::new(path).is_absolute() {
            match project {
                Some(id) => self
                    .projects
                    .get(id)
                    .and_then(|project| absolute_suffix(&project.root.path, path)),
                None => self.absolute_project(path).map(|(path, _, _)| path),
            }
        } else {
            Some(path)
        };
        let Some(relative) = relative else {
            return "所选工程之外的绝对路径".into();
        };
        match relative_input(relative) {
            Ok(Some(path)) if tool_path(&path) => bounded(path.as_str(), 256).to_owned(),
            Ok(None) => ".".to_owned(),
            // Retain the useful relative input without displaying control characters, absolute paths,
            // or arbitrary invalid segments.
            _ if relative.split('/').all(|part| {
                part == ".." || part == "." || RelPath::new(part).is_ok_and(|part| tool_path(&part))
            }) =>
            {
                bounded(relative, 256).to_owned()
            }
            _ => "路径未显示".into(),
        }
    }
}

fn bounded(value: &str, limit: usize) -> &str {
    &value[..value.floor_char_boundary(value.len().min(limit))]
}

impl Location<'_> {
    fn file(&self) -> Result<&RelPath> {
        self.path.as_ref().ok_or_else(|| {
            YssError::new(ErrorCode::InvalidInput, "Pi 工具需要指定文件路径")
                .context("piErrorCode", "invalid_path")
        })
    }

    pub fn display(&self) -> PathBuf {
        match &self.path {
            Some(path) => self.project.root.path.join(path.as_str()),
            None => self.project.root.path.clone(),
        }
    }

    pub fn check_baseline(&self, cancel: &Cancellation) -> Result<()> {
        self.project.root.revalidate()?;
        agent_workspace::check_baseline(&self.repository.path, &self.repository.baseline, cancel)
    }

    pub fn directory(&self) -> Result<PathBuf> {
        if let Some(path) = &self.path {
            self.metadata(path)?;
            self.project.root.open_dir(path)?;
        }
        Ok(self.display())
    }

    fn metadata(&self, path: &RelPath) -> Result<cap_std::fs::Metadata> {
        self.project.root.source_metadata(path).map_err(|error| {
            if missing(&error) {
                missing_path().at(path)
            } else {
                error
            }
        })
    }

    fn read_text(&self, path: &RelPath) -> Result<(String, FileSnapshot)> {
        let metadata = self.metadata(path)?;
        if !metadata.is_file() || metadata.nlink() != 1 {
            return Err(unsafe_path("Pi 文件工具仅处理无硬链接的普通 UTF-8 文本文件").at(path));
        }
        let checked = self.project.root.read_checked(path, MAX_FILE)?;
        if self.metadata(path)?.nlink() != 1 {
            return Err(unsafe_path("Pi 文件工具不能读取硬链接").at(path));
        }
        if checked.bytes.contains(&0) {
            return Err(
                YssError::new(ErrorCode::UnsupportedFormat, "Pi 文件含二进制内容").at(path),
            );
        }
        let text = String::from_utf8(checked.bytes)
            .map_err(|_| unsafe_path("Pi 文件不是 UTF-8 文本").at(path))?;
        Ok((text, checked.snapshot))
    }

    pub fn read(&self, offset: Option<usize>, limit: Option<usize>) -> Result<String> {
        let path = self.file()?;
        let (text, snapshot) = match self.read_text(path) {
            Err(error)
                if error
                    .context
                    .get("piErrorCode")
                    .is_some_and(|code| code == "missing_path") =>
            {
                self.observed.borrow_mut().remove(&self.display());
                return Err(error);
            }
            result => result?,
        };
        if let Some(descriptor) = snapshot.descriptor() {
            self.observed
                .borrow_mut()
                .insert(self.display(), descriptor.clone());
        }
        let start = offset.unwrap_or(1).saturating_sub(1);
        let mut lines = text.split_inclusive('\n').skip(start);
        let mut result = String::new();
        for line in (&mut lines).take(limit.unwrap_or(2000).clamp(1, 2000)) {
            if result.len() + line.len() > MAX_READ {
                result.push_str(bounded(line, MAX_READ.saturating_sub(result.len())));
                result.push_str("\n[达到读取长度上限]");
                return Ok(result);
            }
            result.push_str(line);
        }
        if lines.next().is_some() {
            let next = start + result.lines().count() + 1;
            result.push_str(&format!("\n[后续内容从第 {next} 行继续读取]"));
        }
        Ok(result)
    }

    pub fn list(&self, cancel: &Cancellation) -> Result<String> {
        if let Some(path) = &self.path
            && !self.metadata(path)?.is_dir()
        {
            return Err(YssError::new(ErrorCode::InvalidInput, "列出目录需要目录路径").at(path));
        }
        let mut budget = 500;
        let (entries, complete) =
            self.project
                .root
                .source_entries(self.path.as_ref(), &mut budget, cancel)?;
        let mut result = String::new();
        for (name, metadata) in entries {
            let Ok(path) = self.child_path(self.path.as_ref(), &name) else {
                continue;
            };
            if self.project.includes(&path) && !metadata.file_type().is_symlink() {
                result.push_str(&name);
                result.push_str(if metadata.is_dir() { "/\n" } else { "\n" });
            }
        }
        if !complete {
            result.push_str("[达到列举上限]\n");
        }
        Ok(result)
    }

    fn child_path(&self, directory: Option<&RelPath>, name: &str) -> Result<RelPath> {
        match directory {
            Some(directory) => directory.join(name),
            None => RelPath::new(name),
        }
    }

    pub fn grep(&self, query: &str, cancel: &Cancellation) -> Result<String> {
        if query.is_empty() || query.len() > 1024 {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "请输入 1024 字节以内的查找文本",
            ));
        }
        let mut matches = String::new();
        let mut count = 0;
        let mut search = |path: &RelPath| -> bool {
            if let Ok((text, _)) = self.read_text(path) {
                for (line, text) in text
                    .lines()
                    .enumerate()
                    .filter(|(_, text)| text.contains(query))
                {
                    matches.push_str(&format!(
                        "{}:{}: {}\n",
                        path.as_str(),
                        line + 1,
                        bounded(text, 1024)
                    ));
                    count += 1;
                    if count == 100 || matches.len() >= MAX_READ {
                        matches.push_str("[达到匹配上限]\n");
                        return true;
                    }
                }
            }
            false
        };
        let mut directories = vec![self.path.clone()];
        let mut budget = 4000;
        if let Some(path) = &self.path
            && self.metadata(path)?.is_file()
        {
            search(path);
        } else {
            while let Some(directory) = directories.pop() {
                let entries =
                    match self
                        .project
                        .root
                        .source_entries(directory.as_ref(), &mut budget, cancel)
                    {
                        Ok((entries, _)) => entries,
                        Err(error)
                            if error.code == ErrorCode::UnsafePath && directory != self.path =>
                        {
                            continue;
                        }
                        Err(error) => return Err(error),
                    };
                for (name, metadata) in entries {
                    cancel.check()?;
                    let Ok(relative) = self.child_path(directory.as_ref(), &name) else {
                        continue;
                    };
                    if !self.project.includes(&relative) || metadata.file_type().is_symlink() {
                        continue;
                    }
                    if metadata.is_dir() {
                        if name != "node_modules" {
                            directories.push(Some(relative));
                        }
                    } else if metadata.is_file() && search(&relative) {
                        return Ok(matches);
                    }
                }
                if budget == 0 {
                    matches.push_str("[达到搜索扫描上限，可缩小目录重试]\n");
                    break;
                }
            }
        }
        Ok(matches)
    }

    fn check_review_scope(&self) -> Result<()> {
        let target = self.display();
        let relative = target
            .strip_prefix(&self.repository.path)
            .ok()
            .and_then(Path::to_str)
            .ok_or_else(outside_scope)?;
        if self.repository.recorded_paths.contains(relative) {
            return Ok(());
        }
        let mut input = relative.as_bytes().to_vec();
        input.push(0);
        if !git::output(
            &self.repository.path,
            &["check-ignore", "--stdin", "-z"],
            Some(&input),
            true,
        )?
        .is_empty()
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "此 Git 忽略文件不在本轮可审阅快照中，请修改源码或使用已授权的构建命令",
            )
            .context("piErrorCode", "path_not_reviewable"));
        }
        Ok(())
    }

    fn check_observation(&self, before: &FileSnapshot) -> Result<()> {
        let observations = self.observed.borrow();
        if observations.get(&self.display()) == before.descriptor() {
            return Ok(());
        }
        Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "写入前请重新读取此文件，确认采用原工程的最新内容",
        ))
    }

    pub fn write(&self, text: String, cancel: &Cancellation) -> Result<(&'static str, Sha256)> {
        let path = self.file()?;
        self.check_review_scope()?;
        let before = self.project.root.inspect(path)?;
        if !matches!(before, FileSnapshot::Missing | FileSnapshot::File { .. }) {
            return Err(unsafe_path("Pi 写入目标含受保护对象").at(path));
        }
        self.check_observation(&before)?;
        self.replace(path, before, text, cancel)
    }

    pub fn edit(
        &self,
        old: &str,
        new: &str,
        cancel: &Cancellation,
    ) -> Result<(&'static str, Sha256)> {
        let path = self.file()?;
        self.check_review_scope()?;
        let (text, before) = self.read_text(path)?;
        self.check_observation(&before)?;
        if old.is_empty() || text.match_indices(old).take(2).count() != 1 {
            return Err(YssError::new(ErrorCode::Conflict, "待替换文本须恰好匹配一次").at(path));
        }
        self.replace(path, before, text.replacen(old, new, 1), cancel)
    }

    fn replace(
        &self,
        path: &RelPath,
        before: FileSnapshot,
        text: String,
        cancel: &Cancellation,
    ) -> Result<(&'static str, Sha256)> {
        if text.len() as u64 > MAX_FILE || text.contains('\0') {
            return Err(unsafe_path("Pi 写入内容须为 512 KiB 以内的文本").at(path));
        }
        let mut ops = Vec::new();
        for parent in path.parents() {
            let parent = RelPath::new(parent)?;
            if self.project.root.inspect(&parent)? == FileSnapshot::Missing {
                ops.push(Operation::CreateDir { path: parent });
            }
        }
        let file = RenderedFile {
            bytes: text.into_bytes(),
            mode: before
                .descriptor()
                .map_or(0o644, |descriptor| descriptor.mode),
        };
        let written = FileDescriptor::new(&file.bytes, file.mode);
        let action = match &before {
            FileSnapshot::Missing => {
                ops.push(Operation::Create {
                    path: path.clone(),
                    file,
                });
                "add"
            }
            FileSnapshot::File { .. } if self.project.root.source_metadata(path)?.nlink() == 1 => {
                ops.push(Operation::Replace {
                    path: path.clone(),
                    before: before.clone(),
                    file,
                });
                "update"
            }
            _ => return Err(unsafe_path("Pi 写入目标含受保护对象").at(path)),
        };
        transaction::apply_files_checked(
            &self.project.root.path,
            self.project.root.identity.clone(),
            ops,
            BTreeMap::from([(path.clone(), before)]),
            cancel,
            &|| self.check_baseline(cancel),
        )?;
        let digest = written.sha256.clone();
        self.observed.borrow_mut().insert(self.display(), written);
        Ok((action, digest))
    }
}
