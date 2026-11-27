use crate::{MAIN_VIEW, WORKBENCH_VIEW_ACTIVE, config_file, save_desktop_file, terminal};
use serde::{Deserialize, Serialize};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, AtomicU16, Ordering},
};
use tauri::{AppHandle, Emitter, Manager, PhysicalPosition, PhysicalSize, Webview, Window};

const PREFERENCES_FILE: &str = "terminal-layout.json";
const DEFAULT_RATIO: f64 = 0.4;
const WORKBENCH_MIN_WIDTH: f64 = 560.0;
const TERMINAL_MIN_WIDTH: f64 = 360.0;
const COMPACT_RAIL_WIDTH: u16 = 62;
const EXPANDED_RAIL_WIDTH: u16 = 168;
const MIN_RAIL_EXPANSION_WIDTH: f64 = 720.0;
const DEFAULT_RAIL_EXPANSION_WIDTH: f64 = 880.0;
const UNKNOWN_RAIL_WIDTH: u16 = u16::MAX;

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct SavedPreferences {
    split_ratio: f64,
}

fn saved_ratio() -> Option<f64> {
    let path = config_file().ok()?;
    let root = yss::fs::Root::open(path.parent()?).ok()?;
    let filename = yss::model::RelPath::new(PREFERENCES_FILE).ok()?;
    let file = root.read_checked(&filename, 4096).ok()?;
    let saved = serde_json::from_slice::<SavedPreferences>(&file.bytes).ok()?;
    (saved.split_ratio > 0.0 && saved.split_ratio < 1.0).then_some(saved.split_ratio)
}

#[derive(Clone, Copy)]
struct PanelPreference {
    visible: bool,
    maximized: bool,
    ratio: f64,
    preview_ratio: Option<f64>,
}

struct Preferences {
    panel: PanelPreference,
    rail_width: Arc<AtomicU16>,
    applied: Option<Layout>,
}

pub(super) struct PanelState {
    preferences: Mutex<Preferences>,
    resize_requested: AtomicBool,
    resize_running: AtomicBool,
}

impl Default for PanelState {
    fn default() -> Self {
        Self {
            preferences: Mutex::new(Preferences {
                panel: PanelPreference {
                    visible: false,
                    maximized: false,
                    ratio: saved_ratio().unwrap_or(DEFAULT_RATIO),
                    preview_ratio: None,
                },
                rail_width: Arc::new(AtomicU16::new(UNKNOWN_RAIL_WIDTH)),
                applied: None,
            }),
            resize_requested: AtomicBool::new(false),
            resize_running: AtomicBool::new(false),
        }
    }
}

#[derive(Clone, Copy, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum Mode {
    Hidden,
    Split,
    Maximized,
}

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Layout {
    revision: u64,
    split_epoch: u64,
    mode: Mode,
    temporary: bool,
    ratio: f64,
    terminal_width: f64,
    min_ratio: Option<f64>,
    max_ratio: Option<f64>,
}

impl Layout {
    pub(super) fn is_maximized(&self) -> bool {
        self.mode == Mode::Maximized
    }

    pub(super) fn is_visible(&self) -> bool {
        self.mode != Mode::Hidden
    }
}

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
struct RailViewport {
    can_expand: bool,
    default_collapsed: bool,
}

#[derive(Clone, Copy)]
struct SplitArea {
    width: u32,
    min_terminal: u32,
    max_terminal: u32,
}

impl SplitArea {
    fn terminal_width(self, requested: f64) -> u32 {
        (requested.round() as u32).clamp(self.min_terminal, self.max_terminal)
    }

    fn ratio(self, width: u32) -> f64 {
        f64::from(width) / f64::from(self.width)
    }
}

struct Surface {
    size: PhysicalSize<u32>,
    scale: f64,
    rail_viewport: RailViewport,
    workspace_width: u32,
    split: Option<SplitArea>,
    available: bool,
}

