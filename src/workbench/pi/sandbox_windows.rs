//! Private Windows runtime and authenticated AppContainer command execution.
use super::super::windows_sandbox::{
    self, Arguments, Environment, Invocation, ProbeInputs, ProbeToolchain, Profile, ScratchGrant,
    SystemTools,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{CheckedFile, FileIdentity, FileSnapshot, Root, platform, unique_id},
    model::{RelPath, Sha256},
};
use std::{
    collections::BTreeMap,
    ffi::{OsStr, OsString},
    fs::{File, OpenOptions},
    io,
    os::windows::{
        fs::{MetadataExt, OpenOptionsExt},
        io::AsHandle,
    },
    path::{Path, PathBuf},
    process::{ExitStatus, Output},
    time::Duration,
};
use windows_sys::Win32::Storage::FileSystem::{
    FILE_ATTRIBUTE_DEVICE, FILE_ATTRIBUTE_REPARSE_POINT, FILE_FLAG_BACKUP_SEMANTICS,
    FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES, FILE_SHARE_READ, FILE_SHARE_WRITE,
    READ_CONTROL, WRITE_DAC, WRITE_OWNER,
};

pub(super) const OFFLINE_UNAVAILABLE: &str = "Windows 离线命令隔离尚未通过验证，已关闭";

fn unavailable() -> YssError {
    YssError::new(ErrorCode::NotReady, OFFLINE_UNAVAILABLE)
}

pub(super) struct PrivateDir {
    parent: Root,
    name: RelPath,
    identity: FileIdentity,
    path: PathBuf,
    pin: Option<File>,
    _parent_pins: Vec<File>,
}

/// Keep an absolute Windows directory path stable for a subprocess that must
/// reopen it by name. Pin ancestors from the volume down before returning.
pub(super) fn pin_directory_chain(path: &Path) -> Result<Vec<File>> {
    let mut chain: Vec<_> = path
        .ancestors()
        .filter(|path| path.file_name().is_some())
        .collect();
    chain.reverse();
    let mut pins = Vec::with_capacity(chain.len());
    for path in chain {
        pins.push(Root::open(path)?.pin_directory()?);
    }
    Ok(pins)
}

impl PrivateDir {
    pub fn new() -> Result<Self> {
        let parent_path = directories::BaseDirs::new()
            .ok_or_else(|| YssError::new(ErrorCode::NotReady, "无法确定当前用户目录"))?
            .data_local_dir()
            .to_owned();
        let parent = Root::open(&parent_path)?;
        let parent_pins = pin_directory_chain(&parent.path)?;
        parent.revalidate()?;
        parent.verify_trusted_dir()?;
        let name = RelPath::new(format!("yss-pi-{}", unique_id()?))?;
        let identity = parent.create_private_dir(&name)?;
        let path = parent.path.join(name.as_str());
        let root = Root::open(&path)?;
        let pin = root.pin_directory()?;
        if root.identity != identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "Pi 私有目录已改变",
            ));
        }
        root.verify_private_dir()?;
        Ok(Self {
            parent,
            name,
            identity,
            path,
            pin: Some(pin),
            _parent_pins: parent_pins,
        })
    }
    pub fn path(&self) -> &Path {
        &self.path
    }
}

impl Drop for PrivateDir {
    fn drop(&mut self) {
        // Release the execution pin so cleanup can open with DELETE access.
        // The relative open checks the original identity before traversal.
        drop(self.pin.take());
        let _ = self
            .parent
            .remove_private_tree(&self.name, &self.identity, 32_768, 64);
    }
}

pub(super) fn private_dir(path: &Path) -> Result<()> {
    let parent = path.parent().ok_or_else(unavailable)?;
    let name = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or_else(unavailable)?;
    Root::open(parent)?.create_private_dir(&RelPath::new(name)?)?;
    Ok(())
}

const NODE_ENTRY: &str = "node.exe";
const HOME_ENTRY: &str = "command-home";
const MAX_IMAGE: u64 = 128 * 1024 * 1024;
const MAX_COMMAND_ENTRIES: usize = 32_768;
const MAX_COMMAND_DEPTH: usize = 64;

