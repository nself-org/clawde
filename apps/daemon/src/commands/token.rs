//! `clawd token` command handler.
//!
//! Purpose: Display or render-as-QR-code the daemon's auth token.
//! Inputs:  Daemon config (data dir, port), optional `--relay` flag.
//! Outputs: Token string to stdout, or Unicode QR code to stdout with warning on stderr.
//! Constraints: Sync; reads the `auth_token` file directly — no daemon required.

use anyhow::Result;
use clawd::config::DaemonConfig;

/// Print the raw auth token to stdout.
///
/// Purpose: Let scripts and users retrieve the auth token for manual pairing.
/// Inputs:  Daemon config (data dir only).
/// Outputs: Token string printed to stdout.
/// Constraints: Exits 1 if the token file is missing (daemon not running).
pub fn run_token_show(config: &DaemonConfig) -> Result<()> {
    let token_path = config.data_dir.join("auth_token");
    match std::fs::read_to_string(&token_path) {
        Ok(token) => {
            println!("{}", token.trim());
            Ok(())
        }
        Err(_) => {
            eprintln!("error: auth token not found at {}", token_path.display());
            eprintln!("       Is the daemon running? Start it with: clawd start");
            std::process::exit(1);
        }
    }
}

/// Render the auth token as a Unicode QR code for mobile pairing.
///
/// Purpose: Generate a `clawd://connect?...` deep-link QR code for local IP pairing.
/// Inputs:  Daemon config (data dir + port), `use_relay` flag to append `&relay=1`.
/// Outputs: Warning on stderr; QR code rendered to stdout via `Dense1x2` Unicode.
/// Constraints: Sync; reads the `auth_token` file and detects the local IP.
pub fn run_token_qr(config: &DaemonConfig, use_relay: bool) -> Result<()> {
    use std::net::{IpAddr, Ipv4Addr};

    let token_path = config.data_dir.join("auth_token");
    let token = match std::fs::read_to_string(&token_path) {
        Ok(t) => t.trim().to_string(),
        Err(_) => {
            eprintln!("error: auth token not found at {}", token_path.display());
            eprintln!("       Is the daemon running? Start it with: clawd start");
            std::process::exit(1);
        }
    };

    let ip = local_ip_address::local_ip().unwrap_or_else(|_| {
        eprintln!("warning: could not detect local IP — using 127.0.0.1");
        IpAddr::V4(Ipv4Addr::LOCALHOST)
    });

    let relay_suffix = if use_relay { "&relay=1" } else { "" };
    let payload = format!(
        "clawd://connect?host={}&port={}&token={}{}",
        ip, config.port, token, relay_suffix
    );

    eprintln!("Warning: This QR code contains your auth token. Only share with trusted devices.");

    let code = qrcode::QrCode::new(payload.as_bytes())
        .map_err(|e| anyhow::anyhow!("failed to generate QR code: {e}"))?;
    let image = code.render::<qrcode::render::unicode::Dense1x2>().build();
    println!("{}", image);

    Ok(())
}
