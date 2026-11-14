use super::{
    migration,
    model::{MAX_STATE, WorkbenchState},
    sqlite::{Database, DatabaseIdentity},
    store,
};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root, RootLock},
    model::RelPath,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::path::Path;

pub(crate) const DATABASE_NAME: &str = "workbench.sqlite3";
const MARKER_LIMIT: u64 = 16 * 1024;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub(crate) enum MigrationPhase {
    Preparing,
    Active,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub(crate) enum MigrationSource {
    Empty,
    LegacyJson {
        version: u32,
        #[serde(rename = "backupName")]
        backup_name: String,
        sha256: String,
    },
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct StorageMarker {
    pub schema_version: u32,
    pub marker_version: u32,
    pub storage: String,
    pub phase: MigrationPhase,
    pub database_name: String,
    pub database_id: String,
    pub staged_name: String,
    pub source: MigrationSource,
    pub initial_revision: u64,
}
pub(crate) enum StorageState {
    Empty,
    Legacy,
    Preparing(StorageMarker),
    Active(StorageMarker),
}
fn recovery(message: &str) -> YssError {
    YssError::new(ErrorCode::RecoveryRequired, message)
}
fn unsupported(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsupportedFormat, message)
}
fn missing(error: &YssError) -> bool {
    error.context.get("kind").is_some_and(|k| k == "NotFound")
}
fn hex(s: &str, len: usize) -> bool {
    s.len() == len
        && s.bytes()
            .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
}
fn file_exists(root: &Root, name: &str) -> Result<bool> {
    match root.regular_file_identity(&RelPath::new(name)?) {
        Ok(_) => Ok(true),
        Err(e) if missing(&e) => Ok(false),
        Err(e) => Err(e),
    }
}
impl StorageMarker {
    pub(crate) fn identity(&self) -> DatabaseIdentity {
        DatabaseIdentity {
            id: self.database_id.clone(),
            initial_revision: self.initial_revision,
            source_sha256: match &self.source {
                MigrationSource::Empty => None,
                MigrationSource::LegacyJson { sha256, .. } => Some(sha256.clone()),
            },
        }
    }
    fn validate(&self) -> Result<()> {
        if self.schema_version != 0
            || self.marker_version != 1
            || self.storage != "sqlite"
            || self.database_name != DATABASE_NAME
            || !hex(&self.database_id, 32)
            || self.staged_name != format!("workbench-migrate-{}.sqlite3", self.database_id)
            || self.initial_revision > 9_007_199_254_740_991
        {
            return Err(unsupported("不支持的工作台存储标记；文件已保留"));
        }
        match &self.source {
            MigrationSource::Empty if self.initial_revision != 0 => {
                return Err(unsupported("空工作台迁移标记无效"));
            }
            MigrationSource::LegacyJson {
                version,
                backup_name,
                sha256,
            } if !(1..=3).contains(version)
                || !hex(sha256, 64)
                || backup_name != &format!("state.v{version}-backup-{sha256}.json") =>
            {
                return Err(unsupported("迁移备份标记无效"));
            }
            _ => {}
        }
        Ok(())
    }
}
pub(crate) fn probe(data_dir: &Path) -> Result<StorageState> {
    let path = store::resolve_data_dir(Some(data_dir))?;
    store::reject_git(&path)?;
    if !path.try_exists()? {
        return Ok(StorageState::Empty);
    }
    let canonical = std::fs::canonicalize(path)?;
    store::reject_git(&canonical)?;
    probe_root(&Root::open(&canonical)?)
}
fn probe_root(root: &Root) -> Result<StorageState> {
    let database_exists = file_exists(root, DATABASE_NAME)?;
    if !database_exists {
        for suffix in ["-journal", "-wal", "-shm"] {
            if file_exists(root, &format!("{DATABASE_NAME}{suffix}"))? {
                return Err(recovery("存在无主数据库日志；拒绝创建或发布新库"));
            }
        }
    }
    let file = match root.read_checked(&RelPath::new("state.json")?, MAX_STATE) {
        Ok(file) => file,
        Err(e) if missing(&e) && !database_exists => return Ok(StorageState::Empty),
        Err(e) if missing(&e) => {
            return Err(recovery("数据库存在但存储标记缺失；拒绝创建另一份数据"));
        }
        Err(e) => return Err(e),
    };
    let value: serde_json::Value = codec::decode_json(
        &file.bytes,
        Limits {
            bytes: MAX_STATE,
            ..Limits::manifest()
        },
    )?;
    if value.get("schemaVersion").and_then(|v| v.as_u64()) == Some(0) {
        if file.bytes.len() as u64 > MARKER_LIMIT {
            return Err(unsupported("存储标记超限"));
        }
        let marker: StorageMarker =
            serde_json::from_value(value).map_err(|_| unsupported("存储标记格式无效"))?;
        marker.validate()?;
        return match marker.phase {
            MigrationPhase::Preparing => Ok(StorageState::Preparing(marker)),
            MigrationPhase::Active if database_exists => Ok(StorageState::Active(marker)),
            MigrationPhase::Active => Err(recovery("数据库缺失；原备份已保留，请勿重新导入覆盖")),
        };
    }
    if database_exists {
        return Err(recovery("JSON 与 SQLite 存储冲突；拒绝自动选择或覆盖"));
    }
    let state = migration::decode_state(
        &file.bytes,
        Limits {
            bytes: MAX_STATE,
            ..Limits::manifest()
        },
    )?;
    store::validate_state(&state)?;
    Ok(StorageState::Legacy)
}
fn replace(root: &Root, name: &RelPath, expected: &FileSnapshot, bytes: &[u8]) -> Result<()> {
    let temp = RelPath::new(format!("sqlite-marker-{}.tmp", fs::unique_id()?))?;
    let written = root.create_file(&temp, bytes, 0o600)?;
    if let Err(error) = root.rename(&temp, name, expected, true) {
        let _ = root.remove(&temp, &written);
        return Err(error);
    }
    if root.read_checked(name, MARKER_LIMIT)?.bytes != bytes {
        return Err(recovery("存储标记发布后校验失败，请重启核实"));
    }
    Ok(())
}
fn backup(root: &Root, name: &RelPath, bytes: &[u8]) -> Result<()> {
    match root.read_checked(name, MAX_STATE) {
        Ok(file) if file.bytes == bytes => return Ok(()),
        Ok(_) => return Err(recovery("已有升级备份内容不同；未覆盖")),
        Err(e) if missing(&e) => {}
        Err(e) => return Err(e),
    }
    let temp = RelPath::new(format!("backup-{}.tmp", fs::unique_id()?))?;
    let written = root.create_file(&temp, bytes, 0o600)?;
    if let Err(error) = root.rename(&temp, name, &FileSnapshot::Missing, false) {
        let _ = root.remove(&temp, &written);
        return Err(error);
    }
    if root.read_checked(name, MAX_STATE)?.bytes != bytes {
        return Err(recovery("备份校验失败；原 JSON 未切换"));
    }
    Ok(())
}
fn original_state(root: &Root, marker: &StorageMarker) -> Result<WorkbenchState> {
    let state = match &marker.source {
        MigrationSource::Empty => WorkbenchState::default(),
        MigrationSource::LegacyJson {
            backup_name,
            sha256,
            ..
        } => {
            let file = root.read_checked(&RelPath::new(backup_name)?, MAX_STATE)?;
            if format!("{:x}", Sha256::digest(&file.bytes)) != *sha256 {
                return Err(recovery("迁移备份摘要不匹配；文件已保留"));
            }
            migration::decode_state(
                &file.bytes,
                Limits {
                    bytes: MAX_STATE,
                    ..Limits::manifest()
                },
            )?
        }
    };
    store::validate_state(&state)?;
    store::validate_state_limits(&state)?;
    if state.revision != marker.initial_revision {
        return Err(recovery("迁移初始 revision 不匹配"));
    }
    if state
        .projects
        .iter()
        .any(|p| root.path.starts_with(&p.root_path))
    {
        return Err(YssError::new(
            ErrorCode::UnsafePath,
            "数据目录不能位于登记项目内",
        ));
    }
    Ok(state)
}
fn resume(root: &Root, mut marker: StorageMarker) -> Result<StorageMarker> {
    marker.validate()?;
    let marker_path = RelPath::new("state.json")?;
    let before = root.read_checked(&marker_path, MARKER_LIMIT)?;
    let observed: StorageMarker = codec::decode_json(
        &before.bytes,
        Limits {
            bytes: MARKER_LIMIT,
            ..Limits::manifest()
        },
    )?;
    if codec::encode_json(&observed)? != codec::encode_json(&marker)? {
        return Err(recovery("迁移标记在操作期间改变"));
    }
    let staged = file_exists(root, &marker.staged_name)?;
    let published = file_exists(root, DATABASE_NAME)?;
    if staged == published {
        return Err(recovery("迁移库缺失或同时存在两份；请保留文件后核实"));
    }
    let original = original_state(root, &marker)?;
    let name = RelPath::new(if staged {
        &marker.staged_name
    } else {
        DATABASE_NAME
    })?;
    let mut database = Database::open_read_only(root, &name)?;
    if database.identity() != &marker.identity() || database.load_state(root)? != original {
        return Err(recovery("迁移库与原备份不一致；未发布"));
    }
    drop(database);
    if staged {
        root.rename(
            &name,
            &RelPath::new(DATABASE_NAME)?,
            &FileSnapshot::Missing,
            false,
        )?;
    }
    marker.phase = MigrationPhase::Active;
    replace(
        root,
        &marker_path,
        &before.snapshot,
        &codec::encode_json(&marker)?,
    )?;
    Ok(marker)
}
pub(crate) fn ensure_sqlite(lock: &RootLock) -> Result<StorageMarker> {
    let root = &lock.root;
    match probe_root(root)? {
        StorageState::Active(marker) => return Ok(marker),
        StorageState::Preparing(marker) => return resume(root, marker),
        StorageState::Empty | StorageState::Legacy => {}
    }
    let path = RelPath::new("state.json")?;
    let (state, source, expected) = match root.read_checked(&path, MAX_STATE) {
        Ok(file) => {
            let limits = Limits {
                bytes: MAX_STATE,
                ..Limits::manifest()
            };
            let state = migration::decode_state(&file.bytes, limits)?;
            store::validate_state(&state)?;
            store::validate_state_limits(&state)?;
            if state
                .projects
                .iter()
                .any(|p| root.path.starts_with(&p.root_path))
            {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "数据目录不能位于登记项目内",
                ));
            }
            let value: serde_json::Value = codec::decode_json(&file.bytes, limits)?;
            let version = value["schemaVersion"]
                .as_u64()
                .ok_or_else(|| unsupported("旧状态版本无效"))? as u32;
            let sha256 = format!("{:x}", Sha256::digest(&file.bytes));
            let backup_name = format!("state.v{version}-backup-{sha256}.json");
            backup(root, &RelPath::new(&backup_name)?, &file.bytes)?;
            (
                state,
                MigrationSource::LegacyJson {
                    version,
                    backup_name,
                    sha256,
                },
                file.snapshot,
            )
        }
        Err(e) if missing(&e) => (
            WorkbenchState::default(),
            MigrationSource::Empty,
            FileSnapshot::Missing,
        ),
        Err(e) => return Err(e),
    };
    let database_id = fs::unique_id()?;
    let marker = StorageMarker {
        schema_version: 0,
        marker_version: 1,
        storage: "sqlite".into(),
        phase: MigrationPhase::Preparing,
        database_name: DATABASE_NAME.into(),
        staged_name: format!("workbench-migrate-{database_id}.sqlite3"),
        database_id,
        source,
        initial_revision: state.revision,
    };
    Database::create_staged(
        root,
        &RelPath::new(&marker.staged_name)?,
        &marker.identity(),
        &state,
    )?;
    // Confirm publication can inspect the closed file before disabling the old JSON.
    root.inspect(&RelPath::new(&marker.staged_name)?)?;
    replace(root, &path, &expected, &codec::encode_json(&marker)?)?;
    resume(root, marker)
}