pub(crate) struct StoppedCommand {
    scratch: PathBuf,
    identity: FileIdentity,
    digests: (Sha256, Sha256, Sha256),
    output: Output,
}

impl StoppedCommand {
    pub(crate) fn scratch_path(&self) -> &Path {
        &self.scratch
    }

    pub(crate) fn scratch_identity(&self) -> &FileIdentity {
        &self.identity
    }

    pub(crate) fn binding(&self) -> &(Sha256, Sha256, Sha256) {
        &self.digests
    }

    pub(crate) fn status(&self) -> ExitStatus {
        self.output.status
    }

    pub(crate) fn parts(self) -> (ExitStatus, Vec<u8>, Vec<u8>) {
        (self.output.status, self.output.stdout, self.output.stderr)
    }
}

pub(crate) struct Sandbox {
    tools: SystemTools,
    node_root: Root,
    _tool_dir: PrivateDir,
    node: FileSnapshot,
    shell: FileSnapshot,
    path: OsString,
    executable_digest: Sha256,
}

impl Sandbox {
    pub(super) fn new(node: Option<&Path>, cancel: &Cancellation) -> Result<Self> {
        let node =
            node.ok_or_else(|| not_ready("Windows 离线命令需要 Pi 已验证的 Node.js x64 路径"))?;
        cancel.check()?;
        let tools = native("Windows 命令提示符探测", SystemTools::discover())?;
        let shell = read_image(&tools.command, false)?.snapshot;
        let image = read_image(node, true)?;
        let node_digest = digest(&image.snapshot)?;
        native(
            "Node.js 可执行文件核验",
            windows_sandbox::verify_node_image(&image.bytes, node_digest),
        )?;
        let executable_digest = encode_digest(&(digest(&shell)?, node_digest))?;
        let tool_dir = PrivateDir::new()?;
        let node_root = Root::open(tool_dir.path())?;
        let node = node_root.create_file(&RelPath::new(NODE_ENTRY)?, &image.bytes, 0o700)?;
        let path = std::env::join_paths([&node_root.path, &tools.system_dir])
            .map_err(|_| not_ready("无法建立受检 Windows 命令搜索路径"))?;
        let instance = Self {
            tools,
            node_root,
            _tool_dir: tool_dir,
            node,
            shell,
            path,
            executable_digest,
        };
        instance.verify(cancel)?;
        Ok(instance)
    }

