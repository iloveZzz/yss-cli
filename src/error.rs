use serde::Serialize;
use std::collections::BTreeMap;

pub type Result<T> = std::result::Result<T, YssError>;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ErrorCode {
    Argument,
    UnsupportedFormat,
    InvalidInput,
    SkillRetired,
    Conflict,
    UnsafePath,
    Locked,
    ConcurrentChange,
    RecoveryRequired,
    Cancelled,
    Network,
    NotReady,
    DigestMismatch,
    UnsupportedPlatform,
    VersionConflict,
    Io,
}

#[derive(Debug, Clone, Serialize, thiserror::Error)]
#[error("{message}")]
pub struct YssError {
    pub code: ErrorCode,
    pub message: String,
    pub path: Option<String>,
    pub context: BTreeMap<String, String>,
}

impl YssError {
    pub fn new(code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            path: None,
            context: BTreeMap::new(),
        }
    }
    pub fn at(mut self, path: impl ToString) -> Self {
        self.path = Some(path.to_string());
        self
    }
    pub fn context(mut self, key: &str, value: impl ToString) -> Self {
        self.context.insert(key.into(), value.to_string());
        self
    }
    pub fn exit_code(&self) -> i32 {
        if self.code == ErrorCode::Argument {
            2
        } else {
            1
        }
    }
}

impl From<std::io::Error> for YssError {
    fn from(value: std::io::Error) -> Self {
        let error =
            Self::new(ErrorCode::Io, "文件操作失败").context("kind", format!("{:?}", value.kind()));
        match value.raw_os_error() {
            Some(code) => error.context("osCode", code),
            None => error,
        }
    }
}
