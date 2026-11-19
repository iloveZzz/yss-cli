use super::sources::invalid;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::RelPath,
};
use axum::{
    Router,
    body::Body,
    extract::{Request, State},
    http::{Method, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex, OnceLock},
};

const MAX_FILE: u64 = 32 * 1024 * 1024;
const MAX_TOTAL: u64 = 128 * 1024 * 1024;

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PreviewSession {
    pub url: String,
    pub message: String,
}

type SessionKey = (String, String, String);
type Sessions = BTreeMap<SessionKey, (PreviewSession, Cancellation)>;
static SESSIONS: OnceLock<Mutex<Sessions>> = OnceLock::new();

#[derive(Clone)]
struct Preview {
    root: Arc<Root>,
    directory: String,
    entry: String,
    host: String,
    token: String,
    cookie: String,
    policy: String,
}

fn mime(path: &str) -> Option<&'static str> {
    match path.rsplit('.').next()?.to_ascii_lowercase().as_str() {
        "html" | "htm" => Some("text/html; charset=utf-8"),
        "js" | "mjs" => Some("text/javascript; charset=utf-8"),
        "css" => Some("text/css; charset=utf-8"),
        "json" => Some("application/json; charset=utf-8"),
        "svg" => Some("image/svg+xml"),
        "png" => Some("image/png"),
        "jpg" | "jpeg" => Some("image/jpeg"),
        "webp" => Some("image/webp"),
        "avif" => Some("image/avif"),
        "bmp" => Some("image/bmp"),
        "gif" => Some("image/gif"),
        "ico" => Some("image/x-icon"),
        "woff" => Some("font/woff"),
        "woff2" => Some("font/woff2"),
        "ttf" => Some("font/ttf"),
        "wasm" => Some("application/wasm"),
        "mp4" => Some("video/mp4"),
        "webm" => Some("video/webm"),
        "mp3" => Some("audio/mpeg"),
        "ogg" => Some("audio/ogg"),
        _ => None,
    }
}

fn visible(path: &str) -> bool {
    path.split('/')
        .all(|part| !part.starts_with('.') && part != "node_modules" && part != "src")
}

fn entry_parts(target: &str) -> Result<(String, String)> {
    RelPath::new(target)?;
    if !matches!(mime(target), Some("text/html; charset=utf-8")) {
        return Err(invalid(
            "原型预览需要静态 HTML 入口；请先构建原型并关联 dist/index.html",
        ));
    }
    let (directory, entry) = target.rsplit_once('/').unwrap_or(("", target));
    Ok((directory.into(), entry.into()))
}

fn is_source_entry(root: &Root, target: &str) -> Result<bool> {
    entry_parts(target)?;
    let html = root.source_read(&RelPath::new(target)?, MAX_FILE)?;
    let text = String::from_utf8(html.bytes).map_err(|_| invalid("HTML 入口不是 UTF-8"))?;
    let lower = text.to_ascii_lowercase();
    root.revalidate()?;
    Ok(lower.contains("/@vite/client")
        || ["src=\"/src/", "src='/src/", "src=\"./src/", "src='./src/"]
            .iter()
            .any(|prefix| lower.contains(prefix))
        || [".ts\"", ".ts'", ".tsx\"", ".tsx'", ".vue\"", ".vue'"]
            .iter()
            .any(|suffix| lower.contains(suffix)))
}

pub(super) fn built_path(target: &str) -> Option<String> {
    let (directory, entry) = target.rsplit_once('/').unwrap_or(("", target));
    if entry != "index.html" || directory.rsplit('/').next() == Some("dist") {
        return None;
    }
    Some(if directory.is_empty() {
        "dist/index.html".into()
    } else {
        format!("{directory}/dist/index.html")
    })
}

pub(super) fn built_alternative(root: &Root, target: &str) -> Option<String> {
    let built = built_path(target)?;
    (matches!(is_source_entry(root, &built), Ok(false))
        && matches!(is_source_entry(root, target), Ok(true)))
    .then_some(built)
}

pub(super) fn check_entry(root: &Root, target: &str) -> Result<()> {
    if is_source_entry(root, target)? {
        return Err(invalid(
            "当前关联指向原型源码。请先构建，再关联 dist/index.html；工作台不会执行项目命令",
        ));
    }
    Ok(())
}