    fn environment<'a>(&'a self, home: &'a Path, projects: &'a OsStr) -> Environment<'a> {
        Environment {
            system_root: &self.tools.windows_dir,
            path: &self.path,
            scratch: home,
            projects: Some(projects),
        }
    }

    pub(super) fn binding(
        &self,
        scratch: &Path,
        projects: &[(&str, &Path)],
        cwd: Option<&RelPath>,
        command: &str,
    ) -> Result<(Sha256, Sha256, Sha256)> {
        if !scratch.is_absolute() {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "离线命令的私有工作目录无效",
            ));
        }
        windows_sandbox::validate_command(&self.tools.command, command).map_err(|_| {
            YssError::new(
                ErrorCode::InvalidInput,
                "Windows 离线命令需要一条非空的 cmd.exe 命令，且必须符合命令行长度上限",
            )
        })?;
        let home = home_path(scratch)?;
        let projects = project_environment(scratch, projects)?;
        let environment = native(
            "命令计划环境核验",
            self.environment(&home, &projects).planned_block(),
        )?;
        let policy = encode_digest(&(
            "yss-windows-lpac-job/2",
            &self.tools.command,
            windows_sandbox::CMD_FLAGS,
            scratch,
            cwd,
            Sha256::of(command.as_bytes()),
        ))?;
        Ok((
            self.executable_digest.clone(),
            environment_digest(environment),
            policy,
        ))
    }

    pub(super) fn run(
        &self,
        scratch: &Root,
        projects: &[(&str, &Root)],
        cwd: Option<&RelPath>,
        command: &str,
        cancel: &Cancellation,
        check_scope: impl FnOnce() -> Result<()>,
        confirm_stopped: impl FnOnce() -> Result<()>,
        output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
    ) -> Result<StoppedCommand> {
        self.run_inner(
            scratch,
            projects,
            cwd,
            command,
            cancel,
            check_scope,
            confirm_stopped,
            output,
            Duration::from_secs(600),
        )
    }

    fn run_inner(
        &self,
        scratch: &Root,
        projects: &[(&str, &Root)],
        cwd: Option<&RelPath>,
        command: &str,
        cancel: &Cancellation,
        check_scope: impl FnOnce() -> Result<()>,
        confirm_stopped: impl FnOnce() -> Result<()>,
        mut output: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
        timeout: Duration,
    ) -> Result<StoppedCommand> {
        let before_start = |error: YssError| error.context("cleanupVerified", true);
        let paths: Vec<_> = projects
            .iter()
            .map(|(id, root)| (*id, root.path.as_path()))
            .collect();
        let project_paths = project_environment(&scratch.path, &paths).map_err(before_start)?;
        let result = (|| {
            cancel.check()?;
            scratch.verify_private_dir()?;
            let parent = Root::open(scratch.path.parent().ok_or_else(unavailable)?)?;
            parent.verify_private_dir()?;
            let home = home_path(&scratch.path)?;
            let home_id = parent.create_private_dir(&RelPath::new(HOME_ENTRY)?)?;
            let home_root = Root::open(&home)?;
            home_root.verify_private_dir()?;
            if home_root.identity != home_id {
                return Err(changed());
            }
            let work = match cwd {
                Some(cwd) => Root::open(&scratch.path.join(cwd.as_str()))?,
                None => Root::open(&scratch.path)?,
            };
            let pins = pin_directory_chain(&work.path)?;
            work.revalidate()?;
            let binding = self.binding(&scratch.path, &paths, cwd, command)?;
            let launch_environment = native(
                "命令启动环境核验",
                self.environment(&home_root.path, &project_paths).block(),
            )?;
            if environment_digest(launch_environment) != binding.1 {
                return Err(changed());
            }
            Ok((home_root, work, pins, binding))
        })()
        .map_err(before_start)?;
        let (home, work, _pins, digests) = result;
        let node_relative = RelPath::new(NODE_ENTRY).map_err(before_start)?;
        let node_path = self.node_root.path.join(NODE_ENTRY);
        let node_id = self
            .node
            .identity()
            .ok_or_else(unavailable)
            .map_err(before_start)?;
        let node_handle =
            security_handle(&node_path, node_id, false, true).map_err(before_start)?;
        let tools_handle =
            security_handle(&self.node_root.path, &self.node_root.identity, true, false)
                .map_err(before_start)?;
        let scratch_handle =
            security_handle(&scratch.path, &scratch.identity, true, false).map_err(before_start)?;
        let home_handle =
            security_handle(&home.path, &home.identity, true, false).map_err(before_start)?;
        let readers = projects
            .iter()
            .filter_map(|(_, root)| (root.identity != scratch.identity).then_some(*root))
            .collect::<Vec<_>>();
        let reader_handles = readers
            .iter()
            .map(|root| {
                root.verify_private_dir()?;
                security_handle(&root.path, &root.identity, true, false)
            })
            .collect::<Result<Vec<_>>>()
            .map_err(before_start)?;
        if read_image(&self.tools.command, false)
            .map_err(before_start)?
            .snapshot
            != self.shell
            || self
                .node_root
                .read_checked_single_link(&node_relative, MAX_IMAGE)
                .map_err(before_start)?
                .snapshot
                != self.node
        {
            return Err(before_start(changed()));
        }
        let profile_id = unique_id().map_err(before_start)?;
        cancel.check().map_err(before_start)?;
        // The caller durably records launch intent before profile creation or ACL changes.
        check_scope().map_err(before_start)?;
        let profile =
            native("创建受限 AppContainer", Profile::create(&profile_id)).map_err(before_start)?;

        let (scratch_grant, home_grant, tools_grant, node_grant, reader_grants) = native(
            "限制命令目录和受检工具权限",
            (|| {
                Ok((
                    ScratchGrant::apply_one(scratch_handle.as_handle(), &profile, true, false)?,
                    ScratchGrant::apply_one(home_handle.as_handle(), &profile, true, false)?,
                    ScratchGrant::apply_one(tools_handle.as_handle(), &profile, true, true)?,
                    ScratchGrant::apply_one(node_handle.as_handle(), &profile, false, true)?,
                    reader_handles
                        .iter()
                        .map(|handle| {
                            ScratchGrant::apply_one(handle.as_handle(), &profile, true, true)
                        })
                        .collect::<io::Result<Vec<_>>>()?,
                ))
            })(),
        )?;
        let mut profile = Some(profile);
        let mut stopped = false;
        let mut event_error = None;
        let attempt = (|| {
            let pending_profile = profile.as_ref().ok_or_else(unavailable)?;
            let mut remaining = MAX_COMMAND_ENTRIES;
            grant_children(
                scratch,
                None,
                pending_profile,
                false,
                cancel,
                &mut remaining,
                0,
            )?;
            for reader in &readers {
                let mut remaining = MAX_COMMAND_ENTRIES;
                grant_children(
                    reader,
                    None,
                    pending_profile,
                    true,
                    cancel,
                    &mut remaining,
                    0,
                )?;
            }
            cancel.check()?;
            let profile = profile.take().ok_or_else(unavailable)?;
            native(
                "运行受限 Windows 命令",
                windows_sandbox::capture_input(
                    profile,
                    Invocation {
                        program: &self.tools.command,
                        args: Arguments::Cmd(command),
                        cwd: &work.path,
                        environment: self.environment(&home.path, &project_paths),
                        input: &[],
                        timeout,
                    },
                    &mut stopped,
                    || cancel.is_cancelled(),
                    |chunk| {
                        if event_error.is_none() {
                            event_error = output(chunk).err();
                        }
                        event_error.is_some()
                    },
                ),
            )
        })();

        native(
            "恢复隔离目录权限",
            (|| {
                scratch_grant.restore()?;
                home_grant.restore()?;
                tools_grant.restore()?;
                node_grant.restore()?;
                for grant in reader_grants {
                    grant.restore()?;
                }
                Ok(())
            })(),
        )?;
        scratch.verify_private_dir()?;
        home.verify_private_dir()?;
        self.node_root.verify_private_dir()?;
        for reader in readers {
            reader.verify_private_dir()?;
        }
        if let Some(profile) = profile {
            // No process was created, but profile teardown must still be confirmed.
            native("释放未启动的 AppContainer", profile.close())?;
            stopped = true;
        }
        if stopped {
            confirm_stopped().map_err(|error| error.context("cleanupVerified", false))?;
        }
        if let Some(error) = event_error {
            return Err(error.context("cleanupVerified", stopped));
        }
        cancel
            .check()
            .map_err(|error| error.context("cleanupVerified", stopped))?;
        let output = attempt.map_err(|error| error.context("cleanupVerified", stopped))?;
        if !stopped {
            return Err(not_ready("无法确认 Windows 离线命令的整棵进程树已停止"));
        }
        Ok(StoppedCommand {
            scratch: scratch.path.clone(),
            identity: scratch.identity.clone(),
            digests,
            output,
        })
    }

    fn verify(&self, cancel: &Cancellation) -> Result<()> {
        let storage = PrivateDir::new()?;
        let parent = Root::open(storage.path())?;
        let empty = new_child(&parent, "probe-isolated")?;
        let sentinels = new_child(&parent, "probe-protected")?;
        const SENTINEL: &[u8] = b"YSS private isolation probe marker";
        let outside = RelPath::new("outside.txt")?;
        let credentials = RelPath::new("credentials.txt")?;
        sentinels.create_private_file(&outside, SENTINEL)?;
        sentinels.create_private_file(&credentials, SENTINEL)?;
        let handle = security_handle(&empty.path, &empty.identity, true, false)?;
        let node_path = self.node_root.path.join(NODE_ENTRY);
        let node_id = self.node.identity().ok_or_else(unavailable)?;
        let node_handle = security_handle(&node_path, node_id, false, true)?;
        let tools_handle =
            security_handle(&self.node_root.path, &self.node_root.identity, true, false)?;
        let toolchain = ProbeToolchain {
            node: &node_path,
            tools_dir: tools_handle.as_handle(),
            node_file: node_handle.as_handle(),
        };
        let id = unique_id()?;
        let outside = sentinels.path.join(outside.as_str());
        let credential_sentinel = sentinels.path.join(credentials.as_str());
        native(
            "工程外文件与网络拒绝自检",
            windows_sandbox::probe_files_and_network(
                handle.as_handle(),
                &ProbeInputs {
                    id: &id,
                    scratch: &empty.path,
                    outside_sentinel: &outside,
                    credential_sentinel: &credential_sentinel,
                    sentinel_contents: SENTINEL,
                },
                &toolchain,
                || cancel.is_cancelled(),
            ),
        )?;
        native(
            "取消与异常退出的进程树清理自检",
            windows_sandbox::probe_process_cleanup(
                handle.as_handle(),
                &empty.path,
                &unique_id()?,
                &unique_id()?,
                &toolchain,
                || cancel.is_cancelled(),
            ),
        )?;
        drop(handle);
        empty.verify_private_dir()?;
        let project = new_child(&parent, "probe-project")?;
        let reader = new_child(&parent, "probe-read-only")?;
        reader.create_file(&RelPath::new("protected.txt")?, SENTINEL, 0o600)?;
        const TEST: &[u8] = br#"import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
test('isolated toolchain', () => {
  const [major, minor] = process.versions.node.split('.').map(Number);
  assert.ok(major > 22 || (major === 22 && minor >= 19));
  const projects = JSON.parse(process.env.YSS_PROJECTS_JSON);
  const protectedFile = join(projects.reader, 'protected.txt');
  assert.equal(readFileSync(protectedFile, 'utf8'), 'YSS private isolation probe marker');
  const isDenied = ({code}) => code === 'EACCES' || code === 'EPERM';
  assert.throws(() => writeFileSync(protectedFile, 'bad'), isDenied);
  assert.throws(() => writeFileSync(join(projects.reader, 'new.txt'), 'bad'), isDenied);
  assert.throws(() => unlinkSync(protectedFile), isDenied);
  writeFileSync('yss-result.txt', 'YSSNODE');
});
"#;
        project.create_file(&RelPath::new("yss-node.test.mjs")?, TEST, 0o600)?;
        let result = self.run_inner(
            &project,
            &[("writer", &project), ("reader", &reader)],
            None,
            "node --test-reporter=tap yss-node.test.mjs",
            cancel,
            || Ok(()),
            || Ok(()),
            |_| Ok(()),
            Duration::from_secs(25),
        )?;
        if !result.status().success()
            || !String::from_utf8_lossy(&result.output.stdout)
                .lines()
                .any(|line| line == "ok 1 - isolated toolchain")
            || project
                .read_checked_single_link(&RelPath::new("yss-result.txt")?, 64)?
                .bytes
                != b"YSSNODE"
        {
            return Err(not_ready("Windows 受检 Node.js 工程测试与副本写入自检失败"));
        }
        cancel.check()
    }
}

