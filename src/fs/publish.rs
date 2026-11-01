use super::{FileSnapshot, Root, absolute, unique_id, validate_tree};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    model::{RelPath, RenderedTree},
};
use serde::Serialize;
use std::{collections::BTreeSet, path::Path};

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PublishReport {
    pub root: String,
    pub files: usize,
    pub directories: usize,
}

pub fn publish_new_tree(
    target: &Path,
    tree: &RenderedTree,
    cancel: &Cancellation,
) -> Result<PublishReport> {
    validate_tree(tree)?;
    cancel.check()?;
    let target = absolute(target)?;
    let parent = target
        .parent()
        .ok_or_else(|| YssError::new(ErrorCode::UnsafePath, "目标没有父目录"))?;
    let name = RelPath::new(
        target
            .file_name()
            .and_then(|s| s.to_str())
            .ok_or_else(|| YssError::new(ErrorCode::UnsafePath, "目标名称不合法"))?,
    )?;
    let root = Root::open(parent)?;
    if root.inspect(&name)? != FileSnapshot::Missing {
        return Err(
            YssError::new(ErrorCode::Conflict, "目标目录已存在，包括空目录").at(target.display()),
        );
    }
    let staging = RelPath::new(format!(".yss-stage-{}", unique_id()?))?;
    let identity = root.create_dir(&staging)?;
    let mut owned = Vec::new();
    let result = (|| {
        let mut directories: BTreeSet<RelPath> = tree.directories.clone();
        for path in tree.files.keys().chain(tree.directories.iter()) {
            for parent in path.parents() {
                directories.insert(RelPath::new(parent)?);
            }
        }
        for dir in &directories {
            cancel.check()?;
            let path = staging.join(dir.as_str())?;
            let identity = root.create_dir(&path)?;
            owned.push((path, FileSnapshot::Directory { identity }));
        }
        for (path, file) in &tree.files {
            cancel.check()?;
            let path = staging.join(path.as_str())?;
            let snapshot = root.create_file(&path, &file.bytes, file.mode)?;
            owned.push((path, snapshot));
        }
        for (path, expected) in &owned {
            if &root.inspect(path)? != expected {
                return Err(
                    YssError::new(ErrorCode::ConcurrentChange, "暂存文件在发布前改变").at(path),
                );
            }
        }
        cancel.check()?;
        root.rename(&staging, &name, &FileSnapshot::Missing, false)?;
        Ok(PublishReport {
            root: target.display().to_string(),
            files: tree.files.len(),
            directories: directories.len(),
        })
    })();
    if result.is_err() {
        for (path, expected) in owned.iter().rev() {
            let _ = root.remove(path, expected);
        }
        let _ = root.remove(&staging, &FileSnapshot::Directory { identity });
    }
    result
}
