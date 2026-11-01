use crate::error::{ErrorCode, Result, YssError};
use serde::{Deserialize, Serialize};
use sha2::Digest;
use std::{
    collections::{BTreeMap, BTreeSet},
    fmt,
};

pub const MAX_ASSET: u64 = 256 * 1024 * 1024;
pub const MAX_MANIFEST: u64 = 8 * 1024 * 1024;

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, clap::ValueEnum,
)]
#[serde(rename_all = "lowercase")]
pub enum Profile {
    Spec,
    Backend,
}
#[derive(
    Debug,
    Clone,
    Copy,
    Default,
    PartialEq,
    Eq,
    PartialOrd,
    Ord,
    Serialize,
    Deserialize,
    clap::ValueEnum,
)]
#[serde(rename_all = "lowercase")]
pub enum InstallationKind {
    #[default]
    Project,
    Skills,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Ownership {
    Managed,
    ManagedCustomizable,
    Generated,
    UserOwned,
    Protected,
}
impl Ownership {
    pub fn is_managed(self) -> bool {
        !matches!(self, Self::UserOwned | Self::Protected)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct RelPath(String);
impl RelPath {
    pub fn new(value: impl Into<String>) -> Result<Self> {
        let value = value.into();
        if value.is_empty()
            || value.len() > 4096
            || value.contains('\\')
            || value.contains(':')
            || value
                .chars()
                .any(|c| c.is_control() || matches!(c, '<' | '>' | '"' | '|' | '?' | '*'))
        {
            return Err(YssError::new(ErrorCode::UnsafePath, "非法相对路径"));
        }
        for component in value.split('/') {
            let stem = component
                .split('.')
                .next()
                .unwrap_or("")
                .to_ascii_uppercase();
            if component.is_empty()
                || component == "."
                || component == ".."
                || component.ends_with(['.', ' '])
                || matches!(
                    stem.as_str(),
                    "CON" | "PRN" | "AUX" | "NUL" | "CONIN$" | "CONOUT$"
                )
                || ["COM", "LPT"].iter().any(|prefix| {
                    stem.strip_prefix(prefix).is_some_and(|n| {
                        matches!(
                            n,
                            "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "¹" | "²" | "³"
                        )
                    })
                })
            {
                return Err(YssError::new(ErrorCode::UnsafePath, "非法相对路径"));
            }
        }
        Ok(Self(value))
    }
    pub fn as_str(&self) -> &str {
        &self.0
    }
    pub fn join(&self, child: &str) -> Result<Self> {
        Self::new(format!("{}/{child}", self.0))
    }
    pub fn parents(&self) -> impl Iterator<Item = &str> {
        self.0.match_indices('/').map(|(i, _)| &self.0[..i])
    }
}
impl fmt::Display for RelPath {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.0.fmt(f)
    }
}
impl TryFrom<String> for RelPath {
    type Error = YssError;
    fn try_from(v: String) -> Result<Self> {
        Self::new(v)
    }
}
impl From<RelPath> for String {
    fn from(v: RelPath) -> Self {
        v.0
    }
}

#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(try_from = "String", into = "String")]
pub struct Sha256(String);
impl Sha256 {
    pub fn of(bytes: &[u8]) -> Self {
        Self(format!("{:x}", sha2::Sha256::digest(bytes)))
    }
    pub fn as_str(&self) -> &str {
        &self.0
    }
}
impl fmt::Display for Sha256 {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.0.fmt(f)
    }
}
impl TryFrom<String> for Sha256 {
    type Error = YssError;
    fn try_from(v: String) -> Result<Self> {
        if v.len() != 64
            || !v
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "SHA-256 必须为 64 位小写十六进制",
            ));
        }
        Ok(Self(v))
    }
}
impl From<Sha256> for String {
    fn from(v: Sha256) -> Self {
        v.0
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct FileDescriptor {
    pub sha256: Sha256,
    pub size: u64,
    pub mode: u32,
}
impl FileDescriptor {
    pub fn new(bytes: &[u8], mode: u32) -> Self {
        Self {
            sha256: Sha256::of(bytes),
            size: bytes.len() as u64,
            mode,
        }
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SourceProvenance {
    pub commit: String,
    pub state: String,
    pub snapshot: Sha256,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct BuildInfo {
    pub version: String,
    pub target: String,
    pub source: SourceProvenance,
    pub bundle_digest: Option<Sha256>,
}
impl BuildInfo {
    pub fn current() -> Self {
        Self {
            version: env!("CARGO_PKG_VERSION").into(),
            target: env!("YSS_TARGET").into(),
            source: SourceProvenance {
                commit: env!("YSS_COMMIT").into(),
                state: env!("YSS_SOURCE_STATE").into(),
                snapshot: env!("YSS_SOURCE_DIGEST")
                    .to_owned()
                    .try_into()
                    .expect("build digest"),
            },
            bundle_digest: env!("YSS_BUNDLE_DIGEST").to_owned().try_into().ok(),
        }
    }
}
#[derive(Debug, Clone)]
pub struct RenderedFile {
    pub bytes: Vec<u8>,
    pub mode: u32,
}
#[derive(Debug, Clone, Default)]
pub struct RenderedTree {
    pub files: BTreeMap<RelPath, RenderedFile>,
    pub directories: BTreeSet<RelPath>,
}
impl RenderedTree {
    pub fn insert(&mut self, path: RelPath, bytes: Vec<u8>, mode: u32) -> Result<()> {
        if self.files.contains_key(&path) || self.directories.contains(&path) {
            return Err(YssError::new(ErrorCode::InvalidInput, "重复文件路径").at(path));
        }
        self.files.insert(path, RenderedFile { bytes, mode });
        Ok(())
    }
}
