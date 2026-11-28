use super::{HomeSource, ProjectRecord, Remote, git_remotes};
use crate::{cancel::Cancellation, error::Result, fs::git, workbench::process};
use std::{
    collections::BTreeMap,
    fs::{self, File},
    io::{ErrorKind, Read},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, SystemTime},
};

#[derive(Default)]
pub(in crate::workbench) struct SourceCache {
    config: Mutex<Option<Arc<GitConfig>>>,
    projects: Mutex<BTreeMap<String, LocalRead>>,
}

pub(super) struct GitConfig {
    program: PathBuf,
    executable: WatchedPath,
    paths: Vec<PathBuf>,
}

#[derive(Eq, PartialEq)]
struct WatchedPath {
    path: PathBuf,
    version: Option<FileVersion>,
}

#[derive(Eq, PartialEq)]
struct FileVersion {
    canonical: PathBuf,
    directory: bool,
    len: u64,
    modified: SystemTime,
    #[cfg(unix)]
    unix: (u64, u64, u32, u32, u32, i64, i64),
    #[cfg(windows)]
    windows: (u32, u64),
}

impl WatchedPath {
    fn capture(path: PathBuf) -> Option<Self> {
        let version = match fs::symlink_metadata(&path) {
            Ok(metadata) => {
                if !metadata.is_file() && !metadata.is_dir() {
                    return None;
                }
                #[cfg(unix)]
                use std::os::unix::fs::MetadataExt;
                #[cfg(windows)]
                use std::os::windows::fs::MetadataExt;
                #[cfg(windows)]
                if metadata.file_attributes() & 0x400 != 0 {
                    // A reparse point can redirect lookup without changing the file.
                    return None;
                }
                Some(FileVersion {
                    canonical: fs::canonicalize(&path).ok()?,
                    directory: metadata.is_dir(),
                    len: metadata.len(),
                    modified: metadata.modified().ok()?,
                    #[cfg(unix)]
                    unix: (
                        metadata.dev(),
                        metadata.ino(),
                        metadata.uid(),
                        metadata.gid(),
                        metadata.mode(),
                        metadata.ctime(),
                        metadata.ctime_nsec(),
                    ),
                    #[cfg(windows)]
                    windows: (metadata.file_attributes(), metadata.creation_time()),
                })
            }
            Err(error) if error.kind() == ErrorKind::NotFound => None,
            Err(_) => return None,
        };
        Some(Self { path, version })
    }

    fn is_current(&self) -> bool {
        Self::capture(self.path.clone()).as_ref() == Some(self)
    }
}

struct LocalRead {
    config: Arc<GitConfig>,
    proof: Vec<WatchedPath>,
    remotes: Vec<Remote>,
}

impl SourceCache {
    pub(super) fn config(&self, data_dir: &Path, cancel: &Cancellation) -> Option<Arc<GitConfig>> {
        let path = std::env::var_os("PATH")?;
        if std::env::split_paths(&path).any(|part| !part.is_absolute()) {
            return None;
        }
        let program = process::system_tool_path("git", &path)?;
        let executable = fs::canonicalize(&program).ok()?;
        let previous = self.config.lock().ok()?.clone();
        if let Some(previous) = previous
            && previous.program == program
            && previous.executable.path == executable
            && previous.executable.is_current()
        {
            return Some(previous);
        }
        let executable = WatchedPath::capture(executable)?;
        let mut paths = Vec::new();
        for name in ["GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL"] {
            let mut command = git::command(data_dir);
            command.args(["var", name]);
            let output = process::run_command(command, cancel, Duration::from_secs(5)).ok()?;
            let output = std::str::from_utf8(&output).ok()?;
            for path in output.lines().map(PathBuf::from) {
                if !path.is_absolute() {
                    return None;
                }
                paths.push(path);
            }
        }
        if paths.is_empty() || !executable.is_current() {
            return None;
        }
        let config = Arc::new(GitConfig {
            program,
            executable,
            paths,
        });
        *self.config.lock().ok()? = Some(config.clone());
        Some(config)
    }

