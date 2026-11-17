use super::{
    content,
    model::*,
    preview, sources,
    sqlite::{CommitEffects, RecordWrite},
    store::Store,
};
use crate::{
    codec,
    error::{ErrorCode, Result, YssError},
    fs::Root,
    model::{RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex},
};

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ArtifactFile {
    pub path: String,
    pub sha256: String,
    pub size: usize,
    pub kind: String,
    pub mime: String,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmationSnapshot {
    pub confirmation_id: String,
    pub spec_id: String,
    pub artifact_digest: String,
    pub captured_at: String,
    pub files: Vec<ArtifactFile>,
}

fn mime(path: &str) -> &'static str {
    match path
        .rsplit('.')
        .next()
        .unwrap_or("")
        .to_ascii_lowercase()
        .as_str()
    {
        "md" => "text/markdown",
        "html" | "htm" => "text/html",
        "js" | "mjs" => "text/javascript",
        "css" => "text/css",
        "json" => "application/json",
        "svg" => "image/svg+xml",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "webp" => "image/webp",
        "gif" => "image/gif",
        "avif" => "image/avif",
        "bmp" => "image/bmp",
        "ico" => "image/x-icon",
        _ => "application/octet-stream",
    }
}

pub(crate) type CapturedFiles = Vec<(ArtifactFile, Vec<u8>)>;

pub(crate) fn capture(state: &WorkbenchState, spec_id: &str) -> Result<(String, CapturedFiles)> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let main = root.source_read(&RelPath::new(&spec.source_path)?, MAX_SOURCE)?;
    let text =
        std::str::from_utf8(&main.bytes).map_err(|_| sources::invalid("主规格不是 UTF-8"))?;
    let metadata = content::metadata(text)?;
    let view = content::read_view(state, spec_id)?;
    let mut artifacts = vec![(
        spec.source_path.clone(),
        Sha256::of(&main.bytes).as_str().to_owned(),
    )];
    let mut files = BTreeMap::from([(spec.source_path.clone(), ("spec".to_owned(), main.bytes))]);
    for resource in content::merged_resources(spec, metadata.as_ref()) {
        if resource.target_type == TargetType::Web {
            return Err(sources::invalid("外部产物需要本地快照后保存版本"));
        }
        let kind = serde_json::to_value(&resource.kind)
            .ok()
            .and_then(|value| value.as_str().map(str::to_owned))
            .unwrap_or_else(|| "other".into());
        let digest = if resource.kind == ResourceKind::Prototype {
            let tree = preview::artifact_files(&root, &resource.target)?;
            let digest = preview::files_digest(&tree);
            for (path, bytes) in tree {
                files.insert(path, (kind.clone(), bytes));
            }
            digest
        } else {
            let bytes = root
                .source_read(&RelPath::new(&resource.target)?, MAX_STATE)?
                .bytes;
            let digest = Sha256::of(&bytes).as_str().to_owned();
            files.insert(resource.target.clone(), (kind, bytes));
            digest
        };
        artifacts.push((resource.target, digest));
    }
    artifacts.sort();
    let digest = Sha256::of(&codec::encode_json(&(view.scope, artifacts))?)
        .as_str()
        .to_owned();
    root.revalidate()?;
    let latest = content::read_view(state, spec_id)?;
    if latest.artifact_digest.as_deref() != Some(&digest) {
        return Err(sources::invalid("采集期间来源已变化，请重新读取后确认"));
    }
    let files = files
        .into_iter()
        .map(|(path, (kind, bytes))| {
            (
                ArtifactFile {
                    sha256: Sha256::of(&bytes).as_str().into(),
                    size: bytes.len(),
                    mime: mime(&path).into(),
                    path,
                    kind,
                },
                bytes,
            )
        })
        .collect();
    Ok((digest, files))
}

pub(crate) fn confirmation_effects(state: &WorkbenchState, spec_id: &str) -> Result<CommitEffects> {
    let (digest, files) = capture(state, spec_id)?;
    let record = state
        .confirmations
        .iter()
        .rev()
        .find(|record| record.spec_id == spec_id && record.artifact_digest == digest)
        .ok_or_else(|| sources::invalid("当前内容与确认版本不匹配"))?;
    let snapshot = ConfirmationSnapshot {
        confirmation_id: record.id.clone(),
        spec_id: spec_id.into(),
        artifact_digest: digest,
        captured_at: sources::now(),
        files: files.iter().map(|(file, _)| file.clone()).collect(),
    };
    Ok(CommitEffects {
        records: vec![RecordWrite {
            kind: "confirmationSnapshot".into(),
            id: record.id.clone(),
            payload: Some(
                serde_json::to_string(&snapshot).map_err(|_| sources::invalid("快照编码失败"))?,
            ),
        }],
        blobs: files
            .into_iter()
            .map(|(file, bytes)| (file.sha256, bytes))
            .collect(),
    })
}

