mod pty;

use crate::{EXITING, MAIN_VIEW, Session, State, WORKBENCH_FROZEN, get, random_id, terminal_panel};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    io::{self, Read, Write},
    path::{Path, PathBuf},
    sync::{
        Arc, Condvar, Mutex, MutexGuard,
        atomic::Ordering,
        mpsc::{SyncSender, TrySendError, sync_channel},
    },
    thread,
    time::{Duration, Instant},
};
use tauri::{
    AppHandle, Emitter, Manager, PhysicalPosition, PhysicalSize, Webview, WebviewBuilder,
    WebviewUrl, Window,
    ipc::{Channel, InvokeResponseBody},
    webview::{NewWindowResponse, PageLoadEvent},
};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};

const WEBVIEW_PREFIX: &str = "terminal-";
const CONTEXT_EVENT: &str = "terminal:context-changed";
const SESSION_EVENT: &str = "terminal:session-changed";
// Tauri 2.12.7 (ipc/channel.rs) routes raw payloads of 1024 bytes or more
// through an asynchronous fetch. A rejected fetch leaves a gap in the JS
// channel's message indexes, so keep every output frame on its direct path.
const OUTPUT_FRAME_LIMIT: usize = 1023;
const OUTPUT_READ_CHUNK: usize = 8 * OUTPUT_FRAME_LIMIT;
const OUTPUT_LIMIT: usize = 256 * 1024;
const INPUT_LIMIT: usize = 64 * 1024;
const CLEANUP_TIMEOUT: Duration = Duration::from_secs(12);
const CLEANUP_TIMEOUT_ERROR: &str = "终端关闭超时，进程回收尚未确认";

#[derive(Default)]
pub(super) struct TerminalState {
    window: Mutex<TerminalWindow>,
}

#[derive(Default)]
struct TerminalWindow {
    renderer: Option<String>,
    next_renderer: u64,
    selection: Option<(String, String)>,
    ready: bool,
    generation: u64,
    sessions: BTreeMap<String, Arc<TerminalSession>>,
}

impl TerminalWindow {
    fn owns_renderer(&self, label: &str) -> bool {
        self.renderer.as_deref() == Some(label)
    }

    fn owns_document(&self, label: &str, generation: u64) -> bool {
        self.owns_renderer(label) && self.ready && self.generation == generation
    }

    fn reserve_renderer(&mut self) -> String {
        self.next_renderer += 1;
        let label = format!("{WEBVIEW_PREFIX}{}", self.next_renderer);
        self.renderer = Some(label.clone());
        self.ready = false;
        label
    }

    fn invalidate_sessions(&mut self) -> Vec<Arc<TerminalSession>> {
        self.generation = self.generation.wrapping_add(1);
        let sessions: Vec<_> = self.sessions.values().cloned().collect();
        for session in &sessions {
            session.stop();
        }
        sessions
    }

    fn lose_renderer(&mut self, label: &str) -> Option<Vec<Arc<TerminalSession>>> {
        if !self.owns_renderer(label) {
            return None;
        }
        self.renderer = None;
        self.ready = false;
        Some(self.invalidate_sessions())
    }

    fn prune_confirmed_closed_sessions(&mut self) {
        self.sessions.retain(|_, session| {
            let status = session.lock();
            let should_remove = status.close_requested || session.generation != self.generation;
            !should_remove || !status.result.as_ref().is_some_and(Result::is_ok)
        });
    }
}

type TerminalOutput = Channel<InvokeResponseBody>;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) enum TerminalAckResult {
    Acknowledged,
    Unavailable,
    InvalidCursor,
}