// Include every served asset so changing JS/CSS also invalidates a content confirmation.
pub fn artifact_files(root: &Root, target: &str) -> Result<BTreeMap<String, Vec<u8>>> {
    check_entry(root, target)?;
    served_files(root, target)
}

pub(crate) fn served_files(root: &Root, target: &str) -> Result<BTreeMap<String, Vec<u8>>> {
    served_files_with_cancel(root, target, &Cancellation::new())
}

pub(crate) fn served_files_with_cancel(
    root: &Root,
    target: &str,
    cancel: &Cancellation,
) -> Result<BTreeMap<String, Vec<u8>>> {
    let (directory, _) = entry_parts(target)?;
    let initial = if directory.is_empty() {
        None
    } else {
        Some(RelPath::new(&directory)?)
    };
    let mut stack = vec![(initial, 0)];
    let mut budget = 10000;
    let mut total = 0u64;
    let mut files = BTreeMap::new();
    while let Some((dir, depth)) = stack.pop() {
        let (entries, complete) = root.source_entries(dir.as_ref(), &mut budget, cancel)?;
        if !complete {
            return Err(invalid("原型资源过多，无法完整核对版本"));
        }
        for (name, metadata) in entries {
            cancel.check()?;
            if !visible(&name) {
                continue;
            }
            let path = match &dir {
                Some(dir) => dir.join(&name)?,
                None => RelPath::new(name.clone())?,
            };
            if metadata.is_dir() {
                if depth >= 12 {
                    return Err(invalid("原型资源目录层级过深"));
                }
                stack.push((Some(path), depth + 1));
            } else if mime(&name).is_some() {
                let file = root.source_read(&path, MAX_FILE)?;
                total += file.bytes.len() as u64;
                if total > MAX_TOTAL {
                    return Err(invalid("原型资源超过 128 MiB，无法完整核对版本"));
                }
                files.insert(path.to_string(), file.bytes);
            }
        }
    }
    root.revalidate()?;
    Ok(files)
}

pub fn files_digest(files: &BTreeMap<String, Vec<u8>>) -> String {
    let mut digest = Sha256::new();
    for (path, bytes) in files {
        let hash = Sha256::digest(bytes);
        digest.update((path.len() as u64).to_le_bytes());
        digest.update(path.as_bytes());
        digest.update(hash);
    }
    format!("{:x}", digest.finalize())
}

pub fn artifact_digest(root: &Root, target: &str) -> Result<String> {
    Ok(files_digest(&artifact_files(root, target)?))
}

fn decoded_path(value: &str) -> Result<String> {
    let mut bytes = Vec::new();
    let mut input = value.as_bytes().iter().copied();
    while let Some(byte) = input.next() {
        if byte == b'%' {
            let high = input.next().and_then(|b| (b as char).to_digit(16));
            let low = input.next().and_then(|b| (b as char).to_digit(16));
            match (high, low) {
                (Some(h), Some(l)) => bytes.push((h * 16 + l) as u8),
                _ => return Err(invalid("资源地址编码不合法")),
            }
        } else {
            bytes.push(byte);
        }
    }
    String::from_utf8(bytes).map_err(|_| invalid("资源地址不是 UTF-8"))
}

fn file_response(preview: &Preview, path: &str) -> Result<Response> {
    let path = decoded_path(path.trim_start_matches('/'))?;
    let path = if path.is_empty() {
        &preview.entry
    } else {
        &path
    };
    RelPath::new(path)?;
    if !visible(path) {
        return Err(invalid("原型预览不提供源码或隐藏文件"));
    }
    let content_type = mime(path).ok_or_else(|| invalid("不支持此原型资源类型"))?;
    let relative = if preview.directory.is_empty() {
        RelPath::new(path)?
    } else {
        RelPath::new(&preview.directory)?.join(path)?
    };
    preview.root.revalidate()?;
    let file = preview.root.source_read(&relative, MAX_FILE)?;
    preview.root.revalidate()?;
    Ok((
        [(header::CONTENT_TYPE, content_type)],
        Body::from(file.bytes),
    )
        .into_response())
}

