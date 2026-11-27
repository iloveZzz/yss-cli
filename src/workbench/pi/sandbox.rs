use super::workspace::{Projects, tool_path};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{Root, RootIdentity, alias_key, git::Boundary, unique_id},
    model::RelPath,
    workbench::process,
};
use std::{
    collections::{BTreeMap, BTreeSet},
    env,
    ffi::{OsStr, OsString},
    fs,
    io::Read,
    os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt},
    path::{Component, Path, PathBuf},
    process::{Command, ExitStatus},
    time::Duration,
};

mod pnpm;

const SANDBOX: &str = "/usr/bin/sandbox-exec";
const ALLOW_METADATA: &str = "(allow file-read-metadata file-test-existence (literal $PATH))";
const ALLOW_READ: &str = "(allow file-read* file-map-executable (subpath $PATH))";
const POLICY: &str = r#"
(version 1)
(deny default)
(import "system.sb")
(allow process-exec process-fork)
(deny signal process-info*)
(allow signal process-info* (target same-sandbox))
(deny mach-register)
(deny mach-lookup)
(allow mach-lookup (global-name "com.apple.secinitd" "com.apple.system.opendirectoryd.libinfo"))
(deny sysctl-read (sysctl-name-prefix "kern.procargs"))
(deny network*)
(deny file-write*)
(allow file-write-data (literal "/dev/null"))
(allow file-read* file-map-executable
  (subpath "/bin") (subpath "/sbin") (subpath "/usr/bin") (subpath "/usr/sbin")
  (subpath "/usr/libexec") (subpath "/usr/lib") (subpath "/System")
  (subpath "/private/var/db/timezone") (literal "/private/var/select/sh"))
"#;

pub(super) struct PrivateDir(PathBuf);

impl PrivateDir {
    pub fn new() -> Result<Self> {
        let path = Path::new("/private/tmp").join(format!("yss-pi-{}", unique_id()?));
        private_dir(&path)?;
        Ok(Self(path))
    }

    pub fn path(&self) -> &Path {
        &self.0
    }
}

impl Drop for PrivateDir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

pub(super) fn private_dir(path: &Path) -> Result<()> {
    fs::DirBuilder::new().mode(0o700).create(path)?;
    Ok(())
}

fn not_ready(message: &str) -> YssError {
    YssError::new(ErrorCode::NotReady, message)
}

fn overlaps(a: &Path, b: &Path) -> bool {
    a.starts_with(b) || b.starts_with(a)
}

#[derive(Default)]
struct ToolPathScope {
    home: Option<PathBuf>,
    excluded: Vec<PathBuf>,
}

impl ToolPathScope {
    fn allows(&self, path: &Path) -> bool {
        path.is_absolute()
            && path != Path::new("/")
            && !path.components().any(|part| match part {
                Component::ParentDir => true,
                Component::Normal(name) => name.to_str().is_some_and(|name| {
                    [".git", ".gitmodules", ".yss"]
                        .iter()
                        .any(|reserved| name.eq_ignore_ascii_case(reserved))
                }),
                _ => false,
            })
            && !self.excluded.iter().any(|other| overlaps(path, other))
            && !self.home.as_ref().is_some_and(|home| {
                home.starts_with(path)
                    || [
                        ".aws",
                        ".codex",
                        ".config",
                        ".git-credentials",
                        ".gnupg",
                        ".kube",
                        ".netrc",
                        ".npm",
                        ".npmrc",
                        ".pi",
                        ".ssh",
                    ]
                    .iter()
                    .any(|name| overlaps(path, &home.join(name)))
            })
    }
}

struct Toolchain {
    path: OsString,
    readable: BTreeSet<PathBuf>,
    metadata_only: BTreeSet<PathBuf>,
    homebrew_opt: Option<PathBuf>,
    scope: ToolPathScope,
    node: Option<PathBuf>,
    java_home: Option<PathBuf>,
    maven_repo: Option<PathBuf>,
    pnpm_store: Option<PathBuf>,
}

