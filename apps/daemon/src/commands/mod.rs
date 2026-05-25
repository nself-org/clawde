//! Command handler modules for the clawd binary.
//!
//! Purpose: Groups all `run_*` handler functions that implement clawd subcommands.
//! Inputs:  Parsed CLI arguments forwarded from `main()`.
//! Outputs: Each handler returns `anyhow::Result<()>` or a primitive exit code.
//! Constraints: Handlers are async where they do I/O; sync otherwise.

pub mod account;
pub mod init;
pub mod logs;
pub mod server;
pub mod status;
pub mod tasks;
pub mod token;
pub mod update;
