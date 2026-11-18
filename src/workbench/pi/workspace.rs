use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileSnapshot, Root},
    model::{RelPath, RenderedFile},
    transaction::{self, Operation},
    workbench::agent_workspace::Workspace,
};
use cap_std::fs::MetadataExt;
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

const MAX_FILE: u64 = 512 * 1024;
const MAX_READ: usize = 128 * 1024;

pub(super) struct Worktrees {
    roots: Vec<Root>,
    projects: BTreeMap<String, (usize, String)>,
    primary: String,
}

pub(super) struct Location<'a> {
    root: &'a Root,
    path: Option<RelPath>,
}

fn unsafe_path(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsafePath, message)
}

pub(super) fn tool_path(path: &RelPath) -> bool {
    !path.as_str().split('/').any(|part| {
        [".git", ".gitmodules", ".yss"]
            .iter()
            .any(|name| part.eq_ignore_ascii_case(name))
    })
}

impl Worktrees {
    pub fn new(workspaces: &[Workspace], primary: &str) -> Result<Self> {
        let mut roots = Vec::new();
        let mut projects = BTreeMap::new();
        for workspace in workspaces {
            let root = Root::open(Path::new(&workspace.worktree_root))?;
            if root.identity != workspace.worktree_identity {
                return Err(unsafe_path("Pi worktree 身份已改变"));
            }
            for (id, prefix) in &workspace.project_prefixes {
                projects.insert(id.clone(), (roots.len(), prefix.clone()));
            }
            roots.push(root);
        }
        if !projects.contains_key(primary) {
            return Err(unsafe_path("Pi 缺少主 Spec 对应的隔离工程"));
        }
        Ok(Self {
            roots,
            projects,
            primary: primary.to_owned(),
        })
    }

    pub fn locate(&self, project: Option<&str>, path: &str) -> Result<Location<'_>> {
        let location = Path::new(path);
        let (root, relative) = match (location.is_absolute(), project) {
            (true, None) => self
                .roots
                .iter()
                .find_map(|root| {
                    let relative = path.strip_prefix(root.path.to_str()?)?;
                    (relative.is_empty() || relative.starts_with('/')).then(|| {
                        (
                            root,
                            relative.strip_prefix('/').unwrap_or(relative).to_owned(),
                        )
                    })
                })
                .ok_or_else(|| unsafe_path("Pi 路径不在本次隔离工程内"))?,
            (_, project) => {
                let (index, prefix) = self
                    .projects
                    .get(project.unwrap_or(&self.primary))
                    .ok_or_else(|| unsafe_path("Pi 工程未参与本次运行"))?;
                let root = &self.roots[*index];
                let base = root.path.join(prefix);
                let relative = if location.is_absolute() {
                    let base = base
                        .to_str()
                        .ok_or_else(|| unsafe_path("Pi 工程路径不是 UTF-8"))?;
                    let rest = path
                        .strip_prefix(base)
                        .filter(|rest| rest.is_empty() || rest.starts_with('/'))
                        .ok_or_else(|| unsafe_path("Pi 路径不在所选隔离工程内"))?;
                    rest.strip_prefix('/').unwrap_or(rest).to_owned()
                } else {
                    path.to_owned()
                };
                if !relative.is_empty() && relative != "." {
                    RelPath::new(&relative)?;
                }
                let relative = if relative.is_empty() || relative == "." {
                    prefix.clone()
                } else if prefix.is_empty() {
                    relative
                } else {
                    format!("{prefix}/{relative}")
                };
                (root, relative)
            }
        };
        root.revalidate()?;
        let path = if relative.is_empty() || relative == "." {
            None
        } else {
            let relative = RelPath::new(relative)?;
            if !tool_path(&relative) {
                return Err(unsafe_path("Pi 工具不能访问 Git 或 YSS 控制文件").at(relative));
            }
            Some(relative)
        };
        Ok(Location { root, path })
    }
}

fn bounded(value: &str, limit: usize) -> &str {
    &value[..value.floor_char_boundary(value.len().min(limit))]
}