fn new_child(parent: &Root, name: &str) -> Result<Root> {
    let id = parent.create_private_dir(&RelPath::new(name)?)?;
    let root = Root::open(&parent.path.join(name))?;
    root.verify_private_dir()?;
    if root.identity != id {
        return Err(changed());
    }
    Ok(root)
}

fn home_path(scratch: &Path) -> Result<PathBuf> {
    Ok(scratch.parent().ok_or_else(unavailable)?.join(HOME_ENTRY))
}

fn project_environment(scratch: &Path, projects: &[(&str, &Path)]) -> Result<OsString> {
    let parent = scratch.parent().ok_or_else(unavailable)?;
    let mut mapped = BTreeMap::new();
    for &(id, path) in projects {
        if id.is_empty()
            || id.len() > 256
            || id.chars().any(char::is_control)
            || path.parent() != Some(parent)
            || path.file_name() == Some(OsStr::new(HOME_ENTRY))
            || mapped.values().any(|other| *other == path)
            || mapped.insert(id, path).is_some()
        {
            return Err(not_ready("Windows 命令工程副本环境绑定无效"));
        }
    }
    if !mapped.values().any(|path| *path == scratch) {
        return Err(not_ready("Windows 命令缺少所选工程副本"));
    }
    serde_json::to_string(&mapped)
        .map(OsString::from)
        .map_err(|_| not_ready("无法绑定 Windows 工程副本环境"))
}