#[derive(Serialize)]
#[serde(
    tag = "status",
    rename_all = "camelCase",
    rename_all_fields = "camelCase"
)]
pub(super) enum TerminalWriteResult {
    Accepted { next_offset: u64 },
    QueueFull,
    Closed,
    InvalidOffset,
    InvalidInput,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
struct Project {
    id: String,
    name: String,
    root_path: PathBuf,
    #[serde(default, skip_serializing)]
    active: bool,
}

#[derive(Deserialize)]
struct Catalog {
    projects: Vec<Project>,
}

#[derive(Deserialize)]
struct ProjectSummary {
    project: ProjectStatus,
}

#[derive(Deserialize)]
struct ProjectStatus {
    record: Project,
    available: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Context {
    connection_id: String,
    selected_project_id: Option<String>,
    projects: Vec<Project>,
    sessions: Vec<SessionView>,
    #[cfg(windows)]
    windows_pty: pty::WindowsPty,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct SessionView {
    id: String,
    project_id: String,
    root_path: PathBuf,
    state: &'static str,
    exit_code: Option<i32>,
    error: Option<String>,
}

struct TerminalSession {
    id: String,
    connection_id: String,
    generation: u64,
    project: Project,
    status: Mutex<Execution>,
    changed: Condvar,
}

struct Execution {
    control: Option<Arc<dyn pty::Control>>,
    input: Option<SyncSender<Vec<u8>>>,
    output_sent: u64,
    output_acknowledged: u64,
    input_enqueued: u64,
    read_closed: bool,
    closing: bool,
    close_requested: bool,
    discard_output: bool,
    result: Option<Result<(), String>>,
    cleanup_timed_out: bool,
    exit_code: Option<i32>,
    io_error: Option<String>,
}

impl Execution {
    fn record_cleanup(&mut self, result: Result<(), String>) {
        if result.is_ok() {
            self.control.take();
            self.cleanup_timed_out = false;
        } else {
            self.discard_output = true;
        }
        self.result = Some(result);
    }
}

impl TerminalSession {
    fn lock(&self) -> MutexGuard<'_, Execution> {
        self.status
            .lock()
            .unwrap_or_else(|error| error.into_inner())
    }

    fn view(&self, generation: u64) -> SessionView {
        let status = self.lock();
        let retired = self.generation != generation;
        let state = match &status.result {
            _ if retired => "cleanupPending",
            Some(Ok(())) if status.io_error.is_none() => "exited",
            Some(Ok(())) => "failed",
            Some(Err(_)) => "cleanupPending",
            None if status.cleanup_timed_out => "cleanupPending",
            None if status.closing => "closing",
            None => "running",
        };
        let error = if state == "cleanupPending" {
            Some(match &status.result {
                Some(Err(error)) => error.clone(),
                _ if status.cleanup_timed_out => CLEANUP_TIMEOUT_ERROR.into(),
                _ => "上一次终端会话的进程回收尚未确认，可重试关闭".into(),
            })
        } else {
            status.io_error.clone()
        };
        SessionView {
            id: self.id.clone(),
            project_id: self.project.id.clone(),
            root_path: self.project.root_path.clone(),
            state,
            exit_code: status.exit_code,
            error,
        }
    }

    fn stop(&self) {
        let mut status = self.lock();
        status.closing = true;
        status.discard_output = true;
        status.input.take();
        self.changed.notify_all();
    }

    fn request_close(self: &Arc<Self>, app: &AppHandle) {
        self.stop();
        let mut status = self.lock();
        status.close_requested = true;
        if !status.result.as_ref().is_some_and(Result::is_err) {
            return;
        }
        let Some(control) = status.control.clone() else {
            return;
        };
        // A failed result stays recoverable. Only one caller may start a retry.
        let previous = status.result.take();
        let session = self.clone();
        let app = app.clone();
        if thread::Builder::new()
            .name("yss-terminal-reap".into())
            .spawn(move || {
                session.complete_cleanup(control.close());
                notice(&app, SESSION_EVENT);
            })
            .is_err()
        {
            status.result = previous;
            self.changed.notify_all();
        }
    }

    fn complete_cleanup(&self, result: Result<(), String>) {
        self.lock().record_cleanup(result);
        self.changed.notify_all();
    }

    fn fail_io(&self, error: &str) {
        let mut status = self.lock();
        if !status.discard_output && status.io_error.is_none() {
            status.io_error = Some(error.into());
            self.changed.notify_all();
        }
    }

    fn wait_closed(&self, deadline: Instant) -> Result<(), String> {
        let mut status = self.lock();
        loop {
            if let Some(result) = &status.result {
                return result.clone();
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                status.cleanup_timed_out = true;
                return Err(CLEANUP_TIMEOUT_ERROR.into());
            }
            status = self
                .changed
                .wait_timeout(status, remaining)
                .unwrap_or_else(|error| error.into_inner())
                .0;
        }
    }
}

pub(super) fn is_renderer(view: &Webview) -> bool {
    view.label().starts_with(WEBVIEW_PREFIX)
}

pub(super) fn webview(app: &AppHandle) -> Option<Webview> {
    let label = app
        .state::<TerminalState>()
        .window
        .lock()
        .ok()?
        .renderer
        .clone()?;
    app.get_webview(&label)
}

pub(super) fn local(view: &Webview) -> Result<&AppHandle, String> {
    let app = view.app_handle();
    if !is_renderer(view)
        || view.window().label() != "workbench"
        || !view.url().is_ok_and(|url| terminal_url(&url))
        || !app
            .state::<TerminalState>()
            .window
            .lock()
            .is_ok_and(|window| window.owns_renderer(view.label()))
    {
        return Err("当前窗口无法使用本机终端".into());
    }
    Ok(app)
}

fn terminal_url(url: &tauri::Url) -> bool {
    if url.path() != "/terminal.html"
        || url.query().is_some()
        || url.fragment().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
    {
        return false;
    }
    #[cfg(windows)]
    return url.scheme() == "http" && url.host_str() == Some("tauri.localhost");
    #[cfg(not(windows))]
    return url.scheme() == "tauri" && url.host_str() == Some("localhost");
}

fn notice(app: &AppHandle, event: &str) {
    if let Some(view) = webview(app) {
        let _ = app.emit_to(tauri::EventTarget::webview(view.label()), event, ());
    }
}

pub(super) fn connection_changed(app: &AppHandle) {
    notice(app, CONTEXT_EVENT);
}

fn connection_snapshot(app: &AppHandle) -> Result<(String, Session), String> {
    let desktop = app.state::<Mutex<State>>();
    let desktop = desktop.lock().map_err(|_| "桌面工作台状态不可用")?;
    let connection = desktop.connection.as_ref().ok_or("请先连接工作台")?;
    Ok((connection.client_id.clone(), connection.session.clone()))
}

fn check_connection(state: &State, id: &str) -> Result<(), String> {
    if EXITING.load(Ordering::Acquire) || WORKBENCH_FROZEN.load(Ordering::Acquire) {
        return Err("工作台正在切换或退出，请稍后重试".into());
    }
    if !state
        .connection
        .as_ref()
        .is_some_and(|connection| connection.client_id == id)
    {
        return Err("工作台连接已变化，请重新选择工程".into());
    }
    Ok(())
}

fn terminal_session(view: &Webview, id: &str) -> Result<Arc<TerminalSession>, String> {
    let app = local(view)?;
    let state = app.state::<TerminalState>();
    let window = state.window.lock().map_err(|_| "终端窗口状态不可用")?;
    window
        .sessions
        .get(id)
        .filter(|session| window.owns_document(view.label(), session.generation))
        .cloned()
        .ok_or_else(|| "终端会话已结束".into())
}

fn document(view: &Webview) -> Result<(AppHandle, u64), String> {
    let app = local(view)?;
    let state = app.state::<TerminalState>();
    let window = state.window.lock().map_err(|_| "终端窗口状态不可用")?;
    if !window.owns_document(view.label(), window.generation) {
        return Err("终端窗口已断开，请重新打开".into());
    }
    Ok((app.clone(), window.generation))
}

pub(super) fn show(
    app: &AppHandle,
    project_id: Option<String>,
    client_id: String,
) -> Result<(), String> {
    let desktop = app.state::<Mutex<State>>();
    let desktop = desktop.lock().map_err(|_| "桌面工作台状态不可用")?;
    check_connection(&desktop, &client_id)?;
    let _layout = MAIN_VIEW.lock().map_err(|_| "主窗口不可用")?;
    let selected_project = project_id.is_some();
    if let Some(project_id) = project_id {
        app.state::<TerminalState>()
            .window
            .lock()
            .map_err(|_| "终端窗口状态不可用")?
            .selection = Some((client_id, project_id));
    }
    drop(desktop);

    let window = app.get_window("workbench").ok_or("工作台窗口尚未打开")?;
    let view = attach(&window).map_err(|error| format!("无法打开终端面板：{error}"))?;
    terminal_panel::set_visible(app, true)?;
    let layout = terminal_panel::apply(app, &window).map_err(|_| "无法显示终端面板")?;
    if layout.is_visible() {
        view.set_focus().map_err(|_| "无法聚焦终端面板")?;
    }
    if selected_project {
        notice(app, CONTEXT_EVENT);
    }
    Ok(())
}

// The terminal is attached after the workbench so overlap leaves it on top.
pub(super) fn attach(window: &Window) -> tauri::Result<Webview> {
    let app = window.app_handle();
    if let Some(view) = webview(app) {
        return Ok(view);
    }
    let width = window.inner_size()?.width;
    let (label, abandoned) = {
        let state = app.state::<TerminalState>();
        let mut renderer = state
            .window
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        let abandoned = if renderer.renderer.is_some() {
            renderer.invalidate_sessions()
        } else {
            Vec::new()
        };
        (renderer.reserve_renderer(), abandoned)
    };
    reap_in_background(app, abandoned);
    let builder = WebviewBuilder::new(&label, WebviewUrl::App("terminal.html".into()))
        .disable_drag_drop_handler()
        .on_navigation(terminal_url)
        .on_new_window(|_, _| NewWindowResponse::Deny)
        .on_page_load(|view, payload| {
            if payload.event() == PageLoadEvent::Started {
                page_started(&view);
            }
        });
    let view = match window.add_child(
        builder,
        PhysicalPosition::new(width as i32, 0),
        PhysicalSize::new(1, 1),
    ) {
        Ok(view) => view,
        Err(error) => {
            renderer_lost(app, &label);
            return Err(error);
        }
    };
    #[cfg(windows)]
    if let Err(error) = observe_renderer(&view) {
        renderer_lost(app, &label);
        let _ = view.close();
        return Err(error);
    }
    Ok(view)
}

#[cfg(windows)]
fn observe_renderer(view: &Webview) -> tauri::Result<()> {
    use webview2_com::{Microsoft::Web::WebView2::Win32::*, ProcessFailedEventHandler};
    let app = view.app_handle().clone();
    let view_handle = view.clone();
    view.with_webview(move |webview| {
        let crash_view = view_handle.clone();
        let result = unsafe {
            webview.controller().CoreWebView2().and_then(|core| {
                let handler = ProcessFailedEventHandler::create(Box::new(move |_, args| {
                    let mut kind = COREWEBVIEW2_PROCESS_FAILED_KIND_UNKNOWN_PROCESS_EXITED;
                    if let Some(args) = args {
                        args.ProcessFailedKind(&mut kind)?;
                    }
                    if matches!(
                        kind,
                        COREWEBVIEW2_PROCESS_FAILED_KIND_BROWSER_PROCESS_EXITED
                            | COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED
                    ) {
                        renderer_crashed(&crash_view);
                    }
                    Ok(())
                }));
                let mut token = 0;
                core.add_ProcessFailed(&handler, &mut token)
            })
        };
        if result.is_err() {
            renderer_crashed(&view_handle);
            app.dialog()
                .message("无法监测终端窗口进程")
                .title("无法打开终端")
                .kind(MessageDialogKind::Error)
                .show(|_| {});
        }
    })
}

#[cfg(any(target_os = "macos", windows))]
pub(super) fn renderer_crashed(view: &Webview) {
    let app = view.app_handle().clone();
    if !renderer_lost(&app, view.label()) {
        return;
    }
    let view = view.clone();
    tauri::async_runtime::spawn_blocking(move || {
        if let Ok(_layout) = MAIN_VIEW.lock() {
            let _ = view.hide();
            let _ = view.close();
            if webview(&app).is_none() {
                let _ = terminal_panel::set_visible(&app, false);
                let _ = terminal_panel::apply(&app, &view.window());
            }
        }
    });
}

fn page_started(view: &Webview) {
    let app = view.app_handle();
    let state = app.state::<TerminalState>();
    let sessions = match state.window.lock() {
        Ok(mut window) if window.owns_renderer(view.label()) && window.ready => {
            window.ready = false;
            window.invalidate_sessions()
        }
        _ => return,
    };
    reap_in_background(app, sessions);
}

fn renderer_lost(app: &AppHandle, label: &str) -> bool {
    let state = app.state::<TerminalState>();
    let sessions = match state.window.lock() {
        Ok(mut window) => window.lose_renderer(label),
        Err(_) => None,
    };
    if let Some(sessions) = sessions {
        reap_in_background(app, sessions);
        true
    } else {
        false
    }
}

// Called while the desktop serializes child WebView replacement with MAIN_VIEW.
pub(super) fn retire_renderer(app: &AppHandle) -> Result<(), String> {
    if let Some(view) = webview(app) {
        view.close().map_err(|_| "无法关闭原终端视图")?;
        renderer_lost(app, view.label());
    }
    Ok(())
}

pub(super) fn window_destroyed(app: &AppHandle) {
    let state = app.state::<TerminalState>();
    let sessions = match state.window.lock() {
        Ok(mut window) => {
            window.renderer = None;
            window.ready = false;
            window.invalidate_sessions()
        }
        Err(_) => return,
    };
    reap_in_background(app, sessions);
}

fn reap(app: &AppHandle, sessions: Vec<Arc<TerminalSession>>) -> Result<(), String> {
    let deadline = Instant::now() + CLEANUP_TIMEOUT;
    for session in &sessions {
        session.request_close(app);
    }
    let state = app.state::<TerminalState>();
    let mut result = Ok(());
    for session in sessions {
        let outcome = session.wait_closed(deadline);
        if outcome.is_ok() {
            state
                .window
                .lock()
                .map_err(|_| "终端窗口状态不可用")?
                .sessions
                .remove(&session.id);
        } else if result.is_ok() {
            result = outcome;
        }
    }
    result
}

pub(super) fn shutdown(app: &AppHandle) -> Result<(), String> {
    let state = app.state::<TerminalState>();
    let sessions = {
        let mut window = state.window.lock().map_err(|_| "终端窗口状态不可用")?;
        window.selection = None;
        window.invalidate_sessions()
    };
    let result = reap(app, sessions);
    notice(app, CONTEXT_EVENT);
    result
}

fn reap_in_background(app: &AppHandle, sessions: Vec<Arc<TerminalSession>>) {
    if !sessions.is_empty() {
        let app = app.clone();
        tauri::async_runtime::spawn_blocking(move || {
            let result = reap(&app, sessions);
            notice(&app, SESSION_EVENT);
            if let Err(error) = result {
                app.dialog()
                    .message(error)
                    .title("终端会话关闭未确认")
                    .kind(MessageDialogKind::Error)
                    .show(|_| {});
            }
        });
    }
}

#[tauri::command]
pub(super) async fn terminal_context(window: Webview) -> Result<Context, String> {
    let (app, generation) = document(&window)?;
    let renderer = window.label().to_owned();
    tauri::async_runtime::spawn_blocking(move || {
        let (connection_id, session) = connection_snapshot(&app)?;
        let catalog = get(&session, "/api/workbench/catalog")?;
        let mut projects: Vec<_> = serde_json::from_value::<Catalog>(catalog)
            .map_err(|_| "工作台工程清单格式无效")?
            .projects
            .into_iter()
            .filter(|project| project.active)
            .collect();
        projects.sort_by(|left, right| left.name.cmp(&right.name).then(left.id.cmp(&right.id)));
        let desktop = app.state::<Mutex<State>>();
        let desktop = desktop.lock().map_err(|_| "桌面工作台状态不可用")?;
        check_connection(&desktop, &connection_id)?;
        let terminals = app.state::<TerminalState>();
        let mut state = terminals.window.lock().map_err(|_| "终端窗口状态不可用")?;
        if !state.owns_document(&renderer, generation) {
            return Err("终端窗口已断开，请重新打开".into());
        }
        state.prune_confirmed_closed_sessions();
        let selected_project_id = state
            .selection
            .as_ref()
            .and_then(|(conn, project)| (conn == &connection_id).then(|| project.clone()));
        let sessions = state
            .sessions
            .values()
            .filter(|session| session.connection_id == connection_id)
            .map(|session| session.view(generation))
            .collect();
        Ok(Context {
            connection_id,
            selected_project_id,
            projects,
            sessions,
            #[cfg(windows)]
            windows_pty: pty::windows_pty(),
        })
    })
    .await
    .map_err(|_| "读取终端工程失败")?
}

#[tauri::command]
pub(super) async fn terminal_ready(window: Webview) -> Result<(), String> {
    let app = local(&window)?;
    if EXITING.load(Ordering::Acquire) {
        return Err("桌面正在退出".into());
    }
    let state = app.state::<TerminalState>();
    let mut renderer = state.window.lock().map_err(|_| "终端窗口状态不可用")?;
    if !renderer.owns_renderer(window.label()) {
        return Err("终端窗口已断开，请重新打开".into());
    }
    renderer.ready = true;
    Ok(())
}

fn verify_size(cols: u16, rows: u16) -> Result<(), String> {
    if (2..=500).contains(&cols) && (1..=300).contains(&rows) {
        Ok(())
    } else {
        Err("终端窗口尺寸超出允许范围".into())
    }
}

fn registered_project(session: &Session, id: &str) -> Result<Project, String> {
    if id.len() != 32
        || !id
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        return Err("工程标识无效".into());
    }
    let record = get(session, &format!("/api/projects/{id}/summary"))?;
    let project = serde_json::from_value::<ProjectSummary>(record)
        .map_err(|_| "工程目录信息格式无效")?
        .project;
    if project.record.id != id || !project.record.active {
        return Err("工程已停用或不存在".into());
    }
    if !project.available {
        return Err("工程目录不可访问，请核对登记路径".into());
    }
    Ok(project.record)
}

fn prepare_directory(path: &Path) -> Result<yss::fs::Root, String> {
    if !path.is_absolute() {
        return Err("工程工作目录无效".into());
    }
    yss::fs::Root::open(path).map_err(|_| "工程目录不可访问，请核对登记路径".into())
}

#[tauri::command]
pub(super) async fn terminal_start(
    window: Webview,
    connection_id: String,
    project_id: String,
    cols: u16,
    rows: u16,
    output: TerminalOutput,
) -> Result<SessionView, String> {
    let (app, generation) = document(&window)?;
    let renderer = window.label().to_owned();
    verify_size(cols, rows)?;
    tauri::async_runtime::spawn_blocking(move || {
        let (current_id, session) = connection_snapshot(&app)?;
        if current_id != connection_id {
            return Err("工作台连接已变化，请重新选择工程".into());
        }
        let project = registered_project(&session, &project_id)?;
        let root = prepare_directory(&project.root_path)?;
        let id = random_id()?;
        let desktop = app.state::<Mutex<State>>();
        let desktop = desktop.lock().map_err(|_| "桌面工作台状态不可用")?;
        check_connection(&desktop, &connection_id)?;
        let terminals = app.state::<TerminalState>();
        let mut state = terminals.window.lock().map_err(|_| "终端窗口状态不可用")?;
        if !state.owns_document(&renderer, generation) {
            return Err("终端窗口状态已变化，请重试".into());
        }
        state.prune_confirmed_closed_sessions();
        if let Some(session) = state.sessions.values().find(|session| {
            session.connection_id == connection_id && session.project.id == project_id
        }) {
            if session.view(generation).state == "cleanupPending" {
                return Err("此工程的原终端会话尚未确认回收，请重试关闭".into());
            }
            return Err("此工程已有终端会话，请先关闭原会话".into());
        }
        root.revalidate()
            .map_err(|_| "工程目录已变化，请重新选择工程")?;
        let pty = pty::spawn(&root, cols, rows)?;
        let (tx, rx) = sync_channel(8);
        let session = Arc::new(TerminalSession {
            id: id.clone(),
            connection_id,
            generation,
            project,
            status: Mutex::new(Execution {
                control: Some(pty.control.clone()),
                input: Some(tx),
                output_sent: 0,
                output_acknowledged: 0,
                input_enqueued: 0,
                read_closed: false,
                closing: false,
                close_requested: false,
                discard_output: false,
                result: None,
                cleanup_timed_out: false,
                exit_code: None,
                io_error: None,
            }),
            changed: Condvar::new(),
        });
        state.sessions.insert(id, session.clone());
        drop(state);
        drop(desktop);
        let init = session.view(generation);
        let worker = session.clone();
        let worker_app = app.clone();
        let controller = pty.control.clone();
        if thread::Builder::new()
            .name("yss-terminal".into())
            .spawn(move || run_session(worker_app, worker, pty, rx, output))
            .is_err()
        {
            session.stop();
            let cleanup = controller.close();
            let error = match &cleanup {
                Ok(()) => "无法启动终端后台处理".into(),
                Err(error) => format!("无法启动终端后台处理；{error}"),
            };
            let confirmed = cleanup.is_ok();
            session.complete_cleanup(cleanup);
            if confirmed {
                terminals
                    .window
                    .lock()
                    .map_err(|_| "终端窗口状态不可用")?
                    .sessions
                    .remove(&init.id);
            }
            notice(&app, SESSION_EVENT);
            return Err(error);
        }
        Ok(init)
    })
    .await
    .map_err(|_| "启动终端任务失败")?
}

fn run_session(
    app: AppHandle,
    session: Arc<TerminalSession>,
    pty: pty::Pty,
    input: std::sync::mpsc::Receiver<Vec<u8>>,
    output: TerminalOutput,
) {
    let pty::Pty {
        reader,
        mut writer,
        control,
    } = pty;
    let output_session = session.clone();
    let read_thread = thread::Builder::new()
        .name("yss-terminal-output".into())
        .spawn(move || read_output(&output_session, reader, output));
    let input_session = session.clone();
    let write_thread = thread::Builder::new()
        .name("yss-terminal-input".into())
        .spawn(move || {
            while let Ok(bytes) = input.recv() {
                if input_session.lock().closing {
                    break;
                }
                if writer
                    .write_all(&bytes)
                    .and_then(|()| writer.flush())
                    .is_err()
                {
                    input_session.fail_io("无法写入终端输入");
                    break;
                }
            }
        });
    if read_thread.is_err() || write_thread.is_err() {
        session.fail_io("无法启动终端输入输出");
    }

    let mut exit_code = None;
    loop {
        let status = session.lock();
        if status.discard_output {
            break;
        }
        let io_failed = status.io_error.is_some();
        drop(status);
        match control.poll_exit() {
            Ok(Some(code)) => {
                exit_code = Some(code);
                let mut status = session.lock();
                status.closing = true;
                status.input.take();
                break;
            }
            Ok(None) if io_failed => break,
            Ok(None) => {}
            Err(_) => {
                session.fail_io("无法确认 Shell 的退出状态");
                break;
            }
        }
        let status = session.lock();
        let _unused =
            session
                .changed
                .wait_timeout_while(status, Duration::from_millis(50), |status| {
                    !status.closing && status.io_error.is_none()
                });
    }

    if exit_code.is_none() {
        session.stop();
    }
    notice(&app, SESSION_EVENT);

    // macOS closes the master reader when cleanup finishes. Allow the reader
    // to deliver the final bytes first. Windows instead closes ConPTY before
    // awaiting EOF while the reader continues delivering its final output.
    #[cfg(target_os = "macos")]
    if exit_code.is_some() {
        await_output(&session, Duration::from_secs(2));
    }
    let result = control.close();
    #[cfg(windows)]
    if exit_code.is_some() {
        await_output(&session, Duration::from_secs(2));
    }
    let workers = [read_thread.ok(), write_thread.ok()];
    let deadline = Instant::now() + Duration::from_millis(500);
    let mut workers_stopped = true;
    for worker in workers.into_iter().flatten() {
        while !worker.is_finished() && Instant::now() < deadline {
            thread::sleep(Duration::from_millis(10));
        }
        workers_stopped &= worker.is_finished() && worker.join().is_ok();
    }
    let mut status = session.lock();
    if !workers_stopped && status.io_error.is_none() {
        status.io_error = Some("未能确认终端读写已结束".into());
    }
    status.exit_code = exit_code;
    status.record_cleanup(result);
    session.changed.notify_all();
    drop(status);
    notice(&app, SESSION_EVENT);
}

fn await_output(session: &TerminalSession, timeout: Duration) {
    let (mut status, _) = session
        .changed
        .wait_timeout_while(session.lock(), timeout, |status| {
            !status.discard_output
                && (!status.read_closed || status.output_sent != status.output_acknowledged)
        })
        .unwrap_or_else(|error| error.into_inner());
    if !status.discard_output
        && (!status.read_closed || status.output_sent != status.output_acknowledged)
    {
        status.discard_output = true;
        status.io_error = Some("Shell 已退出，终端尾部输出未能完整确认".into());
        session.changed.notify_all();
    }
}

fn read_output(
    session: &TerminalSession,
    mut reader: Box<dyn Read + Send>,
    output: TerminalOutput,
) {
    let mut buffer = [0; OUTPUT_READ_CHUNK];
    'read: loop {
        let status = session.lock();
        let status = session
            .changed
            .wait_while(status, |status| {
                !status.discard_output
                    && status.output_sent - status.output_acknowledged
                        > (OUTPUT_LIMIT - OUTPUT_READ_CHUNK) as u64
            })
            .unwrap_or_else(|error| error.into_inner());
        if status.discard_output && status.result.is_some() {
            break;
        }
        drop(status);
        let bytes = match reader.read(&mut buffer) {
            Ok(0) => break,
            Ok(bytes) => bytes,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(error) if error.kind() == io::ErrorKind::BrokenPipe => break,
            // The Unix PTY reports EIO when its final slave descriptor closes.
            #[cfg(unix)]
            Err(error) if error.raw_os_error() == Some(libc::EIO) => break,
            Err(_) => {
                session.fail_io("无法读取终端输出");
                break;
            }
        };
        for frame in buffer[..bytes].chunks(OUTPUT_FRAME_LIMIT) {
            let mut status = session.lock();
            if status.discard_output {
                continue 'read;
            }
            status.output_sent += frame.len() as u64;
            drop(status);
            if output
                .send(InvokeResponseBody::Raw(frame.to_vec()))
                .is_err()
            {
                session.fail_io("终端窗口已断开");
                break 'read;
            }
        }
    }
    session.lock().read_closed = true;
    session.changed.notify_all();
}

