use super::model::{Action, MAX_STATE, MutationResult};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::RelPath,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{path::Path, sync::Arc, time::Duration};

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Session {
    version: u32,
    origin: String,
    token: String,
}

pub struct SessionGuard {
    root: Root,
    snapshot: FileSnapshot,
}
impl Drop for SessionGuard {
    fn drop(&mut self) {
        if let Ok(path) = RelPath::new("session.json") {
            let _ = self.root.remove(&path, &self.snapshot);
        }
    }
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}

fn validate(session: &Session) -> Result<()> {
    let url = reqwest::Url::parse(&session.origin).map_err(|_| invalid("工作台会话地址无效"))?;
    if session.version != 1
        || url.scheme() != "http"
        || url.host_str() != Some("127.0.0.1")
        || url.port().is_none()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || session.token.len() != 64
        || !session.token.bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err(invalid("工作台会话信息无效；未发送任何请求"));
    }
    Ok(())
}

pub fn publish(data_dir: &Path, origin: &str, token: &str) -> Result<SessionGuard> {
    let session = Session {
        version: 1,
        origin: origin.into(),
        token: token.into(),
    };
    validate(&session)?;
    let root = Root::open(data_dir)?;
    let path = RelPath::new("session.json")?;
    let previous = root.inspect(&path)?;
    if !matches!(previous, FileSnapshot::Missing | FileSnapshot::File { .. }) {
        return Err(invalid("工作台会话路径不是普通文件"));
    }
    let temp = RelPath::new(format!("session-{}.tmp", fs::unique_id()?))?;
    let written = root.create_file(&temp, &codec::encode_json(&session)?, 0o600)?;
    if let Err(error) = root.rename(&temp, &path, &previous, true) {
        let _ = root.remove(&temp, &written);
        return Err(error);
    }
    let snapshot = root.read_checked(&path, 4096)?.snapshot;
    Ok(SessionGuard { root, snapshot })
}

fn read(data_dir: &Path) -> Result<Option<Session>> {
    if !data_dir.try_exists()? {
        return Ok(None);
    }
    super::store::read_state(data_dir)?;
    let root = Root::open(&std::fs::canonicalize(data_dir)?)?;
    let file = match root.read_checked(&RelPath::new("session.json")?, 4096) {
        Ok(file) => file,
        Err(error)
            if error
                .context
                .get("kind")
                .is_some_and(|kind| kind == "NotFound") =>
        {
            return Ok(None);
        }
        Err(error) => return Err(error),
    };
    #[cfg(unix)]
    if file
        .snapshot
        .descriptor()
        .is_none_or(|descriptor| descriptor.mode & 0o077 != 0)
    {
        return Err(invalid("工作台会话文件权限过宽；请重启工作台生成私有会话"));
    }
    let session = codec::decode_json(
        &file.bytes,
        Limits {
            bytes: 4096,
            ..Limits::manifest()
        },
    )?;
    validate(&session)?;
    root.revalidate()?;
    Ok(Some(session))
}

async fn response_json(mut response: reqwest::Response) -> Result<Value> {
    let status = response.status();
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| YssError::new(ErrorCode::Network, "工作台响应中断，请核对结果后再操作"))?
    {
        if (bytes.len() + chunk.len()) as u64 > MAX_STATE {
            return Err(invalid("工作台响应超过读取上限"));
        }
        bytes.extend_from_slice(&chunk);
    }
    let value: Value = codec::decode_json(
        &bytes,
        Limits {
            bytes: MAX_STATE,
            ..Limits::manifest()
        },
    )?;
    if !status.is_success() {
        let remote = value.get("error");
        let code = match remote.and_then(|v| v["code"].as_str()).unwrap_or("") {
            "CONCURRENT_CHANGE" => ErrorCode::ConcurrentChange,
            "UNSAFE_PATH" => ErrorCode::UnsafePath,
            "RECOVERY_REQUIRED" => ErrorCode::RecoveryRequired,
            "CONFLICT" => ErrorCode::Conflict,
            _ => ErrorCode::InvalidInput,
        };
        return Err(YssError::new(
            code,
            remote
                .and_then(|v| v["message"].as_str())
                .unwrap_or("工作台拒绝操作"),
        )
        .context("httpStatus", status.as_u16()));
    }
    Ok(value)
}

fn send(
    data_dir: &Path,
    action: Option<Action>,
    registration: Option<(&str, &str)>,
) -> Result<Option<MutationResult>> {
    let Some(session) = read(data_dir)? else {
        return Ok(None);
    };
    let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| invalid("本地请求配置失败"))?
    .with_root_certificates(rustls::RootCertStore::empty())
    .with_no_client_auth();
    let client = reqwest::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(2))
        .timeout(Duration::from_secs(30))
        .use_preconfigured_tls(tls)
        .build()
        .map_err(|_| invalid("无法创建本地工作台连接"))?;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    runtime.block_on(async {
        let base = session.origin.trim_end_matches('/');
        let response = client.get(format!("{base}/api/snapshot")).bearer_auth(&session.token).send().await;
        let response = match response {
            Ok(response) => response,
            Err(error) if error.is_connect() => return Ok(None),
            Err(_) => return Err(YssError::new(ErrorCode::Network, "无法读取当前工作台状态；未写入")),
        };
        let snapshot = response_json(response).await?;
        let revision = snapshot["revision"].as_u64().ok_or_else(|| invalid("工作台响应缺少 revision"))?;
        let (path, body) = if let Some((root, source)) = registration {
            ("/api/register", json!({"expectedRevision":revision,"rootPath":root,"sourcePath":source}))
        } else {
            ("/api/commands", json!({"expectedRevision":revision,"action":action.ok_or_else(|| invalid("缺少操作"))?}))
        };
        let response = client.post(format!("{base}{path}")).bearer_auth(&session.token)
            .header(reqwest::header::ORIGIN, base).header(reqwest::header::CONTENT_TYPE, "application/json")
            .body(serde_json::to_vec(&body).map_err(|_| invalid("操作编码失败"))?).send().await
            .map_err(|_| YssError::new(ErrorCode::RecoveryRequired, "工作台写入响应中断；请先核对当前记录，勿直接重复提交"))?;
        let value = response_json(response).await?;
        let result = serde_json::from_value(value).map_err(|_| invalid("工作台操作响应无效；请核对当前记录"))?;
        Ok(Some(result))
    })
}

pub fn mutate_if_running(data_dir: &Path, action: Action) -> Result<Option<MutationResult>> {
    send(data_dir, Some(action), None)
}

pub fn register_if_running(
    data_dir: &Path,
    root: &str,
    source: &str,
) -> Result<Option<MutationResult>> {
    send(data_dir, None, Some((root, source)))
}
