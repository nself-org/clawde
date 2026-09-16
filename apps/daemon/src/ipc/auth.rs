use anyhow::Result;
use std::path::Path;
use uuid::Uuid;

/// Return the auth token for this daemon instance.
///
/// On first call, generates a random 32-character hex token and writes it to
/// `{data_dir}/auth_token` with user-only read/write permissions (mode 0600
/// on Unix). On subsequent calls, reads and returns the existing token.
///
/// The token file must be kept secret — it is the only credential protecting
/// the local WebSocket port from unauthorized access by other processes on
/// the same machine.
pub fn get_or_create_token(data_dir: &Path) -> Result<String> {
    let path = data_dir.join("auth_token");

    if path.exists() {
        let token = std::fs::read_to_string(&path)?.trim().to_string();
        if !token.is_empty() {
            return Ok(token);
        }
    }

    // Generate a new token (UUID v4, hex without dashes = 32 chars)
    let token = Uuid::new_v4().to_string().replace('-', "");

    std::fs::create_dir_all(data_dir)?;

    // Create the file with owner-only permissions from the start to eliminate
    // the TOCTOU window that would exist if we wrote first and chmod'd second.
    #[cfg(unix)]
    {
        use std::io::Write;
        use std::os::unix::fs::OpenOptionsExt;
        let mut f = std::fs::OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(true)
            .mode(0o600)
            .open(&path)?;
        f.write_all(token.as_bytes())?;
    }
    #[cfg(not(unix))]
    std::fs::write(&path, &token)?;

    Ok(token)
}

/// Validate a `Bearer <token>` authorization string against the expected token.
/// Returns `true` if the header value is exactly `"Bearer {expected_token}"`.
pub fn validate_bearer(header_value: &str, expected_token: &str) -> bool {
    header_value
        .strip_prefix("Bearer ")
        .map(|t| t == expected_token)
        .unwrap_or(false)
}

/// The only permission bits the auth token file may carry: owner read/write.
pub const SECURE_TOKEN_MODE: u32 = 0o600;

/// What an inspection of the auth token file's permissions found.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TokenPermissions {
    /// No token file exists yet, so there is nothing to check.
    Absent,
    /// Exactly owner read/write.
    Secure,
    /// Reachable by someone other than the owner; carries the offending mode.
    Insecure { mode: u32 },
    /// The file exists but its metadata could not be read, or the platform has
    /// no Unix permission bits. Not evidence of a bad mode either way.
    Unknown,
}

/// Inspect the permissions on the auth token file (DC.T42).
///
/// Split out from `check_token_permissions` so the decision can be asserted:
/// a function whose only output is a log line cannot be tested, and every
/// mutant the gate generated for the combined version survived for that
/// reason.
pub fn token_permissions(data_dir: &Path) -> TokenPermissions {
    let path = data_dir.join("auth_token");
    if !path.exists() {
        return TokenPermissions::Absent;
    }

    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        match std::fs::metadata(&path) {
            Ok(meta) => {
                // Mask off the file-type bits; only the permission bits matter.
                let mode = meta.permissions().mode() & 0o777;
                if mode == SECURE_TOKEN_MODE {
                    TokenPermissions::Secure
                } else {
                    TokenPermissions::Insecure { mode }
                }
            }
            Err(_) => TokenPermissions::Unknown,
        }
    }
    #[cfg(not(unix))]
    {
        TokenPermissions::Unknown
    }
}

/// Check that the auth token file has secure permissions (DC.T42).
///
/// On Unix, warns if the file is not exclusively owner read/write (0o600).
/// No automatic correction is made — the user must run `chmod 0600 <path>`.
pub fn check_token_permissions(data_dir: &Path) {
    if let TokenPermissions::Insecure { mode } = token_permissions(data_dir) {
        let path = data_dir.join("auth_token");
        tracing::warn!(
            path = %path.display(),
            mode = format!("{mode:04o}"),
            "auth_token file has insecure permissions (expected 0600). \
             Run: chmod 0600 {}",
            path.display()
        );
    }
}

// Tests live in auth/tests.rs.
#[cfg(test)]
mod tests;
