use std::{
    sync::atomic::{AtomicU64, Ordering},
    thread,
    time::Duration,
};

use objc2::MainThreadMarker;
use tauri::{
    AppHandle, LogicalPosition, LogicalSize, Manager, WebviewUrl, WebviewWindow,
    WebviewWindowBuilder, WindowEvent,
    image::Image,
    tray::{MouseButton, MouseButtonState, TrayIcon, TrayIconEvent},
    window::{Color, Effect, EffectState, EffectsBuilder},
};

const LABEL: &str = "menu-bar";
pub(super) const TRAY_ID: &str = "yss-menu-bar";
const WIDTH: f64 = 360.0;
const HEIGHT: f64 = 380.0;
const MARGIN: f64 = 8.0;
static PRESENTATION: AtomicU64 = AtomicU64::new(0);

pub(super) fn icon() -> Image<'static> {
    // Monochrome Y with antialiased edges, rendered as a macOS template image.
    const SIZE: u32 = 36;
    const SAMPLES: u32 = 4;
    let outline = [
        (6.0, 8.0),
        (12.0, 8.0),
        (18.0, 16.0),
        (24.0, 8.0),
        (30.0, 8.0),
        (20.5, 21.0),
        (20.5, 28.0),
        (15.5, 28.0),
        (15.5, 21.0),
    ];
    let mut pixels = Vec::with_capacity((SIZE * SIZE * 4) as usize);
    for y in 0..SIZE {
        for x in 0..SIZE {
            let mut covered = 0;
            for sub_y in 0..SAMPLES {
                for sub_x in 0..SAMPLES {
                    let point_x = f64::from(x) + (f64::from(sub_x) + 0.5) / f64::from(SAMPLES);
                    let point_y = f64::from(y) + (f64::from(sub_y) + 0.5) / f64::from(SAMPLES);
                    let inside = outline.iter().zip(outline.iter().cycle().skip(1)).fold(
                        false,
                        |inside, (&(x1, y1), &(x2, y2))| {
                            inside
                                ^ ((y1 > point_y) != (y2 > point_y)
                                    && point_x < (x2 - x1) * (point_y - y1) / (y2 - y1) + x1)
                        },
                    );
                    covered += u32::from(inside);
                }
            }
            pixels.extend([0, 0, 0, (covered * 255 / (SAMPLES * SAMPLES)) as u8]);
        }
    }
    Image::new_owned(pixels, SIZE, SIZE)
}

pub(super) fn hide(app: &AppHandle) -> tauri::Result<()> {
    PRESENTATION.fetch_add(1, Ordering::Relaxed);
    if let Some(window) = app.get_webview_window(LABEL) {
        window.hide()?;
    }
    Ok(())
}

pub(super) fn prepare(app: &AppHandle) -> tauri::Result<()> {
    let handle = app.clone();
    app.run_on_main_thread(move || {
        let _ = panel_window(&handle);
    })
}

fn panel_window(app: &AppHandle) -> tauri::Result<WebviewWindow> {
    if let Some(window) = app.get_webview_window(LABEL) {
        return Ok(window);
    }
    let window = WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App("menu-bar.html".into()))
        .title("YSS 工作台菜单栏")
        .inner_size(WIDTH, HEIGHT)
        .decorations(false)
        .resizable(false)
        .minimizable(false)
        .maximizable(false)
        .always_on_top(true)
        .visible_on_all_workspaces(true)
        .accept_first_mouse(true)
        .shadow(true)
        .transparent(true)
        .background_color(Color(0, 0, 0, 0))
        .effects(
            EffectsBuilder::new()
                .effect(Effect::Popover)
                .state(EffectState::Active)
                .radius(16.0)
                .build(),
        )
        .visible(false)
        .focused(false)
        .on_navigation(|url| {
            url.scheme() == "tauri"
                && url.host_str() == Some("localhost")
                && url.path() == "/menu-bar.html"
        })
        .on_new_window(|_, _| tauri::webview::NewWindowResponse::Deny)
        .build()?;

    let handle = app.clone();
    window.on_window_event(move |event| match event {
        WindowEvent::Focused(true) => {
            PRESENTATION.fetch_add(1, Ordering::Relaxed);
        }
        WindowEvent::Focused(false) => dismiss_unfocused(handle.clone()),
        _ => {}
    });
    Ok(window)
}

