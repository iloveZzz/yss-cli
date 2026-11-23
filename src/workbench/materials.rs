use super::{
    artifacts::{ArtifactFile, ConfirmationSnapshot, mime},
    content,
    model::*,
    preview, sources,
    sqlite::{CommitEffects, ExtensionRecords, RecordWrite},
    store::Store,
};
use crate::{
    codec,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::{RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

pub(super) const SNAPSHOTS: &str = "confirmationSnapshot";
pub(super) const REVIEWS: &str = "specMaterialReview";

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct Material {
    pub(super) target: String,
    pub(super) digest: String,
    paths: Vec<String>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    shared: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    binding: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum MaterialChangeKind {
    Changed,
    SharedUpdated,
    DependencyReview,
    Unavailable,
    HistoryUnavailable,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MaterialChange {
    pub target: String,
    pub kind: MaterialChangeKind,
    pub reason: String,
    pub confirmed_digest: Option<String>,
    pub observed_digest: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum Use {
    Reference,
    Follow,
    Dependency,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Binding {
    target: String,
    usage: Use,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    scope_ref: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    section: Option<Section>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Section {
    start: String,
    end: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReviewInput {
    pub spec_id: String,
    observed_digest: String,
    #[serde(default)]
    bindings: Option<Vec<Binding>>,
    #[serde(default)]
    adopt_latest: Vec<String>,
    #[serde(default)]
    keep_confirmed: Vec<String>,
    reason: String,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Review {
    input: ReviewInput,
    #[serde(default)]
    observed_sources: Option<String>,
    adopted: BTreeMap<String, ReviewedVersion>,
    #[serde(default)]
    retained: BTreeMap<String, ReviewedVersion>,
    reviewed_at: String,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ReviewedVersion {
    digest: String,
    confirmation_id: Option<String>,
    #[serde(default)]
    binding: Option<String>,
    reason: String,
    reviewed_at: String,
}

#[derive(Default)]
pub(super) struct Records {
    snapshots: BTreeMap<String, ConfirmationSnapshot>,
    reviews: BTreeMap<String, Review>,
}

impl Records {
    pub(super) fn from_raw(raw: &ExtensionRecords) -> Result<Self> {
        fn decode<T: serde::de::DeserializeOwned>(
            raw: &ExtensionRecords,
            kind: &str,
        ) -> Result<BTreeMap<String, T>> {
            raw.get(kind)
                .into_iter()
                .flatten()
                .map(|(id, payload)| {
                    serde_json::from_str(payload)
                        .map(|record| (id.clone(), record))
                        .map_err(|_| sources::invalid("确认材料记录格式无效"))
                })
                .collect()
        }
        Ok(Self {
            snapshots: decode(raw, SNAPSHOTS)?,
            reviews: decode(raw, REVIEWS)?,
        })
    }

    pub(super) fn load(store: &mut Store) -> Result<Self> {
        Ok(Self {
            snapshots: store.record_entries(SNAPSHOTS)?.into_iter().collect(),
            reviews: store.record_entries(REVIEWS)?.into_iter().collect(),
        })
    }
}

type MaterialKey = (bool, String);

#[derive(Default)]
pub(super) struct Shared {
    current: BTreeSet<MaterialKey>,
    confirmed: BTreeMap<MaterialKey, BTreeMap<String, OffsetDateTime>>,
}

impl Shared {
    fn before(&self, record: &ConfirmationRecord, key: &MaterialKey) -> bool {
        let Ok(at) = OffsetDateTime::parse(&record.created_at, &Rfc3339) else {
            return false;
        };
        self.confirmed.get(key).is_some_and(|owners| {
            owners
                .iter()
                .any(|(spec, first)| spec != &record.spec_id && *first <= at)
        })
    }
}

fn material_key(prototype: bool, target: &str) -> MaterialKey {
    let path = if prototype {
        target
            .rsplit_once('/')
            .map_or("", |(directory, _)| directory)
    } else {
        target
    };
    (prototype, fs::alias_key(path))
}

fn key(resource: &ResourceRef) -> MaterialKey {
    material_key(resource.kind == ResourceKind::Prototype, &resource.target)
}

pub(super) fn shared(
    state: &WorkbenchState,
    records: &Records,
    project_id: &str,
    root: &Root,
) -> Shared {
    let mut seen = BTreeSet::new();
    let mut shared = Shared::default();
    let specs: BTreeMap<_, _> = state
        .specs
        .iter()
        .filter(|spec| spec.project_id == project_id)
        .map(|spec| (&spec.id, spec))
        .collect();
    for spec in specs.values() {
        let metadata = sources::text(root, &spec.source_path)
            .ok()
            .and_then(|text| content::metadata(&text).ok().flatten());
        let mut own = BTreeSet::from([(false, fs::alias_key(&spec.source_path))]);
        let bindings = records
            .reviews
            .get(&spec.id)
            .and_then(|review| review.input.bindings.as_deref())
            .unwrap_or_default();
        own.extend(
            content::resolved_resources(spec, metadata.as_ref(), Some(root))
                .iter()
                .filter(|resource| {
                    resource.target_type == TargetType::Local
                        && !bindings.iter().any(|binding| {
                            binding.target == resource.target && binding.usage == Use::Reference
                        })
                })
                .map(key),
        );
        shared
            .current
            .extend(own.into_iter().filter(|key| !seen.insert(key.clone())));
    }
    for record in state
        .confirmations
        .iter()
        .filter(|record| specs.contains_key(&record.spec_id))
    {
        let Some(snapshot) = records.snapshots.get(&record.id).filter(|snapshot| {
            snapshot.spec_id == record.spec_id && snapshot.artifact_digest == record.artifact_digest
        }) else {
            continue;
        };
        let Ok(at) = OffsetDateTime::parse(&record.created_at, &Rfc3339) else {
            continue;
        };
        let recorded = snapshot.files.iter().filter_map(|file| {
            if file.kind == "prototype" && file.mime != "text/html" {
                return None;
            }
            Some(material_key(file.kind == "prototype", &file.path))
        });
        for key in recorded {
            let time = shared
                .confirmed
                .entry(key)
                .or_default()
                .entry(record.spec_id.clone())
                .or_insert(at);
            *time = (*time).min(at);
        }
    }
    shared
}

fn binding_digest(binding: Option<&Binding>) -> Result<Option<String>> {
    binding
        .map(|binding| Ok(Sha256::of(&codec::encode_json(binding)?).as_str().into()))
        .transpose()
}

fn digest(scope: &str, materials: impl Iterator<Item = (String, String)>) -> Result<String> {
    let mut inputs: Vec<_> = materials.collect();
    inputs.sort();
    Ok(Sha256::of(&codec::encode_json(&(scope, inputs))?)
        .as_str()
        .into())
}

fn file(path: &str, kind: &str, bytes: &[u8]) -> ArtifactFile {
    ArtifactFile {
        path: path.into(),
        kind: kind.into(),
        mime: mime(path).into(),
        sha256: Sha256::of(bytes).as_str().into(),
        size: bytes.len(),
    }
}

struct Group {
    material: Material,
    files: Vec<ArtifactFile>,
    blobs: Vec<(String, Vec<u8>)>,
}

impl Group {
    fn new(
        target: &str,
        hash: String,
        files: Vec<ArtifactFile>,
        blobs: Vec<(String, Vec<u8>)>,
    ) -> Self {
        let paths = files.iter().map(|file| file.path.clone()).collect();
        Self {
            material: Material {
                target: target.into(),
                digest: hash,
                paths,
                shared: false,
                binding: None,
            },
            files,
            blobs,
        }
    }

    fn single(path: &str, kind: &str, bytes: Vec<u8>, hash: Option<String>, capture: bool) -> Self {
        let file = file(path, kind, &bytes);
        let hash = hash.unwrap_or_else(|| file.sha256.clone());
        let blobs = capture
            .then(|| (file.sha256.clone(), bytes))
            .into_iter()
            .collect();
        Self::new(path, hash, vec![file], blobs)
    }
}

fn scoped_bytes<'a>(
    bytes: &'a [u8],
    section: &Section,
    reference: &str,
) -> Result<(&'a [u8], String)> {
    let text =
        std::str::from_utf8(bytes).map_err(|_| sources::invalid("需要核实的条款来源不是 UTF-8"))?;
    let mut lines = text.split_inclusive('\n');
    let is_line = |line: &str, marker: &str| line.trim_end_matches(['\r', '\n']) == marker;
    let mut before = 0;
    let mut after = 0;
    let mut start = None;
    let mut end = None;
    let mut offset = 0;
    for line in &mut lines {
        if is_line(line, &section.start) {
            start = Some(offset);
            before += 1;
        }
        offset += line.len();
        if is_line(line, &section.end) {
            end = Some(offset);
            after += 1;
        }
    }
    let selected = match (before, after, start.zip(end)) {
        (1, 1, Some((start, end))) if start < end => &bytes[start..end],
        _ => {
            return Err(sources::invalid(
                "条款起止行不存在、重复或顺序变化，需要核实适用范围",
            ));
        }
    };
    let hash = Sha256::of(&codec::encode_json(&(
        reference,
        section,
        Sha256::of(selected),
    ))?)
    .as_str()
    .into();
    Ok((selected, hash))
}

fn live(root: &Root, resource: &ResourceRef, capture: bool) -> Result<Group> {
    if resource.target_type == TargetType::Web {
        return Err(sources::invalid("外部产物需要关联本地快照"));
    }
    let kind = match resource.kind {
        ResourceKind::Supplement => "supplement",
        ResourceKind::Prototype => "prototype",
        ResourceKind::Interaction => "interaction",
        ResourceKind::Other => "other",
    };
    let path = &resource.target;
    if resource.kind == ResourceKind::Prototype {
        let tree = preview::artifact_files(root, path)?;
        if !tree.contains_key(path) {
            return Err(sources::invalid("原型运行包缺少关联入口"));
        }
        let hash = preview::files_digest(&tree);
        let files: Vec<_> = tree
            .iter()
            .map(|(path, bytes)| file(path, kind, bytes))
            .collect();
        let blobs = if capture {
            files
                .iter()
                .zip(tree.into_values())
                .map(|(file, bytes)| (file.sha256.clone(), bytes))
                .collect()
        } else {
            Vec::new()
        };
        return Ok(Group::new(path, hash, files, blobs));
    }
    let bytes = root.source_read(&RelPath::new(path)?, MAX_STATE)?.bytes;
    Ok(Group::single(path, kind, bytes, None, capture))
}

fn legacy_groups(
    snapshot: &ConfirmationSnapshot,
    main: &str,
    resources: &[ResourceRef],
) -> Option<Vec<Material>> {
    let main_file = snapshot.files.iter().find(|file| file.path == main)?;
    let mut materials = vec![Material {
        target: main.into(),
        digest: main_file.sha256.clone(),
        paths: vec![main.into()],
        shared: false,
        binding: None,
    }];
    for resource in resources {
        if resource.target_type != TargetType::Local {
            return None;
        }
        let path = &resource.target;
        let entry = snapshot.files.iter().find(|file| file.path == *path)?;
        let (hash, paths) = if resource.kind == ResourceKind::Prototype {
            let mut files: Vec<_> = snapshot
                .files
                .iter()
                .filter(|file| preview::served_path(path, &file.path))
                .cloned()
                .collect();
            files.sort_by(|a, b| a.path.cmp(&b.path));
            if !files.iter().any(|file| file.path == *path) {
                return None;
            }
            (
                preview::manifest_digest(&files)?,
                files.into_iter().map(|file| file.path).collect(),
            )
        } else {
            (entry.sha256.clone(), vec![path.clone()])
        };
        materials.push(Material {
            target: path.clone(),
            digest: hash,
            paths,
            shared: false,
            binding: None,
        });
    }
    Some(materials)
}

struct Baseline<'a> {
    snapshot: &'a ConfirmationSnapshot,
    materials: Vec<Material>,
}

impl<'a> Baseline<'a> {
    fn new(
        record: &ConfirmationRecord,
        snapshot: &'a ConfirmationSnapshot,
        main: &str,
        resources: &[ResourceRef],
    ) -> Option<Self> {
        if record.id != snapshot.confirmation_id
            || record.spec_id != snapshot.spec_id
            || record.artifact_digest != snapshot.artifact_digest
        {
            return None;
        }
        let materials = if snapshot.materials.is_empty() {
            legacy_groups(snapshot, main, resources)?
        } else {
            snapshot.materials.clone()
        };
        let proved = digest(
            &record.scope,
            materials
                .iter()
                .map(|group| (group.target.clone(), group.digest.clone())),
        )
        .ok()?;
        (proved == record.artifact_digest).then_some(Self {
            snapshot,
            materials,
        })
    }

    fn material(&self, path: &str) -> Option<&Material> {
        self.materials.iter().find(|item| item.target == path)
    }

    fn group(&self, path: &str) -> Option<Group> {
        let material = self.material(path)?.clone();
        let files = material
            .paths
            .iter()
            .map(|path| {
                self.snapshot
                    .files
                    .iter()
                    .find(|file| &file.path == path)
                    .cloned()
            })
            .collect::<Option<Vec<_>>>()?;
        Some(Group {
            material,
            files,
            blobs: vec![],
        })
    }
}

#[derive(Default)]
pub(super) struct Assessment {
    pub digest: Option<String>,
    pub observed_digest: Option<String>,
    pub review_digest: Option<String>,
    pub changes: Vec<MaterialChange>,
    pub blockers: Vec<String>,
    pub materials: Vec<Material>,
    pub files: BTreeMap<String, ArtifactFile>,
    pub blobs: Vec<(String, Vec<u8>)>,
    pub available: BTreeMap<String, String>,
    pub observed_resources: BTreeMap<String, String>,
}

impl Assessment {
    fn add(&mut self, group: Group) -> Result<()> {
        for file in group.files {
            if let Some(existing) = self.files.get(&file.path)
                && existing.sha256 != file.sha256
            {
                return Err(sources::invalid("同一原型目录须采用一致的运行包版本"));
            }
            self.files.insert(file.path.clone(), file);
        }
        self.blobs.extend(group.blobs);
        self.materials.push(group.material);
        Ok(())
    }
}

pub(super) struct Context<'a> {
    pub records: &'a Records,
    pub shared: &'a Shared,
}

pub(super) struct Source<'a> {
    pub root: &'a Root,
    pub spec: &'a SpecRecord,
    pub text: &'a str,
    pub resources: &'a [ResourceRef],
}

pub(super) fn assess(
    source: Source<'_>,
    view: &ContentView,
    context: Option<&Context<'_>>,
    capture: bool,
) -> Result<Assessment> {
    let Source {
        root,
        spec,
        text,
        resources,
    } = source;
    let review = context.and_then(|context| context.records.reviews.get(&spec.id));
    let bindings = review
        .and_then(|r| r.input.bindings.as_deref())
        .unwrap_or_default();
    let mut result = Assessment::default();
    let mut incomplete = false;
    let main = Group::single(
        &spec.source_path,
        "spec",
        text.as_bytes().to_vec(),
        None,
        capture,
    );
    let mut observations = Vec::new();
    for resource in resources {
        let path = &resource.target;
        let binding = bindings.iter().find(|binding| binding.target == *path);
        let fingerprint = binding_digest(binding)?;
        let section = binding.and_then(|b| b.section.as_ref().zip(b.scope_ref.as_deref()));
        let reference = binding.is_some_and(|b| b.usage == Use::Reference);
        let raw = live(root, resource, !reference && (capture || section.is_some()));
        let raw_digest = raw.as_ref().ok().map(|group| group.material.digest.clone());
        if let Some(hash) = &raw_digest {
            result.observed_resources.insert(path.clone(), hash.clone());
        }
        if reference {
            continue;
        }
        let observed = raw.and_then(|group| {
            if let Some((section, reference)) = section {
                let (_, bytes) = &group.blobs[0];
                let (selected, hash) = scoped_bytes(bytes, section, reference)?;
                return Ok(Group::single(
                    path,
                    &group.files[0].kind,
                    selected.to_vec(),
                    Some(hash),
                    capture,
                ));
            }
            Ok(group)
        });
        observations.push((resource, binding, fingerprint, raw_digest, observed));
    }

    let latest = view.confirmations.last();
    let record = if context.is_some()
        && view.confirmations.len() > 1
        && observations
            .iter()
            .all(|(_, _, _, _, observed)| observed.is_ok())
    {
        let live_digest = digest(
            &view.scope,
            std::iter::once(&main)
                .chain(observations.iter().filter_map(|item| item.4.as_ref().ok()))
                .map(|group| (group.material.target.clone(), group.material.digest.clone())),
        )?;
        view.confirmations
            .iter()
            .rev()
            .find(|record| {
                record.scope == view.scope
                    && record.artifact_digest == live_digest
                    && observations
                        .iter()
                        .all(|(resource, binding, fingerprint, _, _)| {
                            if !binding.is_some_and(|binding| {
                                binding.usage == Use::Dependency && binding.section.is_none()
                            }) {
                                return true;
                            }
                            context
                                .and_then(|context| context.records.snapshots.get(&record.id))
                                .and_then(|snapshot| {
                                    snapshot
                                        .materials
                                        .iter()
                                        .find(|m| m.target == resource.target)
                                })
                                .is_some_and(|material| &material.binding == fingerprint)
                        })
            })
            .or(latest)
    } else {
        latest
    };
    let snapshot = record.and_then(|record| context?.records.snapshots.get(&record.id));
    let baseline = record.zip(snapshot).and_then(|(record, snapshot)| {
        Baseline::new(record, snapshot, &spec.source_path, resources)
    });
    let main_changed = snapshot
        .and_then(|s| s.files.iter().find(|f| f.path == spec.source_path))
        .is_some_and(|file| file.sha256 != main.material.digest);
    if main_changed {
        result.changes.push(MaterialChange {
            target: spec.source_path.clone(),
            kind: MaterialChangeKind::Changed,
            reason: "主 Spec 内容已更新".into(),
            confirmed_digest: snapshot
                .and_then(|s| s.files.iter().find(|f| f.path == spec.source_path))
                .map(|f| f.sha256.clone()),
            observed_digest: Some(main.material.digest.clone()),
        });
    }
    result.add(main)?;
    for (resource, binding, fingerprint, raw_digest, observed) in observations {
        let path = &resource.target;
        let old = baseline
            .as_ref()
            .and_then(|baseline| baseline.material(path));
        let material_key = key(resource);
        let shared = match (old, record, snapshot) {
            (Some(material), _, Some(snapshot)) if !snapshot.materials.is_empty() => {
                material.shared
            }
            (Some(_), Some(record), _) => {
                context.is_some_and(|context| context.shared.before(record, &material_key))
            }
            _ => context.is_some_and(|context| context.shared.current.contains(&material_key)),
        };
        let dependency = binding.is_some_and(|b| b.usage == Use::Dependency);
        let scoped = binding.is_some_and(|b| b.section.is_some());
        let follow = binding.is_some_and(|b| b.usage == Use::Follow);
        let live_digest = observed
            .as_ref()
            .ok()
            .map(|group| group.material.digest.clone());
        if let Some(hash) = &live_digest {
            result.available.insert(path.clone(), hash.clone());
        }
        let reviewed = |version: &ReviewedVersion, hash: Option<&String>| {
            Some(&version.digest) == hash
                && version.confirmation_id.as_ref() == record.map(|record| &record.id)
                && version.binding == fingerprint
        };
        let adopted = review
            .and_then(|r| r.adopted.get(path))
            .is_some_and(|v| reviewed(v, live_digest.as_ref()));
        let retained = review
            .and_then(|r| r.retained.get(path))
            .filter(|v| reviewed(v, raw_digest.as_ref()));
        let frozen = !follow
            && !adopted
            && ((shared || dependency) && !scoped || dependency && observed.is_err());
        let differs = old.is_some_and(|m| Some(&m.digest) != live_digest.as_ref());
        if let Some(mut group) = frozen.then(|| baseline.as_ref()?.group(path)).flatten() {
            if differs {
                let kind = if dependency && retained.is_none() {
                    MaterialChangeKind::DependencyReview
                } else if observed.is_err() && retained.is_none() {
                    MaterialChangeKind::Unavailable
                } else {
                    MaterialChangeKind::SharedUpdated
                };
                let reason = match &observed {
                    _ if retained.is_some() => format!(
                        "已核实并沿用确认材料 · {}",
                        retained.expect("retained").reason
                    ),
                    Err(error) => format!("{}，已确认材料可回看", error.message),
                    Ok(_) if dependency => format!(
                        "核实 {} 的影响",
                        binding.and_then(|b| b.scope_ref.as_deref()).unwrap_or(path)
                    ),
                    Ok(_) => "共用资料有新版，当前仍采用已确认快照".into(),
                };
                result.changes.push(MaterialChange {
                    target: path.clone(),
                    kind,
                    reason,
                    confirmed_digest: old.map(|m| m.digest.clone()),
                    observed_digest: live_digest,
                });
                if dependency && retained.is_none() {
                    result
                        .blockers
                        .push(format!("{} 的依赖影响待核实", resource.label));
                }
            }
            group.material.shared = shared;
            group.material.binding = fingerprint;
            result.add(group)?;
        } else {
            match observed {
                Ok(mut group) => {
                    if differs || old.is_none() && baseline.is_some() {
                        result.changes.push(MaterialChange {
                            target: path.clone(),
                            kind: MaterialChangeKind::Changed,
                            reason: binding.and_then(|b| b.scope_ref.as_ref()).map_or_else(
                                || format!("{}的确认材料已变化", resource.label),
                                |scope| format!("适用范围 {} 的材料已更新", scope),
                            ),
                            confirmed_digest: old.map(|m| m.digest.clone()),
                            observed_digest: live_digest,
                        });
                    }
                    group.material.shared = shared;
                    group.material.binding = fingerprint;
                    result.add(group)?;
                }
                Err(error) => {
                    incomplete = true;
                    let reason = format!("{}（{}）", path, error.message);
                    result.blockers.push(reason.clone());
                    result.changes.push(MaterialChange {
                        target: path.clone(),
                        kind: if dependency {
                            MaterialChangeKind::DependencyReview
                        } else {
                            MaterialChangeKind::Unavailable
                        },
                        reason,
                        confirmed_digest: old.map(|m| m.digest.clone()),
                        observed_digest: None,
                    });
                }
            }
        }
    }
    if let Some(baseline) = &baseline {
        for old in &baseline.materials {
            if !result
                .materials
                .iter()
                .any(|material| material.target == old.target)
                && !result
                    .changes
                    .iter()
                    .any(|change| change.target == old.target)
            {
                result.changes.push(MaterialChange {
                    target: old.target.clone(),
                    kind: MaterialChangeKind::Changed,
                    reason: "该资料已移出本轮确认材料".into(),
                    confirmed_digest: Some(old.digest.clone()),
                    observed_digest: None,
                });
            }
        }
    }
    root.revalidate()?;
    let candidate = digest(
        &view.scope,
        result
            .materials
            .iter()
            .map(|m| (m.target.clone(), m.digest.clone())),
    )?;
    let observed = digest(
        &view.scope,
        result
            .observed_resources
            .iter()
            .map(|(target, hash)| (target.clone(), hash.clone()))
            .chain(
                result
                    .materials
                    .first()
                    .map(|m| (m.target.clone(), m.digest.clone())),
            ),
    )?;
    result.review_digest = Some(
        Sha256::of(&codec::encode_json(&(
            &observed,
            latest.map(|record| &record.id),
            review,
        ))?)
        .as_str()
        .into(),
    );
    result.observed_digest = Some(observed.clone());
    let matches_confirmation = view
        .confirmations
        .iter()
        .any(|r| r.scope == view.scope && r.artifact_digest == candidate);
    if context.is_some()
        && record.is_some()
        && !matches_confirmation
        && baseline.is_none()
        && !main_changed
        && review.is_none_or(|review| {
            review
                .observed_sources
                .as_deref()
                .unwrap_or(&review.input.observed_digest)
                != observed
        })
    {
        let reason = if snapshot.is_none() {
            "历史正文未留存，当前材料适用性待核实"
        } else {
            "旧版材料关联无法完整复原，当前适用性待核实"
        };
        result.blockers.push(reason.into());
        result.changes.push(MaterialChange {
            target: spec.source_path.clone(),
            kind: MaterialChangeKind::HistoryUnavailable,
            reason: reason.into(),
            confirmed_digest: record.map(|r| r.artifact_digest.clone()),
            observed_digest: Some(candidate.clone()),
        });
    }
    result.digest = (!incomplete).then_some(candidate);
    Ok(result)
}

pub(super) fn prepare_review(
    state: &WorkbenchState,
    store: &mut Store,
    mut input: ReviewInput,
    at: &str,
) -> Result<CommitEffects> {
    fn conflict() -> YssError {
        YssError::new(
            ErrorCode::ConcurrentChange,
            "核实期间来源材料已变化，请重新读取",
        )
        .context("httpStatus", 409)
    }
    let spec = super::tasks::active_spec(state, &input.spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let source = sources::text(&root, &spec.source_path)?;
    let metadata = content::metadata(&source)?
        .ok_or_else(|| sources::invalid("主 Spec 缺少内容状态和范围"))?;
    let resources = content::resolved_resources(spec, Some(&metadata), Some(&root));
    let mut records = Records::load(store)?;
    let previous = records.reviews.get(&input.spec_id);
    if input.bindings.is_none() {
        input.bindings = previous.and_then(|r| r.input.bindings.clone());
        if let Some(bindings) = &mut input.bindings {
            bindings.retain(|binding| {
                resources
                    .iter()
                    .any(|resource| resource.target == binding.target)
            });
        }
    }
    let bindings = input.bindings.as_deref().unwrap_or_default();
    if input.reason.trim().is_empty()
        || input.reason.len() > 4096
        || bindings.len() > resources.len()
        || input.adopt_latest.len() + input.keep_confirmed.len() > resources.len()
    {
        return Err(sources::invalid("材料核实须记录原因及实际关联范围"));
    }
    let mut seen = BTreeSet::new();
    for binding in bindings {
        let resource = resources
            .iter()
            .find(|resource| resource.target == binding.target)
            .ok_or_else(|| sources::invalid("仅能核实当前 Spec 已关联的材料"))?;
        if !seen.insert(&binding.target)
            || binding
                .scope_ref
                .as_ref()
                .is_some_and(|scope| scope.trim().is_empty() || scope.len() > 1024)
            || binding.usage == Use::Dependency
                && (binding.scope_ref.is_none() || resource.target_type != TargetType::Local)
            || binding.section.as_ref().is_some_and(|section| {
                binding.usage != Use::Dependency
                    || resource.kind == ResourceKind::Prototype
                    || section.start.trim().is_empty()
                    || section.end.trim().is_empty()
                    || section.start == section.end
                    || section.start.len() > 1024
                    || section.end.len() > 1024
                    || section.start.contains(['\n', '\r'])
                    || section.end.contains(['\n', '\r'])
            })
        {
            return Err(sources::invalid("材料用途、依赖范围或起止行无效"));
        }
    }
    let shared = shared(state, &records, &spec.project_id, &root);
    let context = Context {
        records: &records,
        shared: &shared,
    };
    let (before, _) =
        content::inspect(state, spec, &Ok(root), Some(&source), Some(&context), false);
    if before.material_review_digest.as_deref() != Some(&input.observed_digest) {
        return Err(conflict());
    }
    let spec_id = input.spec_id.clone();
    let confirmation = before.confirmations.last().map(|record| record.id.clone());
    let mut review = Review {
        input,
        observed_sources: before.observed_artifact_digest,
        adopted: BTreeMap::new(),
        retained: BTreeMap::new(),
        reviewed_at: at.into(),
    };
    if let Some(previous) = previous {
        review.adopted.clone_from(&previous.adopted);
        review.retained.clone_from(&previous.retained);
    }
    review
        .adopted
        .retain(|target, _| resources.iter().any(|resource| &resource.target == target));
    review
        .retained
        .retain(|target, _| resources.iter().any(|resource| &resource.target == target));
    records.reviews.insert(spec_id.clone(), review.clone());
    let root = Ok(Root::open(Path::new(&project.root_path))?);
    let context = Context {
        records: &records,
        shared: &shared,
    };
    let (after, material) = content::inspect(state, spec, &root, None, Some(&context), false);
    if after.observed_artifact_digest != review.observed_sources {
        return Err(conflict());
    }
    let material = material.ok_or_else(conflict)?;
    let decision = |target: &str, hash: &str| -> Result<ReviewedVersion> {
        let binding = review
            .input
            .bindings
            .as_deref()
            .unwrap_or_default()
            .iter()
            .find(|binding| binding.target == target);
        Ok(ReviewedVersion {
            digest: hash.into(),
            confirmation_id: confirmation.clone(),
            binding: binding_digest(binding)?,
            reason: review.input.reason.clone(),
            reviewed_at: at.into(),
        })
    };
    let mut selected = BTreeSet::new();
    for target in &review.input.adopt_latest {
        if !selected.insert(target) {
            return Err(sources::invalid("材料核实项重复"));
        }
        let hash = material
            .available
            .get(target)
            .ok_or_else(|| sources::invalid("采用新版须选择当前可读的本地材料"))?;
        review
            .adopted
            .insert(target.clone(), decision(target, hash)?);
        review.retained.remove(target);
    }
    for target in &review.input.keep_confirmed {
        if !selected.insert(target) {
            return Err(sources::invalid("材料核实项重复"));
        }
        let change = after.material_changes.iter().find(|change| {
            change.target == *target
                && change.confirmed_digest.is_some()
                && matches!(
                    change.kind,
                    MaterialChangeKind::DependencyReview | MaterialChangeKind::SharedUpdated
                )
        });
        let hash = material
            .observed_resources
            .get(target)
            .filter(|_| change.is_some())
            .ok_or_else(|| sources::invalid("沿用原版须有原确认和可核对的当前材料"))?;
        review
            .retained
            .insert(target.clone(), decision(target, hash)?);
        review.adopted.remove(target);
    }
    Ok(CommitEffects {
        records: vec![RecordWrite {
            kind: REVIEWS.into(),
            id: spec_id,
            payload: Some(
                serde_json::to_string(&review)
                    .map_err(|_| sources::invalid("材料核实记录编码失败"))?,
            ),
        }],
        ..CommitEffects::default()
    })
}