impl Surface {
    fn resolve(
        size: PhysicalSize<u32>,
        scale: f64,
        reported_rail_width: u16,
        available: bool,
    ) -> Self {
        let logical_width = f64::from(size.width) / scale;
        let rail_viewport = RailViewport {
            can_expand: logical_width >= MIN_RAIL_EXPANSION_WIDTH,
            default_collapsed: logical_width < DEFAULT_RAIL_EXPANSION_WIDTH,
        };
        let rail = match reported_rail_width {
            UNKNOWN_RAIL_WIDTH if rail_viewport.default_collapsed => COMPACT_RAIL_WIDTH,
            UNKNOWN_RAIL_WIDTH => EXPANDED_RAIL_WIDTH,
            width => width,
        };
        let rail_width =
            ((f64::from(rail) * scale).round() as u32).min(size.width.saturating_sub(1));
        let workspace_width = size.width - rail_width;
        let min_workbench = (WORKBENCH_MIN_WIDTH * scale).ceil() as u32;
        let min_terminal = (TERMINAL_MIN_WIDTH * scale).ceil() as u32;
        let split = workspace_width
            .checked_sub(min_workbench)
            .filter(|max_terminal| *max_terminal >= min_terminal)
            .map(|max_terminal| SplitArea {
                width: workspace_width,
                min_terminal,
                max_terminal,
            });
        Self {
            size,
            scale,
            rail_viewport,
            workspace_width,
            split,
            available,
        }
    }

    fn layout(&self, preference: PanelPreference) -> Frame {
        let intended_ratio = preference.preview_ratio.unwrap_or(preference.ratio);
        let (mode, temporary, terminal_width, ratio) = if !preference.visible || !self.available {
            (Mode::Hidden, false, 0, preference.ratio)
        } else if let Some(split) = self.split.filter(|_| !preference.maximized) {
            let width = split.terminal_width(f64::from(split.width) * intended_ratio);
            (Mode::Split, false, width, split.ratio(width))
        } else {
            (
                Mode::Maximized,
                !preference.maximized,
                self.workspace_width,
                preference.ratio,
            )
        };
        Frame {
            size: self.size,
            rail_viewport: self.rail_viewport,
            mode,
            temporary,
            ratio,
            terminal_width,
            scale: self.scale,
            split: self.split,
        }
    }
}

struct Frame {
    size: PhysicalSize<u32>,
    rail_viewport: RailViewport,
    mode: Mode,
    temporary: bool,
    ratio: f64,
    terminal_width: u32,
    scale: f64,
    split: Option<SplitArea>,
}

impl Frame {
    fn project(&self, previous: Option<Layout>) -> Layout {
        let revision = previous.map_or(1, |layout| layout.revision + 1);
        let split_epoch = previous
            .filter(|layout| layout.mode == Mode::Split && self.mode == Mode::Split)
            .map_or(revision, |layout| layout.split_epoch);
        Layout {
            revision,
            split_epoch,
            mode: self.mode,
            temporary: self.temporary,
            ratio: self.ratio,
            terminal_width: f64::from(self.terminal_width) / self.scale,
            min_ratio: self.split.map(|area| area.ratio(area.min_terminal)),
            max_ratio: self.split.map(|area| area.ratio(area.max_terminal)),
        }
    }

    fn resized_ratio(&self, intent: ResizeIntent) -> Option<f64> {
        let split = self.split?;
        let width = match intent {
            ResizeIntent::Width { pixels } => split.terminal_width(pixels * self.scale),
            ResizeIntent::Step { pixels } => {
                split.terminal_width(f64::from(self.terminal_width) + pixels * self.scale)
            }
            ResizeIntent::Minimum => split.min_terminal,
            ResizeIntent::Maximum => split.max_terminal,
        };
        Some(split.ratio(width))
    }
}

fn bounds(left: u32, width: u32, height: u32) -> tauri::Rect {
    tauri::Rect {
        position: PhysicalPosition::new(left as i32, 0).into(),
        size: PhysicalSize::new(width, height).into(),
    }
}

fn read_surface(app: &AppHandle, window: &Window, rail: &AtomicU16) -> tauri::Result<Surface> {
    Ok(Surface::resolve(
        window.inner_size()?,
        window.scale_factor()?,
        rail.load(Ordering::Acquire),
        WORKBENCH_VIEW_ACTIVE.load(Ordering::Acquire)
            && app.get_webview("workbench").is_some()
            && terminal::webview(app).is_some(),
    ))
}