    pub(super) fn retain(&self, projects: &[ProjectRecord]) {
        if let Ok(mut cache) = self.projects.lock() {
            cache.retain(|id, _| projects.iter().any(|project| &project.id == id));
        }
    }

    pub(super) fn remotes(
        &self,
        config: Option<&Arc<GitConfig>>,
        project: &ProjectRecord,
        homes: &[HomeSource],
        cancel: &Cancellation,
    ) -> Result<(Vec<Remote>, PathBuf)> {
        let root = Path::new(&project.root_path);
        if let Some(config) = config {
            let cached = self.projects.lock().ok().and_then(|cache| {
                let entry = cache.get(&project.id)?;
                if !Arc::ptr_eq(config, &entry.config) || entry.proof.first()?.path != root {
                    return None;
                }
                if !entry.proof.iter().all(WatchedPath::is_current) {
                    return None;
                }
                Some(entry.remotes.clone())
            });
            if let Some(remotes) = cached {
                return Ok((remotes, PathBuf::new()));
            }
        }
        let proof = config.and_then(|config| repository_proof(root, config));
        let (remotes, subpath, simple_config) = git_remotes(project, homes, cancel)?;
        if let (Some(config), Some(proof)) = (config, proof)
            && simple_config
            && subpath.as_os_str().is_empty()
            && proof.iter().all(WatchedPath::is_current)
            && let Ok(mut cache) = self.projects.lock()
        {
            cache.insert(
                project.id.clone(),
                LocalRead {
                    config: config.clone(),
                    proof,
                    remotes: remotes.clone(),
                },
            );
        }
        Ok((remotes, subpath))
    }
}

fn control_path(file: &Path, prefix: &str) -> Option<PathBuf> {
    let mut text = String::new();
    File::open(file)
        .ok()?
        .take(4097)
        .read_to_string(&mut text)
        .ok()?;
    if text.len() > 4096 {
        return None;
    }
    let path = text.trim_end_matches(['\r', '\n']).strip_prefix(prefix)?;
    if path.is_empty() || path.contains(['\r', '\n', '\0']) {
        return None;
    }
    Some(file.parent()?.join(path))
}

fn repository_proof(root: &Path, config: &GitConfig) -> Option<Vec<WatchedPath>> {
    let marker = root.join(".git");
    let root_version = WatchedPath::capture(root.to_owned())?;
    let marker_version = WatchedPath::capture(marker.clone())?;
    let git_dir = if marker_version.version.as_ref()?.directory {
        marker.clone()
    } else {
        control_path(&marker, "gitdir: ")?
    };
    let git_dir_version = WatchedPath::capture(git_dir.clone())?;
    let common_marker = WatchedPath::capture(git_dir.join("commondir"))?;
    let common_dir = if common_marker.version.is_some() {
        control_path(&common_marker.path, "")?
    } else {
        git_dir.clone()
    };
    let common_dir_version = WatchedPath::capture(common_dir.clone())?;
    // Git's split index adds dependencies with generated names. Use the full
    // probe for those worktrees and for configurations with include directives.
    for directory in [&git_dir, &common_dir] {
        for entry in fs::read_dir(directory).ok()? {
            if entry
                .ok()?
                .file_name()
                .to_string_lossy()
                .starts_with("sharedindex.")
            {
                return None;
            }
        }
    }
    let mut watched = vec![
        root_version,
        marker_version,
        git_dir_version,
        common_marker,
        common_dir_version,
    ];
    for path in config.paths.iter().cloned().chain([
        common_dir.join("config"),
        common_dir.join("objects"),
        common_dir.join("refs"),
        git_dir.join("gitdir"),
        git_dir.join("config.worktree"),
        git_dir.join("HEAD"),
        git_dir.join("index"),
        root.join(".gitmodules"),
    ]) {
        watched.push(WatchedPath::capture(path)?);
    }
    Some(watched)
}