pub fn confirmations(store: &mut Store, spec_id: &str) -> Result<Value> {
    sources::spec(store.state(), spec_id)?;
    let records = store
        .state()
        .confirmations
        .iter()
        .filter(|record| record.spec_id == spec_id)
        .cloned()
        .collect::<Vec<_>>();
    let mut result = Vec::new();
    for record in records {
        let snapshot = store.record::<ConfirmationSnapshot>("confirmationSnapshot", &record.id)?;
        let mut value =
            serde_json::to_value(record).map_err(|_| sources::invalid("确认记录编码失败"))?;
        value["baselineAvailable"] = json!(snapshot.is_some());
        value["capturedAt"] = json!(snapshot.map(|snapshot| snapshot.captured_at));
        result.push(value);
    }
    Ok(json!(result))
}

pub fn manifest(store: &mut Store, id: &str) -> Result<Value> {
    let snapshot = store.record::<ConfirmationSnapshot>("confirmationSnapshot", id)?;
    Ok(json!({"baselineAvailable":snapshot.is_some(),"snapshot":snapshot}))
}

pub fn backfill_snapshot(shared: &Arc<Mutex<Store>>, id: &str) -> Result<Value> {
    let state = {
        let mut store = super::jobs::lock_store(shared)?;
        if store
            .record::<ConfirmationSnapshot>("confirmationSnapshot", id)?
            .is_some()
        {
            return manifest(&mut store, id);
        }
        store.state().clone()
    };
    let record = state
        .confirmations
        .iter()
        .find(|record| record.id == id)
        .ok_or_else(|| sources::invalid("确认记录不存在"))?;
    let (digest, files) = capture(&state, &record.spec_id)?;
    if digest != record.artifact_digest {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "当前完整产物摘要与旧确认不同，无法补存该历史版本",
        )
        .context("httpStatus", 409));
    }
    let snapshot = ConfirmationSnapshot {
        confirmation_id: id.into(),
        spec_id: record.spec_id.clone(),
        artifact_digest: digest,
        captured_at: sources::now(),
        files: files.iter().map(|(file, _)| file.clone()).collect(),
    };
    let mut store = super::jobs::lock_store(shared)?;
    if store
        .record::<ConfirmationSnapshot>("confirmationSnapshot", id)?
        .is_some()
    {
        return manifest(&mut store, id);
    }
    let spec = sources::spec(&state, &record.spec_id)?;
    let latest = sources::spec(store.state(), &record.spec_id)?;
    let project = sources::project(&state, &spec.project_id)?;
    let latest_project = sources::project(store.state(), &latest.project_id)?;
    if !store.state().confirmations.contains(record)
        || latest.project_id != spec.project_id
        || latest.source_path != spec.source_path
        || latest.resources != spec.resources
        || latest.inferred_resources != spec.inferred_resources
        || latest_project.root_path != project.root_path
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "采集期间规格关联发生变化，请重新读取后补存",
        )
        .context("httpStatus", 409));
    }
    store.write_extensions(&CommitEffects {
        records: vec![RecordWrite {
            kind: "confirmationSnapshot".into(),
            id: id.into(),
            payload: Some(
                serde_json::to_string(&snapshot).map_err(|_| sources::invalid("快照编码失败"))?,
            ),
        }],
        blobs: files
            .into_iter()
            .map(|(file, bytes)| (file.sha256, bytes))
            .collect(),
    })?;
    manifest(&mut store, id)
}