fn apply_bounds(app: &AppHandle, frame: &Frame) -> tauri::Result<()> {
    let terminal = terminal::webview(app);
    if frame.mode == Mode::Hidden
        && let Some(terminal) = terminal.as_ref()
    {
        terminal.hide()?;
    }
    let terminal_left = frame.size.width - frame.terminal_width;
    if let Some(workbench) = app.get_webview("workbench") {
        let width = if frame.mode == Mode::Split {
            terminal_left
        } else {
            frame.size.width
        };
        workbench.set_bounds(bounds(0, width, frame.size.height))?;
    }
    if frame.mode != Mode::Hidden
        && let Some(terminal) = terminal
    {
        terminal.set_bounds(bounds(
            terminal_left,
            frame.terminal_width,
            frame.size.height,
        ))?;
        terminal.show()?;
    }
    Ok(())
}

fn publish(app: &AppHandle, frame: &Frame, mut panel: PanelPreference) -> Layout {
    if frame.mode != Mode::Split {
        panel.preview_ratio = None;
    }
    let state = app.state::<PanelState>();
    let mut preferences = state
        .preferences
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let layout = frame.project(preferences.applied);
    preferences.panel = panel;
    preferences.applied = Some(layout);
    drop(preferences);
    if let Some(workbench) = app.get_webview("workbench") {
        let detail = serde_json::json!({
            "revision": layout.revision,
            "mode": layout.mode,
            "railViewport": frame.rail_viewport,
            "visible": layout.is_visible(),
            "windowWidth": f64::from(frame.size.width) / frame.scale,
        });
        let _ = workbench.eval(format!(
            "window.__YSS_TERMINAL_LAYOUT__ = {detail}; window.dispatchEvent(new CustomEvent('yss:terminal-layout', {{detail: window.__YSS_TERMINAL_LAYOUT__}}));"
        ));
    }
    if let Some(terminal) = terminal::webview(app) {
        let _ = app.emit_to(
            tauri::EventTarget::webview(terminal.label()),
            "terminal:panel-layout",
            layout,
        );
    }
    layout
}

// Layout callers hold MAIN_VIEW to serialize bounds, preferences and publication.
pub(super) fn apply(app: &AppHandle, window: &Window) -> tauri::Result<Layout> {
    let state = app.state::<PanelState>();
    let preferences = state
        .preferences
        .lock()
        .unwrap_or_else(|error| error.into_inner());
    let panel = preferences.panel;
    let rail = Arc::clone(&preferences.rail_width);
    drop(preferences);
    let frame = read_surface(app, window, &rail)?.layout(panel);
    apply_bounds(app, &frame)?;
    Ok(publish(app, &frame, panel))
}

pub(super) fn resized(app: &AppHandle) {
    let state = app.state::<PanelState>();
    state.resize_requested.store(true, Ordering::Release);
    if state.resize_running.swap(true, Ordering::AcqRel) {
        return;
    }
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<PanelState>();
        loop {
            state.resize_requested.store(false, Ordering::Release);
            if let Ok(_guard) = MAIN_VIEW.lock()
                && let Some(window) = app.get_window("workbench")
            {
                let _ = apply(&app, &window);
            }
            state.resize_running.store(false, Ordering::Release);
            if !state.resize_requested.load(Ordering::Acquire)
                || state.resize_running.swap(true, Ordering::AcqRel)
            {
                break;
            }
        }
    });
}

pub(super) fn set_visible(app: &AppHandle, visible: bool) -> Result<(), String> {
    app.state::<PanelState>()
        .preferences
        .lock()
        .map_err(|_| "终端面板状态不可用")?
        .panel
        .visible = visible;
    Ok(())
}

pub(super) fn requested(app: &AppHandle) -> bool {
    app.state::<PanelState>()
        .preferences
        .lock()
        .is_ok_and(|preferences| preferences.panel.visible)
}

