use super::{
    model::*,
    read_model::{self, ReadModel},
    service,
    store::{Store, resolve_data_dir},
};
use crate::{
    cancel::Cancellation,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    model::Sha256,
};
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{StatusCode, header},
    response::{IntoResponse, Response},
};
use futures_util::FutureExt;
use serde_json::json;
use std::{
    borrow::Cow,
    io::Write,
    path::PathBuf,
    sync::{Arc, Mutex, OnceLock},
    time::Duration,
};
use tokio::sync::Notify;
pub(crate) mod desktop;
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

fn immediate_snapshot(server: &Server, spec_id: Option<&str>) -> Result<Snapshot> {
    let input = read_model::capture(&mut *super::jobs::lock_store(&server.store)?)?;
    read_model::build_snapshot(input, spec_id, &server.cancel)
}

async fn handle(State(server): State<Server>, request: Request) -> Response {
    let mut response = dispatch(server, request).await;
    for (key, value) in [
        ("cache-control", "no-store"),
        ("x-content-type-options", "nosniff"),
        ("referrer-policy", "no-referrer"),
        (
            "content-security-policy",
            "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; img-src data: blob:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; frame-src http://127.0.0.1:*",
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
                } else if asset.ends_with(".woff2") {
                    "font/woff2"
                } else if asset.ends_with(".woff") {
                    "font/woff"
                } else if asset.ends_with(".ttf") {
                    "font/ttf"
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
    // Prove knowledge of the token before the desktop sends it to a reused port.
    if path == "/api/desktop-challenge" && method == axum::http::Method::GET {
        return match desktop::challenge(&server.token, &query) {
            Ok(value) => Json(value).into_response(),
            Err(value) => error(value),
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
        let latest = match query.get("basis").map(String::as_str) {
            None | Some("candidate") => false,
            Some("latest") => true,
            Some(_) => {
                return failure(StatusCode::BAD_REQUEST, "INVALID_INPUT", "文件对比范围无效");
            }
        };
        let store = server.store.clone();
        return match tokio::task::spawn_blocking(move || -> Result<_> {
            if current {
                let file = if latest {
                    let state = super::jobs::lock_store(&store)?.state().clone();
                    super::artifacts::current_file(&state, &id, &requested)?
                } else {
                    super::artifacts::candidate_file(
                        &mut *super::jobs::lock_store(&store)?,
                        &id,
                        &requested,
                    )?
                };
                if expected_sha.is_some_and(|expected| expected != file.0.sha256) {
                    return Err(YssError::new(
                        ErrorCode::ConcurrentChange,
                        "对比文件已变化，请重新比较后查看",
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
        let stream = futures_util::stream::unfold(
            (
                server.platform.reads.subscribe(),
                server.shutdown.clone(),
                true,
            ),
            |(mut receiver, stopping, first)| async move {
                if !first {
                    tokio::select! {
                        _ = stopping.cancelled() => return None,
                        result = receiver.changed() => result.ok()?,
                    }
                }
                if stopping.is_cancelled() {
                    return None;
                }
                let current = receiver.borrow_and_update().clone();
                let mut bytes = serde_json::to_vec(&current.change_state()).ok()?;
                bytes.push(b'\n');
                Some((Ok::<_, std::io::Error>(bytes), (receiver, stopping, false)))
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
        let reader = server.store.clone();
        let job_id = id.clone();
        let changed = match tokio::task::spawn_blocking(move || -> Result<_> {
            let mut store = super::jobs::lock_store(&reader)?;
            if store
                .record::<super::jobs::JobRecord>("job", &job_id)?
                .is_none()
            {
                return Err(YssError::new(ErrorCode::InvalidInput, "执行记录不存在")
                    .context("httpStatus", 404));
            }
            Ok(store.subscribe_job_events(&job_id))
        })
        .await
        {
            Ok(Ok(changed)) => changed,
            Ok(Err(error_value)) => return error(error_value),
            Err(_) => return error(YssError::new(ErrorCode::Io, "作业事件订阅失败")),
        };
        let after = super::api::query_values(&query)
            .get("after")
            .and_then(|value| value.parse::<u64>().ok())
            .unwrap_or(0);
        let stopping = server.shutdown.clone();
        let stream = futures_util::stream::unfold(
            (server.store.clone(), changed, stopping, id, after),
            |(store, changed, stopping, id, mut after)| async move {
                loop {
                    if stopping.is_cancelled() {
                        return None;
                    }
                    // Register before the read so a commit between the read and wait wakes us.
                    let notice = changed.clone().notified_owned();
                    tokio::pin!(notice);
                    notice.as_mut().enable();
                    let reader = store.clone();
                    let job_id = id.clone();
                    let result = tokio::task::spawn_blocking(move || -> Result<_> {
                        let mut store = super::jobs::lock_store(&reader)?;
                        let events = store.events(&job_id, after)?;
                        let terminal = events.is_empty()
                            && store
                                .record::<super::jobs::JobRecord>("job", &job_id)?
                                .is_none_or(|job| job.terminal());
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
                        return Some((
                            Ok::<_, std::io::Error>(bytes),
                            (store, changed, stopping, id, after),
                        ));
                    }
                    if terminal {
                        return None;
                    }
                    tokio::select! {
                        _ = stopping.cancelled() => return None,
                        _ = &mut notice => {}
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
    // Keep request cancellation scoped to the blocking Git/platform commands or draft session.
    let draft = method == axum::http::Method::POST
        && path.starts_with("/api/specs/")
        && path
            .trim_end_matches('/')
            .ends_with("/agent-continuation/draft");
    let request_cancel = (draft
        || (path.starts_with("/api/merge-requests/")
            && matches!(method, axum::http::Method::GET | axum::http::Method::POST)))
    .then(|| server.cancel.child());
    let _request_guard = request_cancel.as_ref().map(Cancellation::cancel_on_drop);
    let worker = tokio::task::spawn_blocking(move || -> Result<serde_json::Value> {
        server.cancel.check()?;
        let limits = Limits {
            bytes: 1024 * 1024,
            ..Limits::manifest()
        };
        let decode_error = |e: YssError| {
            YssError::new(ErrorCode::InvalidInput, "请求内容不合法").context("detail", e.message)
        };
        if matches!(
            path.as_str(),
            "/api/desktop-info"
                | "/api/desktop/activity"
                | "/api/desktop/overview"
                | "/api/desktop/attach"
                | "/api/desktop/detach"
                | "/api/desktop/quiesce"
                | "/api/desktop/release"
                | "/api/desktop/shutdown"
                | "/api/desktop/pause-queues"
        ) {
            return desktop::route(
                &server.platform.jobs,
                &server.platform.reads,
                &server.shutdown,
                method.as_str(),
                &path,
                &bytes,
            );
        }
        if method == axum::http::Method::POST && path == "/api/restart" {
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
            let mut restart = server
                .restart
                .lock()
                .map_err(|_| YssError::new(ErrorCode::Io, "重启状态失效"))?;
            desktop::begin_restart(&server.platform.jobs)?;
            *restart = Some(executable);
            server.shutdown.cancel();
            return Ok(
                json!({"restarting":true,"message":"正在重启工作台，新会话将自动打开，请在新页面恢复草稿"}),
            );
        }
        if let Some(value) = server.platform.route(
            method.as_str(),
            &path,
            &query,
            &bytes,
            request_cancel.as_ref(),
        )? {
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
            let root = crate::fs::Root::open(std::path::Path::new(&project.root_path))?;
            let target =
                super::content::preview_target(spec, meta.as_ref(), &root, &request.target)
                    .ok_or_else(|| super::sources::invalid("只能预览此 Spec 已关联的本地原型"))?;
            serde_json::to_value(super::preview::start(
                &project.root_path,
                &target,
                &server.origin,
                &server.cancel,
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/content")
        {
            serde_json::to_value(super::content::read_store_view(
                &mut *super::jobs::lock_store(&server.store)?,
                &path[11..path.len() - 8],
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/request")
        {
            let mut store = super::jobs::lock_store(&server.store)?;
            let id = &path[11..path.len() - 8];
            serde_json::to_value(service::agent_request_from_store(
                &mut store,
                id,
                &server.cancel,
            )?)
        } else if method == axum::http::Method::GET && path == "/api/snapshot" {
            {
                let mut snapshot = immediate_snapshot(&server, None)?;
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
                &immediate_snapshot(&server, None)?,
                &query,
            )?)
        } else if method == axum::http::Method::GET
            && path.starts_with("/api/specs/")
            && path.ends_with("/lifecycle")
        {
            let id = &path[11..path.len() - 10];
            serde_json::to_value(super::lifecycle::build_lifecycle(
                &immediate_snapshot(&server, Some(id))?,
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
    });
    let result = if draft {
        match tokio::time::timeout(Duration::from_secs(50), worker).await {
            Ok(result) => result,
            Err(_) => {
                return failure(
                    StatusCode::GATEWAY_TIMEOUT,
                    "DRAFT_TIMEOUT",
                    "接续草稿生成超时，请重试",
                );
            }
        }
    } else {
        worker.await
    };
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

async fn observe_workbench(
    store: Arc<Mutex<Store>>,
    reads: Arc<ReadModel>,
    changed: Arc<Notify>,
    stopping: Cancellation,
) {
    let mut last_attempt = None;
    while !stopping.is_cancelled() {
        let assigned = Arc::new(OnceLock::new());
        let active_observation = assigned.clone();
        let (shared, published, cancel) = (store.clone(), reads.clone(), stopping.clone());
        let notification = changed.clone();
        let result = tokio::task::spawn_blocking(move || {
            let (observation_id, stamp, captured) = {
                let store = super::jobs::lock_store(&shared);
                // Order observations by capture, including attempts whose storage check fails.
                let observation_id =
                    *active_observation.get_or_init(read_model::next_observation_id);
                let mut store = store?;
                // All commits known at capture time are included in this read.
                let _ = notification.notified().now_or_never();
                (
                    observation_id,
                    store.projection_stamp(),
                    read_model::capture_observation(&mut store, observation_id),
                )
            };
            if published.refresh_pending() || Some(stamp) != last_attempt {
                published.mark_pending(stamp.0);
            }
            let outcome = captured.and_then(|input| read_model::build(input, &cancel));
            let mut store = super::jobs::lock_store(&shared)?;
            // Checking and publishing under the same lock prevents an older computation
            // from becoming current after a commit.
            if store.projection_stamp() != stamp {
                published.mark_pending(store.state().revision);
                return Ok::<_, YssError>((stamp, None));
            }
            let review_at = match outcome {
                Ok(projection) => {
                    if let Err(error) = store.capture_read_records(&[]) {
                        published.mark_failed(stamp.0, observation_id, &error);
                        None
                    } else {
                        let review_at = projection.next_review_at;
                        published.publish(projection);
                        review_at
                    }
                }
                Err(error) if !cancel.is_cancelled() => {
                    published.mark_failed(stamp.0, observation_id, &error);
                    None
                }
                Err(_) => None,
            };
            Ok((stamp, review_at))
        })
        .await;
        let observation_id = *assigned.get_or_init(read_model::next_observation_id);
        let review_at = match result {
            Ok(Ok((stamp, review_at))) => {
                last_attempt = Some(stamp);
                review_at
            }
            Ok(Err(error)) => {
                reads.mark_failed(
                    last_attempt.map_or(0, |stamp| stamp.0),
                    observation_id,
                    &error,
                );
                None
            }
            Err(_) => {
                let error = YssError::new(ErrorCode::Io, "工作台后台核验异常");
                reads.mark_failed(
                    last_attempt.map_or(0, |stamp| stamp.0),
                    observation_id,
                    &error,
                );
                None
            }
        };
        let interval = Duration::from_secs(30);
        let delay = review_at
            .and_then(|at| Duration::try_from(at - time::OffsetDateTime::now_utc()).ok())
            .map_or(interval, |duration| duration.min(interval));
        tokio::select! {
            _ = stopping.cancelled() => break,
            _ = changed.notified() => {}
            _ = reads.refresh_requested() => {}
            _ = tokio::time::sleep(delay) => {}
        }
    }
}

async fn observe_activity(
    store: Arc<Mutex<Store>>,
    reads: Arc<ReadModel>,
    changed: Arc<Notify>,
    stopping: Cancellation,
) {
    while !stopping.is_cancelled() {
        let (shared, reads) = (store.clone(), reads.clone());
        let notification = changed.clone();
        let result = tokio::task::spawn_blocking(move || {
            let (external, activities, jobs, agent_sessions, agent_queues) = {
                let mut store = super::jobs::lock_store(&shared)?;
                let _ = notification.notified().now_or_never();
                let jobs = store.records::<super::jobs::JobRecord>("job")?;
                let agent_sessions = super::agent::reads::versions(&mut store, &jobs)?;
                let agent_queues = super::agent::reads::queue_versions(&mut store)?;
                (
                    store.capture_read_records(&["externalLink"])?,
                    super::progress_agent::read_activities(&mut store, None)?,
                    jobs,
                    agent_sessions,
                    agent_queues,
                )
            };
            let version = Sha256::of(
                &serde_json::to_vec(&external)
                    .map_err(|_| super::sources::invalid("研发关联版本编码失败"))?,
            );
            reads.publish_activity(
                activities,
                jobs,
                version.as_str().to_owned(),
                agent_sessions,
                agent_queues,
            )
        })
        .await;
        match result {
            Ok(Ok(())) => tokio::select! {
                _ = stopping.cancelled() => break,
                _ = changed.notified() => {},
            },
            _ => tokio::select! {
                _ = stopping.cancelled() => break,
                _ = changed.notified() => {},
                _ = tokio::time::sleep(Duration::from_secs(5)) => {},
            },
        }
    }
}

pub fn serve(
    options: ServeOptions,
    assets: AssetLoader,
    bundle_bytes: &[u8],
    cancel: &Cancellation,
    err: &mut dyn Write,
) -> Result<()> {
    let mut store = Store::open(&resolve_data_dir(Some(&options.data_dir))?)?;
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
        super::registration::scan_startup(&mut store, cancel, err)?;
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
        let projection_changed = store.projection_notification();
        let activity_changed = store.activity_notification();
        let store = Arc::new(Mutex::new(store));
        let jobs = super::jobs::JobManager::new(store.clone())?;
        let queue_dispatch = super::agent::queue::start(jobs.clone(), shutdown.clone())?;
        let progress_tracker =
            super::progress_tracking::start(jobs.clone(), cancel.clone(), shutdown.clone());
        let reads = Arc::new(ReadModel::new());
        let observer = tokio::spawn(observe_workbench(
            store.clone(),
            reads.clone(),
            projection_changed,
            shutdown.clone(),
        ));
        let activity_observer = tokio::spawn(observe_activity(
            store.clone(),
            reads.clone(),
            activity_changed,
            shutdown.clone(),
        ));
        let platform = super::api::PlatformApi {
            jobs: jobs.clone(),
            bundle: Arc::from(bundle_bytes),
            cancel: cancel.clone(),
            origin: origin.clone(),
            reads,
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
        let tracker_shutdown = shutdown.clone();
        let shutdown_jobs = jobs.clone();
        let serve_result = axum::serve(listener, Router::new().fallback(handle).with_state(server))
            .with_graceful_shutdown(async move {
                tokio::select! {
                    _ = stopping.cancelled() => {},
                    _ = shutdown.cancelled() => {}
                }
                shutdown.cancel();
                shutdown_jobs.shutdown();
            })
            .await;
        tracker_shutdown.cancel();
        jobs.shutdown();
        let _ = queue_dispatch.join();
        let _ = progress_tracker.join();
        let _ = observer.await;
        let _ = activity_observer.await;
        serve_result?;
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