fn dismiss_unfocused(app: AppHandle) {
    let presentation = PRESENTATION.fetch_add(1, Ordering::Relaxed) + 1;
    thread::spawn(move || {
        // A status item click can resign the WebView before the native click arrives.
        thread::sleep(Duration::from_millis(120));
        let handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            if PRESENTATION.load(Ordering::Relaxed) == presentation
                && handle
                    .get_webview_window(LABEL)
                    .is_some_and(|window| window.is_focused().ok() == Some(false))
            {
                let _ = hide(&handle);
            }
        });
    });
}

pub(super) fn on_tray_event(tray: &TrayIcon, event: TrayIconEvent) {
    let TrayIconEvent::Click {
        button: MouseButton::Left,
        button_state: MouseButtonState::Down,
        ..
    } = event
    else {
        return;
    };
    let app = tray.app_handle().clone();
    let tray = tray.clone();
    let _ = app.run_on_main_thread(move || {
        let app = tray.app_handle();
        let result = panel_window(app).and_then(|window| {
            if window.is_visible()? {
                hide(app)
            } else {
                show(&window, Some(&tray))
            }
        });
        if let Err(error) = result {
            eprintln!("无法打开或收起菜单栏面板：{error}");
        }
    });
}

pub(super) fn open_from_menu(app: AppHandle) {
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        let tray = handle.tray_by_id(TRAY_ID);
        if let Err(error) = panel_window(&handle).and_then(|window| show(&window, tray.as_ref())) {
            eprintln!("无法打开菜单栏面板：{error}");
        }
    });
}

fn show(window: &WebviewWindow, tray: Option<&TrayIcon>) -> tauri::Result<()> {
    PRESENTATION.fetch_add(1, Ordering::Relaxed);
    if let Some((position, size)) = tray.and_then(panel_geometry) {
        window.set_size(size)?;
        window.set_position(position)?;
    } else {
        // A crowded or hidden menu bar may have no accessible status item.
        window.set_size(LogicalSize::new(WIDTH, HEIGHT))?;
        window.center()?;
    }
    window.show()?;
    window.set_focus()
}

fn panel_geometry(tray: &TrayIcon) -> Option<(LogicalPosition<f64>, LogicalSize<f64>)> {
    tray.with_inner_tray_icon(|tray| {
        let main_thread = MainThreadMarker::new()?;
        let status_item = tray.ns_status_item()?;
        let window = status_item.button(main_thread)?.window()?;
        if !status_item.isVisible() || !window.isVisible() {
            return None;
        }
        let screen = window.screen()?;
        let screen_frame = screen.frame();
        let frame = window.frame();
        let center_x = frame.origin.x + frame.size.width / 2.0;
        let center_y = frame.origin.y + frame.size.height / 2.0;
        if center_x < screen_frame.origin.x
            || center_x >= screen_frame.origin.x + screen_frame.size.width
            || center_y < screen_frame.origin.y
            || center_y >= screen_frame.origin.y + screen_frame.size.height
        {
            return None;
        }

        // tray-icon scales absolute coordinates by the status window's backing scale.
        // The status window and its screen also share the same AppKit point space.
        let scale = window.backingScaleFactor();
        let anchor = tray.rect()?.position.to_logical::<f64>(scale);
        let work = screen.visibleFrame();
        let work_x = anchor.x + work.origin.x - frame.origin.x;
        let work_y =
            anchor.y + (frame.origin.y + frame.size.height) - (work.origin.y + work.size.height);
        let width = WIDTH.min((work.size.width - 2.0 * MARGIN).max(1.0));
        let height = HEIGHT.min((work.size.height - 2.0 * MARGIN).max(1.0));
        let left = work_x + MARGIN;
        let top = work_y + MARGIN;
        let right = (work_x + work.size.width - width - MARGIN).max(left);
        let bottom = (work_y + work.size.height - height - MARGIN).max(top);
        let x = (anchor.x + (frame.size.width - width) / 2.0).clamp(left, right);
        let y = (anchor.y + frame.size.height + 5.0).clamp(top, bottom);
        Some((LogicalPosition::new(x, y), LogicalSize::new(width, height)))
    })
    .ok()
    .flatten()
}