pub fn diff(store: &mut Store, spec_id: &str, base_id: &str, path: Option<&str>) -> Result<Value> {
    if !store
        .state()
        .confirmations
        .iter()
        .any(|record| record.id == base_id && record.spec_id == spec_id)
    {
        return Err(sources::invalid("确认版本与当前 Spec 不匹配"));
    }
    let Some(base) = store.record::<ConfirmationSnapshot>("confirmationSnapshot", base_id)? else {
        return Ok(
            json!({"baselineAvailable":false,"baseConfirmationId":base_id,"files":[],"message":"历史正文未留存，请从新增确认版本开始比较"}),
        );
    };
    let spec = sources::spec(store.state(), spec_id)?;
    let project = sources::project(store.state(), &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let mut current_files = BTreeMap::new();
    let mut source_errors = Vec::new();
    let metadata = match root.source_read(&RelPath::new(&spec.source_path)?, MAX_SOURCE) {
        Ok(file) => {
            let text = std::str::from_utf8(&file.bytes)
                .map_err(|_| sources::invalid("主规格不是UTF-8"))?;
            let metadata = content::metadata(text)?;
            current_files.insert(spec.source_path.clone(), ("spec".to_owned(), file.bytes));
            metadata
        }
        Err(error) => {
            source_errors.push(error.message);
            None
        }
    };
    for resource in content::merged_resources(spec, metadata.as_ref()) {
        if resource.target_type != TargetType::Local {
            continue;
        }
        let kind = serde_json::to_value(&resource.kind)
            .ok()
            .and_then(|value| value.as_str().map(str::to_owned))
            .unwrap_or_else(|| "other".into());
        if resource.kind == ResourceKind::Prototype {
            match preview::served_files(&root, &resource.target) {
                Ok(tree) => {
                    for (path, bytes) in tree {
                        current_files.insert(path, (kind.clone(), bytes));
                    }
                }
                Err(error) => source_errors.push(format!("{}：{}", resource.target, error.message)),
            }
        } else {
            match root.source_read(&RelPath::new(&resource.target)?, MAX_STATE) {
                Ok(file) => {
                    current_files.insert(resource.target, (kind, file.bytes));
                }
                Err(error) => source_errors.push(format!("{}：{}", resource.target, error.message)),
            }
        }
    }
    root.revalidate()?;
    let current_digest = content::read_view(store.state(), spec_id)?.artifact_digest;
    let current = current_files
        .into_iter()
        .map(|(path, (kind, bytes))| {
            (
                ArtifactFile {
                    sha256: Sha256::of(&bytes).as_str().into(),
                    size: bytes.len(),
                    mime: mime(&path).into(),
                    path,
                    kind,
                },
                bytes,
            )
        })
        .collect::<Vec<_>>();
    let before = base
        .files
        .iter()
        .map(|file| (&file.path, file))
        .collect::<BTreeMap<_, _>>();
    let after = current
        .iter()
        .map(|(file, bytes)| (&file.path, (file, bytes)))
        .collect::<BTreeMap<_, _>>();
    let mut paths = before
        .keys()
        .chain(after.keys())
        .map(|value| (*value).clone())
        .collect::<Vec<_>>();
    paths.sort();
    paths.dedup();
    let mut changes = Vec::new();
    for file_path in paths {
        let old = before.get(&file_path).copied();
        let new = after.get(&file_path).copied();
        if old.map(|file| &file.sha256) == new.map(|(file, _)| &file.sha256) {
            continue;
        }
        let status = match (old, new) {
            (None, _) => "added",
            (_, None) => "removed",
            _ => "modified",
        };
        let mut change =
            json!({"path":file_path,"status":status,"before":old,"after":new.map(|(file,_)|file)});
        if path == Some(file_path.as_str()) {
            let previous = old.map(|file| store.blob(&file.sha256)).transpose()?;
            let next = new.map(|(_, bytes)| bytes.as_slice());
            let before_text = previous.as_deref().map(std::str::from_utf8).transpose();
            let after_text = next.map(std::str::from_utf8).transpose();
            match (before_text, after_text) {
                (Ok(old), Ok(new)) => {
                    change["binary"] = json!(false);
                    change["beforeText"] = json!(old.unwrap_or(""));
                    change["afterText"] = json!(new.unwrap_or(""));
                    change["lines"] = line_diff(old.unwrap_or(""), new.unwrap_or(""));
                }
                _ => change["binary"] = json!(true),
            }
        }
        changes.push(change);
    }
    Ok(
        json!({"baselineAvailable":true,"baseConfirmationId":base_id,"baseDigest":base.artifact_digest,"currentDigest":current_digest,"files":changes,"sourceErrors":source_errors}),
    )
}

fn line_diff(before: &str, after: &str) -> Value {
    let old = before.lines().collect::<Vec<_>>();
    let new = after.lines().collect::<Vec<_>>();
    let prefix = old.iter().zip(&new).take_while(|(a, b)| a == b).count();
    let suffix = old[prefix..]
        .iter()
        .rev()
        .zip(new[prefix..].iter().rev())
        .take_while(|(a, b)| a == b)
        .count();
    let mut lines = Vec::new();
    for text in &old[..prefix] {
        lines.push(json!({"kind":"context","text":text}));
    }
    for text in &old[prefix..old.len() - suffix] {
        lines.push(json!({"kind":"remove","text":text}));
    }
    for text in &new[prefix..new.len() - suffix] {
        lines.push(json!({"kind":"add","text":text}));
    }
    for text in &new[new.len() - suffix..] {
        lines.push(json!({"kind":"context","text":text}));
    }
    json!(lines)
}

pub fn historical_root(store: &mut Store, confirmation_id: &str) -> Result<std::path::PathBuf> {
    let snapshot = store
        .record::<ConfirmationSnapshot>("confirmationSnapshot", confirmation_id)?
        .ok_or_else(|| sources::invalid("历史正文未留存"))?;
    let data = Root::open(store.data_dir())?;
    let cache = RelPath::new("confirmation-previews")?;
    if data.inspect(&cache)? == crate::fs::FileSnapshot::Missing {
        data.create_dir(&cache)?;
    }
    let cache = Root::open(&data.path.join(cache.as_str()))?;
    let name = RelPath::new(confirmation_id)?;
    if cache.inspect(&name)? == crate::fs::FileSnapshot::Missing {
        cache.create_dir(&name)?;
    }
    let target = Root::open(&cache.path.join(name.as_str()))?;
    for file in snapshot.files {
        let relative = RelPath::new(&file.path)?;
        let mut directory = String::new();
        let parts = file.path.split('/').collect::<Vec<_>>();
        for part in &parts[..parts.len() - 1] {
            if !directory.is_empty() {
                directory.push('/');
            }
            directory.push_str(part);
            let directory = RelPath::new(&directory)?;
            if target.inspect(&directory)? == crate::fs::FileSnapshot::Missing {
                target.create_dir(&directory)?;
            }
        }
        let bytes = store.blob(&file.sha256)?;
        if bytes.len() != file.size {
            return Err(sources::invalid("历史文件长度不一致"));
        }
        match target.inspect(&relative)? {
            crate::fs::FileSnapshot::Missing => {
                target.create_file(&relative, &bytes, 0o600)?;
            }
            crate::fs::FileSnapshot::File { descriptor, .. }
                if descriptor.sha256.as_str() == file.sha256 => {}
            _ => return Err(sources::invalid("历史预览缓存已被修改，请保留目录并核实")),
        }
    }
    target.revalidate()?;
    Ok(target.path)
}

pub fn historical_file(
    store: &mut Store,
    confirmation_id: &str,
    path: &str,
) -> Result<(ArtifactFile, Vec<u8>)> {
    let snapshot = store
        .record::<ConfirmationSnapshot>("confirmationSnapshot", confirmation_id)?
        .ok_or_else(|| sources::invalid("历史正文未留存"))?;
    let file = snapshot
        .files
        .into_iter()
        .find(|file| file.path == path)
        .ok_or_else(|| sources::invalid("历史文件不存在"))?;
    let bytes = store.blob(&file.sha256)?;
    Ok((file, bytes))
}

pub fn current_file(
    state: &WorkbenchState,
    spec_id: &str,
    path: &str,
) -> Result<(ArtifactFile, Vec<u8>)> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let main = root.source_read(&RelPath::new(&spec.source_path)?, MAX_SOURCE)?;
    let metadata = content::metadata(
        std::str::from_utf8(&main.bytes).map_err(|_| sources::invalid("主规格不是 UTF-8"))?,
    )?;
    let mut found = (path == spec.source_path).then_some(("spec".to_owned(), main.bytes));
    for resource in content::merged_resources(spec, metadata.as_ref()) {
        if found.is_some() || resource.target_type != TargetType::Local {
            continue;
        }
        let kind = serde_json::to_value(&resource.kind)
            .ok()
            .and_then(|value| value.as_str().map(str::to_owned))
            .unwrap_or_else(|| "other".into());
        if resource.kind == ResourceKind::Prototype {
            let directory = resource.target.rsplit_once('/').map(|(dir, _)| dir);
            if directory.is_none_or(|directory| path.starts_with(&format!("{directory}/")))
                && let Some((_, bytes)) = preview::served_files(&root, &resource.target)?
                    .into_iter()
                    .find(|(candidate, _)| candidate == path)
            {
                found = Some((kind, bytes));
            }
        } else if resource.target == path {
            found = Some((
                kind,
                root.source_read(&RelPath::new(path)?, MAX_STATE)?.bytes,
            ));
        }
    }
    let (kind, bytes) = found.ok_or_else(|| sources::invalid("当前文件不在此 Spec 产物清单中"))?;
    root.revalidate()?;
    Ok((
        ArtifactFile {
            path: path.into(),
            sha256: Sha256::of(&bytes).as_str().into(),
            size: bytes.len(),
            kind,
            mime: mime(path).into(),
        },
        bytes,
    ))
}