impl Toolchain {
    fn discover(mut excluded: Vec<PathBuf>, pi_config: &Path) -> Result<Self> {
        excluded.push(pi_config.to_owned());
        excluded.extend(fs::canonicalize(pi_config).ok());
        let scope = ToolPathScope {
            home: directories::BaseDirs::new().map(|dirs| dirs.home_dir().to_owned()),
            excluded,
        };
        let safe = |path: &Path| scope.allows(path);
        let directories: Vec<PathBuf> = env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute() && safe(path))
            .filter(|path| fs::canonicalize(path).is_ok_and(|real| real.is_dir() && safe(&real)))
            .collect();
        let on_path = |name: &str| {
            directories
                .iter()
                .filter_map(|dir| fs::canonicalize(dir.join(name)).ok())
                .find(|path| {
                    safe(path)
                        && path
                            .metadata()
                            .is_ok_and(|meta| meta.is_file() && meta.mode() & 0o111 != 0)
                })
        };
        let mut readable = BTreeSet::new();
        let mut metadata_only = BTreeSet::new();
        for path in directories.iter().map(PathBuf::as_path).chain([
            Path::new("/usr/local/lib"),
            Path::new("/opt/homebrew/Cellar"),
            Path::new("/Library/Java/JavaVirtualMachines"),
        ]) {
            if let Ok(resolved) = fs::canonicalize(path)
                && safe(&resolved)
            {
                readable.insert(resolved);
            }
        }
        // Homebrew binaries reference dynamic libraries through opt symlinks. Allow
        // traversal there while keeping file data and executable maps in the Cellar.
        let homebrew_opt = PathBuf::from("/opt/homebrew/opt");
        let homebrew_opt = (readable.contains(Path::new("/opt/homebrew/Cellar"))
            && safe(&homebrew_opt)
            && fs::canonicalize(&homebrew_opt)
                .is_ok_and(|path| path == homebrew_opt && path.is_dir()))
        .then_some(homebrew_opt);
        for name in ["python3", "python"] {
            if let Some(executable) = on_path(name)
                && let Some((bin, version)) = executable
                    .parent()
                    .zip(executable.parent().and_then(Path::parent))
                && bin.ends_with("bin")
                && version.parent()
                    == Some(Path::new("/Library/Frameworks/Python.framework/Versions"))
                && safe(version)
                && version.join("Python").is_file()
            {
                readable.insert(version.to_owned());
            }
        }
        let java_home = env::var_os("JAVA_HOME")
            .and_then(|home| fs::canonicalize(home).ok())
            .filter(|path| safe(path) && path.join("bin/java").is_file())
            .or_else(|| {
                let java = on_path("java")?;
                if java == Path::new("/usr/bin/java") {
                    return None;
                }
                let parent = java.parent()?.parent()?;
                parent.join("lib").is_dir().then(|| parent.to_owned())
            });
        if let Some(home) = &java_home {
            readable.insert(home.clone());
        }
        if let Some(mvn) = on_path("mvn")
            && let Some(distribution) = mvn.parent().and_then(Path::parent)
            && safe(distribution)
            && distribution.join("boot").is_dir()
        {
            readable.insert(distribution.to_owned());
        }
        let node = on_path("node");
        if let Some(node) = &node {
            readable.insert(node.clone());
            if let Some(distribution) = node.parent().and_then(Path::parent)
                && safe(distribution)
                && distribution.join("lib/node_modules").is_dir()
            {
                readable.insert(distribution.join("lib"));
            }
        }
        if let Some(pnpm) = on_path("pnpm") {
            readable.insert(pnpm);
        }
        let pnpm_home = env::var_os("PNPM_HOME")
            .map(PathBuf::from)
            .or_else(|| scope.home.as_ref().map(|home| home.join("Library/pnpm")));
        if let Some(home) = pnpm_home.as_deref().filter(|home| safe(home)) {
            for directory in &directories {
                let shim = directory.join("pnpm");
                if let Some((package, alias)) = global_pnpm_runtime(home, &shim, safe) {
                    readable.insert(package);
                    metadata_only.extend(alias.ancestors().skip(1).map(Path::to_owned));
                }
            }
        }
        let cache = |name: &str, suffix: &str| -> Result<Option<PathBuf>> {
            let Some(path) = env::var_os(name)
                .map(PathBuf::from)
                .or_else(|| scope.home.as_ref().map(|home| home.join(suffix)))
            else {
                return Ok(None);
            };
            if !path.is_absolute() || !safe(&path) {
                return Err(not_ready("Pi 离线缓存路径必须是独立的绝对目录"));
            }
            match fs::canonicalize(path) {
                Ok(path) if path.is_dir() && safe(&path) => Ok(Some(path)),
                Err(error)
                    if error.kind() == std::io::ErrorKind::NotFound
                        && env::var_os(name).is_none() =>
                {
                    Ok(None)
                }
                _ => Err(not_ready("Pi 离线缓存路径不可用")),
            }
        };
        let maven_repo = cache("YSS_PI_MAVEN_REPOSITORY", ".m2/repository")?;
        let pnpm_store = cache("YSS_PI_PNPM_STORE", "Library/pnpm/store")?;
        readable.extend(maven_repo.iter().chain(pnpm_store.iter()).cloned());
        let path = env::join_paths(directories).map_err(|_| not_ready("Pi 命令 PATH 无效"))?;
        Ok(Self {
            path,
            readable,
            metadata_only,
            homebrew_opt,
            scope,
            node,
            java_home,
            maven_repo,
            pnpm_store,
        })
    }
}