async fn handle(State(preview): State<Preview>, request: Request) -> Response {
    let mut response = if request.method() != Method::GET
        || request
            .headers()
            .get(header::HOST)
            .and_then(|v| v.to_str().ok())
            != Some(&preview.host)
    {
        (StatusCode::FORBIDDEN, "原型请求不合法").into_response()
    } else if request.uri().path() == format!("/__open/{}", preview.token) {
        let mut response = (StatusCode::SEE_OTHER, [(header::LOCATION, "/")]).into_response();
        response.headers_mut().insert(
            header::SET_COOKIE,
            header::HeaderValue::from_str(&format!(
                "{}; Path=/; HttpOnly; SameSite=Strict",
                preview.cookie
            ))
            .expect("random cookie"),
        );
        response
    } else if !request
        .headers()
        .get(header::COOKIE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|cookies| {
            cookies
                .split(';')
                .any(|cookie| cookie.trim() == preview.cookie)
        })
    {
        (
            StatusCode::UNAUTHORIZED,
            "预览会话失效，请在工作台重新打开原型",
        )
            .into_response()
    } else {
        let path = request.uri().path().to_owned();
        let state = preview.clone();
        match tokio::task::spawn_blocking(move || file_response(&state, &path)).await {
            Ok(Ok(response)) => response,
            Ok(Err(error)) => (StatusCode::NOT_FOUND, error.message).into_response(),
            Err(_) => (StatusCode::INTERNAL_SERVER_ERROR, "原型资源读取失败").into_response(),
        }
    };
    for (name, value) in [
        ("cache-control", "no-store"),
        ("x-content-type-options", "nosniff"),
        ("referrer-policy", "no-referrer"),
        ("cross-origin-resource-policy", "same-origin"),
    ] {
        response.headers_mut().insert(
            header::HeaderName::from_static(name),
            header::HeaderValue::from_static(value),
        );
    }
    response.headers_mut().insert(
        header::CONTENT_SECURITY_POLICY,
        header::HeaderValue::from_str(&preview.policy).expect("fixed policy and local origin"),
    );
    response
}

pub fn start(
    root_path: &str,
    target: &str,
    workbench_origin: &str,
    cancel: &Cancellation,
) -> Result<PreviewSession> {
    cancel.check()?;
    let origin = reqwest::Url::parse(workbench_origin).map_err(|_| invalid("工作台来源不合法"))?;
    if origin.scheme() != "http"
        || origin.host_str() != Some("127.0.0.1")
        || origin.port().is_none()
    {
        return Err(invalid("原型只允许嵌入当前本地工作台"));
    }
    let root = Root::open(Path::new(root_path))?;
    check_entry(&root, target)?;
    let (directory, entry) = entry_parts(target)?;
    let key = (root_path.into(), target.into(), workbench_origin.into());
    let mut sessions = SESSIONS
        .get_or_init(|| Mutex::new(BTreeMap::new()))
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "预览会话锁失效"))?;
    sessions.retain(|_, (_, cancellation)| !cancellation.is_cancelled());
    if let Some((session, _)) = sessions.get(&key) {
        return Ok(session.clone());
    }
    if sessions.len() >= 32 {
        return Err(invalid("已打开 32 个原型预览，请重启工作台后继续"));
    }
    let listener = std::net::TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, 0))?;
    listener.set_nonblocking(true)?;
    let host = listener.local_addr()?.to_string();
    let token = format!("{}{}", fs::unique_id()?, fs::unique_id()?);
    let session = PreviewSession {
        url: format!("http://{host}/__open/{token}"),
        message: "预览已关联的静态构建产物；未执行项目命令，未连接真实后端。".into(),
    };
    let preview = Preview {
        root: Arc::new(root),
        directory,
        entry,
        host,
        cookie: format!("yss_preview_{}={token}", &token[..16]),
        token,
        policy: format!(
            "sandbox allow-scripts allow-same-origin allow-forms allow-modals; default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'none'; frame-ancestors {workbench_origin}"
        ),
    };
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let stopping = cancel.clone();
    std::thread::Builder::new()
        .name("yss-prototype-preview".into())
        .spawn(move || {
            runtime.block_on(async move {
                let Ok(listener) = tokio::net::TcpListener::from_std(listener) else {
                    return;
                };
                let _ = axum::serve(listener, Router::new().fallback(handle).with_state(preview))
                    .with_graceful_shutdown(async move { stopping.cancelled().await })
                    .await;
            });
        })?;
    sessions.insert(key, (session.clone(), cancel.clone()));
    Ok(session)
}
