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
    os::unix::fs::{DirBuilderExt, MetadataExt},
    path::{Path, PathBuf},
    process::{Command, ExitStatus},
    time::Duration,
};

const SANDBOX: &str = "/usr/bin/sandbox-exec";
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

struct Toolchain {
    path: OsString,
    readable: BTreeSet<PathBuf>,
    java_home: Option<PathBuf>,
    maven_repo: Option<PathBuf>,
    pnpm_store: Option<PathBuf>,
}

impl Toolchain {
    fn discover(excluded: &[PathBuf], pi_config: &Path) -> Result<Self> {
        let home = directories::BaseDirs::new().map(|dirs| dirs.home_dir().to_owned());
        let safe = |path: &Path| {
            path != Path::new("/")
                && !excluded.iter().any(|other| overlaps(path, other))
                && !overlaps(path, pi_config)
                && !home.as_ref().is_some_and(|other| {
                    other.starts_with(path)
                        || [".pi", ".ssh", ".codex", ".config", ".npm", ".gnupg"]
                            .iter()
                            .any(|directory| overlaps(path, &other.join(directory)))
                })
        };
        let directories: Vec<PathBuf> = env::split_paths(&env::var_os("PATH").unwrap_or_default())
            .filter(|path| path.is_absolute() && safe(path))
            .filter(|path| fs::canonicalize(path).is_ok_and(|real| real.is_dir() && safe(&real)))
            .collect();
        let on_path = |name: &str| {
            directories
                .iter()
                .find_map(|dir| fs::canonicalize(dir.join(name)).ok())
                .filter(|path| path.is_file() && safe(path))
        };
        let mut readable = BTreeSet::new();
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
        if let Some(node) = on_path("node") {
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
        let cache = |name: &str, suffix: &str| -> Result<Option<PathBuf>> {
            let Some(path) = env::var_os(name)
                .map(PathBuf::from)
                .or_else(|| home.as_ref().map(|home| home.join(suffix)))
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
            java_home,
            maven_repo,
            pnpm_store,
        })
    }
}

pub(super) struct Sandbox {
    scratch: PathBuf,
    profile: String,
    parameters: Vec<OsString>,
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
        let tools = Toolchain::discover(&excluded, pi_config)?;
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
            .chain(roots)
            .chain(std::iter::once(&scratch))
            .flat_map(|path| path.ancestors().skip(1))
            .collect();
        for path in ancestors {
            parameter(
                &mut profile,
                &mut parameters,
                "(allow file-read-metadata file-test-existence (literal $PATH))",
                path,
            );
        }
        for path in &toolchain.readable {
            parameter(
                &mut profile,
                &mut parameters,
                "(allow file-read* file-map-executable (subpath $PATH))",
                path,
            );
        }
        for path in roots {
            parameter(
                &mut profile,
                &mut parameters,
                if target_scoped {
                    "(allow file-read* file-map-executable (subpath $PATH))"
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
            toolchain,
            roots: BTreeMap::new(),
            target_scoped,
            boundaries,
        })
    }

    fn command(&self, root: &Path, cwd: &Path, script: &str) -> Command {
        let mut command = Command::new(SANDBOX);
        let mut profile = self.profile.clone();
        let mut params = self.parameters.clone();
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
            .env("PATH", &self.toolchain.path)
            .env("LANG", "en_US.UTF-8")
            .env("GIT_OPTIONAL_LOCKS", "0")
            .env("npm_config_offline", "true")
            .env("npm_config_package_import_method", "copy")
            // pnpm prepends ancestor node_modules paths. Use the shell's absolute path
            // so posix_spawn doesn't try paths outside the readable roots first.
            .env("npm_config_script_shell", "/bin/bash")
            .env("MAVEN_ARGS", "-o");
        if let Some(java) = &self.toolchain.java_home {
            command.env("JAVA_HOME", java);
        }
        if let Some(store) = &self.toolchain.pnpm_store {
            command.env("npm_config_store_dir", store);
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
        command
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
        process::capture_command_output(
            self.command(root, cwd, script),
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
    let mut command = sandbox.command(&root, &root, probe);
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
            "macOS Seatbelt 未通过工作区和断网探测，Pi 运行入口已关闭",
        ));
    }
    Ok(())
}
