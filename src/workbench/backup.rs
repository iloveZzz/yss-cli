use super::{
    jobs::{self, JobManager, JobRecord},
    sources,
    storage_migration::DATABASE_NAME,
    store::{self, Store},
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::RelPath,
};
use flate2::{Compression, read::GzDecoder, write::GzEncoder};
use rusqlite::{
    Connection, OpenFlags, OptionalExtension,
    backup::{Backup, StepResult},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    io::{Read, Write},
    path::Path,
    sync::Arc,
    time::Duration,
};

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct BackupManifest {
    schema_version: u32,
    created_at: String,
    revision: u64,
    files: BTreeMap<String, BackupFile>,
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct BackupFile {
    sha256: String,
    size: u64,
}

fn hash_file(mut file: impl Read, cancel: &Cancellation) -> Result<BackupFile> {
    let mut digest = Sha256::new();
    let mut size = 0;
    let mut buffer = [0; 65536];
    loop {
        cancel.check()?;
        let count = file.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        digest.update(&buffer[..count]);
        size += count as u64;
    }
    Ok(BackupFile {
        sha256: format!("{:x}", digest.finalize()),
        size,
    })
}
fn private_temp(parent: &Root, prefix: &str) -> Result<(RelPath, Root)> {
    let name = RelPath::new(format!("{prefix}-{}", fs::unique_id()?))?;
    parent.create_dir(&name)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(
            parent.path.join(name.as_str()),
            std::fs::Permissions::from_mode(0o700),
        )?;
    }
    let root = Root::open(&parent.path.join(name.as_str()))?;
    Ok((name, root))
}

fn reject_unbacked_recovery(store: &mut Store) -> Result<()> {
    if !store.records::<Value>("piCommandRecovery")?.is_empty() {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "存在尚未核对的命令暂存，备份不包含这些文件；请先处理恢复记录",
        ));
    }
    Ok(())
}
fn file_location(path: &str) -> Result<(Root, RelPath)> {
    if !Path::new(path).is_absolute() {
        return Err(sources::invalid("请使用绝对路径"));
    }
    let path = fs::absolute(Path::new(path))?;
    if !Path::new(path.as_path()).is_absolute() {
        return Err(sources::invalid("请使用绝对路径"));
    }
    let parent = path
        .parent()
        .ok_or_else(|| sources::invalid("文件父目录不存在"))?;
    let name = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or_else(|| sources::invalid("文件名无效"))?;
    Ok((Root::open(parent)?, RelPath::new(name)?))
}

fn database_backup(data: &Path, destination: &Root, cancel: &Cancellation) -> Result<u64> {
    let source_root = Root::open(data)?;
    let database_name = RelPath::new(DATABASE_NAME)?;
    let identity = source_root.regular_file_identity(&database_name)?;
    let source = Connection::open_with_flags(
        data.join(DATABASE_NAME),
        OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NOFOLLOW,
    )
    .map_err(|_| sources::invalid("无法读取备份源数据库"))?;
    destination.create_file(&database_name, &[], 0o600)?;
    let mut target = Connection::open_with_flags(
        destination.path.join(DATABASE_NAME),
        OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_NOFOLLOW,
    )
    .map_err(|_| sources::invalid("无法创建备份数据库"))?;
    {
        let backup =
            Backup::new(&source, &mut target).map_err(|_| sources::invalid("无法开始一致备份"))?;
        loop {
            cancel.check()?;
            match backup
                .step(64)
                .map_err(|_| sources::invalid("数据库备份失败"))?
            {
                StepResult::Done => break,
                StepResult::More => {}
                StepResult::Busy | StepResult::Locked => {
                    std::thread::sleep(Duration::from_millis(20))
                }
                _ => return Err(sources::invalid("数据库备份返回未知状态")),
            }
        }
    }
    let integrity: String = target
        .query_row("PRAGMA integrity_check", [], |row| row.get(0))
        .map_err(|_| sources::invalid("备份完整性检查失败"))?;
    let foreign_keys: u64 = target
        .query_row("SELECT COUNT(*) FROM pragma_foreign_key_check", [], |row| {
            row.get::<_, i64>(0)
        })
        .map_err(|_| sources::invalid("备份引用检查失败"))? as u64;
    if integrity != "ok" || foreign_keys != 0 {
        return Err(sources::invalid("备份数据库完整性检查失败"));
    }
    // The archive does not contain command scratch. Check the actual SQLite
    // snapshot, not only a preflight, so a concurrent registration cannot be
    // silently omitted from a published backup.
    let pending: bool = target
        .query_row(
            "SELECT EXISTS(SELECT 1 FROM workbench_records WHERE kind = 'piCommandRecovery')",
            [],
            |row| row.get(0),
        )
        .map_err(|_| sources::invalid("无法核对备份中的命令恢复记录"))?;
    if pending {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "存在尚未核对的命令暂存，备份不包含这些文件；请先处理恢复记录",
        ));
    }
    let lease: Option<String> = target
        .query_row(
            "SELECT id FROM workbench_records WHERE kind = ?1 LIMIT 1",
            [jobs::GROK_CLEANUP_LEASE],
            |row| row.get(0),
        )
        .optional()
        .map_err(|_| sources::invalid("无法核对备份中的 Grok Build 进程收尾记录"))?;
    if let Some(job_id) = lease {
        return Err(YssError::new(
            ErrorCode::RecoveryRequired,
            "Grok Build 进程收尾待核实，请查看原执行记录后重试备份",
        )
        .context("jobId", job_id));
    }
    let revision = target
        .query_row(
            "SELECT revision FROM workbench_meta WHERE id=1",
            [],
            |row| row.get::<_, i64>(0),
        )
        .map_err(|_| sources::invalid("备份版本读取失败"))? as u64;
    target
        .close()
        .map_err(|_| sources::invalid("备份数据库关闭失败"))?;
    if source_root.regular_file_identity(&database_name)? != identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "备份期间数据库身份改变",
        ));
    }
    Ok(revision)
}

