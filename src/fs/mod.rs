pub mod git;
pub(crate) mod platform;
mod publish;
pub use publish::{PublishReport, publish_new_tree};

use crate::{
    error::{ErrorCode, Result, YssError},
    model::{FileDescriptor, MAX_ASSET, RelPath, RenderedTree},
};
use cap_fs_ext::{DirExt, FollowSymlinks, OpenOptionsFollowExt};
use cap_std::fs::{Dir, Metadata, OpenOptions};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    io::{Read, Seek, Write},
    path::{Component, Path, PathBuf},
};
use unicode_normalization::UnicodeNormalization;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileIdentity {
    pub volume: u64,
    pub index: [u8; 16],
}
pub type RootIdentity = FileIdentity;
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub enum FileSnapshot {
    Missing,
    File {
        identity: FileIdentity,
        descriptor: FileDescriptor,
    },
    Directory {
        identity: FileIdentity,
    },
    Dangerous,
}
impl FileSnapshot {
    pub fn descriptor(&self) -> Option<&FileDescriptor> {
        if let Self::File { descriptor, .. } = self {
            Some(descriptor)
        } else {
            None
        }
    }
    pub fn identity(&self) -> Option<&FileIdentity> {
        match self {
            Self::File { identity, .. } | Self::Directory { identity } => Some(identity),
            _ => None,
        }
    }
}
pub struct CheckedFile {
    pub bytes: Vec<u8>,
    pub snapshot: FileSnapshot,
}
pub struct RootLock {
    pub root: Root,
    _file: std::fs::File,
}
pub struct Root {
    pub path: PathBuf,
    pub identity: RootIdentity,
    dir: Dir,
    boundary: git::Boundary,
    lock_identity: Option<(RelPath, FileIdentity)>,
}
struct Parent {
    dir: Dir,
    identity: FileIdentity,
    relative: Option<RelPath>,
    name: String,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum PathLookup {
    Project,
    BundleSource,
    ProjectInventory,
}

pub fn absolute(path: &Path) -> Result<PathBuf> {
    if path.components().any(|c| matches!(c, Component::ParentDir)) {
        return Err(unsafe_path("根路径不允许 .."));
    }
    Ok(std::path::absolute(path)?)
}
pub fn alias_key(path: &str) -> String {
    path.to_lowercase().nfd().collect()
}
/// Windows ordinal, case-insensitive component prefix comparison.
#[cfg(windows)]
pub(crate) fn windows_path_under(path: &str, prefix: &str) -> Option<bool> {
    use windows_sys::Win32::Globalization::{CSTR_EQUAL, CompareStringOrdinal};
    if prefix.is_empty() {
        return Some(true);
    }
    let mut parts = path.split('/');
    for expected in prefix.split('/') {
        let Some(actual) = parts.next() else {
            return Some(false);
        };
        let actual: Vec<u16> = actual.encode_utf16().collect();
        let expected: Vec<u16> = expected.encode_utf16().collect();
        let left = i32::try_from(actual.len()).ok()?;
        let right = i32::try_from(expected.len()).ok()?;
        let result =
            unsafe { CompareStringOrdinal(actual.as_ptr(), left, expected.as_ptr(), right, 1) };
        if result == 0 {
            return None;
        }
        if result != CSTR_EQUAL {
            return Some(false);
        }
    }
    Some(true)
}
pub fn observable_mode(mode: u32) -> u32 {
    if cfg!(windows) {
        if mode & 0o222 == 0 { 0o444 } else { 0o644 }
    } else {
        mode & 0o777
    }
}
fn unsafe_path(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsafePath, message)
}
fn drift() -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, "文件或目录在操作期间改变")
}
fn io_at(error: std::io::Error, path: &RelPath) -> YssError {
    YssError::from(error).at(path)
}
fn plain(metadata: &Metadata) -> Result<()> {
    if platform::dangerous(metadata) {
        Err(unsafe_path("拒绝链接、重解析点或特殊文件"))
    } else {
        Ok(())
    }
}
fn aliases(dir: &Dir, name: &str) -> Result<()> {
    let key = alias_key(name);
    for entry in dir.entries()? {
        let actual = entry?.file_name();
        let actual = actual
            .to_str()
            .ok_or_else(|| unsafe_path("目录包含非 UTF-8 名称"))?;
        let collides = if actual.is_ascii() && name.is_ascii() {
            actual.eq_ignore_ascii_case(name)
        } else {
            alias_key(actual) == key
        };
        if actual != name && collides {
            return Err(unsafe_path("路径存在大小写或 Unicode 别名冲突"));
        }
    }
    Ok(())
}
fn open_absolute(path: &Path) -> Result<Dir> {
    walk_absolute(path, true)
}
fn walk_absolute(path: &Path, check_aliases: bool) -> Result<Dir> {
    let mut base = PathBuf::new();
    let mut names = Vec::new();
    for component in path.components() {
        match component {
            Component::Prefix(_) | Component::RootDir => base.push(component.as_os_str()),
            Component::Normal(name) => names.push(name),
            Component::CurDir => {}
            Component::ParentDir => return Err(unsafe_path("根路径不允许 ..")),
        }
    }
    let mut dir = Dir::open_ambient_dir(base, cap_std::ambient_authority())?;
    for name in names {
        if check_aliases {
            aliases(
                &dir,
                name.to_str()
                    .ok_or_else(|| unsafe_path("根路径不是 UTF-8"))?,
            )?;
        }
        let metadata = dir.symlink_metadata(name)?;
        plain(&metadata)?;
        let before_id = platform::identity_at(&dir, name)?;
        let next = dir.open_dir_nofollow(name)?;
        if before_id != platform::identity_dir(&next)? {
            return Err(drift());
        }
        dir = next;
    }
    Ok(dir)
}
impl Root {
    pub(crate) fn fill_reserved(
        &self,
        path: &RelPath,
        expected: &FileSnapshot,
        bytes: &[u8],
        mode: u32,
    ) -> Result<FileSnapshot> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("私有文件父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        if &self.inspect(path)? != expected || expected.descriptor().is_none_or(|d| d.size != 0) {
            return Err(drift().at(path));
        }
        let mut options = OpenOptions::new();
        options.write(true).follow(FollowSymlinks::No);
        let file = parent.dir.open_with(&parent.name, &options)?;
        let owned = platform::identity_file(&file)?;
        if expected.identity() != Some(&owned) || file.metadata()?.len() != 0 {
            return Err(drift().at(path));
        }
        let mut file = file.into_std();
        file.write_all(bytes)?;
        platform::set_mode(&file, mode)?;
        file.sync_all()?;
        drop(file);
        self.revalidate_parent(&parent)?;
        let actual = self.inspect(path)?;
        if actual.identity() != Some(&owned) {
            return Err(drift().at(path));
        }
        Ok(actual)
    }
    pub(crate) fn exchange(
        &self,
        left: &RelPath,
        right: &RelPath,
        expected: &FileSnapshot,
    ) -> Result<()> {
        let a = self
            .parent(left)?
            .ok_or_else(|| unsafe_path("交换源父目录不存在"))?;
        let b = self
            .parent(right)?
            .ok_or_else(|| unsafe_path("交换目标父目录不存在"))?;
        self.revalidate_parent(&a)?;
        self.revalidate_parent(&b)?;
        if &self.inspect(right)? != expected {
            return Err(drift().at(right));
        }
        platform::exchange(&a.dir, &a.name, &b.dir, &b.name)?;
        platform::sync_dir(&a.dir)?;
        platform::sync_dir(&b.dir)?;
        self.revalidate_parent(&a)?;
        self.revalidate_parent(&b)
    }
    pub(crate) fn is_link(&self, path: &RelPath) -> Result<Option<bool>> {
        let Some(parent) = self.parent(path)? else {
            return Ok(None);
        };
        match parent.dir.symlink_metadata(&parent.name) {
            Ok(metadata) => Ok(Some(metadata.file_type().is_symlink())),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(io_at(e, path)),
        }
    }
    pub(crate) fn read_link(&self, path: &RelPath) -> Result<PathBuf> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("链接父目录不存在"))?;
        let before = parent.dir.symlink_metadata(&parent.name)?;
        if !before.file_type().is_symlink() {
            return Err(unsafe_path("不是符号链接"));
        }
        let before_id = platform::identity_at(&parent.dir, &parent.name)?;
        let target = parent.dir.read_link_contents(&parent.name)?;
        let after = parent.dir.symlink_metadata(&parent.name)?;
        if before_id != platform::identity_at(&parent.dir, &parent.name)?
            || before.modified()? != after.modified()?
        {
            return Err(drift());
        }
        Ok(target)
    }
    pub fn open(path: &Path) -> Result<Self> {
        let path = absolute(path)?;
        let dir = open_absolute(&path)?;
        let identity = platform::identity_dir(&dir)?;
        let boundary = git::Boundary::probe(&path)?;
        Ok(Self {
            path,
            identity,
            dir,
            boundary,
            lock_identity: None,
        })
    }
    pub fn revalidate(&self) -> Result<()> {
        if platform::identity_dir(&walk_absolute(&self.path, false)?)? != self.identity {
            return Err(drift());
        }
        if let Some((lock, identity)) = &self.lock_identity {
            let parent = self.parent(lock)?.ok_or_else(|| drift().at(lock))?;
            let metadata = parent.dir.symlink_metadata(&parent.name)?;
            plain(&metadata)?;
            if !metadata.is_file() || platform::identity_at(&parent.dir, &parent.name)? != *identity
            {
                return Err(drift().at(lock));
            }
        }
        self.boundary.revalidate()
    }
    fn parent(&self, path: &RelPath) -> Result<Option<Parent>> {
        self.parent_checked(path, PathLookup::Project)
    }
    fn parent_checked(&self, path: &RelPath, lookup: PathLookup) -> Result<Option<Parent>> {
        self.boundary.check(path)?;
        let mut dir = self.dir.try_clone()?;
        let mut components = path.as_str().split('/').peekable();
        let mut parent = String::new();
        while let Some(name) = components.next() {
            if lookup != PathLookup::BundleSource {
                aliases(&dir, name)?;
            }
            if components.peek().is_none() {
                return Ok(Some(Parent {
                    identity: platform::identity_dir(&dir)?,
                    dir,
                    relative: if parent.is_empty() {
                        None
                    } else {
                        Some(RelPath::new(parent)?)
                    },
                    name: name.into(),
                }));
            }
            let metadata = match dir.symlink_metadata(name) {
                Ok(m) => m,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
                Err(e) => return Err(io_at(e, path)),
            };
            if lookup == PathLookup::ProjectInventory
                && (!metadata.is_dir() || platform::dangerous(&metadata))
            {
                return Ok(None);
            }
            plain(&metadata)?;
            let before_id = platform::identity_at(&dir, name)?;
            let child = dir.open_dir_nofollow(name).map_err(|e| io_at(e, path))?;
            if before_id != platform::identity_dir(&child)? {
                return Err(drift().at(path));
            }
            if child.symlink_metadata(".git").is_ok() {
                return Err(unsafe_path("拒绝穿越嵌套 Git 工作树").at(path));
            }
            if !parent.is_empty() {
                parent.push('/');
            }
            parent.push_str(name);
            dir = child;
        }
        Err(unsafe_path("空路径"))
    }
    fn revalidate_parent(&self, parent: &Parent) -> Result<()> {
        self.revalidate()?;
        let current = match &parent.relative {
            Some(path) => self.open_dir(path)?,
            None => self.dir.try_clone()?,
        };
        if platform::identity_dir(&current)? != parent.identity {
            return Err(drift());
        }
        aliases(&current, &parent.name)
    }
    pub(crate) fn open_dir(&self, path: &RelPath) -> Result<Dir> {
        self.open_dir_checked(path, PathLookup::Project)
    }
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    pub(crate) fn pin_directory(&self) -> Result<Dir> {
        self.revalidate()?;
        Ok(self.dir.try_clone()?)
    }
    fn open_dir_checked(&self, path: &RelPath, lookup: PathLookup) -> Result<Dir> {
        let parent = self
            .parent_checked(path, lookup)?
            .ok_or_else(|| unsafe_path("父目录不存在"))?;
        let metadata = parent.dir.symlink_metadata(&parent.name)?;
        plain(&metadata)?;
        let before_id = platform::identity_at(&parent.dir, &parent.name)?;
        let dir = parent.dir.open_dir_nofollow(&parent.name)?;
        if before_id != platform::identity_dir(&dir)? {
            return Err(drift());
        }
        if dir.symlink_metadata(".git").is_ok() {
            return Err(unsafe_path("拒绝穿越嵌套 Git 工作树").at(path));
        }
        Ok(dir)
    }
    /// Inspect a directory entry without opening its contents or traversing a
    /// link. Write planning and mutations inspect their actual targets strictly.
    pub(crate) fn entry_metadata(&self, path: &RelPath) -> Result<Option<Metadata>> {
        let Some(parent) = self.parent_checked(path, PathLookup::ProjectInventory)? else {
            return Ok(None);
        };
        match parent.dir.symlink_metadata(&parent.name) {
            Ok(metadata) => Ok(Some(metadata)),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(io_at(e, path)),
        }
    }
    pub(crate) fn regular_file_metadata(&self, path: &RelPath) -> Result<Option<Metadata>> {
        Ok(self
            .entry_metadata(path)?
            .filter(|metadata| metadata.is_file() && !platform::dangerous(metadata)))
    }
    pub fn inspect(&self, path: &RelPath) -> Result<FileSnapshot> {
        let Some(parent) = self.parent(path)? else {
            return Ok(FileSnapshot::Missing);
        };
        let metadata = match parent.dir.symlink_metadata(&parent.name) {
            Ok(m) => m,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(FileSnapshot::Missing),
            Err(e) => return Err(io_at(e, path)),
        };
        if platform::dangerous(&metadata) {
            return Ok(FileSnapshot::Dangerous);
        }
        if metadata.is_dir() {
            let dir = self.open_dir(path)?;
            return Ok(FileSnapshot::Directory {
                identity: platform::identity_dir(&dir)?,
            });
        }
        Ok(self.read_checked(path, MAX_ASSET)?.snapshot)
    }
    /// Inspect mutable database files without reading their entire contents.
    pub(crate) fn regular_file_identity(&self, path: &RelPath) -> Result<FileIdentity> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("文件父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let metadata = parent
            .dir
            .symlink_metadata(&parent.name)
            .map_err(|e| io_at(e, path))?;
        plain(&metadata)?;
        if !metadata.is_file() {
            return Err(unsafe_path("数据库路径不是普通文件").at(path));
        }
        #[cfg(unix)]
        {
            use cap_std::fs::MetadataExt;
            if metadata.nlink() != 1 || platform::mode(&metadata) & 0o077 != 0 {
                return Err(unsafe_path("数据库文件须为私有文件且不能有硬链接").at(path));
            }
        }
        let identity = platform::identity_at(&parent.dir, &parent.name)?;
        self.revalidate_parent(&parent)?;
        if platform::identity_at(&parent.dir, &parent.name)? != identity {
            return Err(drift().at(path));
        }
        Ok(identity)
    }
    pub fn read_checked(&self, path: &RelPath, limit: u64) -> Result<CheckedFile> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("读取路径的父目录不存在").at(path))?;
        let checked = read_in_dir(&parent.dir, &parent.name, limit).map_err(|e| e.at(path))?;
        if platform::identity_dir(&walk_absolute(&self.path, false)?)? != self.identity
            || self
                .parent(path)?
                .is_none_or(|p| p.identity != parent.identity)
        {
            return Err(drift().at(path));
        }
        Ok(checked)
    }
    /// Read a secret only after checking permissions on the opened file itself.
    pub fn read_private_checked(&self, path: &RelPath, limit: u64) -> Result<CheckedFile> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("读取路径的父目录不存在").at(path))?;
        let checked = read_in_dir_impl(&parent.dir, &parent.name, limit, true, false)
            .map_err(|e| e.at(path))?;
        if platform::identity_dir(&walk_absolute(&self.path, false)?)? != self.identity
            || self
                .parent(path)?
                .is_none_or(|p| p.identity != parent.identity)
        {
            return Err(drift().at(path));
        }
        Ok(checked)
    }
    /// Read only while the actual open file remains a single-link ordinary file.
    pub fn read_checked_single_link(&self, path: &RelPath, limit: u64) -> Result<CheckedFile> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("读取路径的父目录不存在").at(path))?;
        let checked = read_in_dir_impl(&parent.dir, &parent.name, limit, false, true)
            .map_err(|e| e.at(path))?;
        if platform::identity_dir(&walk_absolute(&self.path, false)?)? != self.identity
            || self
                .parent(path)?
                .is_none_or(|p| p.identity != parent.identity)
        {
            return Err(drift().at(path));
        }
        Ok(checked)
    }
    // Read-only traversal avoids repeated whole-directory alias scans; writes retain them.
    pub(crate) fn source_read(&self, path: &RelPath, limit: u64) -> Result<CheckedFile> {
        let parent = self
            .parent_checked(path, PathLookup::BundleSource)?
            .ok_or_else(|| YssError::from(std::io::Error::from(std::io::ErrorKind::NotFound)))?;
        let file = read_in_dir(&parent.dir, &parent.name, limit)?;
        if self
            .parent_checked(path, PathLookup::BundleSource)?
            .is_none_or(|p| p.identity != parent.identity)
        {
            return Err(drift());
        }
        Ok(file)
    }
    pub(crate) fn source_read_single_link(
        &self,
        path: &RelPath,
        limit: u64,
    ) -> Result<CheckedFile> {
        let parent = self
            .parent_checked(path, PathLookup::BundleSource)?
            .ok_or_else(|| YssError::from(std::io::Error::from(std::io::ErrorKind::NotFound)))?;
        let file = read_in_dir_impl(&parent.dir, &parent.name, limit, false, true)?;
        if self
            .parent_checked(path, PathLookup::BundleSource)?
            .is_none_or(|p| p.identity != parent.identity)
        {
            return Err(drift());
        }
        Ok(file)
    }
    pub(crate) fn source_metadata(&self, path: &RelPath) -> Result<Metadata> {
        let parent = self
            .parent_checked(path, PathLookup::BundleSource)?
            .ok_or_else(|| YssError::from(std::io::Error::from(std::io::ErrorKind::NotFound)))?;
        let metadata = parent.dir.symlink_metadata(&parent.name)?;
        plain(&metadata)?;
        if metadata.is_file() {
            let before = platform::identity_at(&parent.dir, &parent.name)?;
            let mut options = OpenOptions::new();
            options.read(true).follow(FollowSymlinks::No);
            let file = parent.dir.open_with(&parent.name, &options)?;
            if platform::identity_file(&file)? != before {
                return Err(drift());
            }
        }
        Ok(metadata)
    }
    pub(crate) fn source_entries(
        &self,
        path: Option<&RelPath>,
        budget: &mut usize,
        cancel: &crate::cancel::Cancellation,
    ) -> Result<(Vec<(String, Metadata)>, bool)> {
        let dir = match path {
            Some(p) => self.open_dir_checked(p, PathLookup::BundleSource)?,
            None => self.dir.try_clone()?,
        };
        let mut entries = Vec::new();
        let mut complete = true;
        for entry in dir.entries()?.take(*budget) {
            cancel.check()?;
            *budget -= 1;
            let entry = entry?;
            let name = entry
                .file_name()
                .into_string()
                .map_err(|_| unsafe_path("目录包含非 UTF-8 名称"))?;
            let metadata = dir.symlink_metadata(&name)?;
            entries.push((name, metadata));
        }
        let current = match path {
            Some(p) => self.open_dir_checked(p, PathLookup::BundleSource)?,
            None => self.dir.try_clone()?,
        };
        if platform::identity_dir(&current)? != platform::identity_dir(&dir)? {
            return Err(drift());
        }
        if *budget == 0 {
            complete = false;
        }
        entries.sort_by(|a, b| a.0.cmp(&b.0));
        Ok((entries, complete))
    }
    pub fn list(&self, path: Option<&RelPath>) -> Result<Vec<String>> {
        let dir = if let Some(path) = path {
            self.open_dir(path)?
        } else {
            self.dir.try_clone()?
        };
        let mut entries = Vec::new();
        for entry in dir.entries()? {
            entries.push(
                entry?
                    .file_name()
                    .into_string()
                    .map_err(|_| unsafe_path("目录包含非 UTF-8 名称"))?,
            );
        }
        entries.sort();
        Ok(entries)
    }
    pub fn create_dir(&self, path: &RelPath) -> Result<FileIdentity> {
        self.create_dir_with(path, &cap_std::fs::DirBuilder::new())
    }
    fn create_dir_with(
        &self,
        path: &RelPath,
        builder: &cap_std::fs::DirBuilder,
    ) -> Result<FileIdentity> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("创建目录的父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        parent.dir.create_dir_with(&parent.name, builder)?;
        let child = parent.dir.open_dir_nofollow(&parent.name)?;
        let identity = platform::identity_dir(&child)?;
        platform::sync_dir(&parent.dir)?;
        self.revalidate_parent(&parent)?;
        Ok(identity)
    }
    /// Create a Unix directory with private mode at creation.
    #[cfg(unix)]
    pub fn create_private_dir(&self, path: &RelPath) -> Result<FileIdentity> {
        use cap_std::fs::DirBuilderExt;
        let mut builder = cap_std::fs::DirBuilder::new();
        builder.mode(0o700);
        self.create_dir_with(path, &builder)
    }
    /// Create a Windows directory with a protected inheritable private DACL at creation.
    #[cfg(windows)]
    pub fn create_private_dir(&self, path: &RelPath) -> Result<FileIdentity> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("创建目录的父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let directory = platform::create_private_directory(&parent.dir, &parent.name)?;
        let identity = platform::identity_std_file(&directory)?;
        self.revalidate_parent(&parent)?;
        let child = parent.dir.open_dir_nofollow(&parent.name)?;
        if platform::identity_dir(&child)? != identity {
            return Err(drift().at(path));
        }
        platform::verify_private_directory(&child)?;
        platform::sync_dir(&parent.dir)?;
        self.revalidate_parent(&parent)?;
        Ok(identity)
    }
    #[cfg(windows)]
    pub fn verify_private_dir(&self) -> Result<()> {
        platform::verify_private_directory(&self.dir)?;
        if platform::identity_dir(&walk_absolute(&self.path, false)?)? != self.identity {
            return Err(drift());
        }
        Ok(())
    }
    /// Remove a directly owned private scratch child without reading its contents.
    #[cfg(windows)]
    pub(crate) fn remove_private_tree(
        &self,
        name: &RelPath,
        identity: &FileIdentity,
        max_entries: usize,
        max_depth: usize,
    ) -> Result<()> {
        if name.as_str().contains('/') {
            return Err(unsafe_path("暂存目录须是直接子目录").at(name));
        }
        self.verify_trusted_dir()?;
        platform::remove_private_tree(&self.dir, name.as_str(), identity, max_entries, max_depth)
            .map_err(|error| io_at(error, name))?;
        self.revalidate()
    }
    /// Verify that a Windows directory has no untrusted modification grants.
    #[cfg(windows)]
    pub fn verify_trusted_dir(&self) -> Result<()> {
        platform::verify_trusted_directory(&self.dir)?;
        self.revalidate()
    }
    /// Borrow a security-write handle opened relative to a verified parent.
    #[cfg(windows)]
    #[allow(dead_code)] // AppContainer candidate is not enabled in production yet.
    pub(crate) fn with_verified_handle<T>(
        &self,
        operation: impl FnOnce(std::os::windows::io::RawHandle) -> Result<T>,
    ) -> Result<T> {
        use std::os::windows::io::AsRawHandle;
        self.revalidate()?;
        let parent_path = self
            .path
            .parent()
            .ok_or_else(|| unsafe_path("根目录没有父目录"))?;
        let name = self
            .path
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| unsafe_path("目录名无效"))?;
        let parent = Root::open(parent_path)?;
        let directory = platform::open_security_directory(&parent.dir, name)?;
        if platform::identity_std_file(&directory)? != self.identity {
            return Err(drift());
        }
        parent.revalidate()?;
        let result = operation(directory.as_raw_handle());
        parent.revalidate()?;
        self.revalidate()?;
        result
    }
    /// Hold this directory open without delete sharing after checking its identity.
    #[cfg(windows)]
    pub fn pin_directory(&self) -> Result<std::fs::File> {
        self.revalidate()?;
        let parent_path = self
            .path
            .parent()
            .ok_or_else(|| unsafe_path("根目录没有父目录"))?;
        let name = self
            .path
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| unsafe_path("目录名无效"))?;
        let parent = Root::open(parent_path)?;
        let directory = platform::open_pinned_directory(&parent.dir, name)?;
        if platform::identity_std_file(&directory)? != self.identity {
            return Err(drift());
        }
        parent.revalidate()?;
        self.revalidate()?;
        Ok(directory)
    }
    /// Pin an inherited private file against replacement while retaining write sharing.
    #[cfg(windows)]
    pub(crate) fn pin_private_file(&self, path: &RelPath) -> Result<std::fs::File> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("私有文件父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let file = platform::open_pinned_private_file(&parent.dir, &parent.name)?;
        let identity = platform::identity_std_file(&file)?;
        if platform::identity_at(&parent.dir, &parent.name)? != identity {
            return Err(drift().at(path));
        }
        self.revalidate_parent(&parent)?;
        Ok(file)
    }
    /// Pin a linked-worktree .git pointer before reading its external Git directories.
    #[cfg(windows)]
    pub(crate) fn pin_git_marker(&self) -> Result<Option<std::fs::File>> {
        self.revalidate()?;
        let metadata = self.dir.symlink_metadata(".git")?;
        plain(&metadata)?;
        if metadata.is_dir() {
            return Ok(None);
        }
        if !metadata.is_file() {
            return Err(unsafe_path("Git 工作树入口无效"));
        }
        let before = platform::identity_at(&self.dir, ".git")?;
        let file = platform::open_unchanging_file(&self.dir, ".git")?;
        if platform::identity_std_file(&file)? != before
            || platform::identity_at(&self.dir, ".git")? != before
        {
            return Err(drift());
        }
        self.revalidate()?;
        Ok(Some(file))
    }
    /// Resolve containment by directory identity, including an absent final path.
    #[cfg(windows)]
    pub(crate) fn relative_windows_path(&self, target: &Path) -> Result<Option<String>> {
        use std::ffi::OsString;
        if !target.is_absolute()
            || target
                .components()
                .any(|part| matches!(part, Component::ParentDir))
        {
            return Err(unsafe_path("路径不在授权范围内"));
        }
        self.revalidate()?;
        let mut current = target.to_owned();
        let mut tail: Vec<OsString> = Vec::new();
        loop {
            match std::fs::canonicalize(&current) {
                Ok(canonical) => {
                    if canonical.is_dir() {
                        current = canonical;
                        break;
                    }
                    tail.push(
                        canonical
                            .file_name()
                            .ok_or_else(|| unsafe_path("路径无效"))?
                            .to_owned(),
                    );
                    current = canonical
                        .parent()
                        .ok_or_else(|| unsafe_path("路径无效"))?
                        .to_owned();
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                    if std::fs::symlink_metadata(&current).is_ok() {
                        return Err(unsafe_path("路径包含无效重解析点"));
                    }
                    tail.push(
                        current
                            .file_name()
                            .ok_or_else(|| unsafe_path("路径无效"))?
                            .to_owned(),
                    );
                    current = current
                        .parent()
                        .ok_or_else(|| unsafe_path("路径无效"))?
                        .to_owned();
                }
                Err(error) => return Err(error.into()),
            }
        }
        for ancestor in current.ancestors() {
            let opened = Root::open(ancestor)?;
            if opened.identity == self.identity {
                let mut suffix = current
                    .strip_prefix(ancestor)
                    .map_err(|_| unsafe_path("路径无效"))?
                    .to_owned();
                for part in tail.iter().rev() {
                    suffix.push(part);
                }
                let text = suffix
                    .to_str()
                    .ok_or_else(|| unsafe_path("路径不是 UTF-8"))?
                    .replace('\\', "/");
                if !text.is_empty() {
                    RelPath::new(&text)?;
                }
                self.revalidate()?;
                return Ok(Some(text));
            }
        }
        Ok(None)
    }
    pub fn create_file(&self, path: &RelPath, bytes: &[u8], mode: u32) -> Result<FileSnapshot> {
        if bytes.len() as u64 > MAX_ASSET || mode & !0o777 != 0 {
            return Err(unsafe_path("文件长度或模式不合法"));
        }
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("创建文件的父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let mut options = OpenOptions::new();
        options
            .write(true)
            .create_new(true)
            .follow(FollowSymlinks::No);
        #[cfg(unix)]
        {
            use cap_std::fs::OpenOptionsExt;
            options.mode(mode);
        }
        let file = parent.dir.open_with(&parent.name, &options)?;
        let owned = platform::identity_file(&file)?;
        let mut file = file.into_std();
        file.write_all(bytes)?;
        platform::set_mode(&file, mode)?;
        file.sync_all()?;
        drop(file);
        platform::sync_dir(&parent.dir)?;
        self.revalidate_parent(&parent)?;
        let actual = self.inspect(path)?;
        if actual.identity() != Some(&owned) {
            return Err(drift().at(path));
        }
        Ok(actual)
    }
    /// Create secret files with private permissions before any bytes are written.
    pub fn create_private_file(&self, path: &RelPath, bytes: &[u8]) -> Result<FileSnapshot> {
        #[cfg(not(windows))]
        {
            self.create_file(path, bytes, 0o600)
        }
        #[cfg(windows)]
        {
            if bytes.len() as u64 > MAX_ASSET {
                return Err(unsafe_path("文件长度不合法"));
            }
            let parent = self
                .parent(path)?
                .ok_or_else(|| unsafe_path("创建文件的父目录不存在"))?;
            self.revalidate_parent(&parent)?;
            let mut file = platform::create_private_file(&parent.dir, &parent.name)?;
            let identity = platform::identity_std_file(&file)?;
            self.revalidate_parent(&parent)?;
            if platform::identity_at(&parent.dir, &parent.name)? != identity {
                return Err(drift().at(path));
            }
            file.write_all(bytes)?;
            file.sync_all()?;
            platform::verify_private_file(&file)?;
            drop(file);
            platform::sync_dir(&parent.dir)?;
            self.revalidate_parent(&parent)?;
            let actual = self.read_private_checked(path, MAX_ASSET)?.snapshot;
            if actual.identity() != Some(&identity) {
                return Err(drift().at(path));
            }
            Ok(actual)
        }
    }
    pub(crate) fn create_streamed<F>(&self, path: &RelPath, write: F) -> Result<FileIdentity>
    where
        F: FnOnce(&mut std::fs::File) -> Result<()>,
    {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("创建文件的父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let mut options = OpenOptions::new();
        options
            .write(true)
            .create_new(true)
            .follow(FollowSymlinks::No);
        #[cfg(unix)]
        {
            use cap_std::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let file = parent.dir.open_with(&parent.name, &options)?;
        let identity = platform::identity_file(&file)?;
        let mut file = file.into_std();
        write(&mut file)?;
        file.sync_all()?;
        drop(file);
        platform::sync_dir(&parent.dir)?;
        self.revalidate_parent(&parent)?;
        if self.regular_file_identity(path)? != identity {
            return Err(drift());
        }
        Ok(identity)
    }
    pub(crate) fn open_streamed(&self, path: &RelPath) -> Result<std::fs::File> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("文件父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let before = parent.dir.symlink_metadata(&parent.name)?;
        plain(&before)?;
        if !before.is_file() {
            return Err(unsafe_path("只能读取普通文件"));
        }
        let mut options = OpenOptions::new();
        options.read(true).follow(FollowSymlinks::No);
        let file = parent.dir.open_with(&parent.name, &options)?;
        let identity = platform::identity_file(&file)?;
        if platform::identity_at(&parent.dir, &parent.name)? != identity {
            return Err(drift());
        }
        self.revalidate_parent(&parent)?;
        Ok(file.into_std())
    }
    pub(crate) fn publish_streamed(
        &self,
        from: &RelPath,
        to: &RelPath,
        identity: &FileIdentity,
    ) -> Result<()> {
        let source = self
            .parent(from)?
            .ok_or_else(|| unsafe_path("源父目录不存在"))?;
        let target = self
            .parent(to)?
            .ok_or_else(|| unsafe_path("目标父目录不存在"))?;
        self.revalidate_parent(&source)?;
        self.revalidate_parent(&target)?;
        if self.regular_file_identity(from)? != *identity
            || self.inspect(to)? != FileSnapshot::Missing
        {
            return Err(drift());
        }
        platform::rename_no_replace(&source.dir, &source.name, &target.dir, &target.name)?;
        platform::sync_dir(&target.dir)?;
        self.revalidate_parent(&target)?;
        if self.regular_file_identity(to)? != *identity {
            return Err(drift());
        }
        Ok(())
    }
    pub fn append(&self, path: &RelPath, bytes: &[u8], identity: &FileIdentity) -> Result<()> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("日志父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        let mut options = OpenOptions::new();
        options.append(true).follow(FollowSymlinks::No);
        let mut file = parent.dir.open_with(&parent.name, &options)?;
        if platform::identity_file(&file)? != *identity {
            return Err(drift().at(path));
        }
        file.write_all(bytes)?;
        file.sync_all()?;
        self.revalidate_parent(&parent)
    }
    pub fn rename(
        &self,
        from: &RelPath,
        to: &RelPath,
        expected: &FileSnapshot,
        replace: bool,
    ) -> Result<()> {
        let source = self
            .parent(from)?
            .ok_or_else(|| unsafe_path("源父目录不存在"))?;
        let target = self
            .parent(to)?
            .ok_or_else(|| unsafe_path("目标父目录不存在"))?;
        self.revalidate_parent(&source)?;
        self.revalidate_parent(&target)?;
        if &self.inspect(to)? != expected {
            return Err(drift().at(to));
        }
        let source_state = self.inspect(from)?;
        if source_state == FileSnapshot::Dangerous || source_state == FileSnapshot::Missing {
            return Err(unsafe_path("发布源不是普通对象").at(from));
        }
        let renamed = if replace {
            platform::rename_replace(&source.dir, &source.name, &target.dir, &target.name)
        } else {
            platform::rename_no_replace(&source.dir, &source.name, &target.dir, &target.name)
        };
        renamed.map_err(|e| io_at(e, to).context("systemOperation", "rename"))?;
        (|| {
            platform::sync_dir(&source.dir)?;
            platform::sync_dir(&target.dir)?;
            self.revalidate_parent(&source)?;
            self.revalidate_parent(&target)?;
            if self.inspect(to)?.identity() != source_state.identity() {
                return Err(drift().at(to));
            }
            Ok(())
        })()
        .map_err(|e: YssError| {
            e.context("renameApplied", "true")
                .context("from", from)
                .context("to", to)
        })
    }

    /// Move the exact verified object without replacing another directory entry.
    /// The Windows handle holds deny-write and deny-delete sharing. The name
    /// operation never changes the data or attributes of other hard links.
    #[cfg(windows)]
    pub(crate) fn move_verified_entry(
        &self,
        from: &RelPath,
        to: &RelPath,
        expected: &FileSnapshot,
    ) -> Result<()> {
        let source = self
            .parent(from)?
            .ok_or_else(|| unsafe_path("发布源父目录不存在"))?;
        let target = self
            .parent(to)?
            .ok_or_else(|| unsafe_path("发布目标父目录不存在"))?;
        let entry = self.verified_entry(&source, from, expected, false)?;
        self.revalidate_parent(&target)?;
        if self.inspect(to)? != FileSnapshot::Missing {
            return Err(drift().at(to));
        }
        platform::rename_verified_entry(&entry, &target.dir, &target.name)
            .map_err(|error| io_at(error, to).context("systemOperation", "rename"))?;
        drop(entry);
        (|| {
            self.revalidate_parent(&source)?;
            self.revalidate_parent(&target)?;
            if &self.inspect(to)? != expected {
                return Err(drift().at(to));
            }
            Ok(())
        })()
        .map_err(|error: YssError| {
            error
                .context("renameApplied", "true")
                .context("from", from)
                .context("to", to)
        })
    }

    #[cfg(windows)]
    pub(crate) fn remove_verified_entry(
        &self,
        path: &RelPath,
        expected: &FileSnapshot,
    ) -> Result<()> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("删除路径父目录不存在"))?;
        let entry = self.verified_entry(&parent, path, expected, true)?;
        platform::unlink_verified_entry(&entry, matches!(expected, FileSnapshot::Directory { .. }))
            .map_err(|error| io_at(error, path))?;
        drop(entry);
        self.revalidate_parent(&parent)?;
        if self.inspect(path)? != FileSnapshot::Missing {
            return Err(drift().at(path));
        }
        Ok(())
    }

    #[cfg(windows)]
    fn verified_entry(
        &self,
        parent: &Parent,
        path: &RelPath,
        expected: &FileSnapshot,
        unlink: bool,
    ) -> Result<std::fs::File> {
        let identity = expected
            .identity()
            .ok_or_else(|| unsafe_path("需核验已知对象").at(path))?;
        self.revalidate_parent(parent)?;
        let directory = matches!(expected, FileSnapshot::Directory { .. });
        let mut file = platform::open_verified_entry(&parent.dir, &parent.name, directory, unlink)
            .map_err(|error| io_at(error, path))?;
        platform::verify_cleanup_entry(&file, identity, directory).map_err(|_| drift().at(path))?;
        if let Some(descriptor) = expected.descriptor() {
            if descriptor.size > MAX_ASSET || !platform::has_single_link(&file)? {
                return Err(unsafe_path("事务对象长度或硬链接不安全").at(path));
            }
            let metadata = file.metadata()?;
            let mode = if metadata.permissions().readonly() {
                0o444
            } else {
                0o644
            };
            if metadata.len() != descriptor.size || descriptor.mode != mode {
                return Err(drift().at(path));
            }
            let mut bytes = Vec::new();
            (&mut file)
                .take(descriptor.size + 1)
                .read_to_end(&mut bytes)?;
            if FileDescriptor::new(&bytes, mode) != *descriptor {
                return Err(drift().at(path));
            }
        }
        self.revalidate_parent(parent)?;
        platform::verify_cleanup_entry(&file, identity, directory).map_err(|_| drift().at(path))?;
        if !directory && !platform::has_single_link(&file)? {
            return Err(unsafe_path("事务对象出现硬链接").at(path));
        }
        Ok(file)
    }

    pub fn remove(&self, path: &RelPath, expected: &FileSnapshot) -> Result<()> {
        let parent = self
            .parent(path)?
            .ok_or_else(|| unsafe_path("删除路径父目录不存在"))?;
        self.revalidate_parent(&parent)?;
        if &self.inspect(path)? != expected {
            return Err(drift().at(path));
        }
        match expected {
            FileSnapshot::File { .. } => parent.dir.remove_file(&parent.name)?,
            FileSnapshot::Directory { .. } => parent.dir.remove_dir(&parent.name)?,
            _ => return Err(unsafe_path("不可删除未知对象")),
        }
        platform::sync_dir(&parent.dir)?;
        self.revalidate_parent(&parent)
    }
}
pub fn inspect(root: &Path, path: &RelPath) -> Result<FileSnapshot> {
    Root::open(root)?.inspect(path)
}
pub fn read_checked(root: &Path, path: &RelPath, limit: u64) -> Result<CheckedFile> {
    Root::open(root)?.read_checked(path, limit)
}