fn read_image(binary: &Path, single_link: bool) -> Result<CheckedFile> {
    let directory = binary.parent().ok_or_else(unavailable)?;
    let name = binary
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or_else(unavailable)?;
    let relative = RelPath::new(name)?;
    let root = Root::open(directory)?;
    if single_link {
        root.read_checked_single_link(&relative, MAX_IMAGE)
    } else {
        root.read_checked(&relative, MAX_IMAGE)
    }
}

fn digest(snapshot: &FileSnapshot) -> Result<&Sha256> {
    snapshot
        .descriptor()
        .map(|entry| &entry.sha256)
        .ok_or_else(unavailable)
}

fn encode_digest(value: &impl serde::Serialize) -> Result<Sha256> {
    serde_json::to_vec(value)
        .map(|bytes| Sha256::of(&bytes))
        .map_err(|_| not_ready("无法绑定 Windows 离线命令"))
}

fn environment_digest(block: Vec<u16>) -> Sha256 {
    let bytes: Vec<_> = block.into_iter().flat_map(u16::to_le_bytes).collect();
    Sha256::of(&bytes)
}

fn security_handle(
    path: &Path,
    id: &FileIdentity,
    directory: bool,
    deny_write: bool,
) -> Result<File> {
    let shares = if deny_write {
        FILE_SHARE_READ
    } else {
        FILE_SHARE_READ | FILE_SHARE_WRITE
    };
    let handle = OpenOptions::new()
        .access_mode(FILE_READ_ATTRIBUTES | READ_CONTROL | WRITE_DAC | WRITE_OWNER)
        .share_mode(shares)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)?;
    let metadata = handle.metadata()?;
    if metadata.file_attributes() & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DEVICE) != 0
        || platform::identity_std_file(&handle)? != *id
        || (directory && !metadata.is_dir())
        || (!directory && (!metadata.is_file() || !platform::has_single_link(&handle)?))
    {
        return Err(changed());
    }
    Ok(handle)
}