fn global_pnpm_runtime(
    home: &Path,
    shim: &Path,
    safe: impl Fn(&Path) -> bool,
) -> Option<(PathBuf, PathBuf)> {
    let directory = shim.parent()?;
    if directory != home && directory != home.join("bin") {
        return None;
    }
    let shim = fs::canonicalize(shim).ok().filter(|p| safe(p))?;
    if !shim.starts_with(fs::canonicalize(home).ok()?) {
        return None;
    }
    const SHIM_LIMIT: u64 = 16 * 1024;
    let file = fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
        .open(shim)
        .ok()?;
    if !file.metadata().is_ok_and(|metadata| metadata.is_file()) {
        return None;
    }
    let mut text = String::new();
    file.take(SHIM_LIMIT + 1).read_to_string(&mut text).ok()?;
    if text.len() as u64 > SHIM_LIMIT || !text.starts_with("#!/bin/sh\n") {
        return None;
    }
    let mut targets = text
        .lines()
        .filter_map(|line| line.strip_prefix("# cmd-shim-target="));
    let target = PathBuf::from(targets.next()?);
    let global = home.join("global");
    if targets.next().is_some() || !target.starts_with(&global) || !safe(&target) {
        return None;
    }
    let package = target.parent()?;
    let package = match target.file_name()?.to_str()? {
        "pnpm" => package,
        "pnpm.cjs" | "pnpm.mjs" if package.ends_with("bin") => package.parent()?,
        _ => return None,
    };
    let pnpm_package = |path: &Path| {
        path.ends_with("node_modules/pnpm") || path.ends_with("node_modules/@pnpm/exe")
    };
    if !pnpm_package(package) || !safe(package) {
        return None;
    }
    let resolved_global = fs::canonicalize(global).ok().filter(|p| safe(p))?;
    let resolved_package = fs::canonicalize(package).ok().filter(|p| safe(p))?;
    let resolved_target = fs::canonicalize(&target).ok().filter(|p| safe(p))?;
    (resolved_package.starts_with(resolved_global)
        && pnpm_package(&resolved_package)
        && resolved_target.starts_with(&resolved_package)
        && resolved_target.is_file())
    .then_some((resolved_package, target))
}

pub(super) struct Sandbox {
    scratch: PathBuf,
    profile: String,
    parameters: Vec<OsString>,
    launcher_grants_offset: usize,
    toolchain: Toolchain,
    roots: BTreeMap<PathBuf, RootIdentity>,
    target_scoped: bool,
    boundaries: WorkspaceBoundaries,
}

#[derive(Default)]
struct WorkspaceBoundaries {
    protected: BTreeSet<PathBuf>,
    unselected: BTreeSet<PathBuf>,
}

