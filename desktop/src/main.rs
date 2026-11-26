#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(target_os = "macos")]
mod macos_navigation;
#[cfg(target_os = "macos")]
mod menu_bar_panel;
mod update;
mod window_placement;

use directories::ProjectDirs;
use hmac::{Hmac, Mac};
use reqwest::blocking::{Client, Response};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::Digest;
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Read,
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};
use subtle::ConstantTimeEq;
use tauri::{
    LogicalPosition, Manager, Webview, WebviewBuilder, WebviewUrl, Window, WindowEvent,
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
    webview::NewWindowResponse,
};
use tauri_plugin_autostart::ManagerExt as AutostartExt;
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_notification::NotificationExt;
use url::Url;

const MAX_RESPONSE: u64 = 1024 * 1024;
const DESKTOP_CONTROL_PATH: &str = "/.yss-desktop/";
static MAIN_VIEW: Mutex<()> = Mutex::new(());
static WORKBENCH_VIEW_ACTIVE: AtomicBool = AtomicBool::new(false);
static WORKBENCH_FROZEN: AtomicBool = AtomicBool::new(false);
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Config {
    last_data_dir: Option<PathBuf>,
    #[serde(default)]
    editors: BTreeMap<PathBuf, String>,
    #[serde(default)]
    data_ids: BTreeMap<PathBuf, String>,
    #[serde(default)]
    data_roots: BTreeMap<PathBuf, yss::fs::RootIdentity>,
    #[serde(default = "default_notifications")]
    notifications: bool,
}
fn default_notifications() -> bool {
    true
}
impl Default for Config {
    fn default() -> Self {
        Self {
            last_data_dir: None,
            editors: BTreeMap::new(),
            data_ids: BTreeMap::new(),
            data_roots: BTreeMap::new(),
            notifications: true,
        }
    }
}
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Session {
    version: u32,
    origin: String,
    token: String,
    #[serde(skip)]
    snapshot: Option<yss::fs::FileSnapshot>,
    #[serde(skip)]
    root: Option<yss::fs::RootIdentity>,
    #[serde(skip)]
    data_dir: PathBuf,
}
struct Connection {
    session: Session,
    instance_id: String,
    client_id: String,
    editor_id: String,
    data_dir: PathBuf,
    child: Option<Child>,
    managed_owner: Option<String>,
}
#[derive(Default)]
struct State {
    connection: Option<Connection>,
    message: String,
    service_version: Option<String>,
    exiting: bool,
    notifications: BTreeMap<i32, (String, String)>,
    update: Option<update::Release>,
    update_message: String,
    update_checking: bool,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Status {
    connected: bool,
    connection_kind: Option<&'static str>,
    platform: &'static str,
    message: String,
    desktop_version: &'static str,
    service_version: Option<String>,
    data_dir: Option<PathBuf>,
    autostart: bool,
    notifications: bool,
    update_message: String,
    update_available: bool,
    update_checking: bool,
}

fn random_id() -> Result<String, String> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).map_err(|_| "无法生成安全随机标识".to_owned())?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
fn project_dirs(name: &str) -> Result<ProjectDirs, String> {
    ProjectDirs::from("com", "ysstech", name).ok_or_else(|| "无法确定个人数据目录".into())
}
fn config_file() -> Result<PathBuf, String> {
    #[cfg(debug_assertions)]
    if let Some(root) = std::env::var_os("YSS_DESKTOP_CONFIG_DIR") {
        let root = PathBuf::from(root);
        if root.is_absolute() {
            return Ok(root.join("desktop.json"));
        }
        return Err("隔离配置目录必须是绝对路径".into());
    }
    Ok(project_dirs("yss-workbench-desktop")?
        .config_dir()
        .join("desktop.json"))
}
fn default_dir() -> Result<PathBuf, String> {
    #[cfg(debug_assertions)]
    if let Some(root) = std::env::var_os("YSS_DESKTOP_DATA_DIR") {
        let root = PathBuf::from(root);
        if root.is_absolute() {
            return Ok(root);
        }
        return Err("隔离数据目录必须是绝对路径".into());
    }
    Ok(project_dirs("yss-workbench")?
        .data_local_dir()
        .to_path_buf())
}
fn read_config() -> Result<Config, String> {
    let path = config_file()?;
    let parent = path.parent().ok_or("桌面设置路径无效")?;
    if !parent.exists() {
        return Ok(Config::default());
    }
    let root = yss::fs::Root::open(parent).map_err(|_| "桌面设置目录不安全")?;
    let name = yss::model::RelPath::new("desktop.json").map_err(|_| "桌面设置路径无效")?;
    match root.read_checked(&name, 64 * 1024) {
        Ok(file) => serde_json::from_slice(&file.bytes)
            .map_err(|_| "桌面设置损坏，请检查设置文件；原文件已保留".into()),
        Err(error)
            if error
                .context
                .get("kind")
                .is_some_and(|kind| kind == "NotFound") =>
        {
            Ok(Config::default())
        }
        Err(_) => Err("无法安全读取桌面设置".into()),
    }
}
fn save_config(config: &Config) -> Result<(), String> {
    save_desktop_file("desktop.json", config)
}
fn save_desktop_file(name: &str, value: &impl Serialize) -> Result<(), String> {
    let path = config_file()?;
    let parent = path.parent().ok_or("桌面设置路径无效")?;
    fs::create_dir_all(parent).map_err(|_| "无法创建桌面设置目录")?;
    let root = yss::fs::Root::open(parent).map_err(|_| "桌面设置目录不安全")?;
    let destination = yss::model::RelPath::new(name).map_err(|_| "桌面设置路径无效")?;
    let temp = yss::model::RelPath::new(format!("desktop-{}.tmp", random_id()?))
        .map_err(|_| "桌面设置路径无效")?;
    let bytes = serde_json::to_vec(value).map_err(|_| "无法编码桌面设置")?;
    let previous = root.inspect(&destination).map_err(|_| "无法检查桌面设置")?;
    if !matches!(
        previous,
        yss::fs::FileSnapshot::Missing | yss::fs::FileSnapshot::File { .. }
    ) {
        return Err("桌面设置路径不安全".into());
    }
    let written = root
        .create_file(&temp, &bytes, 0o600)
        .map_err(|_| "无法保存桌面设置")?;
    if let Err(error) = root.rename(&temp, &destination, &previous, true) {
        if error
            .context
            .get("renameApplied")
            .is_some_and(|value| value == "true")
        {
            let written_now = root
                .read_checked(&destination, 64 * 1024)
                .ok()
                .is_some_and(|file| {
                    file.bytes == bytes && file.snapshot.identity() == written.identity()
                });
            return Err(if written_now {
                "桌面设置已写入，但持久化未确认；请核对后重试".into()
            } else {
                "桌面设置保存结果未知；请核对后重试".into()
            });
        }
        let _ = root.remove(&temp, &written);
        return Err(
            if root.inspect(&destination).ok().as_ref() == Some(&previous) {
                "无法保存桌面设置，原设置已保留".into()
            } else {
                "桌面设置保存结果未知；请核对后重试".into()
            },
        );
    }
    Ok(())
}
fn client() -> Result<Client, String> {
    let tls = rustls::ClientConfig::builder_with_provider(std::sync::Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| "连接配置失败")?
    .with_root_certificates(rustls::RootCertStore::empty())
    .with_no_client_auth();
    Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(2))
        .timeout(Duration::from_secs(5))
        .use_preconfigured_tls(tls)
        .build()
        .map_err(|_| "无法建立本机连接".into())
}
fn json_response(response: Response) -> Result<Value, String> {
    let status = response.status();
    let mut bytes = Vec::new();
    response
        .take(MAX_RESPONSE + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| "读取工作台响应失败")?;
    if bytes.len() as u64 > MAX_RESPONSE {
        return Err("工作台响应过大".into());
    }
    let value: Value = serde_json::from_slice(&bytes).map_err(|_| "工作台响应格式无效")?;
    if !status.is_success() {
        return Err(value
            .pointer("/error/message")
            .and_then(Value::as_str)
            .unwrap_or("工作台拒绝操作")
            .to_owned());
    }
    Ok(value)
}
fn challenge(client: &Client, session: &Session) -> Result<(), String> {
    let nonce = format!("{}{}", random_id()?, random_id()?);
    let url = format!(
        "{}/api/desktop-challenge?nonce={nonce}",
        session.origin.trim_end_matches('/')
    );
    let response = client
        .get(url)
        .send()
        .map_err(|_| "无法连接工作台服务".to_owned())?;
    if matches!(
        response.status(),
        reqwest::StatusCode::UNAUTHORIZED
            | reqwest::StatusCode::FORBIDDEN
            | reqwest::StatusCode::NOT_FOUND
    ) {
        return Err("现有服务未提供桌面协议，请在原工作台处理作业，结束旧服务后重试".into());
    }
    let value = json_response(response)?;
    let proof = value
        .get("proof")
        .and_then(Value::as_str)
        .ok_or("工作台身份验证无效")?;
    if proof.len() != 64
        || !proof
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
    {
        return Err("工作台身份验证无效".into());
    }
    let mut decoded = [0u8; 32];
    for (i, out) in decoded.iter_mut().enumerate() {
        *out =
            u8::from_str_radix(&proof[i * 2..i * 2 + 2], 16).map_err(|_| "工作台身份验证无效")?;
    }
    let mut mac = Hmac::<sha2::Sha256>::new_from_slice(session.token.as_bytes())
        .map_err(|_| "工作台身份验证失败")?;
    mac.update(nonce.as_bytes());
    mac.verify_slice(&decoded)
        .map_err(|_| "工作台身份验证失败，请检查端口和会话".into())
}
fn get(session: &Session, path: &str) -> Result<Value, String> {
    validate_live_session(session)?;
    let client = client()?;
    challenge(&client, session)?;
    let response = client
        .get(format!("{}{}", session.origin.trim_end_matches('/'), path))
        .bearer_auth(&session.token)
        .send()
        .map_err(|_| "无法连接工作台服务".to_owned())?;
    if response.status() == reqwest::StatusCode::NOT_FOUND {
        return Err("当前工作台版本尚不支持桌面连接，请先通过原入口处理运行中的作业并升级".into());
    }
    json_response(response)
}
fn post(session: &Session, path: &str, body: Value) -> Result<Value, String> {
    validate_live_session(session)?;
    let client = client()?;
    challenge(&client, session)?;
    let origin = session.origin.trim_end_matches('/');
    let response = client
        .post(format!("{origin}{path}"))
        .bearer_auth(&session.token)
        .header(reqwest::header::ORIGIN, origin)
        .json(&body)
        .send()
        .map_err(|_| "无法连接工作台服务".to_owned())?;
    json_response(response)
}
fn read_session(data_dir: &Path) -> Result<Option<Session>, String> {
    if !data_dir.exists() {
        return Ok(None);
    }
    let root = yss::fs::Root::open(data_dir).map_err(|_| "工作台数据目录不安全或不可访问")?;
    let path = yss::model::RelPath::new("session.json").map_err(|_| "工作台会话路径无效")?;
    let checked = match root.read_private_checked(&path, 4096) {
        Ok(value) => value,
        Err(error)
            if error
                .context
                .get("kind")
                .is_some_and(|kind| kind == "NotFound") =>
        {
            return Ok(None);
        }
        Err(_) => return Err("工作台会话文件权限不安全或不可读取".into()),
    };
    let mut session: Session =
        serde_json::from_slice(&checked.bytes).map_err(|_| "工作台会话无效")?;
    let origin = Url::parse(&session.origin).map_err(|_| "工作台地址无效")?;
    if session.version != 1
        || origin.scheme() != "http"
        || origin.host_str() != Some("127.0.0.1")
        || origin.port().is_none()
        || origin.path() != "/"
        || origin.query().is_some()
        || origin.fragment().is_some()
        || !origin.username().is_empty()
        || origin.password().is_some()
        || session.token.len() != 64
        || !session.token.bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err("工作台会话无效".into());
    }
    root.revalidate().map_err(|_| "工作台目录发生变化")?;
    session.snapshot = Some(checked.snapshot);
    session.root = Some(root.identity.clone());
    session.data_dir = data_dir.to_path_buf();
    Ok(Some(session))
}
fn validate_live_session(session: &Session) -> Result<(), String> {
    if read_session(&session.data_dir)?.as_ref() != Some(session) {
        return Err("工作台会话发生变化，请重新连接".into());
    }
    Ok(())
}
fn read_data_id(data_dir: &Path) -> Result<String, String> {
    let root = yss::fs::Root::open(data_dir).map_err(|_| "工作台数据目录不安全或不可访问")?;
    let path = yss::model::RelPath::new("state.json").map_err(|_| "工作台存储标识无效")?;
    let checked = root
        .read_checked(&path, 16 * 1024)
        .map_err(|_| "无法读取工作台存储标识")?;
    let marker: Value = serde_json::from_slice(&checked.bytes).map_err(|_| "工作台存储标识无效")?;
    let expected = marker
        .get("databaseId")
        .and_then(Value::as_str)
        .ok_or("工作台存储标识缺失")?
        .to_owned();
    root.revalidate().map_err(|_| "工作台目录发生变化")?;
    Ok(expected)
}
fn check_info(session: &Session) -> Result<Value, String> {
    let info = get(session, "/api/desktop-info")?;
    if info.get("protocol").and_then(Value::as_u64) != Some(1)
        || !info
            .get("instanceId")
            .and_then(Value::as_str)
            .is_some_and(|v| !v.is_empty())
        || ![
            "attachment-v1",
            "quiesce-v1",
            "queue-pause-v1",
            "managed-shutdown-v1",
            "challenge-v1",
            "flush-v1",
            "notification-v1",
        ]
        .iter()
        .all(|wanted| {
            info.get("capabilities")
                .and_then(Value::as_array)
                .is_some_and(|items| items.iter().any(|v| v.as_str() == Some(wanted)))
        })
    {
        return Err("现有工作台未提供兼容的桌面协议；请先处理活动作业并升级".into());
    }
    Ok(info)
}
fn sidecar_path() -> Result<PathBuf, String> {
    let current = std::env::current_exe().map_err(|_| "无法确定桌面程序位置")?;
    let name = if cfg!(windows) { "yss.exe" } else { "yss" };
    let path = current.parent().ok_or("桌面程序位置无效")?.join(name);
    if path.is_file() {
        return Ok(path);
    }
    #[cfg(debug_assertions)]
    {
        let path = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(format!(
                "yss-{}{}",
                env!("YSS_DESKTOP_TARGET"),
                if cfg!(windows) { ".exe" } else { "" }
            ));
        if path.is_file() {
            return Ok(path);
        }
    }
    Err("桌面应用缺少工作台服务程序".into())
}
fn show_settings(app: &tauri::AppHandle) -> Result<(), String> {
    open_desktop_settings(app, false)
}

