use super::{
    readiness, rpc::Session, sandbox::PrivateDir, session_directory, session_name, session_ref,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, FileSnapshot, Root, unique_id},
    model::{RelPath, Sha256},
    workbench::{
        agent_protocol::{
            NATIVE_FORK_ANCHOR_RECORD, NATIVE_FORK_SANDBOX, NativeForkAnchor, NativeForkConfig,
            PiSession, RunRequest, native_fork_model_identity, native_fork_unavailable,
            native_fork_version_supported,
        },
        agent_workspace,
        jobs::{JobContext, lock_store},
    },
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256 as Hasher};
use std::{
    collections::{BTreeMap, BTreeSet},
    ffi::OsString,
    fs::{File, Metadata},
    io::{BufRead, BufReader, Read, Seek, Write},
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    process::Command,
};

const MAX_SESSION_BYTES: u64 = 8 * 1024 * 1024;
const SESSION_VERSION: u64 = 3;
const ISOLATION: &str = r#"
(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write* (subpath (param "SCRATCH")))
(deny file-read* (subpath (param "DATA")) (subpath (param "CONFIG")) (subpath (param "PROJECT")))
"#;

#[derive(PartialEq, Eq)]
struct Entry {
    parent: Option<String>,
    kind: String,
    role: Option<String>,
    digest: Sha256,
}

impl Entry {
    fn digest(value: &Value) -> Result<Sha256> {
        let encoded = serde_json::to_vec(value).map_err(|_| native_fork_unavailable())?;
        Ok(Sha256::of(&encoded))
    }
}

struct Transcript {
    digest: Sha256,
    parent_session: Option<PathBuf>,
    ids: Vec<String>,
    entries: BTreeMap<String, Entry>,
}

impl Transcript {
    fn branch_before(&self, entry_id: &str) -> Result<Vec<&str>> {
        let mut branch = Vec::new();
        let mut cursor = self
            .entries
            .get(entry_id)
            .ok_or_else(native_fork_unavailable)?
            .parent
            .as_deref();
        let mut has_conversation = false;
        while let Some(id) = cursor {
            let entry = self.entries.get(id).ok_or_else(native_fork_unavailable)?;
            // Pi reassigns label IDs when materializing a branch. Workbench round sessions
            // do not create labels, so we cannot verify an exact prefix containing them.
            if entry.kind == "label" {
                return Err(native_fork_unavailable());
            }
            has_conversation |= matches!(entry.role.as_deref(), Some("user" | "assistant"));
            branch.push(id);
            cursor = entry.parent.as_deref();
        }
        if !has_conversation {
            return Err(native_fork_unavailable());
        }
        branch.reverse();
        Ok(branch)
    }
}

fn id(value: &Value) -> Result<&str> {
    value
        .as_str()
        .filter(|id| !id.is_empty() && id.len() <= 128 && !id.chars().any(char::is_control))
        .ok_or_else(native_fork_unavailable)
}

pub(super) fn verify_model(state: &Value, anchor: &NativeForkAnchor) -> Result<()> {
    if native_fork_model_identity(state)? != anchor.model_identity {
        return Err(native_fork_unavailable());
    }
    Ok(())
}

fn parent(value: Option<&Value>) -> Result<Option<&str>> {
    match value {
        Some(Value::Null) => Ok(None),
        Some(value) => Ok(Some(id(value)?)),
        None => Err(native_fork_unavailable()),
    }
}

fn identity(metadata: &Metadata) -> FileIdentity {
    FileIdentity {
        volume: u64::from(metadata.dev() as u32),
        index: u128::from(metadata.ino()).to_le_bytes(),
    }
}

fn open_session(root: &Root, session: &PiSession) -> Result<(File, Metadata)> {
    let file = root.open_streamed(&session_name(&session.file)?)?;
    let metadata = file.metadata()?;
    if !metadata.is_file() || metadata.nlink() != 1 || metadata.uid() != unsafe { libc::geteuid() }
    {
        return Err(native_fork_unavailable());
    }
    Ok((file, metadata))
}

struct SessionCopy<'a> {
    writer: &'a mut File,
    cwd: &'a Path,
    parent: Option<&'a Path>,
}