impl WorkspaceBoundaries {
    fn paths(&self) -> impl Iterator<Item = &PathBuf> {
        self.protected.iter().chain(&self.unselected)
    }
}

fn path_parameter(params: &mut Vec<OsString>, path: &Path) -> String {
    let name = format!("PATH_{}", params.len());
    let mut value = OsString::from(&name);
    value.push("=");
    value.push(path);
    params.push(value);
    format!("(param \"{name}\")")
}

fn parameter(profile: &mut String, params: &mut Vec<OsString>, rule: &str, path: &Path) {
    profile.push_str(&rule.replace("$PATH", &path_parameter(params, path)));
    profile.push('\n');
}

impl Sandbox {
    pub fn new(
        private: &PrivateDir,
        pi_config: &Path,
        projects: &Projects,
        unselected_projects: &[PathBuf],
        target_scoped: bool,
        cancel: &Cancellation,
    ) -> Result<Self> {
        let roots: BTreeMap<_, _> = projects
            .roots()
            .map(|root| (root.path.clone(), root.identity.clone()))
            .collect();
        let mut boundaries = WorkspaceBoundaries {
            protected: projects.protected().clone(),
            unselected: unselected_projects
                .iter()
                .filter(|path| !roots.contains_key(*path))
                .cloned()
                .collect(),
        };
        for root in roots.keys() {
            if boundaries
                .protected
                .iter()
                .any(|path| root.starts_with(path))
            {
                return Err(not_ready("所选工程与受保护目录重叠，无法授权离线命令"));
            }
            boundaries
                .protected
                .extend(check_workspace(root, &boundaries, &roots, cancel)?);
        }
        let excluded: Vec<_> = projects
            .repository_paths()
            .map(Path::to_owned)
            .chain(boundaries.paths().cloned())
            .chain(std::iter::once(private.path().to_owned()))
            .collect();
        let tools = Toolchain::discover(excluded, pi_config)?;
        let paths: Vec<_> = roots.keys().cloned().collect();
        let mut sandbox = Self::build(private, &paths, boundaries, tools, target_scoped)?;
        sandbox.roots = roots;
        Ok(sandbox)
    }