fn main_window(app: &tauri::AppHandle) -> tauri::Result<Window> {
    if let Some(window) = app.get_window("workbench") {
        return Ok(window);
    }
    let window = Window::builder(app, "workbench")
        .title("YSS 工作台")
        .inner_size(
            window_placement::DEFAULT_SIZE.width,
            window_placement::DEFAULT_SIZE.height,
        )
        .min_inner_size(
            window_placement::MINIMUM_SIZE.width,
            window_placement::MINIMUM_SIZE.height,
        )
        .background_color(tauri::window::Color(237, 240, 248, 255))
        .visible(false)
        .build()?;
    let _ = window_placement::restore(&window);
    Ok(window)
}

fn show_main_view(window: &Window, view: &Webview) -> tauri::Result<()> {
    view.show()?;
    for sibling in window.webviews() {
        if sibling.label() != view.label() {
            if sibling.label() == "settings" {
                let _ = sibling.eval("window.dispatchEvent(new Event('yss:settings-hidden'));");
            }
            sibling.hide()?;
        }
    }
    if matches!(window.is_visible(), Ok(false)) {
        let _ = window_placement::keep_on_screen(window);
    }
    window.show()?;
    if window.is_minimized()? {
        window.unminimize()?;
    }
    window.set_focus()?;
    view.set_focus()?;
    WORKBENCH_VIEW_ACTIVE.store(view.label() == "workbench", Ordering::Relaxed);
    Ok(())
}