fn read_transcript(
    file: File,
    bytes: u64,
    cwd: &Path,
    session: &PiSession,
    mut copy: Option<SessionCopy<'_>>,
    cancel: &Cancellation,
) -> Result<Transcript> {
    if bytes == 0 || bytes > MAX_SESSION_BYTES {
        return Err(native_fork_unavailable());
    }
    let mut stream = BufReader::new(file.take(bytes));
    let mut line = Vec::new();
    stream.read_until(b'\n', &mut line)?;
    if line.len() > 16 * 1024 || line.last() != Some(&b'\n') {
        return Err(native_fork_unavailable());
    }
    let mut header: Value = serde_json::from_slice(&line).map_err(|_| native_fork_unavailable())?;
    if header["type"] != "session"
        || header["version"].as_u64() != Some(SESSION_VERSION)
        || header["id"].as_str() != Some(&session.id)
        || header["cwd"].as_str().map(Path::new) != Some(cwd)
    {
        return Err(native_fork_unavailable());
    }
    let parent_session = header["parentSession"].as_str().map(PathBuf::from);
    let mut hash = Hasher::new();
    hash.update(&line);
    if let Some(copy) = copy.as_mut() {
        header["cwd"] = json!(copy.cwd);
        let fields = header.as_object_mut().ok_or_else(native_fork_unavailable)?;
        if let Some(parent) = copy.parent {
            fields.insert("parentSession".into(), json!(parent));
        } else {
            fields.remove("parentSession");
        }
        serde_json::to_writer(&mut copy.writer, &header).map_err(|_| native_fork_unavailable())?;
        copy.writer.write_all(b"\n")?;
    }
    let mut ids = Vec::new();
    let mut entries = BTreeMap::new();
    loop {
        cancel.check()?;
        line.clear();
        if stream.read_until(b'\n', &mut line)? == 0 {
            break;
        }
        if line.last() != Some(&b'\n') {
            return Err(native_fork_unavailable());
        }
        hash.update(&line);
        let value: Value = serde_json::from_slice(&line).map_err(|_| native_fork_unavailable())?;
        let entry_id = id(&value["id"])?.to_owned();
        let entry_parent = parent(value.get("parentId"))?.map(str::to_owned);
        let kind = value["type"].as_str().ok_or_else(native_fork_unavailable)?;
        let role = if kind == "message" {
            Some(
                value["message"]["role"]
                    .as_str()
                    .ok_or_else(native_fork_unavailable)?
                    .to_owned(),
            )
        } else {
            None
        };
        if entry_parent
            .as_ref()
            .is_some_and(|parent| !entries.contains_key(parent))
        {
            return Err(native_fork_unavailable());
        }
        let entry = Entry {
            parent: entry_parent,
            kind: kind.to_owned(),
            role,
            digest: Entry::digest(&value)?,
        };
        if entries.insert(entry_id.clone(), entry).is_some() {
            return Err(native_fork_unavailable());
        }
        ids.push(entry_id);
        if let Some(copy) = copy.as_mut() {
            copy.writer.write_all(&line)?;
        }
    }
    if stream.into_inner().limit() != 0 {
        return Err(native_fork_unavailable());
    }
    let digest = Sha256::try_from(format!("{:x}", hash.finalize()))?;
    Ok(Transcript {
        digest,
        parent_session,
        ids,
        entries,
    })
}

fn source(data_dir: &Path, anchor: &NativeForkAnchor) -> Result<Root> {
    Root::open(&session_directory(data_dir, &anchor.run_id, false)?)
}

fn anchored_transcript(
    root: &Root,
    cwd: &Path,
    anchor: &NativeForkAnchor,
    copy: Option<SessionCopy<'_>>,
    cancel: &Cancellation,
) -> Result<Transcript> {
    let (file, metadata) = open_session(root, &anchor.session)?;
    if identity(&metadata) != anchor.source_identity || metadata.len() < anchor.source_len {
        return Err(native_fork_unavailable());
    }
    let transcript = read_transcript(file, anchor.source_len, cwd, &anchor.session, copy, cancel)?;
    let (_, metadata) = open_session(root, &anchor.session)?;
    let entry = transcript
        .entries
        .get(&anchor.entry_id)
        .ok_or_else(native_fork_unavailable)?;
    if identity(&metadata) != anchor.source_identity
        || metadata.len() < anchor.source_len
        || transcript.digest != anchor.source_sha256
        || transcript.ids.last() != Some(&anchor.leaf_id)
        || entry.parent != anchor.parent_id
        || entry.role.as_deref() != Some("user")
    {
        return Err(native_fork_unavailable());
    }
    transcript.branch_before(&anchor.entry_id)?;
    Ok(transcript)
}

