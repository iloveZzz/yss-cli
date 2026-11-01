pub(crate) mod archive;
mod build;
pub mod manifest;
mod selection;
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs,
    model::{InstallationKind, MAX_MANIFEST, Profile, RelPath, RenderedTree, Sha256},
};
pub use build::{BundleBuildReport, build};
use manifest::{Manifest, ProfileBundle};
pub use selection::Selection;
use std::collections::{BTreeMap, BTreeSet};

pub struct Bundle {
    pub manifest: Manifest,
    pub digest: Sha256,
    objects: BTreeMap<Sha256, Vec<u8>>,
}
impl Bundle {
    pub fn profile(&self, profile: Profile) -> Result<&ProfileBundle> {
        self.manifest
            .profiles
            .get(&profile)
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "Bundle 缺少 profile"))
    }
    pub fn bytes(&self, file: &manifest::BundleFile) -> Result<&[u8]> {
        self.objects
            .get(&file.descriptor.sha256)
            .map(Vec::as_slice)
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "Bundle 内容对象缺失"))
    }
    pub fn read(&self, profile: Profile, path: &str) -> Result<&[u8]> {
        let path = RelPath::new(path)?;
        let file =
            self.profile(profile)?.files.get(&path).ok_or_else(|| {
                YssError::new(ErrorCode::InvalidInput, "Bundle 资源缺失").at(path)
            })?;
        self.bytes(file)
    }
    fn validate(&self) -> Result<()> {
        codec::ensure_format(
            &self.manifest.format,
            self.manifest.format_version,
            "yss.bundle",
        )?;
        if self.manifest.profiles.len() != 2
            || !matches!(
                self.manifest.source.state.as_str(),
                "committed" | "working-tree"
            )
            || self.manifest.source.commit.len() != 40
            || !self
                .manifest
                .source
                .commit
                .bytes()
                .all(|c| c.is_ascii_hexdigit())
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "Bundle 来源身份不合法",
            ));
        }
        let mut used = BTreeSet::new();
        for profile in [Profile::Spec, Profile::Backend] {
            let bundle = self.profile(profile)?;
            let available = bundle.skills();
            if bundle.installations.len() != 2 || available.is_empty() {
                return Err(YssError::new(
                    ErrorCode::InvalidInput,
                    "Bundle 安装视图不完整",
                ));
            }
            let mut tree = RenderedTree::default();
            for (path, file) in &bundle.files {
                let bytes = self.bytes(file)?;
                if bytes.len() as u64 != file.descriptor.size
                    || !matches!(file.descriptor.mode, 0o644 | 0o755)
                {
                    return Err(
                        YssError::new(ErrorCode::InvalidInput, "Bundle 描述符不合法").at(path),
                    );
                }
                tree.insert(path.clone(), Vec::new(), file.descriptor.mode)?;
                used.insert(file.descriptor.sha256.clone());
            }
            fs::validate_tree(&tree)?;
            for kind in [InstallationKind::Project, InstallationKind::Skills] {
                let mode = bundle
                    .installations
                    .get(&kind)
                    .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "Bundle 缺少安装视图"))?;
                let mut base_tree = RenderedTree::default();
                for (path, file) in &mode.base {
                    if !matches!(file.descriptor.mode, 0o644 | 0o755) {
                        return Err(YssError::new(ErrorCode::InvalidInput, "入口文件模式不合法"));
                    }
                    base_tree.insert(path.clone(), Vec::new(), file.descriptor.mode)?;
                    if self.bytes(file)?.len() as u64 != file.descriptor.size {
                        return Err(
                            YssError::new(ErrorCode::InvalidInput, "入口描述符不合法").at(path)
                        );
                    }
                    used.insert(file.descriptor.sha256.clone());
                }
                fs::validate_tree(&base_tree)?;
                if mode
                    .skill_references
                    .keys()
                    .cloned()
                    .collect::<BTreeSet<_>>()
                    != available
                    || !mode
                        .skill_dependencies
                        .keys()
                        .all(|s| available.contains(s))
                {
                    return Err(YssError::new(
                        ErrorCode::InvalidInput,
                        "技能声明与可用技能不一致",
                    ));
                }
                bundle.closure(kind, &available)?;
                for paths in mode.skill_references.values() {
                    for path in paths {
                        if !bundle.files.contains_key(path) {
                            return Err(
                                YssError::new(ErrorCode::InvalidInput, "技能资源不存在").at(path)
                            );
                        }
                    }
                }
                self.select(profile, kind, &BTreeSet::new())?;
            }
        }
        if used != self.objects.keys().cloned().collect() {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "Bundle 含未引用的对象",
            ));
        }
        Ok(())
    }
}
pub fn load(bytes: &[u8]) -> Result<Bundle> {
    let mut entries = archive::unpack(bytes)?;
    let manifest = entries
        .remove(&RelPath::new("manifest.json")?)
        .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "缺少 Bundle manifest"))?;
    if manifest.bytes.len() as u64 > MAX_MANIFEST {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "Bundle manifest 过大",
        ));
    }
    let manifest: Manifest = codec::decode_json(&manifest.bytes, Limits::manifest())?;
    let mut objects = BTreeMap::new();
    for (path, entry) in entries {
        let hash: Sha256 = path
            .as_str()
            .strip_prefix("objects/")
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "Bundle 含额外条目"))?
            .to_owned()
            .try_into()?;
        if Sha256::of(&entry.bytes) != hash || entry.mode != 0o644 {
            return Err(YssError::new(ErrorCode::DigestMismatch, "Bundle 对象校验失败").at(path));
        }
        objects.insert(hash, entry.bytes);
    }
    let bundle = Bundle {
        manifest,
        digest: Sha256::of(bytes),
        objects,
    };
    bundle.validate()?;
    Ok(bundle)
}