pub fn create(
    manager: Arc<JobManager>,
    target_path: String,
    request_id: String,
) -> Result<JobRecord> {
    if let Some(job) = manager
        .list()?
        .into_iter()
        .find(|job| job.request_id == request_id)
    {
        if job.kind != "backup" || job.roots != vec![target_path.clone()] {
            return Err(YssError::new(
                ErrorCode::Conflict,
                "请求 ID 已用于另一项操作",
            ));
        }
        return Ok(job);
    }
    let data = jobs::lock_store(&manager.store)?.data_dir().to_owned();
    let (parent, name) = file_location(&target_path)?;
    if parent.inspect(&name)? != crate::fs::FileSnapshot::Missing {
        return Err(sources::invalid("备份文件已存在，请选择新文件名"));
    }
    manager.start(
        "backup".into(),
        "导出个人工作台备份".into(),
        request_id,
        vec![target_path],
        move |context| {
            context.emit("progress", json!({"message":"正在冻结未接收成果"}))?;
            super::agent::freeze_all_shared(&context.store, &context.cancel)?;
            context.emit("progress", json!({"message":"正在生成一致数据库备份"}))?;
            // Keep workbench recovery records stable until the SQLite copy is complete.
            let mut recovery_lock = jobs::lock_store(&context.store)?;
            #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
            super::command_recovery::ensure_none(&mut recovery_lock)?;
            #[cfg(not(all(target_os = "windows", target_arch = "x86_64")))]
            reject_unbacked_recovery(&mut recovery_lock)?;
            jobs::ensure_grok_backup_available(&mut recovery_lock)?;
            let (temp_name, temp) = private_temp(&parent, ".yss-backup")?;
            let revision = database_backup(&data, &temp, &context.cancel)?;
            drop(recovery_lock);
            let source = Root::open(&data)?;
            let mut names = vec![DATABASE_NAME.to_owned(), "state.json".into()];
            for item in source.list(None)? {
                if (item.starts_with("state.v")
                    && item.ends_with(".json")
                    && item.contains("-backup-"))
                    || (item.starts_with("workbench.schema-v1-backup-")
                        && item.ends_with(".sqlite3"))
                {
                    names.push(item);
                }
            }
            let mut manifest = BackupManifest {
                schema_version: 1,
                created_at: sources::now(),
                revision,
                files: BTreeMap::new(),
            };
            for name in &names {
                let root = if name == DATABASE_NAME {
                    &temp
                } else {
                    &source
                };
                let info = hash_file(root.open_streamed(&RelPath::new(name)?)?, &context.cancel)?;
                manifest.files.insert(name.clone(), info);
            }
            let archive_name = RelPath::new(format!(".yss-backup-{}.partial", context.id))?;
            let identity = parent.create_streamed(&archive_name, |output| {
                let encoder = GzEncoder::new(output, Compression::default());
                let mut archive = tar::Builder::new(encoder);
                let bytes = serde_json::to_vec(&manifest)
                    .map_err(|_| sources::invalid("备份清单编码失败"))?;
                let mut header = tar::Header::new_gnu();
                header.set_size(bytes.len() as u64);
                header.set_mode(0o600);
                header.set_cksum();
                archive.append_data(&mut header, "manifest.json", bytes.as_slice())?;
                for name in &names {
                    context.cancel.check()?;
                    let root = if name == DATABASE_NAME {
                        &temp
                    } else {
                        &source
                    };
                    archive.append_file(name, &mut root.open_streamed(&RelPath::new(name)?)?)?;
                }
                archive.into_inner()?.finish()?;
                Ok(())
            })?;
            // Read the archive back before publishing it as a complete backup.
            verify_archive(parent.open_streamed(&archive_name)?, None, &context.cancel)?;
            parent.publish_streamed(&archive_name, &name, &identity)?;
            temp.revalidate()?;
            std::fs::remove_dir_all(parent.path.join(temp_name.as_str()))?;
            Ok(json!({
                "path":parent.path.join(name.as_str()),
                "revision":revision,
                "createdAt":manifest.created_at
            }))
        },
    )
}