#[tauri::command]
pub(super) async fn terminal_ack(
    window: tauri::Webview,
    session_id: String,
    cursor: u64,
) -> TerminalAckResult {
    let Ok(session) = terminal_session(&window, &session_id) else {
        return TerminalAckResult::Unavailable;
    };
    let mut status = session.lock();
    if status.discard_output {
        return TerminalAckResult::Unavailable;
    }
    if cursor > status.output_sent {
        return TerminalAckResult::InvalidCursor;
    }
    if cursor > status.output_acknowledged {
        status.output_acknowledged = cursor;
        session.changed.notify_all();
    }
    TerminalAckResult::Acknowledged
}

#[tauri::command]
pub(super) async fn terminal_write(
    window: tauri::Webview,
    session_id: String,
    offset: u64,
    bytes: Vec<u8>,
) -> TerminalWriteResult {
    if bytes.is_empty() || bytes.len() > INPUT_LIMIT {
        return TerminalWriteResult::InvalidInput;
    }
    let Some(end) = offset.checked_add(bytes.len() as u64) else {
        return TerminalWriteResult::InvalidOffset;
    };
    let Ok(session) = terminal_session(&window, &session_id) else {
        return TerminalWriteResult::Closed;
    };
    let mut status = session.lock();
    if status.discard_output {
        return TerminalWriteResult::Closed;
    }
    if end <= status.input_enqueued {
        return TerminalWriteResult::Accepted {
            next_offset: status.input_enqueued,
        };
    }
    if offset != status.input_enqueued {
        return TerminalWriteResult::InvalidOffset;
    }
    let Some(input) = status.input.as_ref() else {
        return TerminalWriteResult::Closed;
    };
    match input.try_send(bytes) {
        Ok(()) => {
            status.input_enqueued = end;
            TerminalWriteResult::Accepted { next_offset: end }
        }
        Err(TrySendError::Full(_)) => TerminalWriteResult::QueueFull,
        Err(TrySendError::Disconnected(_)) => TerminalWriteResult::Closed,
    }
}