fn grant_children(
    root: &Root,
    directory: Option<&RelPath>,
    profile: &Profile,
    read_only: bool,
    cancel: &Cancellation,
    remaining: &mut usize,
    depth: usize,
) -> Result<()> {
    if depth > MAX_COMMAND_DEPTH {
        return Err(not_ready("命令副本目录层级超过隔离上限"));
    }
    for name in root.list(directory)? {
        cancel.check()?;
        *remaining = remaining
            .checked_sub(1)
            .ok_or_else(|| not_ready("命令副本条目超过隔离上限"))?;
        let relative = match directory {
            Some(parent) => parent.join(&name)?,
            None => RelPath::new(name)?,
        };
        let metadata = root.entry_metadata(&relative)?.ok_or_else(changed)?;
        if platform::dangerous(&metadata) {
            return Err(changed());
        }
        let directory = metadata.is_dir();
        let identity = if directory {
            platform::identity_dir(&root.open_dir(&relative)?)?
        } else {
            root.regular_file_identity(&relative)?
        };
        let handle = security_handle(
            &root.path.join(relative.as_str()),
            &identity,
            directory,
            false,
        )?;
        native(
            "限制命令副本权限",
            ScratchGrant::apply_one(handle.as_handle(), profile, directory, read_only),
        )?
        .keep_in_disposable_copy();
        if directory {
            grant_children(
                root,
                Some(&relative),
                profile,
                read_only,
                cancel,
                remaining,
                depth + 1,
            )?;
        }
    }
    Ok(())
}

fn changed() -> YssError {
    YssError::new(
        ErrorCode::ConcurrentChange,
        "Windows 命令私有文件或受检工具已经变化",
    )
}

fn not_ready(message: impl Into<String>) -> YssError {
    YssError::new(ErrorCode::NotReady, message).context("piIssue", "commandsUnavailable")
}

fn native<T>(phase: &str, result: io::Result<T>) -> Result<T> {
    result.map_err(|error| not_ready(format!("Windows 离线命令隔离未就绪（{phase}）：{error}")))
}