fn verify_archive(
    file: std::fs::File,
    target: Option<&Root>,
    cancel: &Cancellation,
) -> Result<BackupManifest> {
    let mut archive = tar::Archive::new(GzDecoder::new(file));
    let mut manifest = None;
    let mut observed = BTreeMap::new();
    for (index, entry) in archive.entries()?.enumerate() {
        cancel.check()?;
        if index >= 64 {
            return Err(sources::invalid("备份条目数量超过支持范围"));
        }
        let mut entry = entry?;
        if !entry.header().entry_type().is_file() {
            return Err(sources::invalid("备份包含非普通文件"));
        }
        let name = entry
            .path()?
            .to_str()
            .ok_or_else(|| sources::invalid("备份路径不是UTF-8"))?
            .to_owned();
        let relative = RelPath::new(&name)?;
        if name == "manifest.json" {
            if manifest.is_some() || entry.size() > 1024 * 1024 {
                return Err(sources::invalid("备份清单无效或重复"));
            }
            let mut bytes = Vec::new();
            entry.read_to_end(&mut bytes)?;
            let parsed: BackupManifest =
                serde_json::from_slice(&bytes).map_err(|_| sources::invalid("备份清单格式无效"))?;
            if parsed.schema_version != 1 {
                return Err(sources::invalid("不支持的备份版本"));
            }
            manifest = Some(parsed);
            continue;
        }
        if name != DATABASE_NAME
            && name != "state.json"
            && !(name.starts_with("workbench.schema-v1-backup-")
                && name.ends_with(".sqlite3")
                && !name.contains('/'))
            && !(name.starts_with("state.v")
                && name.ends_with(".json")
                && name.contains("-backup-")
                && !name.contains('/'))
        {
            return Err(sources::invalid("备份包含未知文件"));
        }
        if observed.contains_key(&name) || entry.size() > 64 * 1024 * 1024 * 1024 {
            return Err(sources::invalid("备份文件重复或超过恢复容量"));
        }
        let info = if let Some(target) = target {
            let mut info = None;
            target.create_streamed(&relative, |output| {
                let mut digest = Sha256::new();
                let mut size = 0;
                let mut buffer = [0; 65536];
                loop {
                    cancel.check()?;
                    let count = entry.read(&mut buffer)?;
                    if count == 0 {
                        break;
                    }
                    output.write_all(&buffer[..count])?;
                    digest.update(&buffer[..count]);
                    size += count as u64;
                }
                info = Some(BackupFile {
                    sha256: format!("{:x}", digest.finalize()),
                    size,
                });
                Ok(())
            })?;
            info.ok_or_else(|| sources::invalid("恢复文件读取失败"))?
        } else {
            hash_file(&mut entry, cancel)?
        };
        observed.insert(name, info);
    }
    let manifest = manifest.ok_or_else(|| sources::invalid("缺少备份清单"))?;
    if manifest.files.len() != observed.len()
        || !manifest.files.contains_key(DATABASE_NAME)
        || !manifest.files.contains_key("state.json")
        || manifest.files.iter().any(|(name, expected)| {
            observed.get(name).is_none_or(|actual| {
                actual.sha256 != expected.sha256 || actual.size != expected.size
            })
        })
    {
        return Err(sources::invalid("备份文件与清单摘要不匹配"));
    }
    Ok(manifest)
}

