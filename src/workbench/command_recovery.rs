//! Durable registration and cleanup for Windows command scratch.
use super::{
    agent::{AgentRun, ProjectAccess, ProjectMode},
    agent_protocol::{StoppedCommand, VerifiedManifest, WindowsCommandChange},
    agent_workspace::Workspace,
    jobs::JobRecord,
    sqlite::{CommitEffects, EventWrite, RecordWrite},
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, FileSnapshot, Root, RootIdentity, unique_id},
    model::{MAX_ASSET, RelPath, RenderedFile, Sha256},
    transaction::{self, Operation, TransactionPhase},
};
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

const KIND: &str = "piCommandRecovery";
const LEGACY_FORMAT_VERSION: u32 = 1;
const FORMAT_VERSION: u32 = 2;
const MAX_RECORD_BYTES: usize = 16 * 1024 * 1024;
const MAX_FILES: usize = 65_536;
const MAX_PROJECTS: usize = 64;

fn rejected(message: &str) -> YssError {
    YssError::new(ErrorCode::RecoveryRequired, message).context("piIssue", "commandsUnavailable")
}
fn changed(message: &str) -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, message)
}
fn valid_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// Each mapped project has a distinct child of the registered scratch root.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct ProjectBinding {
    pub project_id: String,
    pub source_path: PathBuf,
    pub source_identity: FileIdentity,
    pub scratch_child: RelPath,
    pub scratch_identity: FileIdentity,
}

/// Source identity checked by the caller before requesting a private copy.
pub(super) struct ProjectSource {
    pub project_id: String,
    pub source_path: PathBuf,
    pub source_identity: FileIdentity,
    pub scratch_child: RelPath,
}

/// Frozen job, authorization and filesystem identities, written before copying.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct Binding {
    pub id: String,
    pub job_id: String,
    pub run_id: String,
    pub binding_revision: u64,
    pub data_identity: FileIdentity,
    pub scratch_identity: FileIdentity,
    pub command_digest: Sha256,
    pub grants: Vec<ProjectAccess>,
    pub workspaces: Vec<Workspace>,
    pub projects: Vec<ProjectBinding>,
}

/// The caller must derive the executable, environment and policy digests from
/// the exact checked values handed to the process launcher, not user claims.
/// This value does not by itself authorize a process launch.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct CommandSpec<'a> {
    pub project_id: &'a str,
    pub cwd: Option<&'a RelPath>,
    pub command: &'a str,
    pub executable_digest: &'a Sha256,
    pub environment_digest: &'a Sha256,
    pub policy_digest: &'a Sha256,
}

impl CommandSpec<'_> {
    pub(super) fn digest(&self) -> Result<Sha256> {
        if self.project_id.is_empty()
            || self.project_id.len() > 256
            || self.project_id.chars().any(char::is_control)
            || self.command.trim().is_empty()
            || self.command.len() > 64 * 1024
            || self.command.contains('\0')
        {
            return Err(rejected("命令或工程标识无效，未运行命令"));
        }
        let bytes = serde_json::to_vec(&("yss-pi-command/1", self))
            .map_err(|_| rejected("无法编码命令绑定"))?;
        Ok(Sha256::of(&bytes))
    }
}

fn check_command(binding: &Binding, command: &CommandSpec<'_>) -> Result<()> {
    if command.digest()? != binding.command_digest
        || !binding
            .projects
            .iter()
            .any(|project| project.project_id == command.project_id)
        || !binding.grants.iter().any(|grant| {
            grant.project_id == command.project_id && grant.mode == ProjectMode::Execute
        })
        || command
            .cwd
            .is_some_and(|cwd| !authorized_path(binding, command.project_id, cwd).unwrap_or(false))
    {
        return Err(changed("命令、工具链、工作目录或授权已变化，已保留暂存"));
    }
    Ok(())
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct InputEntry {
    pub source: FileSnapshot,
    pub copied: FileSnapshot,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct ProjectInput {
    pub project_id: String,
    pub entries: BTreeMap<RelPath, InputEntry>,
    pub excluded: Vec<RelPath>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "phase", rename_all = "camelCase", deny_unknown_fields)]
enum Phase {
    Copying,
    // A v1 record could have reached the process launcher at this point.
    Ready { inputs: Vec<ProjectInput> },
    Prepared { inputs: Vec<ProjectInput> },
    Launching { inputs: Vec<ProjectInput> },
    Stopped { inputs: Vec<ProjectInput> },
    Cleaning,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Record {
    format_version: u32,
    binding: Binding,
    phase: Phase,
    #[serde(default)]
    root_transactions: Vec<RootTransaction>,
}

/// This durable binding is an audit and recovery record, not permission to
/// run or retry a transaction. A missing journal is always an unknown result.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RootTransaction {
    root_identity: RootIdentity,
    transaction_id: String,
    plan_digest: Sha256,
}

fn valid_record(record: &Record) -> bool {
    let phase_supported = match record.format_version {
        LEGACY_FORMAT_VERSION => {
            matches!(
                record.phase,
                Phase::Copying | Phase::Ready { .. } | Phase::Cleaning
            )
        }
        FORMAT_VERSION => !matches!(record.phase, Phase::Ready { .. }),
        _ => false,
    };
    phase_supported && valid_transactions(record)
}

fn valid_transactions(record: &Record) -> bool {
    if record.root_transactions.len() > MAX_PROJECTS
        || (!record.root_transactions.is_empty()
            && !matches!(
                record.phase,
                Phase::Ready { .. } | Phase::Stopped { .. } | Phase::Cleaning
            ))
    {
        return false;
    }
    let mut ids = BTreeSet::new();
    let mut roots = Vec::new();
    for item in &record.root_transactions {
        if !valid_id(&item.transaction_id)
            || !ids.insert(&item.transaction_id)
            || roots.contains(&item.root_identity)
            || record
                .binding
                .workspaces
                .iter()
                .filter(|workspace| {
                    workspace.original_identity == item.root_identity && workspace.direct.is_some()
                })
                .count()
                != 1
        {
            return false;
        }
        roots.push(item.root_identity.clone());
    }
    true
}

/// Created only after verifying the stopped Job Object and freezing the
/// selected project's checked output into the exact transaction plan.
struct FrozenOutput {
    recovery_id: String,
    command_digest: Sha256,
    transactions: Vec<RootTransaction>,
}

struct RootPlan<'a> {
    root_identity: &'a RootIdentity,
    transaction_id: &'a str,
    operations: &'a [Operation],
    guards: &'a BTreeMap<RelPath, FileSnapshot>,
}

pub(super) fn scratch_root(store: &Store, binding: &Binding) -> Result<Root> {
    let parent = scratch_parent(store, binding)?;
    let relative = RelPath::new(&binding.id)?;
    let root = Root::open(&parent.path.join(relative.as_str()))?;
    root.verify_private_dir()?;
    if root.identity != binding.scratch_identity
        || parent.inspect(&relative)?.identity() != Some(&binding.scratch_identity)
    {
        return Err(changed("命令暂存目录身份已改变，已保留现场"));
    }
    parent.revalidate()?;
    Ok(root)
}