    fn build(
        private: &PrivateDir,
        roots: &[PathBuf],
        boundaries: WorkspaceBoundaries,
        toolchain: Toolchain,
        target_scoped: bool,
    ) -> Result<Self> {
        let scratch = private.path().join("scratch");
        private_dir(&scratch)?;
        let mut profile = POLICY.to_owned();
        let mut parameters = Vec::new();
        // Toolchains resolve ancestors before opening files in an allowed directory.
        let ancestors: BTreeSet<&Path> = toolchain
            .readable
            .iter()
            .chain(toolchain.homebrew_opt.iter())
            .chain(roots)
            .chain(std::iter::once(&scratch))
            .flat_map(|path| path.ancestors().skip(1))
            .chain(toolchain.metadata_only.iter().map(PathBuf::as_path))
            .collect();
        for path in ancestors {
            parameter(&mut profile, &mut parameters, ALLOW_METADATA, path);
        }
        for path in &toolchain.readable {
            parameter(&mut profile, &mut parameters, ALLOW_READ, path);
        }
        if let Some(opt) = &toolchain.homebrew_opt {
            parameter(
                &mut profile,
                &mut parameters,
                "(allow file-read-metadata file-test-existence (subpath $PATH))",
                opt,
            );
        }
        let launcher_grants_offset = profile.len();
        for path in roots {
            parameter(
                &mut profile,
                &mut parameters,
                if target_scoped {
                    ALLOW_READ
                } else {
                    "(allow file-read* file-write* file-map-executable (subpath $PATH)) (deny file-write* (literal $PATH))"
                },
                path,
            );
        }
        parameter(
            &mut profile,
            &mut parameters,
            "(allow file-read* file-write* file-map-executable (subpath $PATH)) (deny file-write* (literal $PATH))",
            &scratch,
        );
        if target_scoped {
            profile.push_str(
                r#"(allow file-write* (subpath (param "TARGET_ROOT")))
(deny file-write* (literal (param "TARGET_ROOT")))
"#,
            );
        }
        for path in &boundaries.unselected {
            let outside = path_parameter(&mut parameters, path);
            let exceptions: String = roots
                .iter()
                .filter(|root| root.starts_with(path))
                .map(|root| {
                    let selected = path_parameter(&mut parameters, root);
                    format!(" (require-not (subpath {selected}))")
                })
                .collect();
            profile.push_str(&format!(
                "(deny file-read* file-write* file-map-executable (require-all (subpath {outside}){exceptions}))\n"
            ));
        }
        for path in &boundaries.protected {
            parameter(
                &mut profile,
                &mut parameters,
                "(deny file-read* file-write* file-map-executable (subpath $PATH))",
                path,
            );
        }
        // Keep selected and protected subtrees anchored even when the command
        // can write beside them in an ancestor of the same checkout.
        let ancestors: BTreeSet<_> = boundaries
            .paths()
            .chain(roots)
            .flat_map(|path| path.ancestors().skip(1))
            .filter(|path| roots.iter().any(|root| path.starts_with(root)))
            .collect();
        for path in ancestors {
            parameter(
                &mut profile,
                &mut parameters,
                "(deny file-write* (literal $PATH))",
                path,
            );
        }
        profile.push_str(
            r#"
(deny file-read* file-write* file-map-executable (regex #"^.*/[.]([Gg][Ii][Tt]([Mm][Oo][Dd][Uu][Ll][Ee][Ss])?|[Yy][Ss][Ss])(/|$)"))
(deny file-link)
"#,
        );
        Ok(Self {
            scratch,
            profile,
            parameters,
            launcher_grants_offset,
            toolchain,
            roots: BTreeMap::new(),
            target_scoped,
            boundaries,
        })
    }

    fn command(
        &self,
        root: &Path,
        cwd: &Path,
        script: &str,
        pinned: Option<&pnpm::PinnedLauncher>,
    ) -> Result<Command> {
        let mut command = Command::new(SANDBOX);
        let mut profile = self.profile.clone();
        let mut params = self.parameters.clone();
        let path =
            if let Some(pinned) = pinned {
                let mut grants = String::new();
                let paths = std::iter::once(&pinned.bin).chain(pinned.package.iter());
                let ancestors: BTreeSet<_> = paths
                    .clone()
                    .flat_map(|path| path.ancestors().skip(1))
                    .collect();
                for ancestor in ancestors {
                    parameter(&mut grants, &mut params, ALLOW_METADATA, ancestor);
                }
                for path in paths {
                    parameter(&mut grants, &mut params, ALLOW_READ, path);
                }
                profile.insert_str(self.launcher_grants_offset, &grants);
                env::join_paths(std::iter::once(pinned.bin.clone()).chain(
                    env::split_paths(&self.toolchain.path).filter(|path| path.is_absolute()),
                ))
                .map_err(|_| not_ready("Pi 命令 PATH 无效"))?
            } else {
                self.toolchain.path.clone()
            };
        if self.target_scoped {
            let mut target = OsString::from("TARGET_ROOT=");
            target.push(root);
            command.arg("-D").arg(target);
            for other in self
                .roots
                .keys()
                .filter(|other| *other != root && other.starts_with(root))
            {
                parameter(
                    &mut profile,
                    &mut params,
                    "(deny file-write* (subpath $PATH))",
                    other,
                );
            }
        }
        for arg in params {
            command.arg("-D").arg(arg);
        }
        command
            .args([
                "-p",
                &profile,
                "/bin/bash",
                "--noprofile",
                "--norc",
                "-c",
                script,
            ])
            .current_dir(cwd)
            .env_clear()
            .env("HOME", &self.scratch)
            .env("TMPDIR", &self.scratch)
            .env("PATH", path)
            .env("LANG", "en_US.UTF-8")
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .env("GIT_OPTIONAL_LOCKS", "0")
            .env("npm_config_offline", "true")
            .env("pnpm_config_offline", "true")
            .env("npm_config_package_import_method", "copy")
            .env("pnpm_config_package_import_method", "copy")
            // pnpm prepends ancestor node_modules paths. Use the shell's absolute path
            // so posix_spawn doesn't try paths outside the readable roots first.
            .env("npm_config_script_shell", "/bin/bash")
            .env("pnpm_config_script_shell", "/bin/bash")
            .env("MAVEN_ARGS", "-o");
        if let Some(java) = &self.toolchain.java_home {
            command.env("JAVA_HOME", java);
        }
        if let Some(store) = &self.toolchain.pnpm_store {
            command
                .env("npm_config_store_dir", store)
                .env("pnpm_config_store_dir", store);
        }
        let mut java_options = OsString::from("\"-Djava.io.tmpdir=");
        java_options.push(&self.scratch);
        java_options.push("\" \"-Duser.home=");
        java_options.push(&self.scratch);
        java_options.push("\"");
        if let Some(repo) = &self.toolchain.maven_repo {
            java_options.push(" \"-Dmaven.repo.local=");
            java_options.push(repo);
            java_options.push("\"");
        }
        command.env("JAVA_TOOL_OPTIONS", java_options);
        Ok(command)
    }

    pub fn run(
        &self,
        root: &Path,
        cwd: &Path,
        script: &str,
        cancel: &Cancellation,
        on_output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
    ) -> Result<(ExitStatus, Vec<u8>, Vec<u8>)> {
        if !self.roots.contains_key(root) || !cwd.starts_with(root) {
            return Err(not_ready("离线命令需要已授权的工程目录"));
        }
        for (root, identity) in &self.roots {
            if Root::open(root)?.identity != *identity
                || check_workspace(root, &self.boundaries, &self.roots, cancel)?
                    .iter()
                    .any(|path| !self.boundaries.protected.contains(path))
            {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "离线命令的工程文件边界已改变，请重新发起本轮",
                ));
            }
        }
        let pinned = pnpm::PinnedLauncher::prepare(
            root,
            cwd,
            self.scratch.parent().expect("scratch has a private parent"),
            self.toolchain.node.as_deref(),
            |path| self.toolchain.scope.allows(path),
        )?;
        process::capture_command_output(
            self.command(root, cwd, script, pinned.as_ref())?,
            cancel,
            Duration::from_secs(600),
            on_output,
        )
    }
}