impl Location<'_> {
    fn file(&self) -> Result<&RelPath> {
        self.path
            .as_ref()
            .ok_or_else(|| unsafe_path("Pi 工具需要指定文件路径"))
    }

    pub fn display(&self) -> PathBuf {
        match &self.path {
            Some(path) => self.root.path.join(path.as_str()),
            None => self.root.path.clone(),
        }
    }

    pub fn directory(&self) -> Result<PathBuf> {
        if let Some(path) = &self.path {
            self.root.open_dir(path)?;
        }
        Ok(self.display())
    }

    fn read_text(&self, path: &RelPath) -> Result<(String, FileSnapshot)> {
        let checked = self.root.read_checked(path, MAX_FILE)?;
        if self.root.source_metadata(path)?.nlink() != 1 || checked.bytes.contains(&0) {
            return Err(unsafe_path("Pi 文件工具仅处理普通 UTF-8 文本文件").at(path));
        }
        let text = String::from_utf8(checked.bytes)
            .map_err(|_| unsafe_path("Pi 文件不是 UTF-8 文本").at(path))?;
        Ok((text, checked.snapshot))
    }

    pub fn read(&self, offset: Option<usize>, limit: Option<usize>) -> Result<String> {
        let (text, _) = self.read_text(self.file()?)?;
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
        let mut budget = 500;
        let (entries, complete) =
            self.root
                .source_entries(self.path.as_ref(), &mut budget, cancel)?;
        let mut result = String::new();
        for (name, metadata) in entries {
            let path = RelPath::new(&name)?;
            if tool_path(&path) && !metadata.file_type().is_symlink() {
                result.push_str(&name);
                result.push_str(if metadata.is_dir() { "/\n" } else { "\n" });
            }
        }
        if !complete {
            result.push_str("[达到列举上限]\n");
        }
        Ok(result)
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
            && self.root.source_metadata(path)?.is_file()
        {
            search(path);
        } else {
            while let Some(directory) = directories.pop() {
                let entries =
                    match self
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
                    let relative = match &directory {
                        Some(dir) => RelPath::new(format!("{}/{name}", dir.as_str()))?,
                        None => RelPath::new(&name)?,
                    };
                    if !tool_path(&relative) || metadata.file_type().is_symlink() {
                        continue;
                    }
                    if metadata.is_dir() {
                        directories.push(Some(relative));
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

    pub fn write(&self, text: String, cancel: &Cancellation) -> Result<&'static str> {
        let path = self.file()?;
        let before = self.root.inspect(path)?;
        self.replace(path, before, text, cancel)
    }

    pub fn edit(&self, old: &str, new: &str, cancel: &Cancellation) -> Result<&'static str> {
        let path = self.file()?;
        let (text, before) = self.read_text(path)?;
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
    ) -> Result<&'static str> {
        if text.len() as u64 > MAX_FILE || text.contains('\0') {
            return Err(unsafe_path("Pi 写入内容须为 512 KiB 以内的文本").at(path));
        }
        let mut ops = Vec::new();
        for parent in path.parents() {
            let parent = RelPath::new(parent)?;
            if self.root.inspect(&parent)? == FileSnapshot::Missing {
                ops.push(Operation::CreateDir { path: parent });
            }
        }
        let file = RenderedFile {
            bytes: text.into_bytes(),
            mode: before
                .descriptor()
                .map_or(0o644, |descriptor| descriptor.mode),
        };
        let action = match &before {
            FileSnapshot::Missing => {
                ops.push(Operation::Create {
                    path: path.clone(),
                    file,
                });
                "add"
            }
            FileSnapshot::File { .. } if self.root.source_metadata(path)?.nlink() == 1 => {
                ops.push(Operation::Replace {
                    path: path.clone(),
                    before: before.clone(),
                    file,
                });
                "update"
            }
            _ => return Err(unsafe_path("Pi 写入目标含受保护对象").at(path)),
        };
        transaction::apply_files(
            &self.root.path,
            self.root.identity.clone(),
            ops,
            BTreeMap::from([(path.clone(), before)]),
            cancel,
        )?;
        Ok(action)
    }
}
