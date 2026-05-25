// lib.rs — Tauri 2 application library entry point for ClawDE desktop.
//
// Purpose: Configure Tauri plugins, register commands, manage daemon lifecycle,
//          set up system tray, and handle window close-to-tray.
// Inputs:  tauri.conf.json, bundled sidecar, capabilities JSON
// Outputs: Running Tauri application
// Constraints: Single main window; daemon must be bundled as sidecar
// SPORT: T-E1-07 — Tauri 2 migration

mod commands;
mod daemon;

use daemon::DaemonState;
use tauri::{
    menu::{Menu, MenuItemBuilder},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};
use tracing_subscriber::{fmt, EnvFilter};

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Initialise tracing
    fmt()
        .with_env_filter(
            EnvFilter::try_from_env("CLAWD_LOG").unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .init();

    let daemon_state = DaemonState::new();

    tauri::Builder::default()
        // ── Plugins ───────────────────────────────────────────────────────
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_os::init())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        // ── Managed state ─────────────────────────────────────────────────
        .manage(daemon_state)
        // ── Commands ──────────────────────────────────────────────────────
        .invoke_handler(tauri::generate_handler![
            commands::health_check,
            commands::list_sessions,
            commands::get_session,
            commands::create_session,
            commands::submit_task,
            commands::get_metrics,
            commands::get_memory,
            commands::daemon_status,
            commands::pick_project_folder,
        ])
        // ── Setup ─────────────────────────────────────────────────────────
        .setup(|app| {
            let handle = app.handle().clone();
            let state: tauri::State<'_, DaemonState> = app.state();
            // Safety: we need to extract the inner Arc to move into async task.
            // DaemonState is Sync so this is sound.
            let state_ref = state.inner() as *const DaemonState;

            // Start daemon in background before window appears.
            tauri::async_runtime::spawn(async move {
                // SAFETY: state lives for the lifetime of the app.
                let state = unsafe { &*state_ref };
                daemon::ensure_daemon_running(&handle, state).await;
            });

            // System tray
            let quit = MenuItemBuilder::with_id("quit", "Quit ClawDE").build(app)?;
            let show = MenuItemBuilder::with_id("show", "Show ClawDE").build(app)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;

            TrayIconBuilder::new()
                .menu(&menu)
                .icon(app.default_window_icon().unwrap().clone())
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "quit" => {
                        app.exit(0);
                    }
                    "show" => {
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                })
                .build(app)?;

            Ok(())
        })
        // ── Window events ─────────────────────────────────────────────────
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                // Minimise to tray instead of quitting.
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running ClawDE");
}