pub(super) fn register_workbench_rail(app: &AppHandle) -> Arc<AtomicU16> {
    let state = app.state::<PanelState>();
    let rail = Arc::new(AtomicU16::new(UNKNOWN_RAIL_WIDTH));
    state
        .preferences
        .lock()
        .unwrap_or_else(|error| error.into_inner())
        .rail_width = Arc::clone(&rail);
    rail
}

pub(super) fn queue_rail_width(app: &AppHandle, rail: &AtomicU16, width: u16) {
    if rail.swap(width, Ordering::AcqRel) != width {
        resized(app);
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(super) enum PanelAction {
    ToggleMaximize,
    Hide,
    Read,
    Resize,
}

#[derive(Clone, Copy, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub(super) enum ResizeIntent {
    Width { pixels: f64 },
    Step { pixels: f64 },
    Minimum,
    Maximum,
}

impl ResizeIntent {
    fn is_valid(self) -> bool {
        match self {
            Self::Width { pixels } | Self::Step { pixels } => pixels.is_finite(),
            Self::Minimum | Self::Maximum => true,
        }
    }
}

#[tauri::command]
pub(super) async fn terminal_panel_layout(
    window: Webview,
    action: PanelAction,
    resize: Option<ResizeIntent>,
    split_epoch: Option<u64>,
    commit: Option<bool>,
) -> Result<Layout, String> {
    let app = terminal::local(&window)?.clone();
    tauri::async_runtime::spawn_blocking(move || {
        let _guard = MAIN_VIEW.lock().map_err(|_| "主窗口不可用")?;
        terminal::local(&window)?;
        let window = window.window();
        let state = app.state::<PanelState>();
        let preferences = state.preferences.lock().map_err(|_| "终端面板状态不可用")?;
        let original = preferences.panel;
        let mut panel = original;
        let rail = Arc::clone(&preferences.rail_width);
        let previous = preferences.applied;
        drop(preferences);
        if matches!(action, PanelAction::Read) {
            return previous.map_or_else(
                || apply(&app, &window).map_err(|_| "无法读取终端面板尺寸".into()),
                Ok,
            );
        }

        let surface = read_surface(&app, &window, &rail).map_err(|_| "无法读取终端面板尺寸")?;
        let current = surface.layout(panel);
        let mut save = false;
        match action {
            PanelAction::Hide => panel.visible = false,
            PanelAction::ToggleMaximize if current.mode != Mode::Hidden && !current.temporary => {
                panel.maximized = !panel.maximized;
            }
            PanelAction::Resize
                if current.mode == Mode::Split
                    && previous.is_some_and(|layout| {
                        layout.mode == Mode::Split && Some(layout.split_epoch) == split_epoch
                    }) =>
            {
                let intent = resize
                    .filter(|intent| intent.is_valid())
                    .ok_or("终端宽度无效")?;
                let ratio = current
                    .resized_ratio(intent)
                    .ok_or("当前窗口无法并排显示")?;
                if commit.unwrap_or(false) {
                    panel.ratio = ratio;
                    panel.preview_ratio = None;
                    save = true;
                } else {
                    panel.preview_ratio = Some(ratio);
                }
            }
            _ => return apply(&app, &window).map_err(|_| "无法读取终端面板尺寸".into()),
        }

        let next = surface.layout(panel);
        if apply_bounds(&app, &next).is_err() {
            let _ = apply_bounds(&app, &current);
            return Err("无法调整终端面板".into());
        }
        if save
            && let Err(error) = save_desktop_file(
                PREFERENCES_FILE,
                &SavedPreferences {
                    split_ratio: panel.ratio,
                },
            )
        {
            let committed = PanelPreference {
                preview_ratio: None,
                ..original
            };
            let rollback = surface.layout(committed);
            if apply_bounds(&app, &rollback).is_ok() {
                publish(&app, &rollback, committed);
            }
            return Err(error);
        }
        let layout = publish(&app, &next, panel);
        if matches!(action, PanelAction::Hide)
            && let Some(workbench) = app.get_webview("workbench")
        {
            let _ = workbench.set_focus();
        }
        Ok(layout)
    })
    .await
    .map_err(|_| "终端面板调整任务失败")?
}