fn check_workspace(
    root: &Path,
    boundaries: &WorkspaceBoundaries,
    selected: &BTreeMap<PathBuf, RootIdentity>,
    cancel: &Cancellation,
) -> Result<Vec<PathBuf>> {
    let identity = Root::open(root)?.identity;
    let boundary = Boundary::probe(root)?;
    let scoped_aliases: BTreeSet<_> = selected
        .keys()
        .chain(&boundaries.unselected)
        .filter_map(|path| path.strip_prefix(root).ok()?.to_str().map(alias_key))
        .collect();
    let mut blocked: BTreeSet<PathBuf> = boundary
        .blocked
        .iter()
        .filter(|path| !scoped_aliases.contains(*path))
        .map(|path| root.join(path))
        .collect();
    let scoped_boundaries: Vec<_> = boundaries
        .paths()
        .filter(|path| path.starts_with(root))
        .collect();
    let mut directories = vec![root.to_owned()];
    let mut entries = 0;
    while let Some(dir) = directories.pop() {
        cancel.check()?;
        for entry in fs::read_dir(dir)? {
            cancel.check()?;
            entries += 1;
            let path = entry?.path();
            if entries > 200_000 {
                return Err(not_ready("Pi 工作区超出文件检查上限"));
            }
            if selected.contains_key(&path)
                || scoped_boundaries
                    .iter()
                    .any(|blocked| path.starts_with(blocked))
            {
                continue;
            }
            let relative = path.strip_prefix(root).expect("project descendant");
            let relative = RelPath::new(
                relative
                    .to_str()
                    .ok_or_else(|| not_ready("Pi 工作区包含非 UTF-8 路径"))?,
            )?;
            let metadata = fs::symlink_metadata(&path)?;
            if boundary.check(&relative).is_err() || !tool_path(&relative) {
                blocked.insert(path);
                continue;
            }
            if metadata.is_dir() {
                if fs::symlink_metadata(path.join(".git")).is_ok() {
                    blocked.insert(path);
                } else {
                    directories.push(path);
                }
            } else if metadata.is_file() && metadata.nlink() != 1 {
                return Err(
                    YssError::new(ErrorCode::UnsafePath, "Pi 命令工作区含硬链接").at(&relative),
                );
            }
        }
    }
    if Root::open(root)?.identity != identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "Pi 命令工作区已改变",
        ));
    }
    Ok(blocked.into_iter().collect())
}