fn open_desktop_settings(app: &tauri::AppHandle, show_updates: bool) -> Result<(), String> {
    const UPDATE_SECTION: &str = "window.__yssShowUpdateSettings = true; window.dispatchEvent(new Event('yss:settings-update'));";
    let _layout = MAIN_VIEW.lock().map_err(|_| "主窗口不可用")?;
    let window = main_window(app).map_err(|_| "无法创建主窗口")?;
    let settings = match app.get_webview("settings") {
        Some(view) => view,
        None => {
            let mut builder = WebviewBuilder::new("settings", WebviewUrl::App("index.html".into()))
                .auto_resize()
                .background_color(tauri::window::Color(237, 240, 248, 255))
                .on_navigation(|url| {
                    matches!(
                        (url.scheme(), url.host_str()),
                        ("tauri", Some("localhost")) | ("http", Some("tauri.localhost"))
                    ) && matches!(url.path(), "" | "/" | "/index.html")
                })
                .on_new_window(|_, _| NewWindowResponse::Deny);
            if show_updates {
                builder = builder.initialization_script(UPDATE_SECTION);
            }
            window
                .add_child(
                    builder,
                    LogicalPosition::new(0, 0),
                    window.inner_size().map_err(|_| "无法读取主窗口尺寸")?,
                )
                .map_err(|_| "无法加载桌面设置")?
        }
    };
    show_main_view(&window, &settings).map_err(|_| "无法显示桌面设置")?;
    let _ = settings.eval("window.dispatchEvent(new Event('yss:settings-shown'));");
    if show_updates {
        let _ = settings.eval(UPDATE_SECTION);
    }
    Ok(())
}
fn reopen_workbench(app: tauri::AppHandle) {
    thread::spawn(move || {
        let result = {
            let state = app.state::<Mutex<State>>();
            match state.lock() {
                Ok(state) => match state.connection.as_ref() {
                    Some(conn) => open_window(&app, conn, true),
                    None => show_settings(&app),
                },
                Err(_) => Err("桌面状态不可用".into()),
            }
        };
        if let Err(error) = result {
            if let Ok(mut state) = app.state::<Mutex<State>>().lock() {
                state.message = error;
            }
            let _ = show_settings(&app);
        }
    });
}
fn handle_desktop_control(app: &tauri::AppHandle, target: &Url, settings_url: &Url) -> bool {
    if target.origin() != settings_url.origin() || !target.path().starts_with(DESKTOP_CONTROL_PATH)
    {
        return false;
    }
    if target == settings_url && !WORKBENCH_FROZEN.load(Ordering::SeqCst) {
        let app = app.clone();
        // Creating a WebView during a synchronous navigation callback may block the event loop.
        thread::spawn(move || {
            let _ = show_settings(&app);
        });
    }
    true
}
fn open_window(
    app: &tauri::AppHandle,
    connection: &Connection,
    visible: bool,
) -> Result<(), String> {
    let _layout = MAIN_VIEW.lock().map_err(|_| "主窗口不可用")?;
    let window = main_window(app).map_err(|_| "无法创建主窗口")?;
    if let Some(view) = app.get_webview("workbench") {
        if visible {
            show_main_view(&window, &view).map_err(|_| "无法显示工作台")?;
        }
        return Ok(());
    }
    let mut url = Url::parse(&connection.session.origin).map_err(|_| "工作台地址无效")?;
    url.query_pairs_mut()
        .append_pair("desktopEditor", &connection.editor_id);
    url.set_fragment(Some(&format!("token={}", connection.session.token)));
    let allowed_origin = url.origin();
    let settings_url = url
        .join(&format!("{DESKTOP_CONTROL_PATH}settings/{}", random_id()?))
        .map_err(|_| "桌面设置地址无效")?;
    // Keep the control link in the workbench's top document and accept only its exact URL.
    let init_settings_link = format!(
        "if (window === window.top && location.origin === {} && location.pathname === '/') {{\
          Object.defineProperty(window, '__YSS_DESKTOP_SETTINGS_URL__', {{ value: {} }});\
        }}",
        json!(allowed_origin.ascii_serialization()),
        json!(settings_url.as_str()),
    );
    let navigation_app = app.clone();
    let navigation_settings_url = settings_url.clone();
    let new_window_app = app.clone();
    let builder = WebviewBuilder::new("workbench", WebviewUrl::External(url))
        .auto_resize()
        .initialization_script(init_settings_link)
        .on_navigation(move |target| {
            if handle_desktop_control(&navigation_app, target, &navigation_settings_url) {
                return false;
            }
            if WORKBENCH_FROZEN.load(Ordering::SeqCst) {
                return false;
            }
            if target.origin() == allowed_origin {
                return true;
            }
            if matches!(target.scheme(), "http" | "https") {
                let _ = webbrowser::open(target.as_str());
            }
            false
        })
        .on_new_window(move |target, _features| {
            if !handle_desktop_control(&new_window_app, &target, &settings_url)
                && !WORKBENCH_FROZEN.load(Ordering::SeqCst)
                && matches!(target.scheme(), "http" | "https")
            {
                let _ = webbrowser::open(target.as_str());
            }
            NewWindowResponse::Deny
        });
    let view = window
        .add_child(
            builder,
            LogicalPosition::new(0, 0),
            window.inner_size().map_err(|_| "无法读取主窗口尺寸")?,
        )
        .map_err(|_| "无法加载工作台")?;
    #[cfg(target_os = "macos")]
    macos_navigation::install(&view).map_err(|_| "无法设置工作台导航保护")?;
    if visible {
        show_main_view(&window, &view).map_err(|_| "无法显示工作台")?;
    }
    Ok(())
}
fn flush(app: &tauri::AppHandle, conn: &Connection) -> Result<(), String> {
    let window = app
        .get_webview("workbench")
        .ok_or("工作台尚未就绪，无法确认草稿；请重试")?;
    let request = random_id()?;
    window.eval(format!("window.dispatchEvent(new CustomEvent('yss:desktop-flush',{{detail:{{requestId:'{request}'}}}}))")).map_err(|_| "无法确认工作台草稿")?;
    let until = Instant::now() + Duration::from_secs(10);
    while Instant::now() < until {
        if let Ok(value) = get(&conn.session, &format!("/api/desktop/flush/{request}"))
            && value.get("found").and_then(Value::as_bool) == Some(true)
        {
            if value.get("editorId").and_then(Value::as_str) != Some(&conn.editor_id)
                || value.get("ok").and_then(Value::as_bool) != Some(true)
            {
                return Err("草稿未能保存，请回到工作台处理".into());
            }
            return Ok(());
        }
        thread::sleep(Duration::from_millis(200));
    }
    Err("等待草稿保存超时，请回到工作台重试".into())
}
struct FrozenWorkbench {
    window: Webview,
    request: String,
}
impl Drop for FrozenWorkbench {
    fn drop(&mut self) {
        let _ = self.window.eval(format!(
            r#"(() => {{
          const freeze = window.__yssDesktopFreeze;
          if (freeze && freeze.request === '{}') {{
            for (const type of freeze.events) window.removeEventListener(type, freeze.block, true);
            freeze.body.inert = freeze.wasInert;
            freeze.overlay.remove();
            delete window.__yssDesktopFreeze;
            if (freeze.focus && freeze.focus.isConnected) freeze.focus.focus();
          }}
        }})()"#,
            self.request
        ));
        WORKBENCH_FROZEN.store(false, Ordering::SeqCst);
    }
}

fn freeze_and_flush(app: &tauri::AppHandle, conn: &Connection) -> Result<FrozenWorkbench, String> {
    let window = app
        .get_webview("workbench")
        .ok_or("工作台尚未就绪，无法确认草稿；请重试")?;
    if WORKBENCH_FROZEN
        .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
        .is_err()
    {
        return Err("工作台正在进行其他协调操作，请稍后重试".into());
    }
    let request = match random_id() {
        Ok(request) => request,
        Err(error) => {
            WORKBENCH_FROZEN.store(false, Ordering::SeqCst);
            return Err(error);
        }
    };
    let guard = FrozenWorkbench { window, request };
    guard.window.eval(format!(r#"(() => {{
      if (!document.body || window.__yssDesktopFreeze || !('inert' in document.body)) return;
      const body = document.body, focus = document.activeElement;
      const overlay = document.createElement('div');
      overlay.textContent = '正在保存草稿并协调工作台，请稍候…';
      overlay.setAttribute('role', 'status');
      overlay.setAttribute('tabindex', '-1');
      Object.assign(overlay.style, {{position:'fixed',inset:'0',zIndex:'2147483647',display:'grid',placeItems:'center',background:'rgba(247,248,250,.8)',color:'#25272b',font:'500 14px system-ui'}});
      document.documentElement.appendChild(overlay);
      const events = ['keydown','keypress','keyup','beforeinput','compositionstart','paste','cut','drop'];
      const block = event => {{event.preventDefault(); event.stopImmediatePropagation();}};
      for (const type of events) window.addEventListener(type, block, true);
      window.__yssDesktopFreeze = {{request:'{0}',body,focus,overlay,events,block,wasInert:body.inert}};
      if (focus && typeof focus.blur === 'function') focus.blur();
      body.inert = true;
      overlay.focus();
      window.dispatchEvent(new CustomEvent('yss:desktop-flush',{{detail:{{requestId:'{0}'}}}}));
    }})()"#, guard.request)).map_err(|_| "无法冻结工作台并确认草稿")?;
    let until = Instant::now() + Duration::from_secs(10);
    while Instant::now() < until {
        if let Ok(value) = get(
            &conn.session,
            &format!("/api/desktop/flush/{}", guard.request),
        ) && value.get("found").and_then(Value::as_bool) == Some(true)
        {
            if value.get("editorId").and_then(Value::as_str) != Some(&conn.editor_id)
                || value.get("ok").and_then(Value::as_bool) != Some(true)
            {
                return Err("草稿未能保存，请回到工作台处理".into());
            }
            return Ok(guard);
        }
        thread::sleep(Duration::from_millis(200));
    }
    Err("等待草稿保存超时，请回到工作台重试".into())
}