fn fork_error(error: YssError) -> YssError {
    if error.code == ErrorCode::Cancelled {
        error
    } else {
        native_fork_unavailable()
    }
}

pub(in super::super) fn verify_source(
    data_dir: &Path,
    cwd: &Path,
    anchor: &NativeForkAnchor,
    cancel: &Cancellation,
) -> Result<()> {
    source(data_dir, anchor)
        .and_then(|root| anchored_transcript(&root, cwd, anchor, None, cancel))
        .map(|_| ())
        .map_err(fork_error)
}

struct Checkpoint {
    leaf_id: String,
    identity: FileIdentity,
    size: u64,
    digest: Sha256,
}

impl Checkpoint {
    fn verify(&self, file: &mut File, metadata: &Metadata, cancel: &Cancellation) -> Result<()> {
        if self.identity != identity(metadata) || self.size >= metadata.len() {
            return Err(native_fork_unavailable());
        }
        let mut prefix = Read::by_ref(file).take(self.size);
        let mut buffer = [0; 16 * 1024];
        let mut digest = Hasher::new();
        while prefix.limit() != 0 {
            cancel.check()?;
            let read = prefix.read(&mut buffer)?;
            if read == 0 {
                return Err(native_fork_unavailable());
            }
            digest.update(&buffer[..read]);
        }
        if format!("{:x}", digest.finalize()) != self.digest.as_str() {
            return Err(native_fork_unavailable());
        }
        file.rewind()?;
        Ok(())
    }
}

pub(super) struct Capture {
    session_id: String,
    checkpoint: Option<Checkpoint>,
    config_identity: NativeForkConfig,
    model_identity: Sha256,
    prompt_digest: Sha256,
}

fn is_settled(state: &Value) -> bool {
    state["isStreaming"] == false
        && state["isCompacting"] == false
        && state["pendingMessageCount"] == 0
}

fn unchanged(before: &Metadata, after: &Metadata) -> Result<bool> {
    Ok(identity(before) == identity(after)
        && before.len() == after.len()
        && before.modified()? == after.modified()?)
}

pub(super) fn before_prompt(
    request: &RunRequest<'_>,
    directory: &Path,
    resumed: Option<&PiSession>,
    state: &Value,
    prompt: &str,
    cancel: &Cancellation,
) -> Result<Capture> {
    if !native_fork_version_supported(request.runtime.version.as_ref()) || !is_settled(state) {
        return Err(native_fork_unavailable());
    }
    let session_id = id(&state["sessionId"])?.to_owned();
    let checkpoint = if let Some(session) = resumed {
        // Read the active session's persisted leaf after Pi has applied session settings.
        // The incremental RPC after prompting must confirm it still belongs to this session.
        if session_ref(directory, state)?.as_ref() != Some(session) {
            return Err(native_fork_unavailable());
        }
        let root = Root::open(directory)?;
        let (file, before) = open_session(&root, session)?;
        let transcript = read_transcript(file, before.len(), request.cwd, session, None, cancel)?;
        let (_, after) = open_session(&root, session)?;
        if !unchanged(&before, &after)? {
            return Err(native_fork_unavailable());
        }
        Some(Checkpoint {
            leaf_id: transcript
                .ids
                .last()
                .ok_or_else(native_fork_unavailable)?
                .clone(),
            identity: identity(&before),
            size: before.len(),
            digest: transcript.digest,
        })
    } else {
        if state["messageCount"] != 0 {
            return Err(native_fork_unavailable());
        }
        None
    };
    Ok(Capture {
        session_id,
        checkpoint,
        config_identity: NativeForkConfig::read(&request.runtime.config_dir)?,
        model_identity: native_fork_model_identity(state)?,
        prompt_digest: Sha256::of(prompt.as_bytes()),
    })
}

