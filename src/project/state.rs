use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{
        FileDescriptor, InstallationKind, MAX_MANIFEST, Ownership, Profile, RelPath, Sha256,
        SourceProvenance,
    },
};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};
pub const STATE_PATH: &str = ".yss/project.json";
pub const LEGACY_MARKERS: &[&str] = &[
    ".yss.json",
    ".yss-template.json",
    ".yss-harness-backend.json",
    ".yss-plugin.json",
    ".yss-backend-plugin.json",
    ".yss-execution-scope.yaml",
];
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectState {
    pub format: String,
    pub format_version: u32,
    pub profile: Profile,
    pub installation: InstallationKind,
    pub variables: BTreeMap<String, String>,
    pub source: SourceProvenance,
    pub bundle_digest: Sha256,
    pub explicit_skills: BTreeSet<String>,
    pub managed: BTreeMap<RelPath, ManagedFile>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ManagedFile {
    pub ownership: Ownership,
    pub last_installed: FileDescriptor,
}
pub fn detect(root: &Root) -> Result<(Option<ProjectState>, FileSnapshot)> {
    for marker in LEGACY_MARKERS {
        if root.inspect(&RelPath::new(*marker)?)? != FileSnapshot::Missing {
            return Err(YssError::new(
                ErrorCode::UnsupportedFormat,
                "不支持旧安装格式；请使用新的安装目录",
            )
            .at(marker));
        }
    }
    let old_tool = RelPath::new("installation.json")?;
    if root
        .inspect(&old_tool)?
        .descriptor()
        .is_some_and(|d| d.size <= MAX_MANIFEST)
    {
        let bytes = root.read_checked(&old_tool, MAX_MANIFEST)?.bytes;
        if let Ok(value) = codec::decode_json::<serde_json::Value>(&bytes, Limits::manifest())
            && matches!(
                value.get("schemaVersion").and_then(|v| v.as_u64()),
                Some(1 | 2)
            )
            && value.get("cliVersion").is_some()
            && value.get("archiveDigest").is_some()
        {
            return Err(YssError::new(
                ErrorCode::UnsupportedFormat,
                "旧工具安装不能接入为项目；请使用新目录",
            )
            .at(old_tool));
        }
    }
    if root.inspect(&RelPath::new(".yss")?)? != FileSnapshot::Missing {
        fs::validate_control(root)?;
    }
    if root.inspect(&RelPath::new(".yss/tool.json")?)? != FileSnapshot::Missing {
        return Err(YssError::new(
            ErrorCode::Conflict,
            "程序安装目录不能同时接入项目",
        ));
    }
    let path = RelPath::new(STATE_PATH)?;
    let before = root.inspect(&path)?;
    if before == FileSnapshot::Missing {
        return Ok((None, before));
    }
    let checked = root.read_checked(&path, MAX_MANIFEST)?;
    let state: ProjectState = codec::decode_json(&checked.bytes, Limits::manifest())?;
    codec::ensure_format(&state.format, state.format_version, "yss.project")?;
    let mut tree = crate::model::RenderedTree::default();
    for (path, file) in &state.managed {
        if path
            .as_str()
            .split('/')
            .next()
            .is_some_and(|s| s.eq_ignore_ascii_case(".yss") || s.eq_ignore_ascii_case(".git"))
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "项目所有权不能覆盖控制目录",
            ));
        }
        tree.insert(path.clone(), Vec::new(), file.last_installed.mode)?;
    }
    fs::validate_tree(&tree)?;
    Ok((Some(state), checked.snapshot))
}