fn detach_or_stop(conn: &mut Connection) -> Result<(), String> {
    if conn.child.is_some() {
        let lease = post(
            &conn.session,
            "/api/desktop/quiesce",
            json!({"clientId":conn.client_id}),
        )?;
        let lease_id = lease
            .get("leaseId")
            .and_then(Value::as_str)
            .ok_or("工作台未返回有效协调租约")?;
        if let Err(error) = post(
            &conn.session,
            "/api/desktop/shutdown",
            json!({"clientId":conn.client_id,"leaseId":lease_id,"managedOwner":conn.managed_owner}),
        ) {
            let _ = post(
                &conn.session,
                "/api/desktop/release",
                json!({"clientId":conn.client_id,"leaseId":lease_id}),
            );
            return Err(error);
        }
        let until = Instant::now() + Duration::from_secs(10);
        while Instant::now() < until {
            if conn
                .child
                .as_mut()
                .and_then(|child| child.try_wait().ok())
                .flatten()
                .is_some()
            {
                return Ok(());
            }
            thread::sleep(Duration::from_millis(100));
        }
        return Err("工作台正在停止，请稍后重试；未强制终止进程".into());
    }
    post(
        &conn.session,
        "/api/desktop/detach",
        json!({"clientId":conn.client_id}),
    )?;
    Ok(())
}
fn stop_jobs(conn: &Connection) -> Result<(), String> {
    post(
        &conn.session,
        "/api/desktop/pause-queues",
        json!({"clientId":conn.client_id}),
    )?;
    let jobs = get(&conn.session, "/api/jobs")?;
    let jobs = jobs.as_array().ok_or("工作台作业列表无效")?;
    for job in jobs {
        let status = job.get("status").and_then(Value::as_str).unwrap_or("");
        if matches!(
            status,
            "completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired"
        ) {
            continue;
        }
        let id = job
            .get("id")
            .and_then(Value::as_str)
            .ok_or("工作台作业标识无效")?;
        if id.is_empty() || !id.bytes().all(|c| c.is_ascii_hexdigit()) {
            return Err("工作台作业标识无效".into());
        }
        post(&conn.session, &format!("/api/jobs/{id}/cancel"), json!({}))?;
    }
    let until = Instant::now() + Duration::from_secs(15);
    while Instant::now() < until {
        let jobs = get(&conn.session, "/api/jobs")?;
        let jobs = jobs.as_array().ok_or("工作台作业列表无效")?;
        if jobs.iter().all(|job| {
            matches!(
                job.get("status").and_then(Value::as_str),
                Some("completed" | "failed" | "canceled" | "interrupted" | "recoveryRequired")
            )
        }) {
            return Ok(());
        }
        thread::sleep(Duration::from_millis(200));
    }
    Err("作业仍在停止，请稍后重试；未强制终止服务".into())
}
fn cleanup_staged(conn: &mut Connection) -> bool {
    if conn.child.is_some() {
        if conn
            .child
            .as_mut()
            .and_then(|child| child.try_wait().ok())
            .flatten()
            .is_some()
        {
            return true;
        }
        if let Ok(lease) = post(
            &conn.session,
            "/api/desktop/quiesce",
            json!({"clientId":conn.client_id}),
        ) && let Some(lease_id) = lease.get("leaseId").and_then(Value::as_str)
        {
            if post(&conn.session, "/api/desktop/shutdown", json!({"clientId":conn.client_id,"leaseId":lease_id,"managedOwner":conn.managed_owner})).is_ok() {
                    let until = Instant::now() + Duration::from_secs(5);
                    while Instant::now() < until {
                        if conn.child.as_mut().and_then(|child| child.try_wait().ok()).flatten().is_some() { return true; }
                        thread::sleep(Duration::from_millis(100));
                    }
                    return false;
                }
            let _ = post(
                &conn.session,
                "/api/desktop/release",
                json!({"clientId":conn.client_id,"leaseId":lease_id}),
            );
        }
    }
    let detached = post(
        &conn.session,
        "/api/desktop/detach",
        json!({"clientId":conn.client_id}),
    )
    .is_ok();
    conn.child.is_none() && detached
}
fn owner_matches(info: &Value, owner: &str) -> bool {
    let Some(instance) = info.get("instanceId").and_then(Value::as_str) else {
        return false;
    };
    let expected =
        sha2::Sha256::digest(format!("yss-desktop-owner/1:{owner}:{instance}").as_bytes());
    let expected: String = expected.iter().map(|byte| format!("{byte:02x}")).collect();
    let actual = info
        .get("managedOwnerProof")
        .and_then(Value::as_str)
        .unwrap_or("");
    actual.len() == expected.len() && bool::from(actual.as_bytes().ct_eq(expected.as_bytes()))
}
// Never kill an unverified or busy process. A successful quiesce is the only
// authority for stopping the process we spawned but could not finish attaching.
fn cleanup_failed_spawn(child: &mut Child, data_dir: &Path, owner: &str) -> bool {
    if child.try_wait().ok().flatten().is_some() {
        return true;
    }
    let attempt = (|| -> Result<(), String> {
        let session = read_session(data_dir)?.ok_or("会话尚未就绪")?;
        let info = check_info(&session)?;
        if !owner_matches(&info, owner) {
            return Err("服务所有权无法确认".into());
        }
        let client_id = random_id()?;
        post(
            &session,
            "/api/desktop/attach",
            json!({"clientId":client_id,"managedOwner":owner}),
        )?;
        let lease = match post(
            &session,
            "/api/desktop/quiesce",
            json!({"clientId":client_id}),
        ) {
            Ok(lease) => lease,
            Err(error) => {
                let _ = post(
                    &session,
                    "/api/desktop/detach",
                    json!({"clientId":client_id}),
                );
                return Err(error);
            }
        };
        let lease_id = lease
            .get("leaseId")
            .and_then(Value::as_str)
            .ok_or("协调租约无效")?;
        if let Err(error) = post(
            &session,
            "/api/desktop/shutdown",
            json!({"clientId":client_id,"leaseId":lease_id,"managedOwner":owner}),
        ) {
            let _ = post(
                &session,
                "/api/desktop/release",
                json!({"clientId":client_id,"leaseId":lease_id}),
            );
            let _ = post(
                &session,
                "/api/desktop/detach",
                json!({"clientId":client_id}),
            );
            return Err(error);
        }
        Ok(())
    })();
    if attempt.is_err() {
        return child.try_wait().ok().flatten().is_some();
    }
    let until = Instant::now() + Duration::from_secs(5);
    while Instant::now() < until {
        if child.try_wait().ok().flatten().is_some() {
            return true;
        }
        thread::sleep(Duration::from_millis(100));
    }
    false
}
fn connect(
    app: &tauri::AppHandle,
    requested: Option<PathBuf>,
    visible: bool,
) -> Result<(), String> {
    let state = app.state::<Mutex<State>>();
    let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
    let mut config = read_config()?;
    let remembered = config.last_data_dir.is_some();
    let dir = requested
        .clone()
        .or_else(|| config.last_data_dir.clone())
        .unwrap_or(default_dir()?);
    if !dir.is_absolute() {
        return Err("数据目录必须是绝对路径".into());
    }
    let exists = dir.is_dir();
    if !exists && (remembered || requested.is_some()) {
        return Err("所选工作台目录不可用；请重试或选择已有目录".into());
    }
    if requested.is_some()
        && (!dir.join("state.json").is_file() || !dir.join("workbench.sqlite3").is_file())
    {
        return Err("请选择已有的有效工作台数据目录".into());
    }
    let canonical = if exists {
        fs::canonicalize(&dir).map_err(|_| "无法打开数据目录")?
    } else {
        dir.clone()
    };
    let initial_root = if exists {
        Some(
            yss::fs::Root::open(&canonical)
                .map_err(|_| "工作台数据目录不安全或不可访问")?
                .identity,
        )
    } else {
        None
    };
    if exists && requested.is_none() && remembered {
        match (
            config.data_roots.get(&canonical),
            config.data_ids.get(&canonical),
        ) {
            (Some(expected), _) if initial_root.as_ref() != Some(expected) => {
                return Err("工作台数据目录身份已变化；请核对后重新选择".into());
            }
            (None, None) => return Err("无法确认先前工作台目录身份；请选择已有目录以继续".into()),
            _ => {}
        }
    }
    if exists
        && requested.is_none()
        && config.data_ids.get(&canonical).is_some_and(|expected| {
            read_data_id(&canonical).map_or(true, |actual| &actual != expected)
        })
    {
        return Err("工作台数据目录身份已变化；请核对后重新选择".into());
    }
    if let Some(current) = state
        .connection
        .as_ref()
        .filter(|c| c.data_dir == canonical)
    {
        let live = check_info(&current.session).is_ok_and(|info| {
            info.get("instanceId").and_then(Value::as_str) == Some(&current.instance_id)
        });
        if !live {
            let message = "原工作台连接已中断，当前页面已保留；请先返回核对未保存的内容";
            state.message = message.into();
            return Err(message.into());
        }
        open_window(app, current, visible)?;
        state.message = "工作台已连接".into();
        return Ok(());
    }
    let editor_id = match config.editors.get(&canonical) {
        Some(value)
            if value.len() == 32
                && value
                    .bytes()
                    .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase()) =>
        {
            value.clone()
        }
        _ => random_id()?,
    };
    let mut owned: Option<Child> = None;
    let preexisting = if exists {
        read_session(&canonical)?
    } else {
        None
    };
    let initial_info = match preexisting.as_ref() {
        Some(session) => match check_info(session) {
            Ok(info) => Some(info),
            Err(e) if e == "无法连接工作台服务" => None,
            Err(e) => return Err(e),
        },
        None => None,
    };
    let managed_owner = random_id()?;
    if initial_info.is_none() {
        let path = sidecar_path()?;
        let mut command = Command::new(path);
        #[cfg(target_os = "macos")]
        yss::workbench::process::host::apply_macos_login_environment(&mut command);
        command
            .args(["workbench", "--no-open", "--port", "0", "--data-dir"])
            .arg(&canonical)
            .env("YSS_DESKTOP_MANAGED", "1")
            .env("YSS_DESKTOP_INSTANCE", &managed_owner)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0800_0000);
        }
        owned = Some(command.spawn().map_err(|_| "无法启动工作台服务")?);
    }
    let setup = (|| -> Result<(Session, Value, String, String, String), String> {
        let until = Instant::now() + Duration::from_secs(15);
        let (session, info) = loop {
            if let Some(session) = read_session(&canonical)? {
                match check_info(&session) {
                    Ok(info) => break (session, info),
                    Err(e) if e == "无法连接工作台服务" => {}
                    Err(e) => return Err(e),
                }
            }
            if Instant::now() >= until {
                return Err("工作台未能就绪；请检查已有服务或重试".into());
            }
            thread::sleep(Duration::from_millis(250));
        };
        let data_id = read_data_id(&canonical)?;
        if info.get("dataId").and_then(Value::as_str) != Some(data_id.as_str()) {
            return Err("工作台服务与所选数据目录不一致".into());
        }
        if read_session(&canonical)? != Some(session.clone()) {
            return Err("工作台会话发生变化，请重试".into());
        }
        let instance_id = info
            .get("instanceId")
            .and_then(Value::as_str)
            .ok_or("工作台实例标识无效")?
            .to_owned();
        if owned.is_some() && !owner_matches(&info, &managed_owner) {
            let child = owned.as_mut().expect("checked");
            let until = Instant::now() + Duration::from_secs(2);
            while Instant::now() < until
                && child
                    .try_wait()
                    .map_err(|_| "无法检查工作台进程")?
                    .is_none()
            {
                thread::sleep(Duration::from_millis(100));
            }
            if child
                .try_wait()
                .map_err(|_| "无法检查工作台进程")?
                .is_none()
            {
                return Err("检测到其他工作台，但本次启动进程状态未知".into());
            }
            owned = None;
        }
        if owned
            .as_mut()
            .is_some_and(|child| child.try_wait().ok().flatten().is_some())
        {
            owned = None;
        }
        let client_id = random_id()?;
        let attach = if owned.is_some() {
            json!({"clientId":client_id,"managedOwner":managed_owner})
        } else {
            json!({"clientId":client_id})
        };
        let response = post(&session, "/api/desktop/attach", attach)?;
        if owned.is_some() && response.get("owner").and_then(Value::as_bool) != Some(true) {
            return Err("工作台未确认本次桌面进程所有权".into());
        }
        Ok((session, info, data_id, instance_id, client_id))
    })();
    let (session, info, data_id, instance_id, client_id) = match setup {
        Ok(value) => value,
        Err(error) => {
            if let Some(child) = owned.as_mut()
                && !cleanup_failed_spawn(child, &canonical, &managed_owner)
            {
                return Err(format!(
                    "{error}；启动进程状态未知，已保留进程，请先检查工作台再重试"
                ));
            }
            return Err(error);
        }
    };
    let managed_owner = owned.as_ref().map(|_| managed_owner);
    let mut conn = Connection {
        session,
        instance_id,
        client_id,
        editor_id: editor_id.clone(),
        data_dir: canonical.clone(),
        child: owned,
        managed_owner,
    };
    let connected_root = match yss::fs::Root::open(&canonical) {
        Ok(root) => root.identity,
        Err(_) => {
            let cleaned = cleanup_staged(&mut conn);
            return Err(if cleaned {
                "工作台数据目录不安全或不可访问".into()
            } else {
                "工作台数据目录不可访问；启动进程状态未知，已保留，请检查后重试".into()
            });
        }
    };
    if initial_root
        .as_ref()
        .is_some_and(|identity| identity != &connected_root)
    {
        let cleaned = cleanup_staged(&mut conn);
        return Err(if cleaned {
            "工作台目录身份发生变化，请重新选择".into()
        } else {
            "工作台目录身份发生变化；启动进程状态未知，已保留，请检查后重试".into()
        });
    }
    let freeze = if let Some(current) = state.connection.as_ref() {
        match freeze_and_flush(app, current) {
            Ok(guard) => Some(guard),
            Err(error) => {
                if !cleanup_staged(&mut conn) {
                    return Err(format!(
                        "{error}；新启动的工作台状态未知，已保留，请检查后重试"
                    ));
                }
                return Err(error);
            }
        }
    } else {
        None
    };
    if let Some(current) = state.connection.as_mut() {
        if let Err(error) = detach_or_stop(current) {
            if !cleanup_staged(&mut conn) {
                return Err(format!(
                    "{error}；新启动的工作台状态未知，已保留，请检查后重试"
                ));
            }
            return Err(error);
        }
        if let Some(view) = app.get_webview("workbench")
            && view.close().is_err()
        {
            let cleaned = cleanup_staged(&mut conn);
            state.connection = None;
            return Err(if cleaned {
                "无法关闭原工作台窗口，请重试".into()
            } else {
                "无法关闭原工作台窗口；新工作台状态未知，已保留，请检查后重试".into()
            });
        }
    }
    config.last_data_dir = Some(canonical.clone());
    config.editors.insert(canonical.clone(), editor_id);
    config.data_ids.insert(canonical.clone(), data_id);
    config.data_roots.insert(canonical, connected_root);
    if let Err(error) = save_config(&config) {
        let cleaned = cleanup_staged(&mut conn);
        state.connection = None;
        state.message = "桌面设置未能确认，请核对后重新连接工作台".into();
        return Err(if cleaned {
            error
        } else {
            format!("{error}；新工作台状态未知，已保留，请检查后重试")
        });
    }
    state.connection = Some(conn);
    state.service_version = info
        .pointer("/build/version")
        .and_then(Value::as_str)
        .map(str::to_owned);
    state.message = "工作台已连接".into();
    drop(freeze);
    open_window(
        app,
        state.connection.as_ref().expect("connection set"),
        visible,
    )?;
    Ok(())
}
#[tauri::command]
async fn desktop_status(app: tauri::AppHandle) -> Result<Status, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let state = state.lock().map_err(|_| "桌面状态不可用")?;
        Ok(Status {
            connected: state.connection.is_some(),
            connection_kind: state.connection.as_ref().map(|conn| {
                if conn.child.is_some() {
                    "owned"
                } else {
                    "external"
                }
            }),
            platform: std::env::consts::OS,
            message: if state.message.is_empty() {
                "尚未连接工作台".into()
            } else {
                state.message.clone()
            },
            desktop_version: env!("CARGO_PKG_VERSION"),
            service_version: state.service_version.clone(),
            data_dir: state.connection.as_ref().map(|c| c.data_dir.clone()),
            autostart: app.autolaunch().is_enabled().unwrap_or(false),
            notifications: read_config().map(|c| c.notifications).unwrap_or(false),
            update_message: state.update_message.clone(),
            update_available: state.update.is_some(),
            update_checking: state.update_checking,
        })
    })
    .await
    .map_err(|_| "无法读取桌面状态")?
}