pub(super) fn capture(
    request: &RunRequest<'_>,
    context: &JobContext,
    directory: &Path,
    pi: &mut Session,
    state: &Value,
    capture: Capture,
) -> Result<()> {
    let session = session_ref(directory, state)?.ok_or_else(native_fork_unavailable)?;
    if !is_settled(state)
        || session.id != capture.session_id
        || native_fork_model_identity(state)? != capture.model_identity
    {
        return Err(native_fork_unavailable());
    }
    let model = readiness::state_model(state)?;
    let since = capture
        .checkpoint
        .as_ref()
        .map(|prior| prior.leaf_id.as_str());
    let root = Root::open(directory)?;
    let (mut file, metadata) = open_session(&root, &session)?;
    let bytes = metadata.len();
    if bytes == 0 || bytes > MAX_SESSION_BYTES {
        return Err(native_fork_unavailable());
    }
    if let Some(checkpoint) = &capture.checkpoint {
        checkpoint.verify(&mut file, &metadata, &context.cancel)?;
    }
    let changed = pi.entries(since, &context.cancel)?;
    let leaf = id(&changed["leafId"])?;
    let entries = changed["entries"]
        .as_array()
        .ok_or_else(native_fork_unavailable)?;
    let mut users = entries
        .iter()
        .filter(|entry| entry["type"] == "message" && entry["message"]["role"] == "user");
    let selected = users.next().ok_or_else(native_fork_unavailable)?;
    if users.next().is_some() {
        return Err(native_fork_unavailable());
    }
    let content = selected["message"]["content"]
        .as_array()
        .ok_or_else(native_fork_unavailable)?;
    if !matches!(content.as_slice(), [block] if block["type"] == "text"
        && block["text"].as_str().is_some_and(|text| Sha256::of(text.as_bytes()) == capture.prompt_digest))
    {
        return Err(native_fork_unavailable());
    }
    let entry_id = id(&selected["id"])?;
    let parent_id = parent(selected.get("parentId"))?;
    let entry_digest = Entry::digest(selected)?;
    let transcript = read_transcript(file, bytes, request.cwd, &session, None, &context.cancel)?;
    let (_, after) = open_session(&root, &session)?;
    if !unchanged(&metadata, &after)? {
        return Err(native_fork_unavailable());
    }
    let tail = if let Some(since) = since {
        let at = transcript
            .ids
            .iter()
            .position(|id| id == since)
            .ok_or_else(native_fork_unavailable)?;
        &transcript.ids[at + 1..]
    } else {
        &transcript.ids
    };
    if transcript.ids.last().map(String::as_str) != Some(leaf)
        || tail.len() != entries.len()
        || !tail
            .iter()
            .zip(entries)
            .all(|(stored, rpc)| rpc["id"].as_str() == Some(stored))
        || transcript.entries.get(entry_id).is_none_or(|entry| {
            entry.parent.as_deref() != parent_id || entry.digest != entry_digest
        })
    {
        return Err(native_fork_unavailable());
    }
    let mut current = Some(leaf);
    let mut selected_is_ancestor = false;
    while let Some(id) = current {
        if Some(id) == since {
            break;
        }
        selected_is_ancestor |= id == entry_id;
        current = transcript.entries[id].parent.as_deref();
    }
    if current != since || !selected_is_ancestor {
        return Err(native_fork_unavailable());
    }
    if NativeForkConfig::read(&request.runtime.config_dir)? != capture.config_identity {
        return Err(native_fork_unavailable());
    }
    context.cancel.check()?;
    let anchor = NativeForkAnchor {
        run_id: request.run_id.into(),
        job_id: context.id.clone(),
        session,
        entry_id: entry_id.into(),
        parent_id: parent_id.map(str::to_owned),
        leaf_id: leaf.into(),
        model,
        config_dir: request.runtime.config_dir.clone(),
        config_identity: capture.config_identity,
        model_identity: capture.model_identity,
        source_identity: identity(&metadata),
        source_len: bytes,
        source_sha256: transcript.digest,
    };
    lock_store(&context.store)?.put_record(NATIVE_FORK_ANCHOR_RECORD, &context.id, &anchor)
}

pub(super) fn prepare(
    request: &RunRequest<'_>,
    directory: &Path,
    anchor: &NativeForkAnchor,
    cancel: &Cancellation,
) -> Result<PiSession> {
    prepare_inner(request, directory, anchor, cancel).map_err(fork_error)
}

