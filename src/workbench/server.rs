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
    platform: super::api::PlatformApi,
    shutdown: Cancellation,
    restart: Arc<Mutex<Option<PathBuf>>>,
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
    (status,Json(json!({"error":{"code":e.code,"message":e.message,"revision":e.context.get("revision").and_then(|s|s.parse::<u64>().ok()),"context":e.context}}))).into_response()
}
async fn handle(State(server): State<Server>, request: Request) -> Response {
    let mut response = dispatch(server, request).await;
    for (key, value) in [
        ("cache-control", "no-store"),
        ("x-content-type-options", "nosniff"),
        ("referrer-policy", "no-referrer"),
        (
            "content-security-policy",
            "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src data: blob:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; frame-src http://127.0.0.1:*",
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
    let query = request.uri().query().unwrap_or("").to_owned();
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
    if method == axum::http::Method::GET
        && (path.starts_with("/api/confirmations/") || path.starts_with("/api/specs/"))
        && path.ends_with("/file")
    {
        let current = path.starts_with("/api/specs/");
        let prefix_len = if current { 11 } else { 19 };
        let id = path[prefix_len..path.len() - 5].to_owned();
        let query = super::api::query_values(&query);
        let requested = query.get("path").cloned().unwrap_or_default();
        let expected_sha = query.get("sha256").cloned();
        let store = server.store.clone();
        return match tokio::task::spawn_blocking(move || -> Result<_> {
            if current {
                let state = super::jobs::lock_store(&store)?.state().clone();
                let file = super::artifacts::current_file(&state, &id, &requested)?;
                if expected_sha.is_some_and(|expected| expected != file.0.sha256) {
                    return Err(YssError::new(
                        ErrorCode::ConcurrentChange,
                        "当前图片已变化，请重新比较后预览",
                    )
                    .context("httpStatus", 409));
                }
                Ok(file)
            } else {
                let mut store = super::jobs::lock_store(&store)?;
                super::artifacts::historical_file(&mut store, &id, &requested)
            }
        })
        .await
        {
            Ok(Ok((file, bytes))) => (
                [
                    (header::CONTENT_TYPE, file.mime),
                    (header::CONTENT_DISPOSITION, "attachment".into()),
                ],
                Body::from(bytes),
            )
                .into_response(),
            Ok(Err(error_value)) => error(error_value),
            Err(_) => failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                "INTERNAL",
                "历史文件读取失败",
            ),
        };
    }
    if method == axum::http::Method::GET && path == "/api/changes" {
        let platform = server.platform.clone();
        let stopping = server.shutdown.clone();
        let stream = futures_util::stream::unfold(
            (platform, stopping, String::new()),
            |(platform, stopping, mut previous)| async move {
                loop {
                    if stopping.is_cancelled() || platform.cancel.is_cancelled() {
                        return None;
                    }
                    let reader = platform.clone();
                    let value = tokio::task::spawn_blocking(move || reader.source_state()).await;
                    let value = match value {
                        Ok(Ok(value)) => value,
                        _ => return None,
                    };
                    let key =
                        serde_json::json!([value["revision"], value["sourceVersions"]]).to_string();
                    if key != previous {
                        previous = key;
                        let mut bytes = serde_json::to_vec(&value).unwrap_or_default();
                        bytes.push(b'\n');
                        return Some((
                            Ok::<_, std::io::Error>(bytes),
                            (platform, stopping, previous),
                        ));
                    }
                    tokio::select! {
                        _ = stopping.cancelled() => return None,
                        _ = platform.cancel.cancelled() => return None,
                        _ = tokio::time::sleep(std::time::Duration::from_secs(5)) => {}
                    }
                }
            },
        );
        return (
            [(header::CONTENT_TYPE, "application/x-ndjson")],
            Body::from_stream(stream),
        )
            .into_response();
    }
    if method == axum::http::Method::GET
        && path.starts_with("/api/jobs/")
        && path.ends_with("/events")
    {
        let id = path[10..path.len() - 7].to_owned();
        if let Err(error_value) = server.platform.jobs.get(&id) {
            return error(error_value);
        }
        let after = super::api::query_values(&query)
            .get("after")
            .and_then(|value| value.parse::<u64>().ok())
            .unwrap_or(0);
        let jobs = server.platform.jobs.clone();
        let stopping = server.shutdown.clone();
        let stream = futures_util::stream::unfold(
            (jobs, stopping, id, after),
            |(jobs, stopping, id, mut after)| async move {
                loop {
                    if stopping.is_cancelled() {
                        return None;
                    }
                    let reader = jobs.clone();
                    let job_id = id.clone();
                    let result = tokio::task::spawn_blocking(move || -> Result<_> {
                        let events =
                            super::jobs::lock_store(&reader.store)?.events(&job_id, after)?;
                        let terminal = reader.get(&job_id)?.terminal();
                        Ok((events, terminal))
                    })
                    .await;
                    let (events, terminal) = match result {
                        Ok(Ok(value)) => value,
                        _ => return None,
                    };
                    if !events.is_empty() {
                        let mut bytes = Vec::new();
                        for event in events {
                            after = event.id;
                            serde_json::to_writer(&mut bytes, &event).ok()?;
                            bytes.push(b'\n');
                        }
                        return Some((Ok::<_, std::io::Error>(bytes), (jobs, stopping, id, after)));
                    }
                    if terminal {
                        return None;
                    }
                    tokio::select! {
                        _ = stopping.cancelled() => return None,
                        _ = tokio::time::sleep(std::time::Duration::from_millis(200)) => {}
                    }
                }
            },
        );
        return (
            [(header::CONTENT_TYPE, "application/x-ndjson")],
            Body::from_stream(stream),
        )
            .into_response();
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
        if method == axum::http::Method::POST && path == "/api/restart" {
            if server.platform.jobs.list()?.iter().any(|job| !job.terminal()) {
                return Err(YssError::new(
                    ErrorCode::Locked,
                    "请等待活动作业结束或逐项停止后重启",
                ));
            }
            #[derive(serde::Deserialize)]
            #[serde(rename_all = "camelCase", deny_unknown_fields)]
            struct Restart {
                tool_root: Option<String>,
            }
            let request: Restart = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            let executable = if let Some(root) = request.tool_root {
                let root = crate::fs::Root::open(std::path::Path::new(&root))?;
                let name = if cfg!(windows) { "yss.exe" } else { "yss" };
                let path = root.path.join(name);
                let _ = crate::updater::release::read_binary_info(&path)?;
                root.revalidate()?;
                path
            } else {
                std::env::current_exe()?
            };
            *server.restart.lock().map_err(|_| {
                YssError::new(ErrorCode::Io, "重启状态失效")
            })? = Some(executable);
            server.shutdown.cancel();
            return Ok(json!({"restarting":true,"message":"正在重启工作台，新会话将自动打开，请在新页面恢复草稿"}));
        }
        if let Some(value) = server.platform.route(method.as_str(), &path, &query, &bytes)? {
            return Ok(value);
        }
        if method == axum::http::Method::POST && path == "/api/register" {
            #[derive(serde::Deserialize)]
            #[serde(rename_all = "camelCase", deny_unknown_fields)]
            struct Register {
                expected_revision: u64,
                root_path: String,
                source_path: String,
            }
            let request: Register = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            let mut store = server
                .store
                .lock()
                .map_err(|_| YssError::new(ErrorCode::Io, "数据锁失效，请重启"))?;
            return serde_json::to_value(super::registration::register(
                &mut store,
                &request.root_path,
                &request.source_path,
                request.expected_revision,
            )?)
            .map_err(|_| YssError::new(ErrorCode::Io, "响应编码失败"));
        }
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
        let value = if method == axum::http::Method::POST && path == "/api/document" {
            #[derive(serde::Deserialize)]
            #[serde(rename_all = "camelCase", deny_unknown_fields)]
            struct Document {
                spec_id: String,
                path: String,
            }
            let request: Document = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            serde_json::to_value(super::registration::read_document(
                &state,
                &request.spec_id,
                &request.path,
            )?)
        } else if method == axum::http::Method::POST
            && path.starts_with("/api/specs/")
            && path.ends_with("/preview")
        {
            #[derive(serde::Deserialize)]
            #[serde(deny_unknown_fields)]
            struct Preview {
                target: String,
            }
            let request: Preview = codec::decode_json(&bytes, limits).map_err(decode_error)?;
            let id = &path[11..path.len() - 8];
            let spec = super::sources::spec(&state, id)?;
            let project = super::sources::project(&state, &spec.project_id)?;
            let source = super::sources::read_source(&state, id)?;
            let meta = super::content::metadata(&source.text)?;
            if !super::content::merged_resources(spec, meta.as_ref())
                .iter()
                .any(|r| {
                    r.kind == ResourceKind::Prototype
                        && r.target_type == TargetType::Local
                        && r.target == request.target
                })
            {
                return Err(super::sources::invalid("只能预览此 Spec 已关联的本地原型"));
            }
            serde_json::to_value(super::preview::start(
                &project.root_path,
                &request.target,
                &server.origin,
                &server.cancel,
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/content")
        {
            serde_json::to_value(super::content::read_view(
                &state,
                &path[11..path.len() - 8],
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/request")
        {
            serde_json::to_value(super::content::agent_request(
                &state,
                &path[11..path.len() - 8],
                server
                    .store
                    .lock()
                    .map_err(|_| YssError::new(ErrorCode::Io, "数据锁失效，请重启"))?
                    .data_dir(),
            )?)
        } else if method == axum::http::Method::GET && path == "/api/snapshot" {
            {
                let mut snapshot = service::snapshot(&state, &server.cancel)?;
                if super::api::query_values(&query)
                    .get("compact")
                    .is_some_and(|value| value == "1")
                {
                    let start = snapshot.history.len().saturating_sub(32);
                    snapshot.history.drain(..start);
                }
                serde_json::to_value(snapshot)
            }
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
    bundle_bytes: &[u8],
    cancel: &Cancellation,
    err: &mut dyn Write,
) -> Result<()> {
    let store = Store::open(&resolve_data_dir(Some(&options.data_dir))?)?;
    let canonical_data_dir = store.data_dir().to_owned();
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let restart = Arc::new(Mutex::new(None));
    let shutdown = Cancellation::new();
    let mut restart_port = options.port;
    let result: Result<()> = runtime.block_on(async {
        let listener =
            tokio::net::TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, options.port)).await?;
        let host = listener.local_addr()?.to_string();
        restart_port = listener.local_addr()?.port();
        let origin = format!("http://{host}");
        let token = format!("{}{}", crate::fs::unique_id()?, crate::fs::unique_id()?);
        let _session = super::session::publish(&canonical_data_dir, &origin, &token)?;
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
        let store = Arc::new(Mutex::new(store));
        let jobs = super::jobs::JobManager::new(store.clone())?;
        let platform = super::api::PlatformApi {
            jobs: jobs.clone(),
            bundle: Arc::from(bundle_bytes),
            cancel: cancel.clone(),
            origin: origin.clone(),
        };
        let server = Server {
            store,
            platform,
            shutdown: shutdown.clone(),
            restart: restart.clone(),
            host,
            origin,
            token: format!("Bearer {token}"),
            assets,
            cancel: cancel.clone(),
        };
        let stopping = cancel.clone();
        let shutdown_jobs = jobs.clone();
        axum::serve(listener, Router::new().fallback(handle).with_state(server))
            .with_graceful_shutdown(async move {
                tokio::select! {
                    _ = stopping.cancelled() => {},
                    _ = shutdown.cancelled() => {}
                }
                shutdown.cancel();
                shutdown_jobs.shutdown();
            })
            .await?;
        jobs.shutdown();
        Ok(())
    });
    drop(runtime);
    result?;
    let executable = restart
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Io, "重启状态失效"))?
        .take();
    if let Some(executable) = executable {
        let mut command = std::process::Command::new(executable);
        command
            .args(["workbench", "--data-dir"])
            .arg(canonical_data_dir)
            .arg("--port")
            .arg(restart_port.to_string());
        if !options.open_browser {
            command.arg("--no-open");
        }
        let status = command.status()?;
        if !status.success() {
            return Err(YssError::new(
                ErrorCode::Io,
                "新版工作台启动失败，请从终端重新打开并核对数据目录",
            ));
        }
    }
    Ok(())
}