fn scratch_parent(store: &Store, binding: &Binding) -> Result<Root> {
    if !valid_id(&binding.id) {
        return Err(rejected("命令暂存标识无效，已保留现场"));
    }
    let data = Root::open(store.data_dir())?;
    if data.identity != binding.data_identity {
        return Err(changed("工作台数据目录身份已改变，已保留命令暂存"));
    }
    let parent = Root::open(&data.path.join("command-scratch"))?;
    parent.verify_private_dir()?;
    parent.revalidate()?;
    data.revalidate()?;
    Ok(parent)
}

fn current_job(store: &mut Store, binding: &Binding) -> Result<JobRecord> {
    let job: JobRecord = store
        .record("job", &binding.job_id)?
        .ok_or_else(|| rejected("命令作业不存在，已保留暂存"))?;
    let run: AgentRun = store
        .record("agentRun", &binding.run_id)?
        .ok_or_else(|| rejected("命令会话不存在，已保留暂存"))?;
    if job.id != binding.job_id
        || job.terminal()
        || job.kind != "agent"
        || job.agent_run_id.as_deref() != Some(binding.run_id.as_str())
        || run.id != binding.run_id
        || run.job_id != binding.job_id
        || run.binding_revision != binding.binding_revision
        || run.project_access.as_ref() != Some(&binding.grants)
        || run.workspaces != binding.workspaces
    {
        return Err(changed("作业、会话或工程授权已变化，已保留命令暂存"));
    }
    Ok(job)
}

fn roots(
    store: &Store,
    binding: &Binding,
    cancel: &Cancellation,
) -> Result<BTreeMap<String, (Root, Root)>> {
    if binding.projects.is_empty()
        || binding.projects.len() > MAX_PROJECTS
        || binding.grants.len() > MAX_PROJECTS
        || binding.workspaces.len() > MAX_PROJECTS
    {
        return Err(rejected("命令工程数量无效，已保留暂存"));
    }
    let scratch = scratch_root(store, binding)?;
    let mut grants = BTreeMap::new();
    for grant in &binding.grants {
        if grants
            .insert(grant.project_id.clone(), grant.mode)
            .is_some()
        {
            return Err(rejected("命令工程授权重复，已保留暂存"));
        }
    }
    let mut result = BTreeMap::new();
    let mut children = BTreeSet::new();
    let mut scratch_ids = Vec::new();
    for project in &binding.projects {
        cancel.check()?;
        let mode = grants
            .get(&project.project_id)
            .ok_or_else(|| rejected("命令工程未授权，已保留暂存"))?;
        if !project.source_path.is_absolute()
            || !valid_id(project.scratch_child.as_str())
            || project.scratch_child.as_str().contains('/')
            || !children.insert(project.scratch_child.as_str().to_ascii_lowercase())
            || scratch_ids.contains(&project.scratch_identity)
        {
            return Err(rejected("命令工程或暂存绑定无效，已保留现场"));
        }
        let matches: Vec<_> = binding
            .workspaces
            .iter()
            .filter(|workspace| workspace.project_ids.contains(&project.project_id))
            .collect();
        if matches.len() != 1 {
            return Err(rejected("命令工程映射不唯一，已保留暂存"));
        }
        let workspace = matches[0];
        let direct = workspace
            .direct
            .as_ref()
            .ok_or_else(|| rejected("命令工程不是直接授权模式"))?;
        let git_root = Root::open(&PathBuf::from(&workspace.original_root))?;
        if git_root.identity != workspace.original_identity {
            return Err(changed("命令 Git 根目录身份已改变，已保留暂存"));
        }
        super::agent_workspace::check_baseline(&git_root.path, &direct.baseline, cancel)?;
        let prefix = workspace
            .project_prefixes
            .get(&project.project_id)
            .ok_or_else(|| rejected("命令工程路径映射缺失"))?;
        if !prefix.is_empty() {
            RelPath::new(prefix)?;
        }
        let expected_root = Root::open(&PathBuf::from(&workspace.original_root).join(prefix))?;
        let source = Root::open(&project.source_path)?;
        let child = Root::open(&scratch.path.join(project.scratch_child.as_str()))?;
        child.verify_private_dir()?;
        if direct.project_identities.get(&project.project_id) != Some(&project.source_identity)
            || direct
                .project_modes
                .as_ref()
                .and_then(|m| m.get(&project.project_id))
                != Some(mode)
            || source.identity != project.source_identity
            || expected_root.identity != project.source_identity
            || child.identity != project.scratch_identity
            || scratch.inspect(&project.scratch_child)?.identity()
                != Some(&project.scratch_identity)
        {
            return Err(changed("命令工程授权或目录身份已改变，已保留暂存"));
        }
        source.revalidate()?;
        expected_root.revalidate()?;
        child.revalidate()?;
        scratch_ids.push(project.scratch_identity.clone());
        if result
            .insert(project.project_id.clone(), (source, child))
            .is_some()
        {
            return Err(rejected("命令工程重复，已保留暂存"));
        }
    }
    if !result.keys().eq(grants.keys()) {
        return Err(rejected("命令工程与已选授权不一致，已保留暂存"));
    }
    scratch.revalidate()?;
    Ok(result)
}

fn check_cwd(roots: &BTreeMap<String, (Root, Root)>, command: &CommandSpec<'_>) -> Result<()> {
    let (source, scratch) = roots
        .get(command.project_id)
        .ok_or_else(|| rejected("命令工程未授权"))?;
    if let Some(cwd) = command.cwd {
        if !matches!(source.inspect(cwd)?, FileSnapshot::Directory { .. })
            || !matches!(scratch.inspect(cwd)?, FileSnapshot::Directory { .. })
        {
            return Err(changed("命令工作目录不可用，已保留暂存").at(cwd));
        }
    }
    Ok(())
}

fn save(store: &mut Store, job: &JobRecord, record: &Record, event_kind: &str) -> Result<()> {
    let recovery = serde_json::to_string(record).map_err(|_| rejected("无法编码命令恢复记录"))?;
    if recovery.len() > MAX_RECORD_BYTES {
        return Err(rejected("命令恢复记录过大，已保留暂存"));
    }
    let job_json = serde_json::to_string(job).map_err(|_| rejected("无法编码作业记录"))?;
    let effects = CommitEffects {
        records: vec![
            RecordWrite {
                kind: KIND.into(),
                id: record.binding.id.clone(),
                payload: Some(recovery),
            },
            RecordWrite {
                kind: "job".into(),
                id: job.id.clone(),
                payload: Some(job_json),
            },
        ],
        blobs: Vec::new(),
    };
    let payload = json!({"recoveryId": record.binding.id, "phase": event_kind});
    store.write_extensions_with_event(
        &effects,
        EventWrite {
            job_id: &job.id,
            kind: "commandRecovery",
            payload: &payload,
        },
    )
}