pub fn restore(
    manager: Arc<JobManager>,
    archive_path: String,
    target_path: String,
    request_id: String,
) -> Result<JobRecord> {
    if let Some(job) = manager
        .list()?
        .into_iter()
        .find(|job| job.request_id == request_id)
    {
        if job.kind != "restore" || job.roots != vec![target_path.clone()] {
            return Err(YssError::new(
                ErrorCode::Conflict,
                "请求 ID 已用于另一项操作",
            ));
        }
        return Ok(job);
    }
    if !Path::new(&target_path).is_absolute() {
        return Err(sources::invalid("恢复目录必须是绝对路径"));
    }
    let target = fs::absolute(Path::new(&target_path))?;
    store::reject_git(&target)?;
    let parent = Root::open(
        target
            .parent()
            .ok_or_else(|| sources::invalid("恢复目录没有父目录"))?,
    )?;
    let name = RelPath::new(
        target
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| sources::invalid("恢复目录名无效"))?,
    )?;
    let existing = parent.inspect(&name)?;
    if existing != crate::fs::FileSnapshot::Missing
        && (!matches!(existing, crate::fs::FileSnapshot::Directory { .. })
            || !Root::open(&target)?.list(None)?.is_empty())
    {
        return Err(sources::invalid("恢复目标必须是新目录或空目录"));
    }
    manager.start(
        "restore".into(),
        "恢复工作台备份".into(),
        request_id,
        vec![target_path],
        move |context| {
            let (archive_root, archive_name) = file_location(&archive_path)?;
            let (staging_name, staging) = private_temp(&parent, ".yss-restore")?;
            let manifest = verify_archive(
                archive_root.open_streamed(&archive_name)?,
                Some(&staging),
                &context.cancel,
            )?;
            let state = store::read_state(&staging.path)?;
            if state.revision != manifest.revision {
                return Err(sources::invalid("恢复状态版本与备份清单不一致"));
            }
            if state
                .projects
                .iter()
                .any(|project| target.starts_with(&project.root_path))
            {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "恢复后的数据目录不能位于登记工程内",
                ));
            }
            {
                let mut restored = Store::open(&staging.path)?;
                // Backups omit scratch. Never follow, clean, or replay paths
                // from a different data directory or host; reject such an
                // archive before publishing the restored directory on any OS.
                reject_unbacked_recovery(&mut restored).map_err(|error| {
                    if error.code == ErrorCode::RecoveryRequired {
                        YssError::new(
                            ErrorCode::RecoveryRequired,
                            "备份含未决命令恢复记录，但不包含对应暂存；未恢复或操作原路径",
                        )
                    } else {
                        error
                    }
                })?;
                if let Some((job_id, _)) = restored
                    .record_entries::<Value>(jobs::GROK_CLEANUP_LEASE)?
                    .into_iter()
                    .next()
                {
                    return Err(YssError::new(
                        ErrorCode::RecoveryRequired,
                        "备份含待核实的 Grok Build 进程收尾记录，请在来源工作台查看该轮",
                    )
                    .context("jobId", job_id));
                }
                for mut job in restored.records::<JobRecord>("job")? {
                    if !job.terminal() {
                        job.interrupt(None);
                        restored.put_record("job", &job.id, &job)?;
                    }
                }
            }
            context.cancel.check()?;
            if existing != crate::fs::FileSnapshot::Missing {
                if !Root::open(&target)?.list(None)?.is_empty() {
                    return Err(sources::invalid("恢复目标目录已出现其他内容"));
                }
                parent.remove(&name, &existing)?;
            }
            parent.rename(
                &staging_name,
                &name,
                &crate::fs::FileSnapshot::Missing,
                false,
            )?;
            Ok(json!({
                "dataDir":target,
                "revision":manifest.revision,
                "nextCommand":["yss","workbench","--data-dir",target.to_string_lossy().as_ref()],
                "message":"恢复完成，请用此数据目录打开新工作台并核对工程路径"
            }))
        },
    )
}