pub(super) fn probe(cancel: &Cancellation) -> Result<()> {
    let private = PrivateDir::new()?;
    let root = private.path().join("probe");
    private_dir(&root)?;
    let shared = root.join("shared-directory");
    private_dir(&shared)?;
    let nested = shared.join("nested-project");
    let other = private.path().join("other-project");
    let unselected = root.join("unselected-project");
    let selected_child = unselected.join("selected-project");
    let roots = [&root, &nested, &other, &selected_child];
    for path in [&nested, &other, &unselected, &selected_child] {
        private_dir(path)?;
        fs::write(path.join("sentinel"), "original")?;
    }
    let root_ids = roots
        .into_iter()
        .map(|path| Ok((path.clone(), Root::open(path)?.identity)))
        .collect::<Result<BTreeMap<_, _>>>()?;
    let outside = private.path().join("sentinel");
    fs::write(&outside, "original")?;
    fs::write(root.join(".git"), "original")?;
    fs::write(root.join(".yss"), "original")?;
    std::os::unix::fs::symlink(&outside, root.join("link"))?;
    let listener = std::net::TcpListener::bind(("127.0.0.1", 0))?;
    let port = listener.local_addr()?.port().to_string();
    let toolchain = Toolchain {
        path: OsStr::new("/usr/bin:/bin").to_owned(),
        readable: BTreeSet::new(),
        metadata_only: BTreeSet::new(),
        homebrew_opt: None,
        scope: ToolPathScope::default(),
        node: None,
        java_home: None,
        maven_repo: None,
        pnpm_store: None,
    };
    let mut sandbox = Sandbox::build(
        &private,
        &roots.map(PathBuf::clone),
        WorkspaceBoundaries {
            unselected: BTreeSet::from([unselected]),
            ..WorkspaceBoundaries::default()
        },
        toolchain,
        true,
    )?;
    sandbox.roots = root_ids;
    let probe = r#"
printf allowed > allowed || exit 1
if printf denied > ../sentinel || printf denied > .git || printf denied > .yss || printf denied > link; then exit 2; fi
if /bin/ln allowed alias; then exit 3; fi
if printf denied > "/dev/tcp/127.0.0.1/$1"; then exit 4; fi
test "$(/bin/cat shared-directory/nested-project/sentinel)" = original || exit 5
test "$(/bin/cat ../other-project/sentinel)" = original || exit 6
test "$(/bin/cat unselected-project/selected-project/sentinel)" = original || exit 7
if printf denied > shared-directory/nested-project/sentinel || printf denied > ../other-project/sentinel || printf denied > unselected-project/selected-project/sentinel; then exit 8; fi
if /bin/cat unselected-project/sentinel || /bin/cat .git || /bin/cat .yss; then exit 9; fi
if /bin/mv shared-directory moved; then exit 10; fi
printf allowed > shared-directory/own || exit 11
printf allowed > "$TMPDIR/probe" || exit 12
"#;
    let mut command = sandbox.command(&root, &root, probe, None)?;
    command.args(["sandbox-probe", &port]);
    let (status, _, _) = process::capture_command(command, cancel, Duration::from_secs(5))?;
    if !status.success()
        || fs::read_to_string(root.join("allowed"))? != "allowed"
        || fs::read_to_string(&outside)? != "original"
        || fs::read_to_string(root.join(".git"))? != "original"
        || fs::read_to_string(root.join(".yss"))? != "original"
        || root.join("alias").exists()
    {
        return Err(not_ready(
            "macOS Seatbelt 未通过工作区和断网探测，离线命令已关闭",
        ));
    }
    Ok(())
}
