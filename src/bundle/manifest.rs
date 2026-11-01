use crate::model::{
    FileDescriptor, InstallationKind, Ownership, Profile, RelPath, SourceProvenance,
};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Manifest {
    pub format: String,
    pub format_version: u32,
    pub source: SourceProvenance,
    pub profiles: BTreeMap<Profile, ProfileBundle>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProfileBundle {
    pub files: BTreeMap<RelPath, BundleFile>,
    pub installations: BTreeMap<InstallationKind, Installation>,
    pub retired_skills: BTreeMap<String, RetiredSkill>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct BundleFile {
    pub descriptor: FileDescriptor,
    pub ownership: Ownership,
    pub render: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Installation {
    pub defaults: BTreeSet<String>,
    pub base: BTreeMap<RelPath, BundleFile>,
    pub skill_dependencies: BTreeMap<String, BTreeSet<String>>,
    pub skill_references: BTreeMap<String, BTreeSet<RelPath>>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RetiredSkill {
    pub replacements: Vec<String>,
    pub reason: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SourcePolicy {
    pub schema_version: u32,
    pub profile: Profile,
    pub manifest: SourceManifest,
    pub installations: BTreeMap<InstallationKind, SourceInstallation>,
    pub retired_skills: BTreeMap<String, RetiredSkill>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SourceInstallation {
    pub skills: BTreeSet<String>,
    pub files: BTreeSet<RelPath>,
    pub entries: BTreeMap<RelPath, RelPath>,
    pub skill_dependencies: BTreeMap<String, BTreeSet<String>>,
    pub skill_references: BTreeMap<String, BTreeSet<RelPath>>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SourceManifest {
    #[serde(default)]
    pub allow_root_entries: BTreeSet<String>,
    #[serde(default)]
    pub allow_root_files: BTreeSet<String>,
    #[serde(default)]
    pub allow_files: BTreeSet<String>,
    #[serde(default)]
    pub exclude_root_entries: BTreeSet<String>,
    #[serde(default)]
    pub exclude_root_files: BTreeSet<String>,
    #[serde(default)]
    pub exclude_paths: BTreeSet<String>,
    #[serde(default)]
    pub exclude_path_suffixes: BTreeSet<String>,
    #[serde(default)]
    pub render_paths: BTreeSet<RelPath>,
    #[serde(default)]
    pub example_doc_paths: BTreeSet<RelPath>,
    #[serde(default)]
    pub init_exclude_root_entries: BTreeSet<String>,
    #[serde(default)]
    pub init_exclude_root_files: BTreeSet<String>,
    #[serde(default)]
    pub init_exclude_paths: BTreeSet<RelPath>,
    pub ownership_policy: OwnershipPolicy,
    #[serde(default)]
    pub customization_policy: Option<CustomizationPolicy>,
    #[serde(default)]
    pub generator_policy: Option<GeneratorPolicy>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OwnershipPolicy {
    pub version: u32,
    pub default: Ownership,
    pub rules: Vec<OwnershipRule>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OwnershipRule {
    pub pattern: String,
    pub ownership: Ownership,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CustomizationPolicy {
    pub version: u32,
    pub default_strategy: String,
    pub rules: Vec<CustomizationRule>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CustomizationRule {
    pub pattern: String,
    pub strategy: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratorPolicy {
    pub version: u32,
    pub rules: Vec<GeneratorRule>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct GeneratorRule {
    pub pattern: String,
    pub generator_id: String,
    pub generator_version: u32,
}

pub fn beneath(path: &str, prefix: &str) -> bool {
    path == prefix || path.starts_with(&format!("{prefix}/"))
}
impl SourceManifest {
    pub fn includes(&self, path: &str) -> bool {
        if self.allow_files.iter().any(|p| beneath(path, p)) {
            return true;
        }
        let root = path.split('/').next().unwrap_or("");
        (self.allow_root_entries.contains(root) || self.allow_root_files.contains(path))
            && !self.exclude_root_entries.contains(root)
            && !self.exclude_root_files.contains(root)
            && !self.exclude_paths.iter().any(|p| beneath(path, p))
            && !path
                .split('/')
                .any(|p| self.exclude_path_suffixes.iter().any(|s| p.ends_with(s)))
    }
    pub fn ownership(&self, path: &str) -> Ownership {
        self.ownership_policy
            .rules
            .iter()
            .find(|r| {
                if let Some(prefix) = r.pattern.strip_suffix("/**") {
                    beneath(path, prefix)
                } else if let Some(prefix) = r.pattern.strip_suffix("/*") {
                    path.rsplit_once('/').is_some_and(|(p, _)| p == prefix)
                } else {
                    path == r.pattern
                }
            })
            .map_or(self.ownership_policy.default, |r| r.ownership)
    }
}