/// Create and register all private scratch directories while continuously
/// holding the same lock as job admission. Every failure retains the evidence.
pub(super) fn create_and_begin_copying(
    shared: &Arc<Mutex<Store>>,
    job_id: &str,
    run_id: &str,
    recovery_id: &str,
    projects: Vec<ProjectSource>,
    command: &CommandSpec<'_>,
    cancel: &Cancellation,
) -> Result<Binding> {
    let mut store = super::jobs::lock_store(shared)?;
    cancel.check()?;
    ensure_none(&mut store)?;
    if !valid_id(recovery_id) || projects.is_empty() || projects.len() > MAX_PROJECTS {
        return Err(rejected("命令暂存绑定无效"));
    }
    let job: JobRecord = store
        .record("job", job_id)?
        .ok_or_else(|| rejected("命令作业不存在"))?;
    let run: AgentRun = store
        .record("agentRun", run_id)?
        .ok_or_else(|| rejected("命令会话不存在"))?;
    let grants = run
        .project_access
        .ok_or_else(|| rejected("Windows 离线命令需要显式工程授权"))?;
    if job.id != job_id
        || job.terminal()
        || job.kind != "agent"
        || job.agent_run_id.as_deref() != Some(run_id)
        || run.id != run_id
        || run.job_id != job_id
    {
        return Err(changed("作业或会话已变化，未创建命令暂存"));
    }
    if !projects
        .iter()
        .any(|project| project.project_id == command.project_id)
    {
        return Err(rejected("未提供命令工程的受检来源"));
    }
    let command_digest = command.digest()?;
    let scratch_path = RelPath::new(recovery_id)?;
    let mut children = BTreeSet::new();
    for project in &projects {
        if !valid_id(project.scratch_child.as_str())
            || !children.insert(project.scratch_child.as_str())
        {
            return Err(rejected("命令暂存子目录无效或重复"));
        }
    }
    for other in store.records::<JobRecord>("job")? {
        if other.id != job.id
            && !other.terminal()
            && (other.roots.is_empty() || super::jobs::roots_overlap(&job.roots, &other.roots)?)
        {
            return Err(rejected("存在可能写入此次命令工程的作业，暂不能登记命令"));
        }
    }
    let data = Root::open(store.data_dir())?;
    let parent_path = RelPath::new("command-scratch")?;
    if data.inspect(&parent_path)? == FileSnapshot::Missing {
        data.create_private_dir(&parent_path)?;
    }
    let parent = Root::open(&data.path.join(parent_path.as_str()))?;
    parent.verify_private_dir()?;
    if !parent.list(None)?.is_empty() {
        return Err(rejected("发现未登记的命令暂存，请保留现场并核对"));
    }
    let scratch_identity = parent.create_private_dir(&scratch_path)?;
    let register = (|| {
        let scratch = Root::open(&parent.path.join(scratch_path.as_str()))?;
        scratch.verify_private_dir()?;
        if scratch.identity != scratch_identity {
            return Err(changed("命令暂存目录身份已改变，已保留现场"));
        }
        let mut bindings = Vec::with_capacity(projects.len());
        for project in projects {
            cancel.check()?;
            let scratch_identity = scratch.create_private_dir(&project.scratch_child)?;
            bindings.push(ProjectBinding {
                project_id: project.project_id,
                source_path: project.source_path,
                source_identity: project.source_identity,
                scratch_child: project.scratch_child,
                scratch_identity,
            });
        }
        let binding = Binding {
            id: recovery_id.to_owned(),
            job_id: job_id.to_owned(),
            run_id: run_id.to_owned(),
            binding_revision: run.binding_revision,
            data_identity: data.identity.clone(),
            scratch_identity: scratch_identity.clone(),
            command_digest,
            grants,
            workspaces: run.workspaces,
            projects: bindings,
        };
        scratch.revalidate()?;
        parent.revalidate()?;
        data.revalidate()?;
        begin_copying(&mut store, binding.clone(), command, cancel)?;
        Ok(binding)
    })();
    if register.is_err() {
        // No process could have received this new directory. If registration
        // committed before the error, close that exact record durably first.
        match store.record::<Record>(KIND, recovery_id) {
            Ok(Some(mut record))
                if record.format_version == FORMAT_VERSION
                    && record.binding.id == recovery_id
                    && record.binding.job_id == job_id
                    && record.binding.run_id == run_id
                    && record.binding.scratch_identity == scratch_identity
                    && record.root_transactions.is_empty()
                    && matches!(record.phase, Phase::Copying) =>
            {
                finish(&mut store, &job, &mut record)?;
            }
            Ok(None) => {
                parent.remove_private_tree(&scratch_path, &scratch_identity, MAX_FILES, 64)?
            }
            _ => (),
        }
    }
    register
}

fn begin_copying(
    store: &mut Store,
    binding: Binding,
    command: &CommandSpec<'_>,
    cancel: &Cancellation,
) -> Result<()> {
    check_command(&binding, command)?;
    let job = current_job(store, &binding)?;
    if store.has_record(KIND, &binding.id)? {
        return Err(rejected("命令暂存已有恢复记录，请先核对"));
    }
    // The caller holds the same Store mutex used by job admission. Do not call
    // unresolved here: this command's newly created scratch is not registered
    // yet and would itself be reported as an orphan.
    if !store.records::<Record>(KIND)?.is_empty() {
        return Err(rejected("存在其他尚未核对的命令暂存，请保留现场"));
    }
    let roots = roots(store, &binding, cancel)?;
    let parent = Root::open(&store.data_dir().join("command-scratch"))?;
    parent.verify_private_dir()?;
    if parent.list(None)?.as_slice() != [binding.id.as_str()] {
        return Err(rejected("发现其他未登记的命令暂存，请保留现场并核对"));
    }
    parent.revalidate()?;
    // During Copying the scratch tree is still empty, so only source cwd can be
    // checked here. Both source and scratch are checked after the copy.
    if let Some(cwd) = command.cwd {
        let (source, _) = roots
            .get(command.project_id)
            .ok_or_else(|| rejected("命令工程未授权"))?;
        if !matches!(source.inspect(cwd)?, FileSnapshot::Directory { .. }) {
            return Err(changed("命令工作目录不可用，已保留暂存").at(cwd));
        }
    }
    save(
        store,
        &job,
        &Record {
            format_version: FORMAT_VERSION,
            binding: binding.clone(),
            phase: Phase::Copying,
            root_transactions: Vec::new(),
        },
        "copying",
    )?;
    for (source, scratch) in roots.values() {
        source
            .revalidate()
            .and_then(|_| scratch.revalidate())
            .map_err(|_| rejected("登记后目录身份待核实，已保留命令暂存"))?;
    }
    scratch_root(store, &binding).map_err(|_| rejected("登记后暂存身份待核实，已保留现场"))?;
    Ok(())
}

fn snapshot(root: &Root, path: &RelPath, expected: &FileSnapshot) -> Result<()> {
    let actual = match expected {
        FileSnapshot::File { .. } => {
            root.read_checked_single_link(path, crate::model::MAX_ASSET)?
                .snapshot
        }
        FileSnapshot::Directory { .. } => root.inspect(path)?,
        _ => return Err(rejected("命令起始快照包含不安全对象")),
    };
    if &actual != expected {
        return Err(changed("命令起始文件已变化，已保留暂存").at(path));
    }
    Ok(())
}