#[tauri::command]
pub(super) async fn terminal_resize(
    window: Webview,
    session_id: String,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    verify_size(cols, rows)?;
    let session = terminal_session(&window, &session_id)?;
    tauri::async_runtime::spawn_blocking(move || {
        let status = session.lock();
        if status.closing {
            return Err("终端已结束".into());
        }
        status
            .control
            .as_ref()
            .ok_or("终端已结束")?
            .resize(cols, rows)
    })
    .await
    .map_err(|_| "调整终端尺寸失败")?
}

#[tauri::command]
pub(super) async fn terminal_close(
    window: Webview,
    connection_id: String,
    session_id: String,
) -> Result<(), String> {
    let (app, generation) = document(&window)?;
    let renderer = window.label().to_owned();
    tauri::async_runtime::spawn_blocking(move || {
        let session = {
            let desktop = app.state::<Mutex<State>>();
            let desktop = desktop.lock().map_err(|_| "桌面工作台状态不可用")?;
            check_connection(&desktop, &connection_id)?;
            let terminals = app.state::<TerminalState>();
            let state = terminals.window.lock().map_err(|_| "终端窗口状态不可用")?;
            if !state.owns_document(&renderer, generation) {
                return Err("终端窗口已断开，请重新打开".into());
            }
            let Some(session) = state.sessions.get(&session_id) else {
                return Ok(());
            };
            if session.connection_id != connection_id {
                return Err("终端会话不属于当前工作台连接".into());
            }
            session.clone()
        };
        let result = reap(&app, vec![session]);
        notice(&app, SESSION_EVENT);
        result
    })
    .await
    .map_err(|_| "关闭终端任务失败")?
}
