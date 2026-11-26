use serde::{Deserialize, Serialize};
use tauri::{LogicalPosition, LogicalSize, PhysicalPosition, Window, window::Monitor};

pub(super) const DEFAULT_SIZE: LogicalSize<f64> = LogicalSize::new(1360.0, 820.0);
pub(super) const MINIMUM_SIZE: LogicalSize<f64> = LogicalSize::new(740.0, 520.0);
const MARGIN: f64 = 12.0;
const TITLE_INSET: LogicalPosition<f64> = LogicalPosition::new(24.0, 16.0);
const TITLE_GRAB_WIDTH: f64 = 96.0;
const FILE_NAME: &str = "window-placement.json";

#[derive(Deserialize, Serialize)]
struct Placement {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

impl Placement {
    fn read() -> Option<Self> {
        let path = super::config_file().ok()?;
        let root = yss::fs::Root::open(path.parent()?).ok()?;
        let name = yss::model::RelPath::new(FILE_NAME).ok()?;
        let file = root.read_checked(&name, 4096).ok()?;
        let saved: Self = serde_json::from_slice(&file.bytes).ok()?;
        (saved.width > 0.0 && saved.height > 0.0).then_some(saved)
    }

    fn capture(window: &Window) -> tauri::Result<Self> {
        let scale = window.scale_factor()?;
        let position = desktop_position(window.outer_position()?, scale);
        let size = window.inner_size()?.to_logical::<f64>(scale);
        Ok(Self {
            x: position.x,
            y: position.y,
            width: size.width,
            height: size.height,
        })
    }

    fn monitor(&self, window: &Window) -> tauri::Result<Option<Monitor>> {
        // Test a point inside the title bar, away from the native resize border.
        window.monitor_from_point(self.x + TITLE_INSET.x, self.y + TITLE_INSET.y)
    }

    fn has_visible_title(&self, monitor: &Monitor) -> bool {
        let scale = monitor.scale_factor();
        let units = desktop_units_per_point(scale);
        let area = monitor.work_area();
        let origin = desktop_position(area.position, scale);
        let size = area.size.to_logical::<f64>(scale);
        title_is_visible(self, origin, size, units)
    }
}

pub(super) fn restore(window: &Window) -> tauri::Result<()> {
    let saved = Placement::read();
    if let Some(saved) = &saved
        && let Some(monitor) = saved.monitor(window)?
    {
        return place(window, &monitor, Some(saved), true);
    }
    if let Some(monitor) = preferred_monitor(window)? {
        place(window, &monitor, saved.as_ref(), false)?;
    }
    Ok(())
}

pub(super) fn keep_on_screen(window: &Window) -> tauri::Result<()> {
    if window.is_maximized()? || window.is_fullscreen()? {
        return Ok(());
    }
    let current = Placement::capture(window)?;
    if let Some(monitor) = current.monitor(window)? {
        if !current.has_visible_title(&monitor) {
            place(window, &monitor, Some(&current), true)?;
        }
    } else if let Some(monitor) = preferred_monitor(window)? {
        place(window, &monitor, Some(&current), false)?;
    }
    Ok(())
}

fn title_is_visible(
    placement: &Placement,
    work_origin: LogicalPosition<f64>,
    work_size: LogicalSize<f64>,
    units: f64,
) -> bool {
    let title_x = placement.x + TITLE_INSET.x * units;
    let title_y = placement.y + TITLE_INSET.y * units;
    let grab_width = TITLE_GRAB_WIDTH.min((placement.width - TITLE_INSET.x).max(0.0));
    title_x >= work_origin.x
        && title_x + grab_width * units <= work_origin.x + work_size.width * units
        && title_y >= work_origin.y
        && title_y < work_origin.y + work_size.height * units
}

pub(super) fn save(window: &Window) {
    if !window.is_visible().unwrap_or(false)
        || window.is_minimized().unwrap_or(true)
        || window.is_maximized().unwrap_or(true)
        || window.is_fullscreen().unwrap_or(true)
    {
        return;
    }
    if let Ok(placement) = Placement::capture(window) {
        let _ = super::save_desktop_file(FILE_NAME, &placement);
    }
}

fn preferred_monitor(window: &Window) -> tauri::Result<Option<Monitor>> {
    let primary = window.primary_monitor()?;
    if let Ok(cursor) = window.cursor_position() {
        // Tao reports the cursor at the primary scale on macOS, while its
        // monitor lookup expects AppKit points. Windows uses physical pixels.
        let cursor = desktop_position(cursor, primary.as_ref().map_or(1.0, Monitor::scale_factor));
        if let Some(monitor) = window.monitor_from_point(cursor.x, cursor.y)? {
            return Ok(Some(monitor));
        }
    }
    Ok(primary)
}

fn desktop_position<T: tauri::Pixel>(
    position: PhysicalPosition<T>,
    scale: f64,
) -> LogicalPosition<f64> {
    position.to_logical(if cfg!(target_os = "macos") {
        scale
    } else {
        1.0
    })
}

fn desktop_units_per_point(scale: f64) -> f64 {
    if cfg!(target_os = "macos") {
        1.0
    } else {
        scale
    }
}

fn place(
    window: &Window,
    monitor: &Monitor,
    saved: Option<&Placement>,
    restore_position: bool,
) -> tauri::Result<()> {
    let scale = monitor.scale_factor();
    let work = monitor.work_area();
    let origin = desktop_position(work.position, scale);
    let usable = work.size.to_logical::<f64>(scale);
    let window_scale = window.scale_factor()?;
    let inner = window.inner_size()?.to_logical::<f64>(window_scale);
    let outer = window.outer_size()?.to_logical::<f64>(window_scale);
    let available_width = usable.width - (outer.width - inner.width).max(0.0);
    let available_height = usable.height - (outer.height - inner.height).max(0.0);
    let (width, min_width, x_max) = fit_axis(
        available_width,
        saved.map_or(DEFAULT_SIZE.width, |p| p.width),
        MINIMUM_SIZE.width,
    );
    let (height, min_height, y_max) = fit_axis(
        available_height,
        saved.map_or(DEFAULT_SIZE.height, |p| p.height),
        MINIMUM_SIZE.height,
    );
    let units_per_point = desktop_units_per_point(scale);
    let x = origin.x + x_max * units_per_point / 2.0;
    let y = origin.y + y_max * units_per_point / 2.0;
    let (x, y) = match saved.filter(|_| restore_position) {
        Some(saved) => (
            saved.x.clamp(origin.x, origin.x + x_max * units_per_point),
            saved.y.clamp(origin.y, origin.y + y_max * units_per_point),
        ),
        None => (x, y),
    };
    let position = if cfg!(target_os = "macos") {
        tauri::Position::from(LogicalPosition::new(x, y))
    } else {
        tauri::Position::from(PhysicalPosition::new(x.round() as i32, y.round() as i32))
    };
    // Move first so the logical size uses the target monitor's scale on Windows.
    window.set_position(position)?;
    window.set_min_size(Some(LogicalSize::new(min_width, min_height)))?;
    window.set_size(LogicalSize::new(width, height))?;
    window.set_position(position)
}

fn fit_axis(available: f64, desired: f64, minimum: f64) -> (f64, f64, f64) {
    let maximum = (available - 2.0 * MARGIN).max(1.0);
    let minimum = minimum.min(maximum);
    let size = desired.clamp(minimum, maximum);
    (size, minimum, (available - size).max(0.0))
}
