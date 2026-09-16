// SPDX-License-Identifier: MIT
//! Guards applied to a provider's output stream as it is read.
//!
//! Both the Codex and Cursor runners read a child process's stdout and stderr
//! line by line, and both apply the same two rules to every line: notice when
//! the provider is telling us it has been rate limited, and stop accumulating
//! once the captured output would pass a byte cap.
//!
//! The rules lived inline and identically in both runners, which meant they
//! could only be exercised by spawning a real child process — so in practice
//! they were not exercised at all.  Pulling them out here makes them ordinary
//! pure functions with ordinary tests, and leaves one definition instead of
//! two to keep in step.

/// Substrings that mean a provider is refusing work because of a rate limit.
///
/// Matched case-insensitively against a single line of provider output.
const RATE_LIMIT_MARKERS: [&str; 4] = ["rate limit", "rate_limit", "too many requests", "429"];

/// Returns `true` if `line` looks like a rate-limit notice from a provider.
///
/// # Inputs
/// * `line` — one raw line of provider stderr/stdout, any case.
///
/// # Outputs
/// `true` when any known marker appears anywhere in the line.
///
/// # Constraints
/// Matching is case-insensitive and substring-based, so it is deliberately
/// permissive: a false positive costs one spurious `RATE_LIMITED` status
/// event, whereas a false negative means a stalled session with no
/// explanation.
pub(crate) fn is_rate_limit_notice(line: &str) -> bool {
    let lower = line.to_lowercase();
    RATE_LIMIT_MARKERS
        .iter()
        .any(|marker| lower.contains(marker))
}

/// Returns `true` if appending `line_len` bytes to `accumulated_len` bytes
/// would push the captured output past `cap`.
///
/// # Inputs
/// * `accumulated_len` — bytes captured so far.
/// * `line_len` — bytes in the line about to be appended.
/// * `cap` — the hard ceiling, in bytes.
///
/// # Outputs
/// `true` when the line must be refused.
///
/// # Constraints
/// The `+ 1` accounts for the newline the caller appends after each line, so
/// the check matches what is actually stored rather than what was read.  The
/// comparison is strict: landing exactly on `cap` is allowed, since the cap is
/// the largest permitted size and not the first forbidden one.
pub(crate) fn would_exceed_cap(accumulated_len: usize, line_len: usize, cap: usize) -> bool {
    accumulated_len + line_len + 1 > cap
}

#[cfg(test)]
mod tests;