pub fn lock_root(path: &Path) -> Result<RootLock> {
    let root = Root::open(path)?;
    let control = RelPath::new(".yss")?;
    let created = match root.inspect(&control)? {
        FileSnapshot::Missing => {
            root.create_dir(&control)?;
            true
        }
        FileSnapshot::Directory { .. } => {
            validate_control(&root)?;
            false
        }
        _ => return Err(unsafe_path("YSS 控制目录不安全")),
    };
    acquire_lock(root, &RelPath::new(".yss/lock")?, !created)
}

pub(crate) fn lock_file(root: Root, path: &RelPath) -> Result<RootLock> {
    acquire_lock(root, path, true)
}

fn acquire_lock(mut root: Root, lock: &RelPath, allow_existing: bool) -> Result<RootLock> {
    let parent = root
        .parent(lock)?
        .ok_or_else(|| unsafe_path("控制目录不存在"))?;
    root.revalidate_parent(&parent)?;
    let mut options = OpenOptions::new();
    options
        .read(true)
        .write(true)
        .create_new(true)
        .follow(FollowSymlinks::No);
    let file = match parent.dir.open_with(&parent.name, &options) {
        Ok(mut file) => {
            file.write_all(b"yss.lock/1\n")?;
            file.sync_all()?;
            platform::sync_dir(&parent.dir)?;
            file
        }
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists && allow_existing => {
            if root.read_checked(lock, 64)?.bytes != b"yss.lock/1\n" {
                return Err(unsafe_path("未知锁文件"));
            }
            let mut options = OpenOptions::new();
            options.read(true).write(true).follow(FollowSymlinks::No);
            parent.dir.open_with(&parent.name, &options)?
        }
        Err(e) => return Err(e.into()),
    };
    let locked_identity = platform::identity_file(&file)?;
    let mut file = file.into_std();
    file.try_lock()
        .map_err(|_| YssError::new(ErrorCode::Locked, "另一个 YSS 写入操作持有安装锁"))?;
    // Windows exclusive locks permit content reads only through the locking handle.
    file.rewind()?;
    let mut magic = Vec::new();
    (&mut file).take(65).read_to_end(&mut magic)?;
    if magic != b"yss.lock/1\n" {
        return Err(unsafe_path("锁文件已改变"));
    }
    root.lock_identity = Some((lock.clone(), locked_identity));
    root.revalidate_parent(&parent)?;
    Ok(RootLock { root, _file: file })
}
pub fn validate_control(root: &Root) -> Result<()> {
    let entries = root.list(Some(&RelPath::new(".yss")?))?;
    if entries.is_empty() {
        return Err(YssError::new(
            ErrorCode::UnsupportedFormat,
            "无法识别已有 .yss 控制目录",
        ));
    }
    for name in entries {
        match name.as_str() {
            "lock" => {
                if root.read_checked(&RelPath::new(".yss/lock")?, 64)?.bytes != b"yss.lock/1\n" {
                    return Err(unsafe_path("未知锁文件"));
                }
            }
            "transactions" => {}
            "project.json" | "tool.json" | "scaffold.json" => {
                let bytes = root
                    .read_checked(
                        &RelPath::new(format!(".yss/{name}"))?,
                        crate::model::MAX_MANIFEST,
                    )?
                    .bytes;
                let value: serde_json::Value =
                    crate::codec::decode_json(&bytes, crate::codec::Limits::manifest())?;
                let expected = format!("yss.{}", name.trim_end_matches(".json"));
                if value["format"] != expected || value["formatVersion"] != 1 {
                    return Err(YssError::new(
                        ErrorCode::UnsupportedFormat,
                        "不支持旧安装格式",
                    ));
                }
            }
            _ => {
                return Err(YssError::new(
                    ErrorCode::UnsupportedFormat,
                    "无法识别已有 YSS 控制状态",
                ));
            }
        }
    }
    Ok(())
}
pub fn validate_tree(tree: &RenderedTree) -> Result<()> {
    let mut paths: BTreeMap<String, (&RelPath, bool)> = BTreeMap::new();
    let mut total = 0u64;
    for (path, is_file) in tree
        .files
        .keys()
        .map(|p| (p, true))
        .chain(tree.directories.iter().map(|p| (p, false)))
    {
        if paths
            .insert(alias_key(path.as_str()), (path, is_file))
            .is_some()
        {
            return Err(unsafe_path("树中存在路径别名或类型冲突").at(path));
        }
    }
    for path in tree.files.keys().chain(tree.directories.iter()) {
        for parent in path.parents() {
            if let Some((existing, is_file)) = paths.get(&alias_key(parent))
                && (*is_file || existing.as_str() != parent)
            {
                return Err(unsafe_path("树中存在父路径冲突").at(path));
            }
        }
    }
    let mut all_components = BTreeMap::new();
    for path in tree.files.keys().chain(tree.directories.iter()) {
        for part in path.parents().chain(std::iter::once(path.as_str())) {
            if let Some(existing) = all_components.insert(alias_key(part), part)
                && existing != part
            {
                return Err(unsafe_path("树中存在目录别名冲突").at(path));
            }
        }
    }
    for file in tree.files.values() {
        total = total
            .checked_add(file.bytes.len() as u64)
            .ok_or_else(|| unsafe_path("树大小溢出"))?;
        if total > MAX_ASSET || !matches!(file.mode, 0o444 | 0o644 | 0o755 | 0o600 | 0o700) {
            return Err(unsafe_path("树大小或文件模式不合法"));
        }
    }
    Ok(())
}
pub fn unique_id() -> Result<String> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).map_err(|_| YssError::new(ErrorCode::Io, "无法取得系统随机数"))?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}