pub(super) fn verify_checkout(request: &RunRequest<'_>, cancel: &Cancellation) -> Result<()> {
    let changed = || {
        YssError::new(
            ErrorCode::ConcurrentChange,
            "原生分叉启动前工程的 Git 检出已变化，请重新核对后发送",
        )
        .context("nativeForkIssue", "workspaceChanged")
        .context("projectScopeChanged", true)
    };
    for workspace in request.workspaces {
        let direct = workspace.direct.as_ref().ok_or_else(changed)?;
        let (_, actual) =
            agent_workspace::native_fork_checkout(workspace, cancel).map_err(|error| {
                if error.code == ErrorCode::Cancelled {
                    error
                } else {
                    changed()
                }
            })?;
        if direct.baseline != actual {
            return Err(changed());
        }
    }
    Ok(())
}

pub(super) fn read_only_command(
    launcher: Command,
    request: &RunRequest<'_>,
    directory: &Path,
    private: &Path,
) -> Result<Command> {
    // Pi performs project-local migrations before loading tools, so broker restrictions
    // alone do not protect the repository during startup.
    let mut profile = String::from(
        "(version 1) (allow default)\n(deny file-write*)\n\
         (allow file-write* (subpath (param \"PRIVATE\")) (subpath (param \"CONFIG\")))\n\
         (deny file-write*",
    );
    let mut command = Command::new(NATIVE_FORK_SANDBOX);
    let private = Root::open(private)?.path;
    define_path(&mut command, "PRIVATE", &private);
    define_path(
        &mut command,
        "CONFIG",
        &Root::open(&request.runtime.config_dir)?.path,
    );
    let mut scope = BTreeSet::from([
        Root::open(request.cwd)?.path,
        Root::open(request.data_dir)?.path,
    ]);
    for workspace in request.workspaces {
        scope.insert(Root::open(Path::new(&workspace.original_root))?.path);
    }
    for (index, path) in scope.into_iter().enumerate() {
        let parameter = format!("PROTECTED_{index}");
        profile.push_str(&format!(" (subpath (param \"{parameter}\"))"));
        define_path(&mut command, &parameter, &path);
    }
    profile.push_str(")\n(allow file-write* (subpath (param \"SESSION\")))");
    define_path(&mut command, "SESSION", directory);
    command
        .arg("-p")
        .arg(profile)
        .arg(launcher.get_program())
        .args(launcher.get_args());
    if let Some(cwd) = launcher.get_current_dir() {
        command.current_dir(cwd);
    }
    for (key, value) in launcher.get_envs() {
        if let Some(value) = value {
            command.env(key, value);
        } else {
            command.env_remove(key);
        }
    }
    command.env("TMPDIR", private);
    Ok(command)
}

fn define_path(command: &mut Command, parameter: &str, path: &Path) {
    let mut value = OsString::from(format!("{parameter}="));
    value.push(path.as_os_str());
    command.arg("-D").arg(value);
}