#[derive(Serialize, Deserialize)]
struct MenuBarActivity {
    running: u64,
    attention: u64,
    queued: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct MenuBarSnapshot {
    connection: &'static str,
    service_version: Option<String>,
    activity: Option<MenuBarActivity>,
    message: Option<&'static str>,
}

#[tauri::command]
async fn menubar_snapshot(window: tauri::WebviewWindow) -> Result<MenuBarSnapshot, String> {
    if window.label() != "menu-bar" {
        return Err("菜单栏面板不可用".into());
    }
    let app = window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        let current = {
            let state = app.state::<Mutex<State>>();
            let state = state
                .try_lock()
                .map_err(|_| "工作台正在处理其他操作，请稍后刷新")?;
            state.connection.as_ref().map(|conn| {
                let kind = if conn.child.is_some() {
                    "owned"
                } else {
                    "external"
                };
                (conn.session.clone(), kind, state.service_version.clone())
            })
        };
        let Some((session, connection, service_version)) = current else {
            return Ok(MenuBarSnapshot {
                connection: "disconnected",
                service_version: None,
                activity: None,
                message: Some("在桌面设置中连接工作台。"),
            });
        };
        let activity = get(&session, "/api/desktop/activity")
            .ok()
            .and_then(|value| serde_json::from_value(value).ok());
        let state = app.state::<Mutex<State>>();
        let current = state
            .try_lock()
            .map_err(|_| "工作台正在处理其他操作，请稍后刷新")?;
        if !current
            .connection
            .as_ref()
            .is_some_and(|conn| conn.session == session)
        {
            return Err("工作台连接已变化，请刷新状态".into());
        }
        Ok(MenuBarSnapshot {
            connection,
            service_version,
            message: activity.is_none().then_some("暂时无法读取作业状态。"),
            activity,
        })
    })
    .await
    .map_err(|_| "无法读取菜单栏状态")?
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
enum MenuBarAction {
    Workbench,
    Settings,
    Exit,
    Dismiss,
}