fn read_in_dir(dir: &Dir, name: &str, limit: u64) -> Result<CheckedFile> {
    read_in_dir_impl(dir, name, limit, false, false)
}
fn read_in_dir_impl(
    dir: &Dir,
    name: &str,
    limit: u64,
    private: bool,
    single_link: bool,
) -> Result<CheckedFile> {
    let before = dir.symlink_metadata(name)?;
    plain(&before)?;
    if !before.is_file() || before.len() > limit.min(MAX_ASSET) {
        return Err(unsafe_path("读取对象类型或长度不合法").at(name));
    }
    let before_id = platform::identity_at(dir, name)?;
    let mut options = OpenOptions::new();
    options.read(true).follow(FollowSymlinks::No);
    let mut file = dir.open_with(name, &options)?;
    let opened = file.metadata()?;
    plain(&opened)?;
    if before_id != platform::identity_file(&file)? {
        return Err(drift().at(name));
    }
    #[cfg(unix)]
    if single_link {
        use cap_std::fs::MetadataExt;
        if opened.nlink() != 1 {
            return Err(unsafe_path("不能读取硬链接").at(name));
        }
    }
    #[cfg(windows)]
    if single_link && !platform::has_single_link(&file)? {
        return Err(unsafe_path("不能读取硬链接").at(name));
    }
    if private {
        #[cfg(unix)]
        if platform::mode(&opened) & 0o077 != 0 {
            return Err(unsafe_path("私有文件权限过宽").at(name));
        }
        #[cfg(windows)]
        platform::verify_private_file(&file)
            .map_err(|_| unsafe_path("私有文件访问控制不安全").at(name))?;
    }
    let mut bytes = Vec::new();
    (&mut file)
        .take(limit.min(MAX_ASSET) + 1)
        .read_to_end(&mut bytes)?;
    let after = file.metadata()?;
    #[cfg(unix)]
    if single_link {
        use cap_std::fs::MetadataExt;
        if after.nlink() != 1 {
            return Err(unsafe_path("不能读取硬链接").at(name));
        }
    }
    #[cfg(windows)]
    if single_link && !platform::has_single_link(&file)? {
        return Err(unsafe_path("不能读取硬链接").at(name));
    }
    #[cfg(windows)]
    if private {
        platform::verify_private_file(&file)
            .map_err(|_| unsafe_path("私有文件访问控制不安全").at(name))?;
    }
    let path_after = dir.symlink_metadata(name)?;
    plain(&path_after)?;
    if bytes.len() as u64 > limit.min(MAX_ASSET)
        || opened.len() != after.len()
        || opened.modified()? != after.modified()?
        || platform::mode(&opened) != platform::mode(&after)
        || before_id != platform::identity_at(dir, name)?
        || opened.len() != bytes.len() as u64
    {
        return Err(drift().at(name));
    }
    Ok(CheckedFile {
        snapshot: FileSnapshot::File {
            identity: before_id,
            descriptor: FileDescriptor::new(&bytes, platform::mode(&after)),
        },
        bytes,
    })
}
