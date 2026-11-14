use super::{
    model::*,
    service,
    store::{Store, resolve_data_dir},
};
use crate::{
    cancel::Cancellation,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
};
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{StatusCode, header},
    response::{IntoResponse, Response},
};
use serde_json::json;
use std::{
    borrow::Cow,
    io::Write,
    path::PathBuf,
    sync::{Arc, Mutex},
};
pub type AssetLoader = fn(&str) -> Option<Cow<'static, [u8]>>;
pub struct ServeOptions {
    pub data_dir: PathBuf,
    pub port: u16,
    pub open_browser: bool,
}
#[derive(Clone)]
struct Server {
    store: Arc<Mutex<Store>>,
    host: String,
    origin: String,
    token: String,
    assets: AssetLoader,
    cancel: Cancellation,
}
fn failure(status: StatusCode, code: &str, message: &str) -> Response {
    (
        status,
        Json(json!({"error":{"code":code,"message":message}})),
    )
        .into_response()
}
fn error(e: YssError) -> Response {
    let status = e
        .context
        .get("httpStatus")
        .and_then(|s| s.parse::<u16>().ok())
        .and_then(|n| StatusCode::from_u16(n).ok())
        .unwrap_or(match e.code {
            ErrorCode::Argument | ErrorCode::InvalidInput => StatusCode::BAD_REQUEST,
            ErrorCode::UnsafePath => StatusCode::FORBIDDEN,
            ErrorCode::Conflict
            | ErrorCode::ConcurrentChange
            | ErrorCode::Locked
            | ErrorCode::RecoveryRequired => StatusCode::CONFLICT,
            _ => StatusCode::INTERNAL_SERVER_ERROR,
        });
    (status,Json(json!({"error":{"code":e.code,"message":e.message,"revision":e.context.get("revision").and_then(|s|s.parse::<u64>().ok())}}))).into_response()
}
async fn handle(State(server): State<Server>, request: Request) -> Response {
    let mut response = dispatch(server, request).await;
    for (key, value) in [
        ("cache-control", "no-store"),
        ("x-content-type-options", "nosniff"),
        ("referrer-policy", "no-referrer"),
        (
            "content-security-policy",
            "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
        ),
    ] {
        response.headers_mut().insert(
            header::HeaderName::from_static(key),
            header::HeaderValue::from_static(value),
        );
    }
    response
}
async fn dispatch(server: Server, request: Request) -> Response {
    if request
        .headers()
        .get(header::HOST)
        .and_then(|v| v.to_str().ok())
        != Some(&server.host)
    {
        return failure(StatusCode::FORBIDDEN, "HOST", "请求主机不合法");
    }
    let path = request.uri().path().to_owned();
    let method = request.method().clone();
    if !path.starts_with("/api/") {
        if method != axum::http::Method::GET {
            return failure(StatusCode::NOT_FOUND, "NOT_FOUND", "资源不存在");
        }
        let asset = if path == "/" {
            "index.html"
        } else if path.starts_with("/assets/") {
            path.trim_start_matches('/')
        } else {
            return failure(StatusCode::NOT_FOUND, "NOT_FOUND", "资源不存在");
        };
        return match (server.assets)(asset) {
            Some(bytes) => {
                let mime = if asset.ends_with(".html") {
                    "text/html; charset=utf-8"
                } else if asset.ends_with(".js") {
                    "text/javascript; charset=utf-8"
                } else if asset.ends_with(".css") {
                    "text/css; charset=utf-8"
                } else {
                    "application/octet-stream"
                };
                (
                    [(header::CONTENT_TYPE, mime)],
                    Body::from(bytes.into_owned()),
                )
                    .into_response()
            }
            None => failure(StatusCode::NOT_FOUND, "NOT_FOUND", "资源不存在"),
        };
    }
    if request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        != Some(&server.token)
    {
        return failure(
            StatusCode::UNAUTHORIZED,
            "SESSION_EXPIRED",
            "会话无效，请从终端重新打开工作台链接",
        );
    }
    if method != axum::http::Method::GET
        && request
            .headers()
            .get(header::ORIGIN)
            .and_then(|v| v.to_str().ok())
            != Some(&server.origin)
    {
        return failure(StatusCode::FORBIDDEN, "ORIGIN", "请求来源不合法");
    }
    let bytes = match to_bytes(request.into_body(), 1024 * 1024).await {
        Ok(b) => b,
        Err(_) => {
            return failure(
                StatusCode::PAYLOAD_TOO_LARGE,
                "BODY_LIMIT",
                "请求体超过 1 MiB",
            );
        }
    };
    let result = tokio::task::spawn_blocking(move || -> Result<serde_json::Value> {
        server.cancel.check()?;
        let limits = Limits {
            bytes: 1024 * 1024,
            ..Limits::manifest()
        };
        let decode_error = |e: YssError| {
            YssError::new(ErrorCode::InvalidInput, "请求内容不合法").context("detail", e.message)
        };
        if method == axum::http::Method::POST && path == "/api/commands" {
            let request: MutationRequest =
                codec::decode_json(&bytes, limits).map_err(decode_error)?;
            let mut store = server
                .store
                .lock()
                .map_err(|_| YssError::new(ErrorCode::Io, "数据锁失效，请重启"))?;
            return serde_json::to_value(service::mutate(&mut store, request)?)
                .map_err(|_| YssError::new(ErrorCode::Io, "响应编码失败"));
        }
        let state = server
            .store
            .lock()
            .map_err(|_| YssError::new(ErrorCode::Io, "数据锁失效，请重启"))?
            .state()
            .clone();
        let value = if method == axum::http::Method::GET && path == "/api/snapshot" {
            serde_json::to_value(service::snapshot(&state, &server.cancel)?)
        } else if method == axum::http::Method::POST && path == "/api/query" {
            let query: WorkQuery = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            serde_json::to_value(super::query::query(
                &service::snapshot(&state, &server.cancel)?,
                &query,
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/lifecycle")
        {
            let id = &path[11..path.len() - 10];
            serde_json::to_value(super::lifecycle::build_lifecycle(
                &service::snapshot(&state, &server.cancel)?,
                id,
            )?)
        } else if method == axum::http::Method::POST && path == "/api/discover" {
            let request = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            serde_json::to_value(service::discover(&state, &request, &server.cancel)?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/source")
        {
            let id = &path[11..path.len() - 7];
            serde_json::to_value(service::read_source(&state, id)?)
        } else {
            return Err(
                YssError::new(ErrorCode::InvalidInput, "接口不存在").context("httpStatus", 404)
            );
        };
        value.map_err(|_| YssError::new(ErrorCode::Io, "响应编码失败"))
    })
    .await;
    match result {
        Ok(Ok(value)) => Json(value).into_response(),
        Ok(Err(e)) => error(e),
        Err(_) => failure(
            StatusCode::INTERNAL_SERVER_ERROR,
            "INTERNAL",
            "请求处理失败",
        ),
    }
}
pub fn serve(
    options: ServeOptions,
    assets: AssetLoader,
    cancel: &Cancellation,
    err: &mut dyn Write,
) -> Result<()> {
    let store = Store::open(&resolve_data_dir(Some(&options.data_dir))?)?;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    runtime.block_on(async {
        let listener =
            tokio::net::TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, options.port)).await?;
        let host = listener.local_addr()?.to_string();
        let origin = format!("http://{host}");
        let token = format!("{}{}", crate::fs::unique_id()?, crate::fs::unique_id()?);
        let url = format!("{origin}/#token={token}");
        writeln!(
            err,
            "个人 Spec 工作台：{url}\n按 Ctrl-C 停止服务。个人数据：{}",
            options.data_dir.display()
        )?;
        err.flush()?;
        if options.open_browser && webbrowser::open(&url).is_err() {
            writeln!(err, "无法自动打开浏览器，请复制上方链接。").ok();
        }
        let server = Server {
            store: Arc::new(Mutex::new(store)),
            host,
            origin,
            token: format!("Bearer {token}"),
            assets,
            cancel: cancel.clone(),
        };
        let stopping = cancel.clone();
        axum::serve(listener, Router::new().fallback(handle).with_state(server))
            .with_graceful_shutdown(async move { stopping.cancelled().await })
            .await?;
        Ok(())
    })
}
