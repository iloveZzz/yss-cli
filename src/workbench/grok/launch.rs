use super::{acp::Acp, not_ready};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result},
    fs::{Root, unique_id},
    model::{RelPath, Sha256},
    workbench::{agent_protocol::PiModel, jobs::JobContext, process},
};
use std::{
    ffi::{CStr, OsStr, OsString},
    fs::{self, Metadata},
    mem::MaybeUninit,
    os::unix::{
        ffi::OsStrExt,
        fs::{DirBuilderExt, MetadataExt},
    },
    path::{Path, PathBuf},
    process::Command,
    ptr,
    time::Duration,
};

pub(super) const VERSION: &str = "1.0.92";
const SIGNATURE: &str = "-R=anchor apple generic and certificate leaf[subject.OU] = \"5Y6N3AJ54S\" and identifier \"xai-grok-pager\"";
const LAUNCHER: &str = "/usr/bin/sandbox-exec";
const LOGIN_COMMAND: &str = r#"GROK_HOME="$HOME/.yss-grok-build" "$HOME/.grok/bin/grok" login"#;

const POLICY: &str = r#"
(version 1)
(deny default)
(import "system.sb")
(deny process-fork process-exec)
(allow process-exec (literal (param "BIN")))
(deny signal process-info*)
(allow signal process-info* (target same-sandbox))
(deny mach-register mach-lookup)
(allow mach-lookup (global-name "com.apple.secinitd" "com.apple.system.opendirectoryd.libinfo" "com.apple.mDNSResponder"))
(deny sysctl-read (sysctl-name-prefix "kern.procargs"))
(deny network*)
(allow network-outbound (remote tcp "*:443"))
(deny network-outbound (remote tcp "localhost:443"))
(allow network-outbound (remote unix-socket (literal "/private/var/run/mDNSResponder")))
(deny file-write*)
(allow file-write-data (literal "/dev/null"))
(allow file-read* file-map-executable
  (subpath "/usr/lib") (subpath "/System/Library") (subpath "/private/var/db/timezone")
  (literal "/private/etc/hosts") (literal "/private/etc/resolv.conf")
  (literal "/private/etc/ssl/openssl.cnf") (literal "/private/etc/ssl/cert.pem")
  (literal (param "BIN")))
(allow file-read* file-write* (subpath (param "SCRATCH")))
(allow file-read* (literal (param "CWD")))
(allow file-read* (literal (param "AUTH")))
(allow file-read* file-write* (subpath (param "SESSIONS")))
(deny file-link)
"#;