#[tauri::command]
async fn menubar_action(window: tauri::WebviewWindow, action: MenuBarAction) -> Result<(), String> {
    if window.label() != "menu-bar" {
        return Err("菜单栏面板不可用".into());
    }
    let app = window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move || {
        #[cfg(target_os = "macos")]
        menu_bar_panel::hide(&app).map_err(|_| "无法收起菜单栏面板")?;
        match action {
            MenuBarAction::Workbench => {
                reopen_workbench(app);
                Ok(())
            }
            MenuBarAction::Settings => show_settings(&app),
            MenuBarAction::Exit => open_desktop_settings(&app, true),
            MenuBarAction::Dismiss => Ok(()),
        }
    })
    .await
    .map_err(|_| "无法执行菜单栏操作")?
}

#[tauri::command]
async fn desktop_connect(app: tauri::AppHandle) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || connect(&app, None, true))
        .await
        .map_err(|_| "连接任务失败")?
}
#[tauri::command]
async fn desktop_select_directory(app: tauri::AppHandle) -> Result<(), String> {
    let selected = app.dialog().file().blocking_pick_folder();
    let Some(selected) = selected else {
        return Ok(());
    };
    let path = selected.into_path().map_err(|_| "所选路径无效")?;
    tauri::async_runtime::spawn_blocking(move || connect(&app, Some(path), true))
        .await
        .map_err(|_| "切换任务失败")?
}
#[tauri::command]
async fn desktop_pause_queues(app: tauri::AppHandle) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let state = state.lock().map_err(|_| "桌面状态不可用")?;
        let conn = state.connection.as_ref().ok_or("请先连接工作台")?;
        let result = post(
            &conn.session,
            "/api/desktop/pause-queues",
            json!({"clientId": conn.client_id}),
        )?;
        let paused = result
            .get("paused")
            .and_then(Value::as_u64)
            .ok_or("工作台未返回有效的暂停结果")?;
        Ok(json!({"paused": paused}))
    })
    .await
    .map_err(|_| "暂停排队任务失败")?
}
#[tauri::command]
async fn desktop_open_workbench(app: tauri::AppHandle) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let state = state.lock().map_err(|_| "桌面状态不可用")?;
        open_window(
            &app,
            state.connection.as_ref().ok_or("工作台尚未连接")?,
            true,
        )
    })
    .await
    .map_err(|_| "无法打开工作台")?
}
#[tauri::command]
fn desktop_set_autostart(app: tauri::AppHandle, enabled: bool) -> Result<(), String> {
    let launcher = app.autolaunch();
    if enabled {
        launcher.enable()
    } else {
        launcher.disable()
    }
    .map_err(|_| "无法更改开机启动设置".to_owned())
}
#[tauri::command]
async fn desktop_set_notifications(app: tauri::AppHandle, enabled: bool) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let _state = state.lock().map_err(|_| "桌面状态不可用")?;
        let mut config = read_config()?;
        config.notifications = enabled;
        save_config(&config)
    })
    .await
    .map_err(|_| "无法保存通知设置")?
}
fn check_updates(app: &tauri::AppHandle) -> Result<(), String> {
    {
        let state = app.state::<Mutex<State>>();
        let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
        if state.update_checking {
            return Err("更新检查或安装正在进行".into());
        }
        state.update_checking = true;
        state.update_message = "正在检查桌面更新…".into();
    }
    let result = update::check();
    let state = app.state::<Mutex<State>>();
    let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
    state.update_checking = false;
    match result {
        Ok(Some(release)) => {
            state.update_message = format!("发现稳定版 {}，可由您选择安装", release.version);
            state.update = Some(release);
            drop(state);
            let _ = app
                .notification()
                .builder()
                .title("发现桌面新版")
                .body("可在桌面设置中查看和安装")
                .show();
            Ok(())
        }
        Ok(None) => {
            state.update = None;
            state.update_message = "已是最新稳定版".into();
            Ok(())
        }
        Err(error) => {
            state.update = None;
            state.update_message = format!("检查更新失败：{error}");
            Err(error)
        }
    }
}
#[tauri::command]
async fn desktop_check_updates(app: tauri::AppHandle) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || check_updates(&app))
        .await
        .map_err(|_| "更新检查任务失败")?
}
#[tauri::command]
async fn desktop_install_update(app: tauri::AppHandle) -> Result<(), String> {
    let release = {
        let state = app.state::<Mutex<State>>();
        let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
        if state.update_checking {
            return Err("更新检查或安装正在进行".into());
        }
        if state.connection.is_none() {
            return Err("请先连接工作台并核对活动作业，再安装更新".into());
        }
        let release = state.update.clone().ok_or("请先检查桌面更新")?;
        state.update_checking = true;
        state.update_message = "正在下载并验证桌面更新…".into();
        release
    };
    let ready = match update::prepare(&app, &release).await {
        Ok(ready) => ready,
        Err(error) => {
            if let Ok(mut state) = app.state::<Mutex<State>>().lock() {
                state.update_checking = false;
                state.update_message = format!("更新准备失败：{error}");
            }
            return Err(error);
        }
    };
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
        let mut external_lease: Option<(Session, String, String)> = None;
        let mut stopped_owned = false;
        let coordinated = (|| -> Result<FrozenWorkbench, String> {
            if let Some(conn) = state.connection.as_mut() {
                let guard = freeze_and_flush(&app, conn)?;
                if conn.child.is_some() {
                    detach_or_stop(conn)?;
                    stopped_owned = true;
                } else {
                    let lease = post(
                        &conn.session,
                        "/api/desktop/quiesce",
                        json!({"clientId":conn.client_id}),
                    )?;
                    let lease_id = lease
                        .get("leaseId")
                        .and_then(Value::as_str)
                        .ok_or("工作台未返回有效协调租约")?
                        .to_owned();
                    external_lease = Some((conn.session.clone(), conn.client_id.clone(), lease_id));
                }
                Ok(guard)
            } else {
                Err("工作台连接已中断，请重新连接后安装更新".into())
            }
        })();
        let _freeze = match coordinated {
            Ok(guard) => guard,
            Err(error) => {
                state.update_checking = false;
                state.update_message = format!("暂不能安装更新：{error}");
                return Err(error);
            }
        };
        if stopped_owned {
            state.connection = None;
        }
        // On Windows the updater terminates this process after launching the
        // installer. An external service remains alive and releases its lease
        // through the server-side TTL if this process can no longer release it.
        if let Some(window) = app.get_window("workbench") {
            window_placement::save(&window);
        }
        let result = ready.install();
        if let Some((session, client_id, lease_id)) = external_lease {
            let _ = post(
                &session,
                "/api/desktop/release",
                json!({"clientId":client_id,"leaseId":lease_id}),
            );
            if result.is_ok() {
                let _ = post(
                    &session,
                    "/api/desktop/detach",
                    json!({"clientId":client_id}),
                );
            }
        }
        match result {
            Ok(()) => {
                state.exiting = true;
                drop(state);
                app.restart();
            }
            Err(error) => {
                state.update_checking = false;
                state.update_message = format!("安装更新失败：{error}");
                if stopped_owned {
                    state.message = "更新未完成，请重新连接工作台".into();
                }
                Err(error)
            }
        }
    })
    .await
    .map_err(|_| "更新安装任务失败")?
}
fn notify_job(app: &tauri::AppHandle, instance: &str, id: &str, title: &str, message: &str) {
    let foreground = WORKBENCH_VIEW_ACTIVE.load(Ordering::Relaxed)
        && app.get_window("workbench").is_some_and(|window| {
            window.is_visible().unwrap_or(false) && window.is_focused().unwrap_or(false)
        });
    if foreground || !read_config().is_ok_and(|config| config.notifications) {
        return;
    }
    let mut bytes = [0u8; 4];
    if getrandom::fill(&mut bytes).is_err() {
        return;
    }
    let notification_id = (u32::from_le_bytes(bytes) & 0x7fff_ffff) as i32;
    let shown_title: String = title
        .chars()
        .filter(|c| !c.is_control())
        .take(120)
        .collect();
    let state = app.state::<Mutex<State>>();
    if let Ok(mut state) = state.lock() {
        if !state
            .connection
            .as_ref()
            .is_some_and(|conn| conn.instance_id == instance)
        {
            return;
        }
        state
            .notifications
            .insert(notification_id, (instance.to_owned(), id.to_owned()));
        while state.notifications.len() > 200 {
            if let Some(first) = state.notifications.keys().next().copied() {
                state.notifications.remove(&first);
            }
        }
    } else {
        return;
    }
    let _ = app
        .notification()
        .builder()
        .id(notification_id)
        .title(shown_title)
        .body(message)
        .show();
}
fn monitor_jobs(app: tauri::AppHandle) {
    let mut instance = String::new();
    let mut cursor: Option<u64> = None;
    let mut seen = BTreeSet::<(String, String)>::new();
    loop {
        thread::sleep(Duration::from_secs(3));
        let current = {
            let state = app.state::<Mutex<State>>();
            let Ok(state) = state.lock() else {
                continue;
            };
            state
                .connection
                .as_ref()
                .map(|conn| (conn.session.clone(), conn.instance_id.clone()))
        };
        let Some((session, current_instance)) = current else {
            instance.clear();
            cursor = None;
            seen.clear();
            continue;
        };
        if instance != current_instance {
            instance = current_instance.clone();
            cursor = None;
            seen.clear();
        }
        if cursor.is_none() {
            let Ok(base) = get(&session, "/api/desktop/notification-baseline") else {
                continue;
            };
            let Some(base_cursor) = base.get("cursor").and_then(Value::as_u64) else {
                continue;
            };
            let Some(event_count) = base.get("eventCount").and_then(Value::as_u64) else {
                continue;
            };
            let mut after = 0u64;
            let mut complete = false;
            for _ in 0..1000 {
                let Ok(page) = get(
                    &session,
                    &format!(
                        "/api/desktop/notification-pending-page?baseline={base_cursor}&eventCount={event_count}&after={after}"
                    ),
                ) else {
                    break;
                };
                let Some(items) = page.get("items").and_then(Value::as_array) else {
                    break;
                };
                let Some(next) = page.get("nextAfter").and_then(Value::as_u64) else {
                    break;
                };
                let mut valid = true;
                for item in items {
                    let Some(id) = item.get("jobId").and_then(Value::as_str) else {
                        valid = false;
                        break;
                    };
                    if id.is_empty() || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
                        valid = false;
                        break;
                    }
                    let Some(request) = item
                        .get("requestId")
                        .and_then(Value::as_str)
                        .filter(|value| !value.is_empty())
                    else {
                        continue;
                    };
                    let key = (id.to_owned(), format!("input:{request}"));
                    if !seen.contains(&key) {
                        let Ok(pending) = get(&session, &format!("/api/desktop/jobs/{id}/pending"))
                        else {
                            valid = false;
                            break;
                        };
                        let Some(current) = pending.get("items").and_then(Value::as_array) else {
                            valid = false;
                            break;
                        };
                        if current.iter().any(|entry| {
                            entry.get("requestId").and_then(Value::as_str) == Some(request)
                        }) {
                            notify_job(
                                &app,
                                &instance,
                                id,
                                item.get("title")
                                    .and_then(Value::as_str)
                                    .unwrap_or("工作台作业"),
                                "需要处理",
                            );
                        }
                        seen.insert(key);
                    }
                }
                if !valid || next < after {
                    break;
                }
                if page.get("hasMore").and_then(Value::as_bool) != Some(true) {
                    complete = true;
                    break;
                }
                if next == after {
                    break;
                }
                after = next;
            }
            if complete {
                cursor = Some(base_cursor);
            }
            continue;
        }
        for _ in 0..20 {
            let current_cursor = cursor.unwrap_or(0);
            let Ok(page) = get(
                &session,
                &format!("/api/desktop/notification-events?after={current_cursor}"),
            ) else {
                break;
            };
            let Some(events) = page.get("items").and_then(Value::as_array) else {
                break;
            };
            let Some(next) = page.get("nextAfter").and_then(Value::as_u64) else {
                break;
            };
            let mut retry = false;
            for event in events {
                let Some(event_id) = event.get("id").and_then(Value::as_u64) else {
                    retry = true;
                    break;
                };
                let Some(id) = event.get("jobId").and_then(Value::as_str) else {
                    retry = true;
                    break;
                };
                if id.is_empty() || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
                    retry = true;
                    break;
                }
                let title = event
                    .get("title")
                    .and_then(Value::as_str)
                    .unwrap_or("工作台作业");
                match event.get("kind").and_then(Value::as_str) {
                    Some("inputRequested") => {
                        let Some(request) = event
                            .get("requestId")
                            .and_then(Value::as_str)
                            .filter(|value| !value.is_empty())
                        else {
                            cursor = Some(event_id);
                            continue;
                        };
                        let key = (id.to_owned(), format!("input:{request}"));
                        if !seen.contains(&key) {
                            let Ok(pending) =
                                get(&session, &format!("/api/desktop/jobs/{id}/pending"))
                            else {
                                retry = true;
                                break;
                            };
                            let Some(items) = pending.get("items").and_then(Value::as_array) else {
                                retry = true;
                                break;
                            };
                            if items.iter().any(|item| {
                                item.get("requestId").and_then(Value::as_str) == Some(request)
                            }) {
                                notify_job(&app, &instance, id, title, "需要处理");
                            }
                            seen.insert(key);
                        }
                    }
                    Some("finished") => {
                        let key = (id.to_owned(), format!("finished:{event_id}"));
                        if seen.insert(key) {
                            match event.get("status").and_then(Value::as_str) {
                                Some("completed") => {
                                    notify_job(&app, &instance, id, title, "已完成")
                                }
                                Some("failed" | "recoveryRequired") => {
                                    notify_job(&app, &instance, id, title, "执行失败，请查看")
                                }
                                _ => {}
                            }
                        }
                    }
                    _ => {}
                }
                cursor = Some(event_id);
            }
            if retry {
                break;
            }
            if next < cursor.unwrap_or(0) {
                break;
            }
            cursor = Some(next);
            if page.get("hasMore").and_then(Value::as_bool) != Some(true) {
                break;
            }
        }
    }
}
#[tauri::command]
async fn desktop_exit(app: tauri::AppHandle, stop_jobs: bool) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Mutex<State>>();
        let mut state = state.lock().map_err(|_| "桌面状态不可用")?;
        let _freeze = if let Some(conn) = state.connection.as_mut() {
            if stop_jobs && conn.child.is_none() {
                return Err("外部工作台的作业请从原入口处理；可单独退出桌面应用".into());
            }
            let freeze = freeze_and_flush(&app, conn)?;
            if stop_jobs {
                self::stop_jobs(conn)?;
            }
            detach_or_stop(conn)?;
            Some(freeze)
        } else {
            None
        };
        if let Some(window) = app.get_window("workbench") {
            window_placement::save(&window);
            window.destroy().map_err(|_| "无法关闭工作台窗口，请重试")?;
        }
        state.connection = None;
        state.exiting = true;
        drop(state);
        app.exit(0);
        Ok(())
    })
    .await
    .map_err(|_| "退出任务失败")?
}
fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            reopen_workbench(app.clone());
        }))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            Some(vec!["--autostart"]),
        ))
        .plugin(tauri_plugin_notification::init())
        .manage(Mutex::new(State::default()))
        .invoke_handler(tauri::generate_handler![
            desktop_status,
            desktop_connect,
            desktop_select_directory,
            desktop_pause_queues,
            desktop_open_workbench,
            desktop_set_autostart,
            desktop_set_notifications,
            desktop_check_updates,
            desktop_install_update,
            desktop_exit,
            menubar_snapshot,
            menubar_action
        ])
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                if window.label() == "workbench" {
                    window_placement::save(window);
                }
                let _ = window.hide();
            }
        })
        .setup(|app| {
            #[cfg(target_os = "macos")]
            {
                use tauri::menu::{AboutMetadata, PredefinedMenuItem, Submenu};
                let about = PredefinedMenuItem::about(app, Some("关于 YSS 工作台"), Some(AboutMetadata {
                    name: Some("YSS 工作台".into()),
                    version: Some(env!("CARGO_PKG_VERSION").into()),
                    ..Default::default()
                }))?;
                let settings = MenuItem::with_id(app, "app-settings", "桌面设置…", true, Some("CmdOrCtrl+,"))?;
                let updates = MenuItem::with_id(app, "app-updates", "检查更新…", true, None::<&str>)?;
                let quit = MenuItem::with_id(app, "app-quit", "退出 YSS 工作台…", true, Some("CmdOrCtrl+Q"))?;
                let app_menu = Submenu::with_items(app, "YSS 工作台", true, &[
                    &about,
                    &PredefinedMenuItem::separator(app)?,
                    &settings,
                    &updates,
                    &PredefinedMenuItem::separator(app)?,
                    &PredefinedMenuItem::hide(app, None)?,
                    &PredefinedMenuItem::hide_others(app, None)?,
                    &PredefinedMenuItem::show_all(app, None)?,
                    &PredefinedMenuItem::separator(app)?,
                    &quit,
                ])?;
                let open = MenuItem::with_id(app, "app-open-workbench", "打开工作台", true, Some("CmdOrCtrl+O"))?;
                let file_menu = Submenu::with_items(app, "文件", true, &[
                    &open,
                    &PredefinedMenuItem::separator(app)?,
                    &PredefinedMenuItem::close_window(app, Some("关闭窗口"))?,
                ])?;
                let edit_menu = Submenu::with_items(app, "编辑", true, &[
                    &PredefinedMenuItem::undo(app, None)?, &PredefinedMenuItem::redo(app, None)?,
                    &PredefinedMenuItem::separator(app)?, &PredefinedMenuItem::cut(app, None)?,
                    &PredefinedMenuItem::copy(app, None)?, &PredefinedMenuItem::paste(app, None)?,
                    &PredefinedMenuItem::select_all(app, None)?,
                ])?;
                let overview = MenuItem::with_id(app, "app-menu-bar-overview", "菜单栏概览", true, None::<&str>)?;
                let window_menu = Submenu::with_items(app, "窗口", true, &[
                    &PredefinedMenuItem::minimize(app, None)?,
                    &PredefinedMenuItem::fullscreen(app, None)?,
                    &PredefinedMenuItem::separator(app)?,
                    &overview,
                ])?;
                app.set_menu(Menu::with_items(app, &[&app_menu, &file_menu, &edit_menu, &window_menu])?)?;
                app.on_menu_event(|app, event| match event.id().as_ref() {
                    "app-open-workbench" => reopen_workbench(app.clone()),
                    "app-menu-bar-overview" => menu_bar_panel::open_from_menu(app.clone()),
                    "app-settings" | "app-quit" | "app-updates" => {
                        let app = app.clone();
                        let updates_tab = event.id().as_ref() != "app-settings";
                        let check = event.id().as_ref() == "app-updates";
                        thread::spawn(move || {
                            let _ = open_desktop_settings(&app, updates_tab);
                            if check {
                                let _ = check_updates(&app);
                            }
                        });
                    }
                    _ => (),
                });
            }
            let action_app = app.handle().clone();
            app.notification().on_action(move |action| {
                if action.action_id() != "tap" {
                    return;
                }
                let Some(notification) = action.notification() else {
                    reopen_workbench(action_app.clone());
                    return;
                };
                let app = action_app.clone();
                let notification_id = notification.id();
                thread::spawn(move || {
                    let state = app.state::<Mutex<State>>();
                    let Ok(state) = state.lock() else {
                        return;
                    };
                    let Some((instance, job_id)) = state.notifications.get(&notification_id) else {
                        let _ = show_settings(&app);
                        return;
                    };
                    let Some(conn) = state
                        .connection
                        .as_ref()
                        .filter(|conn| conn.instance_id == *instance)
                    else {
                        let _ = show_settings(&app);
                        return;
                    };
                    if !job_id.bytes().all(|b| b.is_ascii_hexdigit())
                        || get(&conn.session, &format!("/api/jobs/{job_id}")).is_err()
                    {
                        let _ = show_settings(&app);
                        return;
                    }
                    if open_window(&app, conn, true).is_ok() && flush(&app, conn).is_ok()
                        && let Some(window) = app.get_webview("workbench") {
                            let _ = window.eval(format!("if (window.confirm('即将前往作业，请确认当前页面的输入已保存。')) window.location.hash='#/jobs/{job_id}'"));
                        }
                });
            })?;
            let show = MenuItem::with_id(app, "show", "打开工作台", true, None::<&str>)?;
            let settings = MenuItem::with_id(app, "settings", "桌面设置", true, None::<&str>)?;
            let exit = MenuItem::with_id(app, "exit", "退出…", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &settings, &exit])?;
            #[cfg(target_os = "macos")]
            let tray = TrayIconBuilder::with_id(menu_bar_panel::TRAY_ID).icon(menu_bar_panel::icon())
                .icon_as_template(true)
                .show_menu_on_left_click(false)
                .on_tray_icon_event(menu_bar_panel::on_tray_event);
            #[cfg(not(target_os = "macos"))]
            let tray = {
                let builder = TrayIconBuilder::new();
                if let Some(icon) = app.default_window_icon() { builder.icon(icon.clone()) } else { builder }
            };
            tray
                .tooltip("YSS 工作台")
                .menu(&menu)
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "show" => {
                        reopen_workbench(app.clone());
                    }
                    "settings" | "exit" => {
                        let app = app.clone();
                        let show_updates = event.id().as_ref() == "exit";
                        thread::spawn(move || { let _ = open_desktop_settings(&app, show_updates); });
                    }
                    _ => {}
                })
                .build(app)?;
            #[cfg(target_os = "macos")]
            let _ = menu_bar_panel::prepare(app.handle());
            let start_hidden = std::env::args_os().any(|arg| arg == "--autostart");
            match update::register(app.handle()) {
                Ok(()) => {
                    let updates = app.handle().clone();
                    thread::spawn(move || {
                        let _ = check_updates(&updates);
                    });
                }
                Err(error) => {
                    if let Ok(mut state) = app.state::<Mutex<State>>().lock() {
                        state.update_message = error;
                    }
                }
            }
            let boot_app = app.handle().clone();
            thread::spawn(move || {
                if let Err(error) = connect(&boot_app, None, !start_hidden) {
                    if let Ok(mut state) = boot_app.state::<Mutex<State>>().lock() {
                        state.message = error;
                    }
                    if !start_hidden {
                        let _ = show_settings(&boot_app);
                    }
                }
            });
            let heartbeat = app.handle().clone();
            thread::spawn(move || {
                loop {
                    thread::sleep(Duration::from_secs(20));
                    if let Ok(mut state) = heartbeat.state::<Mutex<State>>().lock()
                        && let Some(conn) = state.connection.as_mut() {
                            let result = post(
                                &conn.session,
                                "/api/desktop/attach",
                                json!({"clientId":conn.client_id,"managedOwner":conn.managed_owner}),
                            );
                            let owner_lost = conn.managed_owner.is_some() && result.as_ref().is_ok_and(|value| value.get("owner").and_then(Value::as_bool) != Some(true));
                            if result.is_err() || owner_lost {
                                let external = check_info(&conn.session).is_ok_and(|info| info.get("managed").and_then(Value::as_bool) == Some(false) && info.get("instanceId").and_then(Value::as_str) == Some(conn.instance_id.as_str()));
                                if external && post(&conn.session, "/api/desktop/attach", json!({"clientId":conn.client_id})).is_ok() {
                                    conn.child.take();
                                    conn.managed_owner = None;
                                    state.message = "工作台已转为外部服务，退出桌面不会关闭它".into();
                                } else { state.message = "与工作台的连接已中断，请重试".into(); }
                            }
                        }
                }
            });
            let notifications = app.handle().clone();
            thread::spawn(move || monitor_jobs(notifications));
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("桌面应用构建失败")
        .run(|app, event| {
            #[cfg(target_os = "macos")]
            if let tauri::RunEvent::Reopen { has_visible_windows: false, .. } = &event {
                reopen_workbench(app.clone());
            }
            if let tauri::RunEvent::ExitRequested { api, .. } = event {
                let may_exit = app
                    .state::<Mutex<State>>()
                    .try_lock()
                    .is_ok_and(|state| state.exiting);
                if !may_exit {
                    api.prevent_exit();
                    let handle = app.clone();
                    thread::spawn(move || { let _ = show_settings(&handle); });
                }
            }
        });
}
