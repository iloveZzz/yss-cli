//! Desktop attachment and short-lived maintenance coordination.
//!
//! Job admission and queue changes acquire the store before this state. Keeping
//! that order makes checking for work and blocking new work one atomic step.
use super::super::{
    agent::queue,
    jobs::{JobManager, JobRecord, lock_store},
    storage_migration::{self, StorageState},
};
use crate::{
    cancel::Cancellation,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs,
    model::BuildInfo,
};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    sync::{Arc, MutexGuard},
    time::{Duration, Instant},
};

const ATTACH_TTL: Duration = Duration::from_secs(60);
const LEASE_TTL: Duration = Duration::from_secs(30);

struct Lease {
    client: String,
    id: String,
    expires: Instant,
}

pub(crate) struct DesktopState {
    instance: String,
    managed: bool,
    managed_owner: Option<String>,
    owner_client: Option<String>,
    owner_deadline: Option<Instant>,
    attachments: BTreeMap<String, Instant>,
    lease: Option<Lease>,
    stopping: bool,
}

impl DesktopState {
    pub(crate) fn new() -> Result<Self> {
        let managed_owner = std::env::var("YSS_DESKTOP_INSTANCE")
            .ok()
            .filter(|value| valid_id(value));
        let managed = std::env::var_os("YSS_DESKTOP_MANAGED").is_some_and(|v| v == "1")
            && managed_owner.is_some();
        Ok(Self {
            instance: fs::unique_id()?,
            managed,
            managed_owner: if managed { managed_owner } else { None },
            owner_client: None,
            owner_deadline: managed.then(|| Instant::now() + ATTACH_TTL),
            attachments: BTreeMap::new(),
            lease: None,
            stopping: false,
        })
    }

    fn expire(&mut self) {
        self.expire_at(Instant::now());
    }

    fn expire_at(&mut self, now: Instant) {
        self.attachments.retain(|_, until| *until > now);
        // Owner liveness is renewed only by a heartbeat carrying the secret
        // nonce. Other authenticated attachments must never extend ownership.
        if self.managed && self.owner_deadline.is_some_and(|until| until <= now) {
            self.managed = false;
            self.managed_owner = None;
            self.owner_client = None;
            self.owner_deadline = None;
        }
        if self.lease.as_ref().is_some_and(|lease| {
            lease.expires <= now || !self.attachments.contains_key(&lease.client)
        }) {
            self.lease = None;
        }
    }

    fn attach(
        &mut self,
        client: String,
        supplied_owner: Option<String>,
        now: Instant,
    ) -> Result<bool> {
        self.expire_at(now);
        if self.stopping {
            return Err(busy("工作台正在退出"));
        }
        let owner = match supplied_owner {
            Some(owner) if self.managed_owner.as_deref() == Some(owner.as_str()) => true,
            Some(_) => {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "桌面服务所有权无效或已过期，请重新连接",
                ));
            }
            None => false,
        };
        if owner
            && self
                .owner_client
                .as_ref()
                .is_some_and(|previous| previous != &client)
            && let Some(previous) = self.owner_client.take()
        {
            self.attachments.remove(&previous);
            if self
                .lease
                .as_ref()
                .is_some_and(|lease| lease.client == previous)
            {
                self.lease = None;
            }
        }
        if !self.attachments.contains_key(&client) && self.attachments.len() >= 16 {
            return Err(busy("桌面连接过多，请关闭不再使用的桌面应用"));
        }
        let until = now + ATTACH_TTL;
        self.attachments.insert(client.clone(), until);
        if owner {
            self.owner_client = Some(client);
            self.owner_deadline = Some(until);
        }
        Ok(owner)
    }

    fn attached(&mut self, client: &str) -> Result<()> {
        self.expire();
        if self.attachments.contains_key(client) {
            Ok(())
        } else {
            Err(YssError::new(
                ErrorCode::Locked,
                "桌面连接已失效，请重新连接",
            ))
        }
    }
}

fn state(manager: &JobManager) -> Result<MutexGuard<'_, DesktopState>> {
    manager
        .desktop
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "桌面协调状态失效"))
}

fn busy(message: &str) -> YssError {
    YssError::new(ErrorCode::Locked, message)
}

fn valid_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
}