fn prepare_inner(
    request: &RunRequest<'_>,
    directory: &Path,
    anchor: &NativeForkAnchor,
    cancel: &Cancellation,
) -> Result<PiSession> {
    request.runtime.verify_native_fork(anchor)?;
    if request.resume.is_some()
        || !request.read_only
        || request.allow_commands
        || request.run_id == anchor.run_id
        || request.expected_model != Some(&anchor.model)
    {
        return Err(native_fork_unavailable());
    }
    let private = PrivateDir::new()?;
    let scratch_path = private.path();
    let scratch = Root::open(scratch_path)?;
    let cwd = scratch.path.join("cwd");
    let output = scratch.path.join("output");
    super::sandbox::private_dir(&cwd)?;
    super::sandbox::private_dir(&output)?;
    let agent_dir = scratch.path.join("agent");
    super::sandbox::private_dir(&agent_dir)?;
    let original = source(request.data_dir, anchor)?;
    let original_path = original.path.join(&anchor.session.file);
    let copy_name = RelPath::new("source.jsonl")?;
    let staged_source = scratch.path.join(copy_name.as_str());
    let mut transcript = None;
    scratch.create_streamed(&copy_name, |writer| {
        transcript = Some(anchored_transcript(
            &original,
            request.cwd,
            anchor,
            Some(SessionCopy {
                writer,
                cwd: &cwd,
                parent: None,
            }),
            cancel,
        )?);
        Ok(())
    })?;
    let transcript = transcript.ok_or_else(native_fork_unavailable)?;
    let expected = transcript.branch_before(&anchor.entry_id)?;
    let protected = [request.data_dir.to_path_buf(), request.cwd.to_path_buf()];
    let launcher = readiness::rpc_command(
        &request.runtime.executable,
        request.runtime.entry.as_deref(),
        &agent_dir,
        &protected,
    )?;
    let mut command = Command::new(NATIVE_FORK_SANDBOX);
    command
        .env_clear()
        .envs(
            launcher
                .get_envs()
                .filter_map(|(key, value)| value.map(|value| (key, value))),
        )
        .env("HOME", scratch_path)
        .env("TMPDIR", scratch_path)
        .env("PI_OFFLINE", "1")
        .current_dir(&cwd)
        .args(["-p", ISOLATION])
        .arg("-D")
        .arg(format!("SCRATCH={}", scratch_path.display()))
        .arg("-D")
        .arg(format!("DATA={}", request.data_dir.display()))
        .arg("-D")
        .arg(format!("CONFIG={}", request.runtime.config_dir.display()))
        .arg("-D")
        .arg(format!("PROJECT={}", request.cwd.display()))
        .arg(launcher.get_program())
        .args(launcher.get_args())
        .arg("--no-tools")
        .arg("--session")
        .arg(&staged_source)
        .arg("--session-dir")
        .arg(&output);
    let mut pi = Session::open(command, cancel)?;
    let state = pi.state(cancel)?;
    if !is_settled(&state)
        || state["sessionId"].as_str() != Some(&anchor.session.id)
        || state["sessionFile"].as_str().map(Path::new) != Some(staged_source.as_path())
    {
        return Err(native_fork_unavailable());
    }
    pi.fork_before(&anchor.entry_id, cancel)?;
    let state = pi.state(cancel)?;
    let session = session_ref(&output, &state)?.ok_or_else(native_fork_unavailable)?;
    if !is_settled(&state) || session.id == anchor.session.id {
        return Err(native_fork_unavailable());
    }
    drop(pi);
    let output = Root::open(&output)?;
    let (file, metadata) = open_session(&output, &session)?;
    let branch = read_transcript(file, metadata.len(), &cwd, &session, None, cancel)?;
    let extra = branch
        .ids
        .get(expected.len()..)
        .ok_or_else(native_fork_unavailable)?;
    if branch.parent_session.as_deref() != Some(staged_source.as_path())
        || !expected.iter().zip(&branch.ids).all(|(want, actual)| {
            *want == actual && transcript.entries[*want] == branch.entries[actual]
        })
    {
        return Err(native_fork_unavailable());
    }
    let mut leaf = expected.last().copied();
    for id in extra {
        let entry = &branch.entries[id];
        if transcript.entries.contains_key(id)
            || entry.parent.as_deref() != leaf
            || !matches!(
                entry.kind.as_str(),
                "model_change" | "thinking_level_change"
            )
        {
            return Err(native_fork_unavailable());
        }
        leaf = Some(id);
    }
    let target = Root::open(directory)?;
    let temporary = RelPath::new(format!(".{}.tmp", unique_id()?))?;
    let name = session_name(&session.file)?;
    let published = (|| {
        let staged_identity = target.create_streamed(&temporary, |writer| {
            let (file, reopened) = open_session(&output, &session)?;
            if identity(&reopened) != identity(&metadata) || reopened.len() != metadata.len() {
                return Err(native_fork_unavailable());
            }
            let written = read_transcript(
                file,
                reopened.len(),
                &cwd,
                &session,
                Some(SessionCopy {
                    writer,
                    cwd: request.cwd,
                    parent: Some(&original_path),
                }),
                cancel,
            )?;
            if written.digest != branch.digest {
                return Err(native_fork_unavailable());
            }
            Ok(())
        })?;
        cancel.check()?;
        target.publish_streamed(&temporary, &name, &staged_identity)
    })();
    if published.is_err()
        && let Ok(snapshot @ FileSnapshot::File { .. }) = target.inspect(&temporary)
    {
        let _ = target.remove(&temporary, &snapshot);
    }
    published?;
    Ok(session)
}