fn authorized_path(binding: &Binding, project_id: &str, path: &RelPath) -> Result<bool> {
    let workspace = binding
        .workspaces
        .iter()
        .find(|workspace| workspace.project_ids.iter().any(|id| id == project_id))
        .ok_or_else(|| rejected("命令工程授权缺失"))?;
    let prefix = workspace
        .project_prefixes
        .get(project_id)
        .ok_or_else(|| rejected("命令工程路径映射缺失"))?;
    let full = repository_path(prefix, path)?;
    if !super::agent_workspace::direct_path_allowed(workspace, &full) {
        return Ok(false);
    }
    let mut best: Option<(usize, &str)> = None;
    for (id, candidate) in &workspace.project_prefixes {
        let contained = crate::fs::windows_path_under(full.as_str(), candidate)
            .ok_or_else(|| rejected("无法核验 Windows 工程路径"))?;
        if contained {
            let depth = candidate.split('/').filter(|part| !part.is_empty()).count();
            if best.is_some_and(|(current, other)| current == depth && other != id) {
                return Err(rejected("命令工程路径映射有歧义"));
            }
            if best.is_none_or(|(current, _)| depth > current) {
                best = Some((depth, id));
            }
        }
    }
    Ok(best.is_some_and(|(_, id)| id == project_id)
        && workspace
            .direct
            .as_ref()
            .and_then(|direct| direct.project_modes.as_ref())
            .and_then(|modes| modes.get(project_id))
            .is_some_and(|mode| {
                binding
                    .grants
                    .iter()
                    .any(|grant| grant.project_id == project_id && &grant.mode == mode)
            }))
}

fn repository_path(prefix: &str, path: &RelPath) -> Result<RelPath> {
    if prefix.is_empty() {
        Ok(path.clone())
    } else {
        RelPath::new(prefix)?.join(path.as_str())
    }
}

/// Record the checked inputs. The launcher has not been authorized yet.
pub(super) fn mark_ready(
    store: &mut Store,
    binding: &Binding,
    command: &CommandSpec<'_>,
    verified: Vec<VerifiedManifest<'_>>,
    cancel: &Cancellation,
) -> Result<()> {
    check_command(binding, command)?;
    let job = current_job(store, binding)?;
    let old: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令恢复登记不存在，已保留暂存"))?;
    if old.format_version != FORMAT_VERSION
        || old.binding != *binding
        || old.phase != Phase::Copying
        || !old.root_transactions.is_empty()
    {
        return Err(changed("命令恢复登记已变化，已保留暂存"));
    }
    let roots = roots(store, binding, cancel)?;
    check_cwd(&roots, command)?;
    if verified.len() != roots.len() {
        return Err(rejected("命令工程快照不完整，已保留暂存"));
    }
    let mut inputs = Vec::with_capacity(verified.len());
    for proof in &verified {
        proof.revalidate(cancel)?;
        let manifest = proof.manifest();
        let project = binding
            .projects
            .iter()
            .find(|project| project.project_id == manifest.binding.project_id)
            .ok_or_else(|| rejected("命令快照包含未授权工程"))?;
        if manifest.binding.job_id != binding.job_id
            || manifest.binding.source_identity != project.source_identity
            || manifest.binding.scratch_identity != project.scratch_identity
            || !binding.grants.iter().any(|grant| {
                grant.project_id == project.project_id && grant.mode == manifest.binding.mode
            })
        {
            return Err(changed("命令快照与恢复绑定不一致，已保留暂存"));
        }
        let (source, scratch) = roots
            .get(&project.project_id)
            .ok_or_else(|| rejected("命令工程绑定缺失"))?;
        let manifest_source = Root::open(&manifest.binding.source_path)?;
        let manifest_scratch = Root::open(&manifest.binding.scratch_path)?;
        if manifest_source.identity != source.identity
            || manifest_scratch.identity != scratch.identity
        {
            return Err(changed("命令快照目录身份已改变，已保留暂存"));
        }
        manifest_source.revalidate()?;
        manifest_scratch.revalidate()?;
        inputs.push(ProjectInput {
            project_id: project.project_id.clone(),
            entries: manifest
                .entries
                .iter()
                .map(|(path, entry)| {
                    (
                        path.clone(),
                        InputEntry {
                            source: entry.source.clone(),
                            copied: entry.copied.clone(),
                        },
                    )
                })
                .collect(),
            excluded: manifest.excluded.clone(),
        });
    }
    let mut seen = BTreeSet::new();
    let mut total = 0usize;
    for input in &inputs {
        let (source, scratch) = roots
            .get(&input.project_id)
            .ok_or_else(|| rejected("命令快照包含未授权工程"))?;
        if !seen.insert(&input.project_id) {
            return Err(rejected("命令工程快照重复"));
        }
        total = total
            .checked_add(input.entries.len())
            .ok_or_else(|| rejected("命令快照条目过多"))?;
        if total > MAX_FILES
            || input.excluded.len() > MAX_FILES
            || input.entries.len().saturating_mul(input.excluded.len()) > 1_000_000
        {
            return Err(rejected("命令快照条目过多，已保留暂存"));
        }
        for (path, entry) in &input.entries {
            if !authorized_path(binding, &input.project_id, path)? {
                return Err(rejected("命令快照包含未授权路径，已保留暂存").at(path));
            }
            for excluded in &input.excluded {
                if crate::fs::windows_path_under(path.as_str(), excluded.as_str()) != Some(false) {
                    return Err(rejected("命令快照包含排除范围，已保留暂存").at(path));
                }
            }
            match (&entry.source, &entry.copied) {
                (
                    FileSnapshot::File { descriptor: a, .. },
                    FileSnapshot::File { descriptor: b, .. },
                ) if a == b => {}
                (FileSnapshot::Directory { .. }, FileSnapshot::Directory { .. }) => {}
                _ => return Err(rejected("命令输入快照不一致，已保留暂存").at(path)),
            }
            snapshot(source, path, &entry.source)?;
            snapshot(scratch, path, &entry.copied)?;
        }
        source.revalidate()?;
        scratch.revalidate()?;
    }
    // Re-enumerate immediately before the SQLite transaction; filesystem and
    // SQLite cannot share a transaction, so launch must revalidate again.
    for proof in &verified {
        proof.revalidate(cancel)?;
    }
    save(
        store,
        &job,
        &Record {
            format_version: FORMAT_VERSION,
            binding: binding.clone(),
            phase: Phase::Prepared { inputs },
            root_transactions: Vec::new(),
        },
        "prepared",
    )?;
    for (source, scratch) in roots.values() {
        source
            .revalidate()
            .and_then(|_| scratch.revalidate())
            .map_err(|_| rejected("快照登记后目录身份待核实，已保留暂存"))?;
    }
    for proof in &verified {
        proof
            .revalidate(cancel)
            .map_err(|_| rejected("快照登记后内容待核实，已保留现场"))?;
    }
    scratch_root(store, binding).map_err(|_| rejected("快照登记后暂存身份待核实，已保留现场"))?;
    Ok(())
}

fn launch_record(
    store: &mut Store,
    binding: &Binding,
    command: &CommandSpec<'_>,
) -> Result<(JobRecord, Record)> {
    check_command(binding, command)?;
    let job = current_job(store, binding)?;
    let record: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令恢复登记不存在，已保留暂存"))?;
    if record.format_version != FORMAT_VERSION
        || record.binding != *binding
        || !record.root_transactions.is_empty()
    {
        return Err(changed("命令恢复登记已改变，已保留暂存"));
    }
    scratch_root(store, binding)?;
    Ok((job, record))
}