/// Prove possession of the secret without disclosing it to an unknown listener.
/// Token and nonce are their ASCII hex encodings, not decoded bytes.
pub(super) fn challenge(bearer: &str, query: &str) -> Result<Value> {
    let nonce = query
        .strip_prefix("nonce=")
        .filter(|s| {
            s.len() == 64
                && s.bytes()
                    .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
        })
        .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "桌面挑战参数无效"))?;
    let token = bearer
        .strip_prefix("Bearer ")
        .filter(|s| {
            s.len() == 64
                && s.bytes()
                    .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase())
        })
        .ok_or_else(|| YssError::new(ErrorCode::Io, "工作台会话无效"))?;
    let mut inner_pad = [0x36_u8; 64];
    let mut outer_pad = [0x5c_u8; 64];
    for (index, byte) in token.bytes().enumerate() {
        inner_pad[index] ^= byte;
        outer_pad[index] ^= byte;
    }
    let mut inner = Sha256::new();
    inner.update(inner_pad);
    inner.update(nonce.as_bytes());
    let mut outer = Sha256::new();
    outer.update(outer_pad);
    outer.update(inner.finalize());
    let proof: String = outer
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    Ok(json!({"proof": proof}))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Client {
    client_id: String,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct AttachClient {
    client_id: String,
    #[serde(default)]
    managed_owner: Option<String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct LeasedClient {
    client_id: String,
    lease_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ShutdownClient {
    client_id: String,
    lease_id: String,
    managed_owner: String,
}

fn decode<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T> {
    codec::decode_json(
        bytes,
        Limits {
            bytes: 4096,
            ..Limits::manifest()
        },
    )
}
fn client(bytes: &[u8]) -> Result<String> {
    let value: Client = decode(bytes)?;
    if !valid_id(&value.client_id) {
        return Err(YssError::new(ErrorCode::InvalidInput, "桌面连接标识无效"));
    }
    Ok(value.client_id)
}
fn leased(bytes: &[u8]) -> Result<LeasedClient> {
    let value: LeasedClient = decode(bytes)?;
    if !valid_id(&value.client_id) || !valid_id(&value.lease_id) {
        return Err(YssError::new(ErrorCode::InvalidInput, "桌面协调标识无效"));
    }
    Ok(value)
}

// Caller must hold the Store lock until it has finished admission or queue mutation.
pub(crate) fn check_admission(manager: &JobManager) -> Result<()> {
    let mut desktop = state(manager)?;
    desktop.expire();
    if desktop.stopping || desktop.lease.is_some() {
        Err(busy("工作台正在准备退出或更新，请稍后重试"))
    } else {
        Ok(())
    }
}

pub(crate) fn begin_restart(manager: &JobManager) -> Result<()> {
    let mut store = lock_store(&manager.store)?;
    let mut desktop = state(manager)?;
    desktop.expire();
    if desktop.managed || !desktop.attachments.is_empty() || desktop.lease.is_some() {
        return Err(busy("桌面工作台正在使用此服务，请在桌面应用中操作"));
    }
    if desktop.stopping
        || store
            .records::<JobRecord>("job")?
            .iter()
            .any(|job| !job.terminal())
        || queue::desktop_pending(&mut store)? != 0
    {
        return Err(busy("请等待活动作业和排队任务结束或逐项停止后重启"));
    }
    desktop.stopping = true;
    Ok(())
}

pub(crate) fn route(
    manager: &Arc<JobManager>,
    shutdown: &Cancellation,
    method: &str,
    path: &str,
    bytes: &[u8],
) -> Result<Value> {
    match (method, path) {
        ("GET", "/api/desktop-info") => {
            let store = lock_store(&manager.store)?;
            let data_id = match storage_migration::probe(store.data_dir())? {
                StorageState::Active(marker) => marker.database_id,
                _ => {
                    return Err(YssError::new(
                        ErrorCode::RecoveryRequired,
                        "工作台存储身份不可用",
                    ));
                }
            };
            let mut desktop = state(manager)?;
            desktop.expire();
            // The owner nonce authorizes shutdown and must never be disclosed to
            // an attached workbench client. A digest permits only the process
            // that generated it to recognize its own child.
            let managed_owner_proof = desktop.managed_owner.as_ref().map(|owner| {
                let mut hash = Sha256::new();
                hash.update(b"yss-desktop-owner/1:");
                hash.update(owner.as_bytes());
                hash.update(b":");
                hash.update(desktop.instance.as_bytes());
                hash.finalize()
                    .iter()
                    .map(|byte| format!("{byte:02x}"))
                    .collect::<String>()
            });
            Ok(
                json!({"protocol":1,"instanceId":desktop.instance,"dataId":data_id,"build":BuildInfo::current(),"managed":desktop.managed,"managedOwnerProof":managed_owner_proof,"capabilities":["challenge-v1","flush-v1","notification-v1","attachment-v1","quiesce-v1","queue-pause-v1","managed-shutdown-v1"]}),
            )
        }
        ("GET", "/api/desktop/activity") => {
            let mut store = lock_store(&manager.store)?;
            let mut queued = queue::desktop_pending(&mut store)?;
            let (mut running, mut attention) = (0usize, 0usize);
            for job in store.records::<JobRecord>("job")? {
                match job.status.as_str() {
                    "preparing" | "running" => running += 1,
                    "waitingInput" | "waitingApproval" => attention += 1,
                    "queued" => queued += 1,
                    _ => (),
                }
            }
            Ok(json!({"running":running,"attention":attention,"queued":queued}))
        }
        ("POST", "/api/desktop/attach") => {
            let request: AttachClient = decode(bytes)?;
            if !valid_id(&request.client_id)
                || request
                    .managed_owner
                    .as_ref()
                    .is_some_and(|owner| !valid_id(owner))
            {
                return Err(YssError::new(ErrorCode::InvalidInput, "桌面连接标识无效"));
            }
            let client = request.client_id;
            let mut desktop = state(manager)?;
            let owner = desktop.attach(client, request.managed_owner, Instant::now())?;
            Ok(
                json!({"attached":true,"managed":desktop.managed,"owner":owner,"ttlSeconds":ATTACH_TTL.as_secs()}),
            )
        }
        ("POST", "/api/desktop/detach") => {
            let client = client(bytes)?;
            let mut desktop = state(manager)?;
            desktop.attachments.remove(&client);
            if desktop
                .lease
                .as_ref()
                .is_some_and(|lease| lease.client == client)
            {
                desktop.lease = None;
            }
            Ok(json!({"detached":true}))
        }
        ("POST", "/api/desktop/pause-queues") => {
            let client = client(bytes)?;
            state(manager)?.attached(&client)?;
            Ok(json!({"paused":queue::desktop_pause(manager)?}))
        }
        ("POST", "/api/desktop/quiesce") => {
            let client = client(bytes)?;
            let mut store = lock_store(&manager.store)?;
            let mut desktop = state(manager)?;
            desktop.attached(&client)?;
            if desktop.stopping || desktop.lease.is_some() {
                return Err(busy("工作台已有协调操作进行中"));
            }
            let active = store
                .records::<JobRecord>("job")?
                .iter()
                .filter(|job| !job.terminal())
                .count();
            let queued = queue::desktop_pending(&mut store)?;
            if active != 0 || queued != 0 {
                return Err(busy("请先完成或停止活动作业，并处理排队任务")
                    .context("activeJobs", active)
                    .context("queuedItems", queued));
            }
            let lease_id = fs::unique_id()?;
            desktop.lease = Some(Lease {
                client,
                id: lease_id.clone(),
                expires: Instant::now() + LEASE_TTL,
            });
            Ok(json!({"leaseId":lease_id,"ttlSeconds":LEASE_TTL.as_secs()}))
        }
        ("POST", "/api/desktop/release") => {
            let value = leased(bytes)?;
            let mut desktop = state(manager)?;
            desktop.expire();
            if desktop
                .lease
                .as_ref()
                .is_some_and(|lease| lease.client == value.client_id && lease.id == value.lease_id)
            {
                desktop.lease = None;
            }
            Ok(json!({"released":true}))
        }
        ("POST", "/api/desktop/shutdown") => {
            let value: ShutdownClient = decode(bytes)?;
            if !valid_id(&value.client_id)
                || !valid_id(&value.lease_id)
                || !valid_id(&value.managed_owner)
            {
                return Err(YssError::new(ErrorCode::InvalidInput, "桌面协调标识无效"));
            }
            let _store = lock_store(&manager.store)?;
            let mut desktop = state(manager)?;
            desktop.attached(&value.client_id)?;
            if !desktop.managed
                || desktop.managed_owner.as_deref() != Some(&value.managed_owner)
                || desktop.owner_client.as_deref() != Some(&value.client_id)
            {
                return Err(YssError::new(
                    ErrorCode::UnsafePath,
                    "不能关闭外部工作台服务",
                ));
            }
            if !desktop
                .lease
                .as_ref()
                .is_some_and(|lease| lease.client == value.client_id && lease.id == value.lease_id)
            {
                return Err(busy("协调租约已失效，请重新核对作业"));
            }
            desktop.stopping = true;
            shutdown.cancel();
            Ok(json!({"stopping":true}))
        }
        _ => {
            Err(YssError::new(ErrorCode::InvalidInput, "桌面接口不存在").context("httpStatus", 404))
        }
    }
}