pub(super) fn credentials() -> crate::error::YssError {
    not_ready("credentials", "请先在本机终端完成 Grok Build 专用配置登录")
        .context("loginCommand", LOGIN_COMMAND)
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Stamp {
    device: u64,
    inode: u64,
    length: u64,
    modified: i64,
    modified_ns: i64,
    changed: i64,
    changed_ns: i64,
}

impl From<Metadata> for Stamp {
    fn from(value: Metadata) -> Self {
        Self {
            device: value.dev(),
            inode: value.ino(),
            length: value.len(),
            modified: value.mtime(),
            modified_ns: value.mtime_nsec(),
            changed: value.ctime(),
            changed_ns: value.ctime_nsec(),
        }
    }
}

#[derive(Debug)]
pub(super) struct Installation {
    pub binary: PathBuf,
    profile: PathBuf,
    auth: PathBuf,
    binary_stamp: Stamp,
    auth_stamp: Stamp,
}

pub(super) struct Scratch(PathBuf);
impl Scratch {
    pub(super) fn new() -> Result<Self> {
        let path = Path::new("/private/tmp").join(format!("yss-grok-{}", unique_id()?));
        fs::DirBuilder::new().mode(0o700).create(&path)?;
        Ok(Self(path))
    }

    pub(super) fn path(&self) -> &Path {
        &self.0
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn owned(metadata: &Metadata) -> bool {
    metadata.uid() == unsafe { libc::geteuid() } && metadata.mode() & 0o022 == 0
}

fn real_directory(path: &Path) -> Result<()> {
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_dir() || !owned(&metadata) || Root::open(path)?.path != path {
        return Err(not_ready(
            "configuration",
            "Grok Build 专用目录无法安全核对",
        ));
    }
    Ok(())
}

fn directory(path: &Path) -> Result<()> {
    match fs::DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error.into()),
    }
    real_directory(path)
}

fn auth_stamp(path: &Path) -> Result<Stamp> {
    let metadata = fs::symlink_metadata(path).map_err(|_| credentials())?;
    if !metadata.is_file()
        || !owned(&metadata)
        || metadata.mode() & 0o077 != 0
        || metadata.nlink() != 1
        || metadata.len() == 0
    {
        return Err(not_ready(
            "configuration",
            "Grok Build 专用登录文件的权限或身份无法核对",
        ));
    }
    Ok(metadata.into())
}

fn verify_signature(path: &Path, cancel: &Cancellation) -> Result<()> {
    let mut command = Command::new("/usr/bin/codesign");
    command
        .env_clear()
        .current_dir("/private/tmp")
        .args(["--verify", "--strict", SIGNATURE])
        .arg(path);
    process::run_command(command, cancel, Duration::from_secs(5)).map_err(|error| {
        if error.code == ErrorCode::Cancelled {
            error
        } else {
            not_ready("executable", "无法验证 Grok Build 可执行文件的 xAI 签名")
        }
    })?;
    Ok(())
}

fn binary_stamp(path: &Path) -> Result<Stamp> {
    let metadata = fs::symlink_metadata(path)
        .map_err(|_| not_ready("missing", "未找到本机官方 Grok Build CLI"))?;
    if !metadata.is_file()
        || !owned(&metadata)
        || metadata.mode() & 0o111 == 0
        || metadata.nlink() != 1
    {
        return Err(not_ready(
            "executable",
            "Grok Build 可执行文件的权限或身份无法核对",
        ));
    }
    Ok(metadata.into())
}

fn overlapping(left: &Path, right: &Path) -> bool {
    left.starts_with(right) || right.starts_with(left)
}

fn host_home() -> Result<PathBuf> {
    let mut account = MaybeUninit::<libc::passwd>::uninit();
    let mut result = ptr::null_mut();
    let mut buffer = vec![0; 64 * 1024];
    let code = unsafe {
        libc::getpwuid_r(
            libc::geteuid(),
            account.as_mut_ptr(),
            buffer.as_mut_ptr(),
            buffer.len(),
            &mut result,
        )
    };
    if code == 0 && !result.is_null() {
        let home = unsafe { (*result).pw_dir };
        if !home.is_null() {
            let home = Path::new(OsStr::from_bytes(
                unsafe { CStr::from_ptr(home) }.to_bytes(),
            ));
            if home.is_absolute() {
                return Ok(Root::open(home)?.path);
            }
        }
    }
    Err(not_ready(
        "configuration",
        "无法定位当前 macOS 用户的主目录",
    ))
}

impl Installation {
    pub(super) fn discover(protected: &[PathBuf], cancel: &Cancellation) -> Result<Self> {
        let home = host_home()?;
        let launcher = home.join(".grok/bin/grok");
        let expected = home.join(format!(".grok/downloads/grok-{VERSION}-macos-aarch64"));
        let binary = fs::canonicalize(launcher)
            .map_err(|_| not_ready("missing", "未找到本机官方 Grok Build CLI"))?;
        if binary != expected {
            return Err(not_ready(
                "version",
                "此工作台候选只支持官方 Grok Build 1.0.92 ARM64",
            ));
        }
        let dedicated = home.join(".yss-grok-build");
        let profile = match fs::symlink_metadata(&dedicated) {
            Ok(_) => dedicated,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => home.join(".grok"),
            Err(_) => {
                return Err(not_ready(
                    "configuration",
                    "无法核对 Grok Build 登录配置目录",
                ));
            }
        };
        if protected
            .iter()
            .any(|path| overlapping(path, &binary) || overlapping(path, &profile))
        {
            return Err(not_ready(
                "configuration",
                "Grok Build 安装或认证目录与工程保护范围重叠",
            ));
        }
        let initial = binary_stamp(&binary)?;
        verify_signature(&binary, cancel)?;
        if initial != binary_stamp(&binary)? {
            return Err(not_ready(
                "identityChanged",
                "核对期间 Grok Build 可执行文件已变化",
            ));
        }
        let auth = profile.join("auth.json");
        let auth_stamp = auth_stamp(&auth).map_err(|error| {
            error
                .context("executable", binary.display())
                .context("version", VERSION)
        })?;
        real_directory(&profile)?;
        Ok(Self {
            binary,
            profile,
            auth,
            binary_stamp: initial,
            auth_stamp,
        })
    }

    pub(super) fn identity(&self) -> String {
        Sha256::of(
            format!(
                "grok-acp-{VERSION}\n{:?}\n{:?}\n{:?}",
                self.binary, self.binary_stamp, self.profile
            )
            .as_bytes(),
        )
        .as_str()
        .into()
    }

    fn revalidate_installation(&self, cancel: &Cancellation) -> Result<()> {
        real_directory(&self.profile)?;
        let home = self.profile.parent().expect("profile is beneath home");
        let launcher = home.join(".grok/bin/grok");
        let dedicated = home.join(".yss-grok-build");
        let selection_changed = self.profile != dedicated
            && !matches!(fs::symlink_metadata(&dedicated), Err(error) if error.kind() == std::io::ErrorKind::NotFound);
        if fs::canonicalize(launcher).ok().as_deref() != Some(&self.binary)
            || selection_changed
            || binary_stamp(&self.binary)? != self.binary_stamp
        {
            return Err(not_ready(
                "identityChanged",
                "Grok Build 的可执行文件或登录配置已变化，请重新核对",
            ));
        }
        verify_signature(&self.binary, cancel)
    }

    pub(super) fn revalidate(&self, cancel: &Cancellation) -> Result<()> {
        self.revalidate_installation(cancel)?;
        if auth_stamp(&self.auth)? != self.auth_stamp {
            return Err(not_ready(
                "identityChanged",
                "Grok Build 的登录文件已变化，请重新核对",
            ));
        }
        Ok(())
    }

    pub(super) fn refresh_auth_stamp(&mut self, cancel: &Cancellation) -> Result<()> {
        self.revalidate_installation(cancel)?;
        self.auth_stamp = auth_stamp(&self.auth)?;
        Ok(())
    }

    pub(super) fn start(
        &self,
        scratch: &Scratch,
        cwd: &Path,
        sessions: &Path,
        model: Option<&PiModel>,
        context: Option<&JobContext>,
        cancel: &Cancellation,
    ) -> Result<Acp> {
        if !Path::new(LAUNCHER).is_file() {
            return Err(not_ready(
                "sandbox",
                "macOS Seatbelt 不可用，Grok Build 只读分析已关闭",
            ));
        }
        self.revalidate(cancel)?;
        real_directory(cwd)?;
        real_directory(sessions)?;
        let launch_home = scratch.path().join("home");
        directory(&launch_home)?;
        let grok_home = launch_home.join(".grok");
        directory(&grok_home)?;
        std::os::unix::fs::symlink(sessions, grok_home.join("sessions"))?;
        let mut command = Command::new(LAUNCHER);
        command
            .env_clear()
            .env("HOME", &launch_home)
            .env("GROK_HOME", &grok_home)
            .env("GROK_AUTH_PATH", &self.auth)
            .env("TMPDIR", scratch.path())
            .env("PATH", "/usr/bin:/bin")
            .env("LANG", "en_US.UTF-8")
            .env("GROK_TELEMETRY_ENABLED", "0")
            .env("GROK_GOAL", "0")
            .env("GROK_WORKFLOWS", "0")
            .env("GROK_LSP_TOOLS", "0")
            .env("GROK_WEB_FETCH", "0")
            .env("GROK_MANAGED_MCPS_ENABLED", "0")
            .env("GROK_MANAGED_MCP_GATEWAY_TOOLS_ENABLED", "0")
            .env("GROK_OFFICIAL_MARKETPLACE_AUTO_REGISTER", "0")
            .current_dir(scratch.path());
        for kind in ["GROK_CURSOR", "GROK_CLAUDE"] {
            for capability in ["AGENTS", "RULES", "MCPS", "HOOKS", "SKILLS"] {
                command.env(format!("{kind}_{capability}_ENABLED"), "0");
            }
        }
        let mut policy = String::from(POLICY);
        for (name, path) in [
            ("BIN", self.binary.as_path()),
            ("SCRATCH", scratch.path()),
            ("CWD", cwd),
            ("AUTH", self.auth.as_path()),
            ("SESSIONS", sessions),
        ] {
            let mut value = OsString::from(format!("{name}="));
            value.push(path);
            command.arg("-D").arg(value);
            for ancestor in path.ancestors().skip(1) {
                let rule = format!("ANCESTOR_{}", command.get_args().count());
                let mut value = OsString::from(format!("{rule}="));
                value.push(ancestor);
                command.arg("-D").arg(value);
                policy.push_str(&format!(
                    "\n(allow file-read-metadata file-test-existence (literal (param \"{rule}\")))"
                ));
            }
        }
        command.arg("-p").arg(policy).arg(&self.binary).args([
            "--no-auto-update",
            "--no-memory",
            "--no-subagents",
            "--no-plan",
            "--disable-web-search",
            "--permission-mode",
            "dontAsk",
            "--tools",
            "todo_write",
            "--deny",
            "Read",
            "--deny",
            "Grep",
            "--deny",
            "Glob",
            "--deny",
            "Bash",
            "--deny",
            "Edit",
            "--deny",
            "MCPTool",
            "--deny",
            "WebFetch",
            "--deny",
            "WebSearch",
            "agent",
            "--no-leader",
        ]);
        if let Some(model) = model {
            command.arg("--model").arg(&model.id);
        }
        command.arg("stdio");
        Acp::open(command, cancel, context)
    }
}

pub(super) fn storage(root: &Path, name: &str) -> Result<PathBuf> {
    let relative = RelPath::new(name)?;
    if relative.as_str().contains('/') {
        return Err(not_ready("configuration", "Grok Build 专用目录名无效"));
    }
    let path = Root::open(root)?.path.join(relative.as_str());
    directory(&path)?;
    Ok(path)
}

pub(super) fn session_available(data_dir: &Path, run_id: &str, session_id: &str) -> bool {
    fn verify(data_dir: &Path, run_id: &str, session_id: &str) -> Result<bool> {
        let storage = |id: &str| {
            !id.is_empty()
                && id.len() <= 128
                && id
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
        };
        if !storage(run_id) || !storage(session_id) {
            return Ok(false);
        }
        let path = Root::open(data_dir)?
            .path
            .join("grok")
            .join(run_id)
            .join("sessions");
        real_directory(&path)?;
        let mut found = false;
        for category in fs::read_dir(&path)?.take(128) {
            let category = category?;
            if !category.file_type()?.is_dir() {
                continue;
            }
            let candidate = category.path().join(session_id);
            if !candidate.is_dir() {
                continue;
            }
            real_directory(&category.path())?;
            real_directory(&candidate)?;
            let transcript = fs::symlink_metadata(candidate.join("chat_history.jsonl"))?;
            if found
                || !transcript.is_file()
                || !owned(&transcript)
                || transcript.nlink() != 1
                || transcript.len() == 0
            {
                return Ok(false);
            }
            found = true;
        }
        Ok(found)
    }
    verify(data_dir, run_id, session_id).unwrap_or(false)
}
