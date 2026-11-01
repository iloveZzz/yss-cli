use crate::{
    error::{ErrorCode, Result, YssError},
    model::{MAX_MANIFEST, RelPath, Sha256},
};
use std::{
    collections::BTreeSet,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
};

fn fail() -> YssError {
    YssError::new(ErrorCode::UnsafePath, "Git 边界无法可靠核验")
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
    let mut child = command(root)
        .args(args)
        .stdin(if input.is_some() {
            Stdio::piped()
        } else {
            Stdio::null()
        })
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|_| fail())?;
    if let Some(input) = input {
        child
            .stdin
            .take()
            .ok_or_else(fail)?
            .write_all(input)
            .map_err(|_| fail())?;
    }
    let mut bytes = Vec::new();
    child
        .stdout
        .take()
        .ok_or_else(fail)?
        .take(MAX_MANIFEST + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| fail())?;
    if bytes.len() as u64 > MAX_MANIFEST {
        let _ = child.kill();
        let _ = child.wait();
        return Err(fail());
    }
    let status = child.wait().map_err(|_| fail())?;
    if !status.success() && !(allow_one && status.code() == Some(1)) {
        return Err(fail());
    }
    Ok(bytes)
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
                    .map_err(|_| fail())?
                    .trim_end_matches(['\r', '\n']),
            );
            let prefix = root.strip_prefix(&top).map_err(|_| fail())?;
            let prefix = super::alias_key(&prefix.to_str().ok_or_else(fail)?.replace('\\', "/"));
            let index = output(repository, &["ls-files", "--stage", "-z"], None, false)?;
            let mut paths = BTreeSet::new();
            for entry in index.split(|b| *b == 0).filter(|s| !s.is_empty()) {
                let entry = std::str::from_utf8(entry).map_err(|_| fail())?;
                let (metadata, path) = entry.split_once('\t').ok_or_else(fail)?;
                if metadata.split_whitespace().next() == Some("160000") {
                    paths.insert(RelPath::new(path)?);
                }
            }
            let modules = top.join(".gitmodules");
            if let Ok(metadata) = std::fs::symlink_metadata(&modules) {
                if !metadata.is_file()
                    || metadata.file_type().is_symlink()
                    || metadata.len() > MAX_MANIFEST
                {
                    return Err(fail());
                }
                let dir = super::open_absolute(&top)?;
                let bytes = super::read_in_dir(&dir, ".gitmodules", MAX_MANIFEST)?.bytes;
                let values = output(
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
                )?;
                for entry in values.split(|b| *b == 0).filter(|s| !s.is_empty()) {
                    let entry = std::str::from_utf8(entry).map_err(|_| fail())?;
                    let (_, path) = entry.split_once('\n').ok_or_else(fail)?;
                    paths.insert(RelPath::new(path)?);
                }
            }
            for path in paths {
                fingerprint.extend_from_slice(path.as_str().as_bytes());
                fingerprint.push(0);
                let path = super::alias_key(path.as_str());
                if prefix.is_empty() {
                    blocked.insert(path);
                } else if path == prefix || prefix.starts_with(&format!("{path}/")) {
                    return Err(fail());
                } else if let Some(path) = path.strip_prefix(&format!("{prefix}/")) {
                    blocked.insert(path.to_owned());
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
        let key = super::alias_key(path.as_str());
        if path
            .as_str()
            .split('/')
            .any(|p| p.eq_ignore_ascii_case(".git"))
            || self
                .blocked
                .iter()
                .any(|p| &key == p || key.starts_with(&format!("{p}/")))
        {
            return Err(YssError::new(ErrorCode::UnsafePath, "路径穿越 Git 或子模块边界").at(path));
        }
        Ok(())
    }
    pub fn revalidate(&self) -> Result<()> {
        if Self::probe(&self.root)?.fingerprint != self.fingerprint {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "Git 边界在操作期间改变",
            ));
        }
        Ok(())
    }
}
