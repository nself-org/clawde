// main.rs — Tauri 2 desktop entry point for ClawDE.
//
// Purpose: Bootstrap the Tauri app, start the clawd sidecar daemon,
//          register Tauri commands, and configure tray/shortcuts.
// Inputs:  None (reads from tauri.conf.json + env)
// Outputs: Running GUI window
// Constraints: macOS-first; daemon sidecar must be bundled in resources/
// SPORT: T-E1-07 — Tauri 2 migration

// Prevents additional console window on Windows in release.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    clawde_desktop_lib::run();
}
