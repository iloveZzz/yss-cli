use super::{model::*, store};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{FileIdentity, Root},
    model::RelPath,
};
use rusqlite::{
    Connection, OpenFlags, OptionalExtension, TransactionBehavior, params, params_from_iter,
    types::Value,
};
use serde::{Serialize, de::DeserializeOwned};
use std::{
    collections::{BTreeMap, BTreeSet},
    time::Duration,
};

const APPLICATION_ID: i64 = 0x59535357;
const SCHEMA: &str = "CREATE TABLE workbench_meta (id INTEGER PRIMARY KEY CHECK(id=1), database_id TEXT NOT NULL, source_sha256 TEXT, initial_revision INTEGER NOT NULL, model_version INTEGER NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE projects (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL);
CREATE TABLE specs (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL, project_id TEXT NOT NULL REFERENCES projects(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE tasks (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL, spec_id TEXT NOT NULL REFERENCES specs(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE stage_records (spec_id TEXT NOT NULL REFERENCES specs(id) DEFERRABLE INITIALLY DEFERRED, stage TEXT NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(spec_id,stage));
CREATE TABLE saved_views (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL);
CREATE TABLE question_responses (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL, spec_id TEXT NOT NULL REFERENCES specs(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE confirmations (id TEXT PRIMARY KEY NOT NULL, position INTEGER NOT NULL, payload TEXT NOT NULL, spec_id TEXT NOT NULL REFERENCES specs(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE history (position INTEGER PRIMARY KEY, payload TEXT NOT NULL)";

pub(crate) const EXTENSION_SCHEMA: &str = "CREATE TABLE workbench_records (kind TEXT NOT NULL, id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(kind,id));
CREATE TABLE artifact_blobs (sha256 TEXT PRIMARY KEY NOT NULL, bytes BLOB NOT NULL);
CREATE TABLE job_events (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL)";

#[derive(Clone)]
pub(crate) struct RecordWrite {
    pub kind: String,
    pub id: String,
    pub payload: Option<String>,
}

#[derive(Default)]
pub(crate) struct CommitEffects {
    pub records: Vec<RecordWrite>,
    pub blobs: Vec<(String, Vec<u8>)>,
}

pub(crate) struct EventWrite<'a> {
    pub job_id: &'a str,
    pub kind: &'a str,
    pub payload: &'a serde_json::Value,
}

pub(crate) enum ExtensionChange<'a> {
    Event(EventWrite<'a>),
    DeleteAgentHistory {
        run_id: &'a str,
        job_ids: &'a BTreeSet<String>,
    },
}

pub(crate) type ExtensionRecords = BTreeMap<String, Vec<(String, String)>>;

fn insert_event(
    connection: &Connection,
    job_id: &str,
    kind: &str,
    payload: &str,
    at: &str,
) -> Result<u64> {
    connection
        .execute(
            "INSERT INTO job_events(job_id,kind,payload,created_at) VALUES(?1,?2,?3,?4)",
            params![job_id, kind, payload, at],
        )
        .map_err(sql_error)?;
    Ok(connection.last_insert_rowid() as u64)
}

fn write_effects(connection: &Connection, effects: &CommitEffects) -> Result<()> {
    for record in &effects.records {
        if let Some(payload) = &record.payload {
            connection.execute("INSERT INTO workbench_records(kind,id,payload) VALUES(?1,?2,?3) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload", params![record.kind, record.id, payload]).map_err(sql_error)?;
        } else {
            connection
                .execute(
                    "DELETE FROM workbench_records WHERE kind=?1 AND id=?2",
                    params![record.kind, record.id],
                )
                .map_err(sql_error)?;
        }
    }
    for (digest, bytes) in &effects.blobs {
        let actual = crate::model::Sha256::of(bytes).as_str().to_owned();
        if &actual != digest {
            return Err(invalid("字节对象摘要不匹配"));
        }
        connection
            .execute(
                "INSERT OR IGNORE INTO artifact_blobs(sha256,bytes) VALUES(?1,?2)",
                params![digest, bytes],
            )
            .map_err(sql_error)?;
    }
    Ok(())
}

struct Table {
    name: &'static str,
    columns: &'static [&'static str],
    keys: usize,
    payload: usize,
}
const TABLES: &[Table] = &[
    Table {
        name: "projects",
        columns: &["id", "position", "payload"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "specs",
        columns: &["id", "position", "payload", "project_id"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "tasks",
        columns: &["id", "position", "payload", "spec_id"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "stage_records",
        columns: &["spec_id", "stage", "position", "payload"],
        keys: 2,
        payload: 3,
    },
    Table {
        name: "saved_views",
        columns: &["id", "position", "payload"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "question_responses",
        columns: &["id", "position", "payload", "spec_id"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "confirmations",
        columns: &["id", "position", "payload", "spec_id"],
        keys: 1,
        payload: 2,
    },
    Table {
        name: "history",
        columns: &["position", "payload"],
        keys: 1,
        payload: 1,
    },
];

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct DatabaseIdentity {
    pub id: String,
    pub source_sha256: Option<String>,
    pub initial_revision: u64,
}
pub(crate) struct Database {
    connection: Connection,
    name: RelPath,
    file_identity: FileIdentity,
    identity: DatabaseIdentity,
    data_version: i64,
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::UnsupportedFormat, message)
}
fn sql_error(error: rusqlite::Error) -> YssError {
    let code = match error.sqlite_error_code() {
        Some(rusqlite::ErrorCode::DatabaseBusy | rusqlite::ErrorCode::DatabaseLocked) => {
            ErrorCode::Locked
        }
        Some(rusqlite::ErrorCode::ReadOnly) => ErrorCode::RecoveryRequired,
        Some(rusqlite::ErrorCode::DatabaseCorrupt | rusqlite::ErrorCode::NotADatabase) => {
            ErrorCode::UnsupportedFormat
        }
        _ => ErrorCode::Io,
    };
    // SQL errors can include user payloads; expose only the numeric SQLite code.
    let mut result = YssError::new(
        code,
        match code {
            ErrorCode::Locked => "工作台数据库忙，请稍后重试",
            ErrorCode::RecoveryRequired => "数据库需要写入恢复；请启动新版工作台后再查询",
            ErrorCode::UnsupportedFormat => "工作台数据库损坏或格式无效；原文件已保留",
            _ => "工作台数据库操作失败；请保留数据并核实",
        },
    );
    if let Some(error) = error.sqlite_error() {
        result = result.context("sqliteCode", error.extended_code);
    }
    result
}
fn scalar(connection: &Connection, sql: &str) -> Result<i64> {
    connection
        .query_row(sql, [], |r| r.get(0))
        .map_err(sql_error)
}
fn check_files(root: &Root, name: &RelPath) -> Result<FileIdentity> {
    root.revalidate()?;
    let identity = root.regular_file_identity(name)?;
    for suffix in ["-journal", "-wal", "-shm"] {
        let sidecar = RelPath::new(format!("{}{suffix}", name.as_str()))?;
        match root.regular_file_identity(&sidecar) {
            Ok(_) if suffix != "-journal" => {
                return Err(invalid("不支持的数据库日志模式；文件已保留"));
            }
            Ok(_) => {}
            Err(e) if e.context.get("kind").is_some_and(|k| k == "NotFound") => {}
            Err(e) => return Err(e),
        }
    }
    Ok(identity)
}
fn open(root: &Root, name: &RelPath, writable: bool) -> Result<(Connection, FileIdentity)> {
    let identity = check_files(root, name)?;
    let flags = if writable {
        OpenFlags::SQLITE_OPEN_READ_WRITE
    } else {
        OpenFlags::SQLITE_OPEN_READ_ONLY
    } | OpenFlags::SQLITE_OPEN_NOFOLLOW
        | OpenFlags::SQLITE_OPEN_NO_MUTEX;
    let connection =
        Connection::open_with_flags(root.path.join(name.as_str()), flags).map_err(sql_error)?;
    connection
        .busy_timeout(Duration::from_secs(1))
        .map_err(sql_error)?;
    if check_files(root, name)? != identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "数据库文件在打开期间改变",
        ));
    }
    Ok((connection, identity))
}
fn configure(connection: &Connection) -> Result<()> {
    let mode: String = connection
        .query_row("PRAGMA journal_mode=DELETE", [], |r| r.get(0))
        .map_err(sql_error)?;
    connection
        .execute_batch(
            "PRAGMA synchronous=EXTRA; PRAGMA foreign_keys=ON; PRAGMA trusted_schema=OFF",
        )
        .map_err(sql_error)?;
    if mode != "delete"
        || scalar(connection, "PRAGMA synchronous")? != 3
        || scalar(connection, "PRAGMA foreign_keys")? != 1
    {
        return Err(invalid("SQLite 持久化设置不符合要求"));
    }
    Ok(())
}
fn check_schema(connection: &Connection) -> Result<()> {
    if scalar(connection, "PRAGMA application_id")? != APPLICATION_ID
        || ![1, 2].contains(&scalar(connection, "PRAGMA user_version")?)
    {
        return Err(invalid("不支持的工作台数据库版本；原文件已保留"));
    }
    let schema = if scalar(connection, "PRAGMA user_version")? == 2 {
        format!("{SCHEMA};{EXTENSION_SCHEMA}")
    } else {
        SCHEMA.to_owned()
    };
    let mut expected: Vec<String> = schema
        .split(';')
        .map(|s| s.trim().to_owned())
        .filter(|s| !s.is_empty())
        .collect();
    if scalar(connection, "PRAGMA user_version")? == 2 {
        expected.push("CREATE TABLE sqlite_sequence(name,seq)".into());
    }
    let mut statement = connection.prepare("SELECT CASE WHEN length(CAST(sql AS BLOB))<=4096 THEN sql END FROM sqlite_schema WHERE sql IS NOT NULL LIMIT 32").map_err(sql_error)?;
    let mut actual = statement
        .query_map([], |r| r.get::<_, String>(0))
        .map_err(sql_error)?
        .collect::<rusqlite::Result<Vec<_>>>()
        .map_err(sql_error)?;
    expected.sort();
    actual.sort();
    if actual != expected {
        return Err(invalid("工作台数据库结构已改变；拒绝执行未知结构"));
    }
    let mode: String = connection
        .query_row("PRAGMA journal_mode", [], |r| r.get(0))
        .map_err(sql_error)?;
    if mode != "delete" {
        return Err(invalid("不支持的数据库日志模式"));
    }
    Ok(())
}
fn metadata(connection: &Connection) -> Result<(DatabaseIdentity, u64)> {
    if scalar(connection, "SELECT COUNT(*) FROM workbench_meta")? != 1 {
        return Err(invalid("数据库元信息无效"));
    }
    let (id, source, initial, version, revision): (String, Option<String>, i64, u32, i64) = connection.query_row(
        "SELECT CASE WHEN length(database_id)=32 THEN database_id END, CASE WHEN source_sha256 IS NULL OR length(source_sha256)=64 THEN source_sha256 ELSE 0 END,initial_revision,model_version,revision FROM workbench_meta WHERE id=1", [],
        |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
    ).map_err(sql_error)?;
    if version != 3
        || revision < 0
        || initial < 0
        || revision > 9_007_199_254_740_991
        || initial > revision
    {
        return Err(invalid("数据库状态版本或 revision 无效"));
    }
    Ok((
        DatabaseIdentity {
            id,
            source_sha256: source,
            initial_revision: initial as u64,
        },
        revision as u64,
    ))
}

#[derive(PartialEq)]
struct Record {
    key: String,
    values: Vec<Value>,
}
fn record<T: Serialize>(
    value: &T,
    key: String,
    mut keys: Vec<Value>,
    position: Option<usize>,
    parent: Option<&str>,
) -> Result<Record> {
    if let Some(position) = position {
        keys.push(Value::Integer(position as i64));
    }
    keys.push(Value::Text(
        serde_json::to_string(value).map_err(|_| invalid("记录无法编码"))?,
    ));
    if let Some(parent) = parent {
        keys.push(Value::Text(parent.into()));
    }
    Ok(Record { key, values: keys })
}
fn records(state: &WorkbenchState) -> Result<Vec<Vec<Record>>> {
    macro_rules! rows {
        ($field:ident) => {
            state
                .$field
                .iter()
                .enumerate()
                .map(|(i, r)| {
                    record(
                        r,
                        r.id.clone(),
                        vec![Value::Text(r.id.clone())],
                        Some(i),
                        None,
                    )
                })
                .collect::<Result<Vec<_>>>()?
        };
        ($field:ident,$parent:ident) => {
            state
                .$field
                .iter()
                .enumerate()
                .map(|(i, r)| {
                    record(
                        r,
                        r.id.clone(),
                        vec![Value::Text(r.id.clone())],
                        Some(i),
                        Some(&r.$parent),
                    )
                })
                .collect::<Result<Vec<_>>>()?
        };
    }
    Ok(vec![
        rows!(projects),
        rows!(specs, project_id),
        rows!(tasks, spec_id),
        state
            .stage_records
            .iter()
            .enumerate()
            .map(|(i, r)| {
                let stage = serde_json::to_value(r.stage)
                    .map_err(|_| invalid("阶段无法编码"))?
                    .as_str()
                    .ok_or_else(|| invalid("阶段无效"))?
                    .to_owned();
                record(
                    r,
                    format!("{}/{}", r.spec_id, stage),
                    vec![Value::Text(r.spec_id.clone()), Value::Text(stage)],
                    Some(i),
                    None,
                )
            })
            .collect::<Result<Vec<_>>>()?,
        rows!(saved_views),
        rows!(question_responses, spec_id),
        rows!(confirmations, spec_id),
        state
            .history
            .iter()
            .enumerate()
            .map(|(i, r)| record(r, i.to_string(), vec![Value::Integer(i as i64)], None, None))
            .collect::<Result<Vec<_>>>()?,
    ])
}
fn apply_delta(
    connection: &Connection,
    before: &[Vec<Record>],
    next: &[Vec<Record>],
) -> Result<()> {
    for (table, (old_rows, new_rows)) in TABLES.iter().zip(before.iter().zip(next)) {
        let old: BTreeMap<_, _> = old_rows.iter().map(|r| (&r.key, r)).collect();
        let new: BTreeMap<_, _> = new_rows.iter().map(|r| (&r.key, r)).collect();
        let where_key = table.columns[..table.keys]
            .iter()
            .map(|c| format!("{c}=?"))
            .collect::<Vec<_>>()
            .join(" AND ");
        for (key, row) in &old {
            if !new.contains_key(key) {
                connection
                    .execute(
                        &format!("DELETE FROM {} WHERE {where_key}", table.name),
                        params_from_iter(&row.values[..table.keys]),
                    )
                    .map_err(sql_error)?;
            }
        }
        for row in new_rows {
            if let Some(previous) = old.get(&row.key) {
                let changed: Vec<_> = (table.keys..row.values.len())
                    .filter(|&i| row.values[i] != previous.values[i])
                    .collect();
                if changed.is_empty() {
                    continue;
                }
                let assignments = changed
                    .iter()
                    .map(|&i| format!("{}=?", table.columns[i]))
                    .collect::<Vec<_>>()
                    .join(",");
                let values = changed
                    .iter()
                    .map(|&i| &row.values[i])
                    .chain(row.values[..table.keys].iter());
                connection
                    .execute(
                        &format!("UPDATE {} SET {assignments} WHERE {where_key}", table.name),
                        params_from_iter(values),
                    )
                    .map_err(sql_error)?;
            } else {
                let placeholders = vec!["?"; table.columns.len()].join(",");
                connection
                    .execute(
                        &format!(
                            "INSERT INTO {} ({}) VALUES ({placeholders})",
                            table.name,
                            table.columns.join(",")
                        ),
                        params_from_iter(&row.values),
                    )
                    .map_err(sql_error)?;
            }
        }
    }
    Ok(())
}
fn load_table<T: DeserializeOwned>(
    connection: &Connection,
    table: &Table,
    budget: &mut usize,
) -> Result<(Vec<T>, Vec<Vec<Value>>)> {
    let columns = table
        .columns
        .iter()
        .map(|&c| {
            if c == "payload" {
                format!("CASE WHEN length(CAST(payload AS BLOB))<={MAX_STATE} THEN payload END")
            } else {
                format!("CASE WHEN length(CAST({c} AS BLOB))<=4096 THEN {c} END")
            }
        })
        .collect::<Vec<_>>()
        .join(",");
    let mut statement = connection
        .prepare(&format!(
            "SELECT {columns} FROM {} ORDER BY position",
            table.name
        ))
        .map_err(sql_error)?;
    let mut rows = statement.query([]).map_err(sql_error)?;
    let mut result = vec![];
    let mut stored = vec![];
    while let Some(row) = rows.next().map_err(sql_error)? {
        if result.len() >= 1_000_000 {
            return Err(invalid("数据库记录数量超限"));
        }
        let values = (0..table.columns.len())
            .map(|i| row.get::<_, Value>(i))
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(sql_error)?;
        let Value::Text(payload) = &values[table.payload] else {
            return Err(invalid("数据库记录超限或格式无效"));
        };
        *budget = budget
            .checked_add(payload.len())
            .ok_or_else(|| invalid("数据超限"))?;
        if *budget > MAX_STATE as usize {
            return Err(invalid("数据库逻辑状态超过 16 MiB 上限"));
        }
        result.push(
            codec::decode_json(
                payload.as_bytes(),
                Limits {
                    bytes: MAX_STATE,
                    ..Limits::manifest()
                },
            )
            .map_err(|_| invalid("数据库记录格式无效"))?,
        );
        stored.push(values);
    }
    Ok((result, stored))
}

impl Database {
    pub(crate) fn create_staged(
        root: &Root,
        name: &RelPath,
        identity: &DatabaseIdentity,
        state: &WorkbenchState,
    ) -> Result<()> {
        store::validate_state(state)?;
        store::validate_state_limits(state)?;
        root.create_file(name, &[], 0o600)?;
        let (mut connection, file_identity) = open(root, name, true)?;
        configure(&connection)?;
        let transaction = connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(sql_error)?;
        transaction.execute_batch(SCHEMA).map_err(sql_error)?;
        transaction
            .execute_batch(EXTENSION_SCHEMA)
            .map_err(sql_error)?;
        transaction
            .pragma_update(None, "application_id", APPLICATION_ID)
            .map_err(sql_error)?;
        transaction
            .pragma_update(None, "user_version", 2)
            .map_err(sql_error)?;
        let initial =
            i64::try_from(identity.initial_revision).map_err(|_| invalid("初始 revision 无效"))?;
        transaction
            .execute(
                "INSERT INTO workbench_meta VALUES(1,?1,?2,?3,3,?4)",
                params![
                    identity.id,
                    identity.source_sha256,
                    initial,
                    state.revision as i64
                ],
            )
            .map_err(sql_error)?;
        apply_delta(
            &transaction,
            &records(&WorkbenchState::default())?,
            &records(state)?,
        )?;
        transaction.commit().map_err(sql_error)?;
        let integrity: String = connection
            .query_row("PRAGMA integrity_check", [], |r| r.get(0))
            .map_err(sql_error)?;
        if integrity != "ok"
            || scalar(&connection, "SELECT COUNT(*) FROM pragma_foreign_key_check")? != 0
        {
            return Err(invalid("迁移数据库完整性校验失败"));
        }
        connection.close().map_err(|(_, e)| sql_error(e))?;
        if check_files(root, name)? != file_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "迁移数据库身份改变",
            ));
        }
        let mut db = Self::open_read_only(root, name)?;
        if db.identity() != identity || db.load_state(root)? != *state {
            return Err(invalid("迁移后的状态与原状态不一致"));
        }
        Ok(())
    }
    fn open_existing(root: &Root, name: &RelPath, writable: bool) -> Result<Self> {
        let (mut connection, file_identity) = open(root, name, writable)?;
        check_schema(&connection)?;
        if writable {
            configure(&connection)?;
        }
        if writable && scalar(&connection, "PRAGMA user_version")? == 1 {
            let (before_identity, before_revision) = metadata(&connection)?;
            let backup_name = RelPath::new(format!(
                "workbench.schema-v1-backup-{}-r{}.sqlite3",
                before_identity.id, before_revision
            ))?;
            if matches!(
                root.inspect(&backup_name)?,
                crate::fs::FileSnapshot::Missing
            ) {
                let staged = RelPath::new(format!(
                    "workbench-schema-backup-{}.tmp",
                    crate::fs::unique_id()?
                ))?;
                root.create_file(&staged, &[], 0o600)?;
                let staged_identity = root.regular_file_identity(&staged)?;
                let mut backup = Connection::open_with_flags(
                    root.path.join(staged.as_str()),
                    OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_NOFOLLOW,
                )
                .map_err(sql_error)?;
                rusqlite::backup::Backup::new(&connection, &mut backup)
                    .map_err(sql_error)?
                    .run_to_completion(64, Duration::from_millis(10), None)
                    .map_err(sql_error)?;
                check_schema(&backup)?;
                let integrity: String = backup
                    .query_row("PRAGMA integrity_check", [], |row| row.get(0))
                    .map_err(sql_error)?;
                if integrity != "ok"
                    || metadata(&backup)? != (before_identity.clone(), before_revision)
                {
                    return Err(invalid("升级备份与当前数据库不一致"));
                }
                backup.close().map_err(|(_, error)| sql_error(error))?;
                root.publish_streamed(&staged, &backup_name, &staged_identity)?;
            } else {
                let backup = Connection::open_with_flags(
                    root.path.join(backup_name.as_str()),
                    OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NOFOLLOW,
                )
                .map_err(sql_error)?;
                check_schema(&backup)?;
                if metadata(&backup)? != (before_identity, before_revision) {
                    return Err(invalid("已有升级备份与当前数据库不匹配"));
                }
            }
            let transaction = connection
                .transaction_with_behavior(TransactionBehavior::Immediate)
                .map_err(sql_error)?;
            transaction
                .execute_batch(EXTENSION_SCHEMA)
                .map_err(sql_error)?;
            transaction
                .pragma_update(None, "user_version", 2)
                .map_err(sql_error)?;
            transaction.commit().map_err(sql_error)?;
            check_schema(&connection)?;
        }
        if writable {
            configure(&connection)?;
        }
        let (identity, _) = metadata(&connection)?;
        let data_version = scalar(&connection, "PRAGMA data_version")?;
        Ok(Self {
            connection,
            name: name.clone(),
            file_identity,
            identity,
            data_version,
        })
    }
    pub(crate) fn open_read_only(root: &Root, name: &RelPath) -> Result<Self> {
        Self::open_existing(root, name, false)
    }
    pub(crate) fn open_read_write(root: &Root, name: &RelPath) -> Result<Self> {
        Self::open_existing(root, name, true)
    }
    pub(crate) fn identity(&self) -> &DatabaseIdentity {
        &self.identity
    }
    fn revalidate(&self, root: &Root) -> Result<()> {
        if check_files(root, &self.name)? != self.file_identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "数据库文件被外部替换，请重启核实",
            ));
        }
        Ok(())
    }
    pub(crate) fn load_state(&mut self, root: &Root) -> Result<WorkbenchState> {
        Ok(self.load_state_with_records(root, &[])?.0)
    }
    pub(crate) fn load_state_with_records(
        &mut self,
        root: &Root,
        kinds: &[&str],
    ) -> Result<(WorkbenchState, ExtensionRecords)> {
        self.revalidate(root)?;
        let transaction = self.connection.transaction().map_err(sql_error)?;
        check_schema(&transaction)?;
        let integrity: String = transaction
            .query_row("PRAGMA integrity_check(1)", [], |r| r.get(0))
            .map_err(sql_error)?;
        if integrity != "ok" {
            return Err(invalid("工作台数据库完整性校验失败；原文件已保留"));
        }
        let (identity, revision) = metadata(&transaction)?;
        if identity != self.identity {
            return Err(invalid("数据库身份与存储标记不匹配"));
        }
        let mut state = WorkbenchState {
            revision,
            ..Default::default()
        };
        let mut budget = 0;
        let mut stored = Vec::new();
        macro_rules! load {
            ($field:ident,$i:expr) => {
                let (values, raw) = load_table(&transaction, &TABLES[$i], &mut budget)?;
                state.$field = values;
                stored.push(raw);
            };
        }
        load!(projects, 0);
        load!(specs, 1);
        load!(tasks, 2);
        load!(stage_records, 3);
        load!(saved_views, 4);
        load!(question_responses, 5);
        load!(confirmations, 6);
        load!(history, 7);
        store::validate_state(&state)?;
        store::validate_state_limits(&state)?;
        for (expected, actual) in records(&state)?.iter().zip(stored) {
            if expected.iter().map(|r| &r.values).ne(actual.iter()) {
                return Err(invalid("数据库键、顺序与记录内容不一致"));
            }
        }
        let mut extensions = BTreeMap::new();
        if scalar(&transaction, "PRAGMA user_version")? >= 2 {
            for kind in kinds {
                let mut statement = transaction
                    .prepare("SELECT id,payload FROM workbench_records WHERE kind=?1 ORDER BY id")
                    .map_err(sql_error)?;
                let rows = statement
                    .query_map([kind], |row| Ok((row.get(0)?, row.get(1)?)))
                    .map_err(sql_error)?
                    .collect::<rusqlite::Result<Vec<(String, String)>>>()
                    .map_err(sql_error)?;
                extensions.insert((*kind).to_owned(), rows);
            }
        }
        let data_version = scalar(&transaction, "PRAGMA data_version")?;
        transaction.commit().map_err(sql_error)?;
        self.revalidate(root)?;
        self.data_version = data_version;
        Ok((state, extensions))
    }
    pub(crate) fn commit_delta(
        &mut self,
        root: &Root,
        expected_revision: u64,
        before: &WorkbenchState,
        next: &WorkbenchState,
        effects: &CommitEffects,
    ) -> Result<()> {
        let result = self.commit_transaction(root, expected_revision, before, next, effects);
        if result.is_err() && !self.connection.is_autocommit() {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "事务回滚结果待核实，请重启工作台",
            ));
        }
        result
    }
    fn commit_transaction(
        &mut self,
        root: &Root,
        expected_revision: u64,
        before: &WorkbenchState,
        next: &WorkbenchState,
        effects: &CommitEffects,
    ) -> Result<()> {
        self.revalidate(root)?;
        store::validate_state(next)?;
        store::validate_state_limits(next)?;
        if before.revision != expected_revision
            || expected_revision.checked_add(1) != Some(next.revision)
        {
            return Err(invalid("提交 revision 无效"));
        }
        let old = records(before)?;
        let new = records(next)?;
        let transaction = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(sql_error)?;
        let (identity, revision) = metadata(&transaction)?;
        if identity != self.identity
            || revision != expected_revision
            || scalar(&transaction, "PRAGMA data_version")? != self.data_version
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "数据库已被其他连接修改，请重启核实",
            )
            .context("revision", revision));
        }
        apply_delta(&transaction, &old, &new)?;
        write_effects(&transaction, effects)?;
        let changed = transaction
            .execute(
                "UPDATE workbench_meta SET revision=?1 WHERE id=1 AND revision=?2",
                params![next.revision as i64, expected_revision as i64],
            )
            .map_err(sql_error)?;
        if changed != 1 {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "revision 已改变",
            ));
        }
        if let Err(error) = transaction.commit() {
            let error = sql_error(error);
            if error.code == ErrorCode::Locked && self.connection.is_autocommit() {
                return Err(error);
            }
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "数据库提交结果待核实；请重启工作台，勿重复提交",
            ));
        }
        if self.revalidate(root).is_err()
            || !metadata(&self.connection)
                .is_ok_and(|(id, rev)| id == self.identity && rev == next.revision)
        {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "提交后校验失败，请重启核实，勿重复提交",
            ));
        }
        Ok(())
    }
    pub(crate) fn read_record(&self, kind: &str, id: &str) -> Result<Option<String>> {
        if scalar(&self.connection, "PRAGMA user_version")? < 2 {
            return Ok(None);
        }
        self.connection
            .query_row(
                "SELECT payload FROM workbench_records WHERE kind=?1 AND id=?2",
                params![kind, id],
                |row| row.get(0),
            )
            .optional()
            .map_err(sql_error)
    }
    pub(crate) fn has_record(&self, kind: &str, id: &str) -> Result<bool> {
        if scalar(&self.connection, "PRAGMA user_version")? < 2 {
            return Ok(false);
        }
        self.connection
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM workbench_records WHERE kind=?1 AND id=?2)",
                params![kind, id],
                |row| row.get(0),
            )
            .map_err(sql_error)
    }
    pub(crate) fn read_records(&self, kind: &str) -> Result<Vec<(String, String)>> {
        if scalar(&self.connection, "PRAGMA user_version")? < 2 {
            return Ok(Vec::new());
        }
        let mut statement = self
            .connection
            .prepare("SELECT id,payload FROM workbench_records WHERE kind=?1 ORDER BY id")
            .map_err(sql_error)?;
        statement
            .query_map([kind], |row| Ok((row.get(0)?, row.get(1)?)))
            .map_err(sql_error)?
            .collect::<rusqlite::Result<_>>()
            .map_err(sql_error)
    }
    pub(crate) fn write_records(
        &mut self,
        root: &Root,
        effects: &CommitEffects,
        change: Option<ExtensionChange<'_>>,
    ) -> Result<()> {
        self.revalidate(root)?;
        let transaction = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(sql_error)?;
        if scalar(&transaction, "PRAGMA data_version")? != self.data_version {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "数据库已被其他连接修改，请重启核实",
            ));
        }
        write_effects(&transaction, effects)?;
        match change {
            Some(ExtensionChange::Event(event)) => {
                insert_event(
                    &transaction,
                    event.job_id,
                    event.kind,
                    &serde_json::to_string(event.payload)
                        .map_err(|_| invalid("作业事件编码失败"))?,
                    &super::sources::now(),
                )?;
            }
            Some(ExtensionChange::DeleteAgentHistory { run_id, job_ids }) => {
                for id in job_ids {
                    transaction
                        .execute("DELETE FROM job_events WHERE job_id=?1", [id])
                        .map_err(sql_error)?;
                }
                let prefix = format!("{run_id}/");
                transaction.execute(
                    "DELETE FROM workbench_records WHERE kind=?1 AND substr(id,1,length(?2))=?2",
                    params![super::agent::REVIEW_RECEIPT, prefix],
                ).map_err(sql_error)?;
            }
            None => {}
        }
        if let Err(error) = transaction.commit() {
            let error = sql_error(error);
            if error.code == ErrorCode::Locked && self.connection.is_autocommit() {
                return Err(error);
            }
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "扩展数据提交结果待核实，请重启后查询",
            ));
        }
        self.revalidate(root).map_err(|_| {
            YssError::new(
                ErrorCode::RecoveryRequired,
                "扩展数据提交后文件身份改变，请重启核实",
            )
        })
    }
    pub(crate) fn blob(&self, digest: &str) -> Result<Vec<u8>> {
        let bytes: Vec<u8> = self
            .connection
            .query_row(
                "SELECT bytes FROM artifact_blobs WHERE sha256=?1",
                [digest],
                |row| row.get(0),
            )
            .map_err(sql_error)?;
        if crate::model::Sha256::of(&bytes).as_str() != digest {
            return Err(invalid("已保存字节对象摘要不一致，请保留数据库并核实"));
        }
        Ok(bytes)
    }
    pub(crate) fn append_event(
        &mut self,
        root: &Root,
        job_id: &str,
        kind: &str,
        payload: &str,
        at: &str,
    ) -> Result<u64> {
        self.revalidate(root)?;
        let transaction = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(sql_error)?;
        if scalar(&transaction, "PRAGMA data_version")? != self.data_version {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "数据库已被其他连接修改，请重启核实",
            ));
        }
        let id = insert_event(&transaction, job_id, kind, payload, at)?;
        if let Err(error) = transaction.commit() {
            let error = sql_error(error);
            if error.code == ErrorCode::Locked && self.connection.is_autocommit() {
                return Err(error);
            }
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "作业事件提交结果待核实，请重启后查询",
            ));
        }
        self.revalidate(root).map_err(|_| {
            YssError::new(
                ErrorCode::RecoveryRequired,
                "事件提交后数据库身份改变，请重启核实",
            )
        })?;
        Ok(id)
    }
    pub(crate) fn events_through(
        &self,
        job_id: &str,
        after: u64,
        through: u64,
    ) -> Result<Vec<super::jobs::JobEvent>> {
        let mut statement=self.connection.prepare("SELECT id,kind,payload,created_at FROM job_events WHERE job_id=?1 AND id>?2 AND id<=?3 ORDER BY id LIMIT 200").map_err(sql_error)?;
        let rows = statement
            .query_map(params![job_id, after as i64, through as i64], |row| {
                Ok((
                    row.get::<_, i64>(0)? as u64,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, String>(3)?,
                ))
            })
            .map_err(sql_error)?;
        rows.map(|row| {
            let (id, kind, payload, created_at) = row.map_err(sql_error)?;
            Ok(super::jobs::JobEvent {
                id,
                job_id: job_id.into(),
                kind,
                payload: serde_json::from_str(&payload).map_err(|_| invalid("作业事件无效"))?,
                created_at,
            })
        })
        .collect()
    }
    pub(crate) fn last_event_id(&self, job_id: &str) -> Result<u64> {
        self.connection
            .query_row(
                "SELECT COALESCE(MAX(id),0) FROM job_events WHERE job_id=?1",
                [job_id],
                |row| row.get::<_, i64>(0),
            )
            .map(|id| id as u64)
            .map_err(sql_error)
    }

    pub(crate) fn run_event(&self, job_id: &str) -> Result<Option<serde_json::Value>> {
        let payload: Option<String> = self
            .connection
            .query_row(
                "SELECT payload FROM job_events WHERE job_id=?1 AND kind='run' ORDER BY id LIMIT 1",
                [job_id],
                |row| row.get(0),
            )
            .optional()
            .map_err(sql_error)?;
        payload
            .map(|value| serde_json::from_str(&value).map_err(|_| invalid("执行轮次记录无效")))
            .transpose()
    }
    pub(crate) fn has_events_after(&self, job_id: &str, after: u64) -> Result<bool> {
        self.connection
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM job_events WHERE job_id=?1 AND id>?2)",
                params![job_id, after as i64],
                |row| row.get(0),
            )
            .map_err(sql_error)
    }
    pub(crate) fn storage_usage(&self) -> Result<serde_json::Value> {
        let blobs: i64 = self
            .connection
            .query_row(
                "SELECT COALESCE(SUM(length(bytes)),0) FROM artifact_blobs",
                [],
                |r| r.get(0),
            )
            .map_err(sql_error)?;
        let records: i64 = self
            .connection
            .query_row(
                "SELECT COALESCE(SUM(length(payload)),0) FROM workbench_records",
                [],
                |r| r.get(0),
            )
            .map_err(sql_error)?;
        let events: i64 = self
            .connection
            .query_row(
                "SELECT COALESCE(SUM(length(payload)),0) FROM job_events",
                [],
                |r| r.get(0),
            )
            .map_err(sql_error)?;
        Ok(
            serde_json::json!({"schemaVersion":2,"blobBytes":blobs,"recordBytes":records,"eventBytes":events}),
        )
    }
}