/// Commit before creating the profile, granting ACLs or launching a process.
pub(super) fn begin_launch(
    store: &mut Store,
    binding: &Binding,
    command: &CommandSpec<'_>,
    cancel: &Cancellation,
) -> Result<()> {
    cancel.check()?;
    let (job, mut record) = launch_record(store, binding, command)?;
    let Phase::Prepared { inputs } = record.phase else {
        return Err(changed("命令尚未准备或已开始启动，已保留暂存"));
    };
    record.phase = Phase::Launching { inputs };
    cancel.check()?;
    save(store, &job, &record, "launching")
}

/// Record the broker's proof that the process tree stopped and ACLs were restored.
/// This evidence still has to be saved after cancellation.
pub(super) fn mark_stopped(
    store: &mut Store,
    binding: &Binding,
    command: &CommandSpec<'_>,
) -> Result<()> {
    let (job, mut record) = launch_record(store, binding, command)?;
    let Phase::Launching { inputs } = record.phase else {
        return Err(changed("命令启动登记不一致，已保留暂存"));
    };
    record.phase = Phase::Stopped { inputs };
    save(store, &job, &record, "stopped")
}

/// Reserve every root plan and its exact journal identity together with the
/// current job event. Root filesystem changes follow the SQLite commit.
fn reserve_root_transactions(
    store: &mut Store,
    binding: &Binding,
    command: &CommandSpec<'_>,
    frozen: &FrozenOutput,
    plans: &[RootPlan<'_>],
    cancel: &Cancellation,
) -> Result<()> {
    check_command(binding, command)?;
    let job = current_job(store, binding)?;
    let mut record: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令恢复登记不存在，已保留暂存"))?;
    if record.format_version != FORMAT_VERSION
        || record.binding != *binding
        || !matches!(record.phase, Phase::Stopped { .. })
        || !valid_transactions(&record)
        || !record.root_transactions.is_empty()
        || frozen.recovery_id != binding.id
        || frozen.command_digest != binding.command_digest
        || plans.is_empty()
        || plans.len() > MAX_PROJECTS
    {
        return Err(changed("命令、冻结输出或恢复登记不一致，已保留现场"));
    }
    let checked_roots = roots(store, binding, cancel)?;
    let mut reservations = Vec::with_capacity(plans.len());
    let mut ids = BTreeSet::new();
    let mut identities = Vec::new();
    for plan in plans {
        cancel.check()?;
        if !valid_id(plan.transaction_id)
            || !ids.insert(plan.transaction_id.to_owned())
            || identities.contains(plan.root_identity)
        {
            return Err(rejected("写回事务标识或根目录重复，已保留现场"));
        }
        let matches: Vec<_> = binding
            .workspaces
            .iter()
            .filter(|workspace| {
                &workspace.original_identity == plan.root_identity && workspace.direct.is_some()
            })
            .collect();
        if matches.len() != 1 {
            return Err(rejected("写回根不属于唯一的工程授权，已保留现场"));
        }
        let workspace = matches[0];
        let root = Root::open(Path::new(&workspace.original_root))?;
        if &root.identity != plan.root_identity {
            return Err(changed("写回根目录身份已改变，已保留现场"));
        }
        let digest = transaction::reserved_files_plan_digest(
            plan.transaction_id,
            plan.root_identity,
            plan.operations,
            plan.guards,
        )?;
        // Before registration an existing ID is a conflict, never a journal to
        // adopt. After registration absence is instead an unknown result.
        if transaction::inspect_reserved_files(
            &root.path,
            plan.root_identity,
            plan.transaction_id,
            &digest,
        )?
        .is_some()
        {
            return Err(rejected("写回事务标识已存在，已保留现场"));
        }
        root.revalidate()?;
        identities.push(plan.root_identity.clone());
        reservations.push(RootTransaction {
            root_identity: plan.root_identity.clone(),
            transaction_id: plan.transaction_id.to_owned(),
            plan_digest: digest,
        });
    }
    if reservations != frozen.transactions {
        return Err(changed("写回计划与冻结输出不一致，已保留现场"));
    }
    for (source, scratch) in checked_roots.values() {
        source.revalidate()?;
        scratch.revalidate()?;
    }
    record.root_transactions = reservations;
    save(store, &job, &record, "writebackReserved")?;
    for (source, scratch) in checked_roots.values() {
        source
            .revalidate()
            .and_then(|_| scratch.revalidate())
            .map_err(|_| rejected("写回登记后目录身份待核实，已保留现场"))?;
    }
    Ok(())
}

/// Apply the selected project's output only after the sandbox attests the Job
/// Object has exited. The caller supplies the changes from the mapped-tree diff;
/// this function binds every changed path and byte to the durable input and
/// stopped scratch identities before handing the immutable plan to the journal.
pub(super) fn apply_stopped(
    shared: &Arc<Mutex<Store>>,
    binding: &Binding,
    command: &CommandSpec<'_>,
    stopped: &StoppedCommand,
    changes: Vec<WindowsCommandChange>,
    cancel: &Cancellation,
) -> Result<()> {
    check_command(binding, command)?;
    cancel.check()?;
    let (executable, environment, policy) = stopped.binding();
    if changes.is_empty()
        || !stopped.status().success()
        || executable != command.executable_digest
        || environment != command.environment_digest
        || policy != command.policy_digest
    {
        return Err(rejected("停止证明与命令写回不一致，已保留暂存"));
    }
    let project = binding
        .projects
        .iter()
        .find(|project| project.project_id == command.project_id)
        .ok_or_else(|| rejected("命令工程未授权"))?;
    let workspace = binding
        .workspaces
        .iter()
        .find(|workspace| workspace.project_prefixes.contains_key(command.project_id))
        .ok_or_else(|| rejected("命令工程路径映射缺失"))?;
    let prefix = &workspace.project_prefixes[command.project_id];
    let baseline = &workspace
        .direct
        .as_ref()
        .ok_or_else(|| rejected("命令工程不是直接授权模式"))?
        .baseline;
    let repository = Root::open(Path::new(&workspace.original_root))?;
    if repository.identity != workspace.original_identity {
        return Err(changed("写回根目录身份已改变，已保留现场"));
    }

    let (inputs, scratch) = {
        let mut store = super::jobs::lock_store(shared)?;
        current_job(&mut store, binding)?;
        let record: Record = store
            .record(KIND, &binding.id)?
            .ok_or_else(|| rejected("命令恢复记录缺失，已保留现场"))?;
        if record.format_version != FORMAT_VERSION
            || record.binding != *binding
            || !record.root_transactions.is_empty()
        {
            return Err(changed("命令恢复记录已改变，已保留现场"));
        }
        let Phase::Stopped { inputs } = record.phase else {
            return Err(rejected("命令停止证明尚未登记，已保留现场"));
        };
        let inputs = inputs
            .into_iter()
            .find(|item| item.project_id == command.project_id)
            .ok_or_else(|| rejected("缺少已登记的命令工程快照"))?;
        let root = scratch_root(&store, binding)?;
        let scratch = Root::open(&root.path.join(project.scratch_child.as_str()))?;
        scratch.verify_private_dir()?;
        if scratch.identity != project.scratch_identity
            || stopped.scratch_identity() != &scratch.identity
            || stopped.scratch_path() != scratch.path
        {
            return Err(changed("停止证明与暂存身份不一致，已保留现场"));
        }
        (inputs, scratch)
    };

    let guards = inputs
        .entries
        .iter()
        .map(|(path, input)| Ok((repository_path(prefix, path)?, input.source.clone())))
        .collect::<Result<BTreeMap<_, _>>>()?;
    let mut operations = Vec::with_capacity(changes.len());
    let mut frozen_entries = BTreeMap::new();
    let mut output_bytes = 0u64;
    for change in changes {
        cancel.check()?;
        let (path, before, after) = match change {
            WindowsCommandChange::CreateDir { path } => {
                let after = scratch.inspect(&path)?;
                if !matches!(after, FileSnapshot::Directory { .. }) {
                    return Err(changed("命令输出目录已改变").at(path));
                }
                (path, FileSnapshot::Missing, after)
            }
            WindowsCommandChange::CreateFile { path, output } => {
                (path, FileSnapshot::Missing, output)
            }
            WindowsCommandChange::ReplaceFile {
                path,
                before,
                output,
            } => (path, before, output),
            WindowsCommandChange::DeleteFile { path, before } => {
                (path, before, FileSnapshot::Missing)
            }
        };
        if !authorized_path(binding, command.project_id, &path)?
            || inputs.excluded.iter().any(|excluded| {
                crate::fs::windows_path_under(path.as_str(), excluded.as_str()) != Some(false)
            })
            || frozen_entries.insert(path.clone(), after.clone()).is_some()
            || inputs
                .entries
                .get(&path)
                .map_or(&FileSnapshot::Missing, |entry| &entry.source)
                != &before
        {
            return Err(rejected("命令输出路径、原始基线或授权已改变，已保留暂存").at(path));
        }
        let full = repository_path(prefix, &path)?;
        let operation = match &after {
            FileSnapshot::Missing => {
                if !matches!(before, FileSnapshot::File { .. })
                    || scratch.inspect(&path)? != FileSnapshot::Missing
                {
                    return Err(changed("命令删除项已改变").at(path));
                }
                Operation::Delete { path: full, before }
            }
            FileSnapshot::Directory { .. } if before == FileSnapshot::Missing => {
                snapshot(&scratch, &path, &after)?;
                Operation::CreateDir { path: full }
            }
            FileSnapshot::File { descriptor, .. } => {
                output_bytes = output_bytes
                    .checked_add(descriptor.size)
                    .filter(|total| *total <= MAX_ASSET)
                    .ok_or_else(|| rejected("命令输出超过累计写回限制，已保留暂存"))?;
                let checked = scratch.read_checked_single_link(&path, MAX_ASSET)?;
                if checked.snapshot != after {
                    return Err(changed("读取时命令输出文件已改变").at(path));
                }
                let file = RenderedFile {
                    bytes: checked.bytes,
                    mode: descriptor.mode,
                };
                if before == FileSnapshot::Missing {
                    Operation::Create { path: full, file }
                } else if matches!(before, FileSnapshot::File { .. }) {
                    Operation::Replace {
                        path: full,
                        before,
                        file,
                    }
                } else {
                    return Err(rejected("命令输出改变了原文件类型，已保留暂存").at(path));
                }
            }
            _ => return Err(rejected("命令输出类型无法写回，已保留暂存").at(path)),
        };
        operations.push(operation);
    }

    let transaction_id = unique_id()?;
    let digest = transaction::reserved_files_plan_digest(
        &transaction_id,
        &repository.identity,
        &operations,
        &guards,
    )?;
    let frozen = FrozenOutput {
        recovery_id: binding.id.clone(),
        command_digest: binding.command_digest.clone(),
        transactions: vec![RootTransaction {
            root_identity: repository.identity.clone(),
            transaction_id: transaction_id.clone(),
            plan_digest: digest.clone(),
        }],
    };
    let validate = || -> Result<()> {
        {
            let mut store = super::jobs::lock_store(shared)?;
            current_job(&mut store, binding)?;
        }
        repository.revalidate()?;
        scratch.verify_private_dir()?;
        super::agent_workspace::check_baseline(&repository.path, baseline, cancel)
    };
    validate()?;
    for (path, snapshot_after) in &frozen_entries {
        cancel.check()?;
        match snapshot_after {
            FileSnapshot::Missing if scratch.inspect(path)? == FileSnapshot::Missing => (),
            FileSnapshot::Missing => return Err(changed("命令删除项已改变").at(path)),
            _ => snapshot(&scratch, path, snapshot_after)?,
        }
    }
    {
        let mut store = super::jobs::lock_store(shared)?;
        reserve_root_transactions(
            &mut store,
            binding,
            command,
            &frozen,
            &[RootPlan {
                root_identity: &repository.identity,
                transaction_id: &transaction_id,
                operations: &operations,
                guards: &guards,
            }],
            cancel,
        )?;
    }
    let started = std::cell::Cell::new(false);
    let result = transaction::apply_reserved_files_checked(
        &repository.path,
        repository.identity.clone(),
        &transaction_id,
        &digest,
        operations,
        guards,
        &started,
        cancel,
        &validate,
    );
    let report = match result {
        Ok(report) => report,
        Err(error) => {
            let rolled_back = started.get()
                && matches!(transaction::inspect_reserved_files(
                    &repository.path,
                    &repository.identity,
                    &transaction_id,
                    &digest,
                ), Ok(Some(state)) if state.phase == TransactionPhase::RolledBack && state.cleanup_complete);
            if !started.get() || rolled_back {
                release_reservation(shared, binding, &frozen)?;
                return Err(error);
            }
            return Err(rejected("命令写回事务结果待核对，已保留现场")
                .context("transactionId", &transaction_id)
                .context("reason", error.message));
        }
    };
    if report.status != "committed" || report.cleanup != "complete" {
        return Err(rejected("原工程事务的写回或清理尚待核对，已保留现场"));
    }
    Ok(())
}

fn release_reservation(
    shared: &Arc<Mutex<Store>>,
    binding: &Binding,
    frozen: &FrozenOutput,
) -> Result<()> {
    let mut store = super::jobs::lock_store(shared)?;
    let job = current_job(&mut store, binding)?;
    let mut record: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令恢复记录缺失，已保留现场"))?;
    if record.format_version != FORMAT_VERSION
        || record.binding != *binding
        || !matches!(record.phase, Phase::Stopped { .. })
        || record.root_transactions != frozen.transactions
    {
        return Err(rejected("命令写回登记已改变，已保留现场"));
    }
    record.root_transactions.clear();
    save(&mut store, &job, &record, "writebackAbandoned")
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum RootTransactionStatus {
    /// Includes a missing, unreadable, replaced, or inconsistent journal.
    Unknown,
    Observed(TransactionPhase),
}

/// Read-only journal evidence. A returned status never authorizes a retry,
/// cleanup, rollback, or access to a path from a restored database.
pub(super) fn inspect_root_transaction(
    store: &mut Store,
    binding: &Binding,
    root_path: &Path,
    root_identity: &RootIdentity,
    transaction_id: &str,
) -> Result<RootTransactionStatus> {
    let Some(record): Option<Record> = store.record(KIND, &binding.id)? else {
        return Ok(RootTransactionStatus::Unknown);
    };
    if record.binding != *binding
        || !matches!(record.phase, Phase::Ready { .. } | Phase::Stopped { .. })
        || !valid_record(&record)
        || !matches!(Root::open(store.data_dir()), Ok(root) if root.identity == binding.data_identity)
    {
        return Ok(RootTransactionStatus::Unknown);
    }
    let matching: Vec<_> = record
        .root_transactions
        .iter()
        .filter(|item| {
            &item.root_identity == root_identity && item.transaction_id == transaction_id
        })
        .collect();
    if matching.len() != 1 || !valid_id(transaction_id) {
        return Ok(RootTransactionStatus::Unknown);
    }
    let item = matching[0];
    let evidence = transaction::inspect_reserved_files(
        root_path,
        root_identity,
        transaction_id,
        &item.plan_digest,
    );
    Ok(match evidence {
        Ok(Some(state)) => RootTransactionStatus::Observed(state.phase),
        Ok(None) | Err(_) => RootTransactionStatus::Unknown,
    })
}

/// The broker may finish a command that never started, or whose whole Job
/// Object is known to have exited. The caller must release scratch handles.
pub(super) fn finish_without_writeback(
    shared: &Arc<Mutex<Store>>,
    binding: &Binding,
) -> Result<()> {
    finish_no_writeback(shared, binding, false)
}

/// An active broker can also abandon a launch when the failure proves
/// that the launcher never changed ACLs or started a process.
pub(super) fn finish_verified_failure(
    shared: &Arc<Mutex<Store>>,
    binding: &Binding,
    failure: &YssError,
) -> Result<()> {
    if failure.context.get("cleanupVerified").map(String::as_str) != Some("true") {
        return Err(rejected("命令异常后的清理状态无法确认，已保留暂存"));
    }
    finish_no_writeback(shared, binding, true)
}

fn finish_no_writeback(
    shared: &Arc<Mutex<Store>>,
    binding: &Binding,
    verified_failure: bool,
) -> Result<()> {
    let mut store = super::jobs::lock_store(shared)?;
    let job = current_job(&mut store, binding)?;
    let mut record: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令暂存记录缺失，已保留现场"))?;
    if record.format_version != FORMAT_VERSION
        || record.binding != *binding
        || !record.root_transactions.is_empty()
        || !may_finish_without_writeback(&record.phase, verified_failure)
    {
        return Err(rejected("命令停止或写回状态无法确认，已保留暂存供核对"));
    }
    finish(&mut store, &job, &mut record)
}

fn may_finish_without_writeback(phase: &Phase, verified_failure: bool) -> bool {
    matches!(
        phase,
        Phase::Copying | Phase::Prepared { .. } | Phase::Stopped { .. } | Phase::Cleaning
    ) || (verified_failure && matches!(phase, Phase::Launching { .. }))
}

/// Finish scratch only after the exact reserved original-project journal has
/// committed and cleaned its transaction material.
pub(super) fn finish_after_writeback(shared: &Arc<Mutex<Store>>, binding: &Binding) -> Result<()> {
    let mut store = super::jobs::lock_store(shared)?;
    let job = current_job(&mut store, binding)?;
    let mut record: Record = store
        .record(KIND, &binding.id)?
        .ok_or_else(|| rejected("命令暂存记录缺失，已保留现场"))?;
    if record.format_version != FORMAT_VERSION
        || record.binding != *binding
        || !matches!(record.phase, Phase::Stopped { .. } | Phase::Cleaning)
        || !valid_transactions(&record)
        || record.root_transactions.is_empty()
    {
        return Err(rejected("命令写回记录不一致，已保留现场"));
    }
    if !transactions_finished(&record, false)? {
        return Err(rejected("命令写回事务提交或清理未确认，已保留现场"));
    }
    finish(&mut store, &job, &mut record)
}

fn transactions_finished(record: &Record, allow_rollback: bool) -> Result<bool> {
    if record.root_transactions.is_empty() {
        return Ok(false);
    }
    for item in &record.root_transactions {
        let workspace = record
            .binding
            .workspaces
            .iter()
            .find(|workspace| workspace.original_identity == item.root_identity)
            .ok_or_else(|| rejected("缺少写回根目录绑定，已保留现场"))?;
        let state = transaction::inspect_reserved_files(
            Path::new(&workspace.original_root),
            &item.root_identity,
            &item.transaction_id,
            &item.plan_digest,
        )?;
        if !state.is_some_and(|state| {
            state.cleanup_complete
                && (state.phase == TransactionPhase::Committed
                    || (allow_rollback && state.phase == TransactionPhase::RolledBack))
        }) {
            return Ok(false);
        }
    }
    Ok(true)
}

fn finish(store: &mut Store, job: &JobRecord, record: &mut Record) -> Result<()> {
    if !matches!(record.phase, Phase::Cleaning) {
        // Check identity before committing permission to treat absence as
        // completed cleanup on replay.
        scratch_root(store, &record.binding)?;
        record.phase = Phase::Cleaning;
        save(store, job, record, "cleaning")?;
    }
    complete_cleanup(store, job, record)
}

fn explained_by_command_recovery(job: &JobRecord, id: &str) -> bool {
    let Some(error) = job.error.as_ref() else {
        return false;
    };
    if job.status != "recoveryRequired" || error["code"] != "RECOVERY_REQUIRED" {
        return false;
    }
    let matches_scratch = |error: &serde_json::Value| {
        error["code"] == "RECOVERY_REQUIRED" && error["context"]["scratchId"].as_str() == Some(id)
    };
    matches_scratch(error)
        || (error["message"] == "命令暂存待恢复，请保留现场并核对"
            && error.get("previousError").is_some_and(|previous| {
                matches_scratch(previous)
                    || (previous.is_null() && job.result.is_none())
                    || previous["code"]
                        .as_str()
                        .is_some_and(|code| code != "RECOVERY_REQUIRED")
            }))
}

fn complete_cleanup(store: &mut Store, job: &JobRecord, record: &Record) -> Result<()> {
    let binding = &record.binding;
    let parent = scratch_parent(store, binding)?;
    let child = RelPath::new(&binding.id)?;
    match parent.inspect(&child)? {
        FileSnapshot::Missing => (),
        FileSnapshot::Directory { identity } if identity == binding.scratch_identity => {
            parent
                .remove_private_tree(&child, &binding.scratch_identity, MAX_FILES, 64)
                .map_err(|err| {
                    rejected("命令暂存清理尚未完成，已保留现场").context("reason", err.message)
                })?;
            if parent.inspect(&child)? != FileSnapshot::Missing {
                return Err(rejected("命令暂存清理后路径出现未知内容，已保留现场"));
            }
        }
        _ => return Err(rejected("命令暂存身份已变化，已保留现场")),
    }
    parent.revalidate()?;
    let mut records = vec![RecordWrite {
        kind: KIND.into(),
        id: binding.id.clone(),
        payload: None,
    }];
    let mut payload = json!({"recoveryId": binding.id, "phase": "cleanupComplete"});
    if explained_by_command_recovery(job, &binding.id)
        && store.records::<Record>(KIND)?.iter().all(|pending| {
            pending.binding.id == binding.id || pending.binding.job_id != binding.job_id
        })
    {
        let mut recovered = job.clone();
        recovered.status = "interrupted".into();
        recovered.updated_at = super::sources::now();
        recovered.error = Some(json!({
            "code": "INTERRUPTED",
            "message": "已清理中断的命令暂存，请核对工作区后续接",
            "previousError": job.error,
        }));
        records.push(RecordWrite {
            kind: "job".into(),
            id: recovered.id.clone(),
            payload: Some(
                serde_json::to_string(&recovered)
                    .map_err(|_| rejected("无法编码已恢复的作业记录"))?,
            ),
        });
        payload["jobStatus"] = json!(recovered.status);
    }
    store.write_extensions_with_event(
        &CommitEffects {
            records,
            blobs: Vec::new(),
        },
        EventWrite {
            job_id: &binding.job_id,
            kind: "commandRecovery",
            payload: &payload,
        },
    )
}

fn cleanup_ready(record: &Record, journals_cleaned: impl FnOnce() -> bool) -> bool {
    if !record.root_transactions.is_empty() {
        return journals_cleaned();
    }
    matches!(
        record.phase,
        Phase::Copying | Phase::Prepared { .. } | Phase::Stopped { .. } | Phase::Cleaning
    )
}

/// Reclaim scratch only with durable proof it never launched or has stopped.
/// A legacy Ready record has neither proof unless a writeback was registered.
pub(super) fn retry_cleanups(store: &mut Store) -> Result<()> {
    for item in unresolved(store)? {
        let mut record: Record = store
            .record(KIND, &item.id)?
            .ok_or_else(|| rejected("命令暂存清理记录缺失"))?;
        let job = recovery_job(store, &item)?;
        if cleanup_ready(&record, || {
            matches!(transactions_finished(&record, true), Ok(true))
        }) {
            let _ = finish(store, &job, &mut record);
        }
    }
    Ok(())
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) enum PendingPhase {
    Copying,
    Ready,
    Prepared,
    Launching,
    Stopped,
    Cleaning,
}

/// A read-only projection. It confers no authority to open, write or remove any
/// saved path; a restored database must never operate on the former host's paths.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct Pending {
    pub id: String,
    pub job_id: String,
    pub run_id: String,
    pub phase: PendingPhase,
}

fn recovery_job(store: &mut Store, pending: &Pending) -> Result<JobRecord> {
    store
        .record::<JobRecord>("job", &pending.job_id)?
        .filter(|job| {
            job.id == pending.job_id
                && job.kind == "agent"
                && job.agent_run_id.as_deref() == Some(pending.run_id.as_str())
        })
        .ok_or_else(|| rejected("命令暂存关联的作业或会话不可核对，请保留现场"))
}

/// Running commands have an ordinary scratch record. Only terminal or
/// unidentifiable records prevent the diagnostics from offering commands.
pub(super) fn check_diagnostics(store: &mut Store) -> Result<()> {
    for pending in unresolved(store)? {
        if recovery_job(store, &pending)?.terminal() {
            return Err(rejected("存在尚未核对的命令暂存，请先处理恢复记录"));
        }
    }
    Ok(())
}

pub(super) fn unresolved(store: &mut Store) -> Result<Vec<Pending>> {
    let records = store
        .records::<Record>(KIND)
        .map_err(|_| rejected("命令恢复记录无法读取，请保留现场并核对"))?;
    let data = Root::open(store.data_dir())?;
    let identity = data.identity.clone();
    let mut ids = BTreeSet::new();
    let mut scratch_identities = BTreeMap::new();
    let mut result = Vec::with_capacity(records.len());
    for record in records {
        if !valid_record(&record)
            || !valid_id(&record.binding.id)
            || !ids.insert(record.binding.id.clone())
            || !store.has_record(KIND, &record.binding.id)?
            || record.binding.data_identity != identity
        {
            return Err(rejected(
                "命令恢复记录或数据目录身份不一致，请保留现场并核对",
            ));
        }
        let phase = match record.phase {
            Phase::Copying => PendingPhase::Copying,
            Phase::Ready { .. } => PendingPhase::Ready,
            Phase::Prepared { .. } => PendingPhase::Prepared,
            Phase::Launching { .. } => PendingPhase::Launching,
            Phase::Stopped { .. } => PendingPhase::Stopped,
            Phase::Cleaning => PendingPhase::Cleaning,
        };
        scratch_identities.insert(
            record.binding.id.clone(),
            (
                record.binding.scratch_identity,
                phase == PendingPhase::Cleaning,
            ),
        );
        result.push(Pending {
            id: record.binding.id,
            job_id: record.binding.job_id,
            run_id: record.binding.run_id,
            phase,
        });
    }
    let control = RelPath::new("command-scratch")?;
    match data.inspect(&control)? {
        FileSnapshot::Missing if result.is_empty() => return Ok(result),
        FileSnapshot::Directory {
            identity: parent_identity,
        } => {
            let parent = Root::open(&data.path.join(control.as_str()))?;
            parent.verify_private_dir()?;
            if parent.identity != parent_identity {
                return Err(rejected("命令暂存父目录身份已变化，请保留现场"));
            }
            let mut found = BTreeSet::new();
            for name in parent.list(None)? {
                if !valid_id(&name) {
                    return Err(rejected("命令暂存目录包含未知内容，请保留现场并核对")
                        .context("scratchId", name));
                }
                let (expected, _) = scratch_identities.get(&name).ok_or_else(|| {
                    rejected("发现未登记的命令暂存，请保留现场并核对").context("scratchId", &name)
                })?;
                let path = RelPath::new(&name)?;
                if parent.inspect(&path)?.identity() != Some(expected) {
                    return Err(rejected("命令暂存身份不一致，请保留现场并核对")
                        .context("scratchId", &name));
                }
                let scratch = Root::open(&parent.path.join(path.as_str()))?;
                scratch.verify_private_dir()?;
                if &scratch.identity != expected {
                    return Err(rejected("命令暂存身份已变化，请保留现场"));
                }
                scratch.revalidate()?;
                found.insert(name);
            }
            if scratch_identities
                .iter()
                .any(|(id, (_, cleaning))| !cleaning && !found.contains(id))
            {
                return Err(rejected("登记的命令暂存不存在，请保留现场并核对"));
            }
            parent.revalidate()?;
        }
        _ => return Err(rejected("命令暂存目录缺失或不安全，请保留现场并核对")),
    }
    data.revalidate()?;
    Ok(result)
}

/// Conservative shared gate: until a durable per-root lock/recovery protocol is
/// implemented, any unresolved scratch blocks new mutating work and backup.
/// No filesystem path from a saved record is dereferenced by this check.
pub(super) fn ensure_none(store: &mut Store) -> Result<()> {
    if !unresolved(store)?.is_empty() {
        return Err(rejected("存在尚未核对的命令暂存，请先处理恢复记录"));
    }
    Ok(())
}

/// Prevent deletion of the job or session that identifies retained scratch.
pub(super) fn ensure_history_available(
    store: &mut Store,
    job_id: Option<&str>,
    run_id: Option<&str>,
) -> Result<()> {
    if unresolved(store)?.iter().any(|record| {
        job_id == Some(record.job_id.as_str()) || run_id == Some(record.run_id.as_str())
    }) {
        return Err(rejected("作业或会话仍有关联的命令暂存，请先处理恢复记录"));
    }
    Ok(())
}
