use super::{
    Bundle,
    archive::{self, Entry},
    manifest::{self, BundleFile, Installation, Manifest, ProfileBundle, SourcePolicy},
};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root, git},
    model::{
        FileDescriptor, MAX_ASSET, MAX_MANIFEST, Ownership, Profile, RelPath, Sha256,
        SourceProvenance,
    },
};
use serde::Serialize;
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Component, Path, PathBuf},
};

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct BundleBuildReport {
    pub path: String,
    pub sha256: Sha256,
    pub bytes: usize,
    pub objects: usize,
    pub source: SourceProvenance,
}
struct SourceFile {
    bytes: Vec<u8>,
    descriptor: FileDescriptor,
}
struct Snapshot {
    files: BTreeMap<RelPath, SourceFile>,
    digest: Sha256,
    commit: String,
    status: Vec<u8>,
    inventory: Vec<u8>,
}
fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
fn prefix(profile: Profile) -> &'static str {
    match profile {
        Profile::Spec => "",
        Profile::Backend => "profiles/backend/",
        Profile::Frontend => "profiles/frontend/",
    }
}
fn kernel(path: &str) -> bool {
    [
        ".agents/skills",
        ".template-spec/process",
        ".template-spec/templates",
    ]
    .iter()
    .any(|p| manifest::beneath(path, p))
}
fn admitted(path: &str, policies: &BTreeMap<Profile, SourcePolicy>) -> bool {
    for profile in [Profile::Spec, Profile::Backend, Profile::Frontend] {
        let path = path.strip_prefix(prefix(profile)).unwrap_or(path);
        let path = path.strip_prefix("profiles/_specialist/").unwrap_or(path);
        if policies[&profile].manifest.includes(path) {
            return true;
        }
    }
    false
}
fn policy_paths() -> [RelPath; 3] {
    [
        RelPath::new(".template-source/distribution/bundle-profile.json").unwrap(),
        RelPath::new("profiles/backend/.template-source/distribution/bundle-profile.json").unwrap(),
        RelPath::new("profiles/frontend/.template-source/distribution/bundle-profile.json")
            .unwrap(),
    ]
}
fn source_snapshot(root: &Root, policies: &BTreeMap<Profile, SourcePolicy>) -> Result<Snapshot> {
    let commit = String::from_utf8(git::output(
        &root.path,
        &["rev-parse", "HEAD"],
        None,
        false,
    )?)
    .map_err(|_| invalid("模板 HEAD 不合法"))?
    .trim()
    .to_owned();
    let status = git::output(
        &root.path,
        &["status", "--porcelain=v1", "-z", "--untracked-files=all"],
        None,
        false,
    )?;
    let inventory = git::output(
        &root.path,
        &[
            "ls-files",
            "--cached",
            "--others",
            "--exclude-standard",
            "-z",
        ],
        None,
        false,
    )?;
    let mut listed = BTreeSet::new();
    for path in inventory.split(|b| *b == 0).filter(|p| !p.is_empty()) {
        listed.insert(RelPath::new(
            std::str::from_utf8(path).map_err(|_| invalid("模板含非 UTF-8 路径"))?,
        )?);
    }
    let mut needed: BTreeSet<RelPath> = policy_paths().into_iter().collect();
    for (&profile, policy) in policies {
        for installation in policy.installations.values() {
            for path in installation.entries.values() {
                if !path
                    .as_str()
                    .starts_with(".template-source/distribution/entries/")
                {
                    return Err(invalid("入口来源超出分发目录").at(path));
                }
                let overlay = RelPath::new(format!("{}{path}", prefix(profile)))?;
                needed.insert(if listed.contains(&overlay) {
                    overlay
                } else {
                    path.clone()
                });
            }
        }
    }
    let required = needed.clone();
    for path in &listed {
        if admitted(path.as_str(), policies) {
            needed.insert(path.clone());
        }
    }
    let mut files = BTreeMap::new();
    let mut links = BTreeMap::new();
    let mut total = 0u64;
    for path in needed {
        if !listed.contains(&path) {
            return Err(invalid("模板输入未纳入 Git 或未忽略文件集合").at(path));
        }
        let is_link = match root.is_link(&path)? {
            Some(value) => value,
            None if !required.contains(&path) => continue,
            None => return Err(invalid("源策略或入口文件缺失").at(path)),
        };
        if is_link {
            let target = root.read_link(&path)?;
            let parts: Vec<_> = path.as_str().split('/').collect();
            if parts.len() < 3
                || parts[1] != "skills"
                || !matches!(
                    parts[0],
                    ".claude" | ".codex" | ".cursor" | ".pi" | ".workbuddy"
                )
            {
                return Err(invalid("不安全的技能投影").at(path));
            }
            let mut resolved = PathBuf::from(path.as_str()).parent().unwrap().to_path_buf();
            for component in target.components() {
                match component {
                    Component::ParentDir => {
                        if !resolved.pop() {
                            return Err(invalid("技能投影越界"));
                        }
                    }
                    Component::Normal(s) => resolved.push(s),
                    Component::CurDir => {}
                    _ => return Err(invalid("技能投影必须为相对路径")),
                }
            }
            let resolved = RelPath::new(
                resolved
                    .to_str()
                    .ok_or_else(|| invalid("投影路径不合法"))?
                    .replace('\\', "/"),
            )?;
            if resolved.as_str() != format!(".agents/skills/{}", parts[2..].join("/")) {
                return Err(invalid("技能投影不指向对应权威内容").at(path));
            }
            links.insert(path, (resolved, target.to_string_lossy().into_owned()));
        } else {
            let checked = root.read_checked(&path, MAX_ASSET)?;
            total = total
                .checked_add(checked.bytes.len() as u64)
                .ok_or_else(|| invalid("模板大小溢出"))?;
            if total > MAX_ASSET {
                return Err(invalid("模板累计大小超过限制"));
            }
            let mut descriptor = checked
                .snapshot
                .descriptor()
                .ok_or_else(|| invalid("模板文件不合法"))?
                .clone();
            descriptor.mode = if descriptor.mode & 0o111 != 0 {
                0o755
            } else {
                0o644
            };
            files.insert(
                path,
                SourceFile {
                    bytes: checked.bytes,
                    descriptor,
                },
            );
        }
    }
    let descriptors: BTreeMap<_, _> = files
        .iter()
        .map(|(p, f)| (p.clone(), f.descriptor.clone()))
        .collect();
    let snapshot = codec::encode_json(&(descriptors, &links))?;
    for (projection, (canonical, _)) in &links {
        let matches: Vec<_> = files
            .iter()
            .filter(|(p, _)| manifest::beneath(p.as_str(), canonical.as_str()))
            .map(|(p, f)| {
                (
                    RelPath::new(format!(
                        "{}{}",
                        projection,
                        &p.as_str()[canonical.as_str().len()..]
                    ))
                    .unwrap(),
                    SourceFile {
                        bytes: f.bytes.clone(),
                        descriptor: f.descriptor.clone(),
                    },
                )
            })
            .collect();
        if matches.is_empty() {
            return Err(invalid("技能投影缺少来源文件").at(projection));
        }
        for (path, file) in matches {
            if files.insert(path.clone(), file).is_some() {
                return Err(invalid("重复技能投影").at(path));
            }
        }
    }
    Ok(Snapshot {
        files,
        digest: Sha256::of(&snapshot),
        commit,
        status,
        inventory,
    })
}
pub fn build(source_root: &Path, out: &Path) -> Result<BundleBuildReport> {
    let root = Root::open(source_root)?;
    let mut policies = BTreeMap::new();
    let mut policy_bytes = BTreeMap::new();
    for (profile, path) in [Profile::Spec, Profile::Backend, Profile::Frontend]
        .into_iter()
        .zip(policy_paths())
    {
        let bytes = root.read_checked(&path, MAX_MANIFEST)?.bytes;
        let policy: SourcePolicy =
            codec::decode_json(&bytes, Limits::manifest()).map_err(|e| e.at(&path))?;
        if policy.schema_version != 6
            || policy.profile != profile
            || policy.installations.len() != 2
            || policy.manifest.ownership_policy.version != 1
        {
            return Err(invalid("源分发清单身份不合法").at(path));
        }
        policy_bytes.insert(path, bytes);
        policies.insert(profile, policy);
    }
    let before =
        source_snapshot(&root, &policies).map_err(|e| e.context("operation", "read-template"))?;
    for (path, bytes) in policy_bytes {
        if before.files.get(&path).is_none_or(|f| f.bytes != bytes) {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "源策略在首次读取后改变",
            ));
        }
    }
    let provenance = SourceProvenance {
        commit: before.commit.clone(),
        state: if before.status.is_empty() {
            "committed"
        } else {
            "working-tree"
        }
        .into(),
        snapshot: before.digest.clone(),
    };
    let mut profiles = BTreeMap::new();
    let mut objects = BTreeMap::new();
    for (profile, policy) in policies.iter() {
        let mut selected: BTreeMap<RelPath, &SourceFile> = BTreeMap::new();
        for (path, file) in &before.files {
            if (*profile == Profile::Spec || kernel(path.as_str()))
                && policy.manifest.includes(path.as_str())
            {
                selected.insert(path.clone(), file);
            }
        }
        if *profile != Profile::Spec {
            for overlay in ["profiles/_specialist/", prefix(*profile)] {
                for (path, file) in &before.files {
                    if let Some(path) = path.as_str().strip_prefix(overlay)
                        && policy.manifest.includes(path)
                    {
                        selected.insert(RelPath::new(path)?, file);
                    }
                }
            }
        }
        let mut files = BTreeMap::new();
        for (path, file) in selected {
            objects
                .entry(file.descriptor.sha256.clone())
                .or_insert_with(|| file.bytes.clone());
            let entry = BundleFile {
                descriptor: file.descriptor.clone(),
                ownership: policy.manifest.ownership(path.as_str()),
                render: policy.manifest.render_paths.contains(&path),
            };
            files.insert(path, entry);
        }
        let mut installations = BTreeMap::new();
        for (&kind, mode) in &policy.installations {
            let mut base = BTreeMap::new();
            for path in &mode.files {
                base.insert(
                    path.clone(),
                    files
                        .get(path)
                        .ok_or_else(|| invalid("安装基础资源缺失").at(path))?
                        .clone(),
                );
            }
            for (target, source) in &mode.entries {
                let overlay = RelPath::new(format!("{}{source}", prefix(*profile)))?;
                let file = before
                    .files
                    .get(&overlay)
                    .or_else(|| before.files.get(source))
                    .ok_or_else(|| invalid("安装入口来源缺失").at(source))?;
                objects
                    .entry(file.descriptor.sha256.clone())
                    .or_insert_with(|| file.bytes.clone());
                base.insert(
                    target.clone(),
                    BundleFile {
                        descriptor: file.descriptor.clone(),
                        ownership: Ownership::ManagedCustomizable,
                        render: true,
                    },
                );
            }
            installations.insert(
                kind,
                Installation {
                    defaults: mode.skills.clone(),
                    base,
                    skill_dependencies: mode.skill_dependencies.clone(),
                    skill_references: mode.skill_references.clone(),
                },
            );
        }
        profiles.insert(
            *profile,
            ProfileBundle {
                files,
                installations,
                retired_skills: policy.retired_skills.clone(),
            },
        );
    }
    let manifest = Manifest {
        format: "yss.bundle".into(),
        format_version: 1,
        source: provenance.clone(),
        profiles,
    };
    let bundle = Bundle {
        manifest,
        digest: Sha256::of(b""),
        objects,
    };
    bundle.validate()?;
    let mut entries = BTreeMap::new();
    let manifest = codec::encode_json(&bundle.manifest)?;
    if manifest.len() as u64 > MAX_MANIFEST {
        return Err(invalid("生成的 Bundle manifest 过大"));
    }
    entries.insert(
        RelPath::new("manifest.json")?,
        Entry {
            bytes: manifest,
            mode: 0o644,
        },
    );
    for (hash, bytes) in &bundle.objects {
        entries.insert(
            RelPath::new(format!("objects/{hash}"))?,
            Entry {
                bytes: bytes.clone(),
                mode: 0o644,
            },
        );
    }
    let bytes = archive::pack(&entries)?;
    super::load(&bytes)?;
    let after = source_snapshot(&root, &policies)
        .map_err(|e| e.context("operation", "recheck-template"))?;
    root.revalidate()?;
    if before.digest != after.digest
        || before.commit != after.commit
        || before.status != after.status
        || before.inventory != after.inventory
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "模板输入在构建期间改变；未写出 Bundle",
        ));
    }
    write_artifact(out, &bytes)
        .map_err(|e| e.at(out.display()).context("operation", "publish-bundle"))?;
    Ok(BundleBuildReport {
        path: out.display().to_string(),
        sha256: Sha256::of(&bytes),
        bytes: bytes.len(),
        objects: bundle.objects.len(),
        source: provenance,
    })
}
pub(crate) fn write_artifact(out: &Path, bytes: &[u8]) -> Result<()> {
    let out = fs::absolute(out)?;
    let parent = out.parent().ok_or_else(|| invalid("输出没有父目录"))?;
    if !parent.exists() {
        let grand = parent.parent().ok_or_else(|| invalid("输出父目录不存在"))?;
        Root::open(grand)?.create_dir(&RelPath::new(
            parent
                .file_name()
                .and_then(|s| s.to_str())
                .ok_or_else(|| invalid("输出路径不合法"))?,
        )?)?;
    }
    let root = Root::open(parent)?;
    let name = RelPath::new(
        out.file_name()
            .and_then(|s| s.to_str())
            .ok_or_else(|| invalid("输出路径不合法"))?,
    )?;
    let before = root.inspect(&name)?;
    if matches!(
        before,
        FileSnapshot::Directory { .. } | FileSnapshot::Dangerous
    ) {
        return Err(invalid("输出不是普通文件"));
    }
    let temporary = RelPath::new(format!(".yss-artifact-{}", fs::unique_id()?))?;
    let snapshot = root.create_file(&temporary, bytes, 0o644)?;
    let result = root.rename(&temporary, &name, &before, before != FileSnapshot::Missing);
    if result.is_err() {
        let _ = root.remove(&temporary, &snapshot);
    }
    result
}
