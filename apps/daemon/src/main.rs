use clawd::doctor;

use anyhow::{Context as _, Result};
use clap::{Parser, Subcommand};
use clawd::cli::client::{read_auth_token, DaemonClient};
use clawd::{
    account::AccountRegistry,
    auth,
    config::DaemonConfig,
    identity,
    intelligence::token_tracker::TokenTracker,
    ipc::event::EventBroadcaster,
    license, mdns, relay,
    repo::RepoRegistry,
    service,
    session::SessionManager,
    storage::Storage,
    tasks::{
        storage::{ActivityQueryParams, AgentTaskRow, TaskListParams},
        TaskStorage,
    },
    telemetry, update, AppContext,
};
use std::sync::Arc;
use tracing::{info, warn};

#[derive(Parser)]
#[command(
    name = "clawd",
    about = "ClawDE Host — always-on background daemon",
    version
)]
struct Args {
    #[command(subcommand)]
    command: Option<Command>,

    /// JSON-RPC WebSocket server port
    #[arg(long, env = "CLAWD_PORT", global = true)]
    port: Option<u16>,

    /// Data directory for sessions, config, and SQLite database
    #[arg(long, env = "CLAWD_DATA_DIR", global = true)]
    data_dir: Option<std::path::PathBuf>,

    /// Log level (trace, debug, info, warn, error)
    #[arg(long, env = "CLAWD_LOG")]
    log: Option<String>,

    /// Maximum concurrent sessions (0 = unlimited)
    #[arg(long, env = "CLAWD_MAX_SESSIONS")]
    max_sessions: Option<usize>,

    /// Bind address for the WebSocket server (default: 127.0.0.1; use 0.0.0.0 for LAN access)
    #[arg(long, env = "CLAWD_BIND")]
    bind_address: Option<String>,

    /// Write logs to this file path (rotated daily). Optional.
    #[arg(long, env = "CLAWD_LOG_FILE")]
    log_file: Option<std::path::PathBuf>,

    /// Suppress progress and informational output.
    ///
    /// Errors are still printed to stderr. JSON output (--json flags) is
    /// unaffected. Use this flag when piping output to other tools.
    #[arg(long, short = 'q', global = true)]
    quiet: bool,

    /// Skip database migrations and start in recovery mode (UX.2 — Sprint BB).
    ///
    /// Use when a migration failure prevents the daemon from starting normally.
    /// `daemon.status` reports `recoveryMode: true`; Flutter clients show a
    /// recovery overlay with retry / rollback options.
    ///
    /// Pre-migration backups live in `{data_dir}/backups/`.
    #[arg(long, env = "CLAWD_NO_MIGRATE")]
    no_migrate: bool,
}

#[derive(Subcommand)]
enum Command {
    /// Start the daemon server (default when no subcommand given).
    ///
    /// Runs clawd in the foreground. When invoked with no subcommand, this is the default.
    ///
    /// Examples:
    ///   clawd serve
    ///   clawd
    Serve,
    /// Manage the daemon system service.
    ///
    /// Install, uninstall, or query the platform service (launchd on macOS,
    /// systemd on Linux, SCM on Windows).
    ///
    /// Examples:
    ///   clawd service install
    ///   clawd service status
    ///   clawd service uninstall
    Service {
        #[command(subcommand)]
        action: ServiceAction,
    },
    /// Scaffold .claude/ directory structure for a project.
    ///
    /// Creates the standard AFS (.claude/) layout: rules/, memory/, tasks/,
    /// planning/, qa/, docs/, inbox/, and archive/. Also creates CLAUDE.md,
    /// active.md, and settings.json stubs, and updates .gitignore.
    ///
    /// Safe to re-run: existing files are never overwritten.
    ///
    /// Examples:
    ///   clawd init
    ///   clawd init /path/to/project
    Init {
        /// Project path to initialize (default: current directory)
        path: Option<std::path::PathBuf>,
        /// Force a specific stack template instead of auto-detecting.
        ///
        /// Valid values: rust-cli, nextjs, react-spa, flutter-app, nself-backend, generic
        ///
        /// If omitted, the stack is auto-detected from marker files (Cargo.toml,
        /// pubspec.yaml, next.config.*, vite.config.*, .env.nself).
        #[arg(long, value_name = "STACK")]
        template: Option<String>,
    },
    /// Manage agent tasks.
    ///
    /// Full task lifecycle: create, claim, log activity, mark done, query.
    /// Backed by a local SQLite database. Compatible with the .claude/tasks/
    /// markdown format via `tasks sync` and `tasks from-planning`.
    ///
    /// Examples:
    ///   clawd tasks list --status active
    ///   clawd tasks claim SP1.T1
    ///   clawd tasks done SP1.T1 --notes "implemented and tested"
    ///   clawd tasks summary --json
    Tasks {
        #[command(subcommand)]
        action: TasksAction,
    },
    /// Check for updates, download, and apply.
    ///
    /// Checks the GitHub Releases feed for a newer version of clawd.
    /// Downloads and applies the update in place. The daemon restarts
    /// automatically after applying. Runs silently on a 24h timer when
    /// the daemon is running as a service.
    ///
    /// Examples:
    ///   clawd update --check
    ///   clawd update
    ///   clawd update --apply
    Update {
        /// Only check — do not download or apply
        #[arg(long)]
        check: bool,
        /// Apply a previously downloaded update without re-checking
        #[arg(long)]
        apply: bool,
    },
    /// Start the daemon via the OS service manager.
    ///
    /// Equivalent to `clawd service install` then starting the service.
    /// Use this after `clawd service install` to bring the daemon up.
    ///
    /// Examples:
    ///   clawd start
    Start,
    /// Stop the daemon via the OS service manager.
    ///
    /// Sends a graceful shutdown request. In-progress sessions are paused
    /// and will resume on next start. Equivalent to stopping the platform service.
    ///
    /// Examples:
    ///   clawd stop
    Stop,
    /// Restart the daemon via the OS service manager.
    ///
    /// Equivalent to stop + start. Use after config changes or when the daemon
    /// needs a fresh start without a full reinstall.
    ///
    /// Examples:
    ///   clawd restart
    Restart,
    /// Run diagnostic checks on daemon prerequisites.
    ///
    /// Checks port availability, provider CLI installation and authentication,
    /// SQLite database accessibility, disk space, log directory writability,
    /// and relay server reachability.
    ///
    /// Exit code 0 if all checks pass, 1 if any check fails.
    ///
    /// Examples:
    ///   clawd doctor
    Doctor,
    /// Display pairing information for connecting remote devices.
    ///
    /// Shows instructions for pairing the ClawDE desktop app with remote
    /// devices. A one-time PIN is generated by the running daemon.
    ///
    /// Examples:
    ///   clawd pair
    Pair,
    /// Manage the daemon auth token.
    ///
    /// Show or display the auth token used to authenticate clients.
    ///
    /// Examples:
    ///   clawd token show
    ///   clawd token qr
    ///   clawd token qr --relay
    Token {
        #[command(subcommand)]
        cmd: TokenCmd,
    },
    /// Manage projects (workspaces containing multiple repos).
    ///
    /// Projects group multiple git repositories under one workspace.
    /// All project commands require the daemon to be running.
    ///
    /// Examples:
    ///   clawd project list
    ///   clawd project create my-project
    ///   clawd project add-repo my-project /path/to/repo
    #[command(subcommand)]
    Project(ProjectCommands),
    /// Show daemon status (running, version, active sessions).
    ///
    /// Connects to the running daemon and prints a summary line.
    /// Exits 0 if healthy, 1 if stopped or unresponsive.
    ///
    /// Examples:
    ///   clawd status
    ///   clawd status --json
    Status {
        /// Output as JSON for scripting
        #[arg(long)]
        json: bool,
    },
    /// View daemon log file.
    ///
    /// Prints the last N lines from the daemon log. Use --follow to tail live output.
    ///
    /// Examples:
    ///   clawd logs
    ///   clawd logs -f
    ///   clawd logs --lines 100
    ///   clawd logs --filter warn
    Logs {
        /// Follow log output in real time (like tail -f)
        #[arg(long, short)]
        follow: bool,
        /// Number of lines to show (0 = all)
        #[arg(long, short = 'n', default_value = "50")]
        lines: u64,
        /// Minimum log level to show: trace, debug, info, warn, error
        #[arg(long)]
        filter: Option<String>,
    },
    /// Manage AI provider accounts.
    ///
    /// Add, list, or remove provider accounts (claude, codex, etc.).
    /// Requires the daemon to be running.
    ///
    /// Examples:
    ///   clawd account add --provider claude --credentials ~/.config/claude/credentials
    ///   clawd account list
    ///   clawd account remove <account-id>
    Account {
        #[command(subcommand)]
        cmd: AccountCmd,
    },
    /// Produce a keyless Sigstore / cosign attestation for an autonomous run (SIG.1 — Sprint BB).
    ///
    /// Signs the task output + worktree HEAD SHA with an ambient OIDC identity
    /// (GitHub Actions, Google, etc.) and publishes to the Sigstore transparency log.
    /// Requires `cosign` on PATH (brew install cosign).
    ///
    /// Examples:
    ///   clawd sign-run --task-id SP1.T3 --sha abc123 --notes "done"
    ///   clawd sign-run --task-id SP1.T3 --sha abc123
    SignRun {
        /// Task ID to attest.
        #[arg(long)]
        task_id: String,
        /// Worktree HEAD SHA (git commit hash of the completed work).
        #[arg(long)]
        sha: String,
        /// Completion notes (optional — describes what was done).
        #[arg(long, default_value = "")]
        notes: String,
    },
    /// Interactive AI chat in the terminal (Sprint II CH.1).
    ///
    /// Connects to the running daemon and starts an interactive AI session
    /// directly in your terminal. Use --resume to continue an existing session
    /// or --non-interactive for single-shot scripting.
    ///
    /// Examples:
    ///   clawd chat
    ///   clawd chat --resume <session-id>
    ///   clawd chat --session-list
    ///   clawd chat --non-interactive "What does this code do?"
    Chat {
        /// Resume an existing session by ID.
        #[arg(long)]
        resume: Option<String>,
        /// List recent sessions and pick one interactively.
        #[arg(long)]
        session_list: bool,
        /// Single-shot non-interactive query — print response and exit.
        #[arg(long, value_name = "PROMPT")]
        non_interactive: Option<String>,
        /// AI provider to use when creating a new session (default: claude).
        #[arg(long, default_value = "claude")]
        provider: String,
    },
    /// Ask the AI to explain a file, code range, stdin, or error message (Sprint II EX.1).
    ///
    /// Creates an ephemeral AI session, sends the code/error as context, and
    /// streams the explanation to the terminal. The session is not saved.
    ///
    /// Examples:
    ///   clawd explain src/main.rs
    ///   clawd explain src/main.rs --line 42
    ///   clawd explain src/main.rs --lines 40-60
    ///   clawd explain --stdin
    ///   clawd explain --error "E0308: mismatched types"
    ///   clawd explain src/lib.rs --format json
    Explain {
        /// File to explain (positional).
        file: Option<std::path::PathBuf>,
        /// Focus on a specific line number (1-based).
        #[arg(long)]
        line: Option<u32>,
        /// Focus on a line range, e.g. "40-60".
        #[arg(long)]
        lines: Option<String>,
        /// Read code from stdin.
        #[arg(long)]
        stdin: bool,
        /// Explain an error message string.
        #[arg(long)]
        error: Option<String>,
        /// Output format: text (default) or json.
        #[arg(long, default_value = "text")]
        format: String,
        /// AI provider to use (default: claude).
        #[arg(long, default_value = "claude")]
        provider: String,
    },
    /// Manage instruction graph nodes (Sprint ZZ IG / IL).
    ///
    /// Compile, lint, explain, import, and snapshot project instructions.
    ///
    /// Examples:
    ///   clawd instructions compile
    ///   clawd instructions compile --dry-run
    ///   clawd instructions lint --ci
    ///   clawd instructions explain --path .
    ///   clawd instructions import
    ///   clawd instructions snapshot --check
    ///   clawd instructions doctor
    Instructions {
        #[command(subcommand)]
        action: InstructionsAction,
    },
    /// Run policy YAML tests (Sprint ZZ PT.T02).
    ///
    /// Validates that the daemon policy engine accepts/denies the right commands.
    ///
    /// Examples:
    ///   clawd policy test
    ///   clawd policy test --file .clawd/tests/policy/custom.yaml
    ///   clawd policy test --ci
    ///   clawd policy seed
    Policy {
        #[command(subcommand)]
        action: PolicyAction,
    },
    /// Run and compare benchmark tasks (Sprint ZZ EH.T03/T04).
    ///
    /// Runs benchmark tasks against the daemon and compares pass rates.
    ///
    /// Examples:
    ///   clawd bench run --task BT.001
    ///   clawd bench compare
    ///   clawd bench compare --base-ref abc123
    ///   clawd bench seed
    Bench {
        #[command(subcommand)]
        action: BenchAction,
    },
    /// Observe OpenTelemetry trace for a session (Sprint ZZ OT.T06).
    ///
    /// Pretty-prints the trace tree for a completed session.
    ///
    /// Examples:
    ///   clawd observe --session <session-id>
    Observe {
        /// Session ID to inspect
        #[arg(long)]
        session: String,
    },
    /// List provider capability matrix (Sprint ZZ MP.T04).
    ///
    /// Shows what each AI provider supports (sessions, MCP, worktrees, cost).
    ///
    /// Examples:
    ///   clawd providers
    Providers,
    /// Show diff risk score for the current worktree (Sprint ZZ DR.T04).
    ///
    /// Scores each changed file by criticality and churn.
    ///
    /// Examples:
    ///   clawd diff-risk
    ///   clawd diff-risk --path /path/to/worktree
    DiffRisk {
        /// Worktree path (default: current directory)
        #[arg(long)]
        path: Option<std::path::PathBuf>,
    },
}

#[derive(Subcommand)]
enum InstructionsAction {
    /// Compile instruction nodes to CLAUDE.md / AGENTS.md.
    Compile {
        #[arg(long, default_value = "claude")]
        target: String,
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
        #[arg(long)]
        dry_run: bool,
    },
    /// Explain effective instructions for a directory path.
    Explain {
        #[arg(long, default_value = ".")]
        path: std::path::PathBuf,
    },
    /// Lint instruction nodes.
    Lint {
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
        #[arg(long)]
        ci: bool,
    },
    /// Import .claude/rules/ files as instruction nodes.
    Import {
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
    },
    /// Create or check a golden instruction snapshot.
    Snapshot {
        #[arg(long, default_value = ".")]
        path: std::path::PathBuf,
        #[arg(long)]
        output: Option<std::path::PathBuf>,
        #[arg(long)]
        check: bool,
    },
    /// Validate compiled instruction files (doctor check).
    Doctor {
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
    },
}

#[derive(Subcommand)]
enum PolicyAction {
    /// Run policy tests.
    Test {
        #[arg(long)]
        file: Option<String>,
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
        #[arg(long)]
        ci: bool,
    },
    /// Install seed policy test file.
    Seed {
        #[arg(long, default_value = ".")]
        project: std::path::PathBuf,
    },
}

#[derive(Subcommand)]
enum BenchAction {
    /// Run a benchmark task.
    Run {
        #[arg(long)]
        task: String,
        #[arg(long, default_value = "claude")]
        provider: String,
    },
    /// Compare current benchmark results against a baseline.
    Compare {
        #[arg(long)]
        base_ref: Option<String>,
        #[arg(long, default_value = "claude")]
        provider: String,
    },
    /// Install seed benchmark tasks.
    Seed,
}

#[derive(Subcommand)]
enum TokenCmd {
    /// Print the daemon auth token to stdout.
    ///
    /// The token is stored at {data_dir}/auth_token. Use this to retrieve
    /// the token for connecting remote clients or the mobile app.
    ///
    /// Examples:
    ///   clawd token show
    Show,
    /// Display a QR code encoding the daemon endpoint and auth token.
    ///
    /// Generates a QR code that encodes a `clawd://connect` URL. Scan with
    /// the ClawDE mobile app to pair without manual token entry.
    ///
    /// Warning: The QR code contains your auth token. Only share with trusted devices.
    ///
    /// Examples:
    ///   clawd token qr
    ///   clawd token qr --relay
    Qr {
        /// Include relay=1 in the QR payload so the app connects via relay
        #[arg(long)]
        relay: bool,
    },
}

#[derive(Subcommand)]
enum AccountCmd {
    /// Add a provider account.
    ///
    /// Examples:
    ///   clawd account add --provider claude --credentials ~/.config/claude/credentials
    ///   clawd account add --provider claude --credentials /path/creds --name "Work"
    Add {
        /// Provider name (e.g. claude, codex)
        #[arg(long)]
        provider: String,
        /// Path to credentials file
        #[arg(long)]
        credentials: std::path::PathBuf,
        /// Optional display name for this account
        #[arg(long)]
        name: Option<String>,
        /// Optional priority (lower = preferred; default 0)
        #[arg(long)]
        priority: Option<i64>,
    },
    /// List all configured accounts.
    ///
    /// Examples:
    ///   clawd account list
    ///   clawd account list --json
    List {
        /// Output as JSON
        #[arg(long)]
        json: bool,
    },
    /// Remove an account.
    ///
    /// Examples:
    ///   clawd account remove <account-id>
    ///   clawd account remove <account-id> --yes
    Remove {
        /// Account ID to remove
        id: String,
        /// Skip confirmation prompt
        #[arg(long, short = 'y')]
        yes: bool,
    },
}

#[derive(Subcommand)]
enum ProjectCommands {
    /// List all projects.
    ///
    /// Examples:
    ///   clawd project list
    List,
    /// Create a new project.
    ///
    /// Examples:
    ///   clawd project create my-project
    ///   clawd project create my-project --path /path/to/workspace
    Create {
        /// Project name
        name: String,
        /// Optional root directory for the project workspace
        #[arg(long)]
        path: Option<String>,
    },
    /// Add a git repository to an existing project.
    ///
    /// Examples:
    ///   clawd project add-repo my-project /path/to/repo
    AddRepo {
        /// Project ID or name
        project: String,
        /// Path to the git repository to add
        path: String,
    },
}

#[derive(Subcommand)]
enum TasksAction {
    /// List tasks, optionally filtered by repo, status, or phase.
    ///
    /// Reads the task database and prints a formatted table. Use --json for
    /// machine-readable output suitable for piping to other tools.
    ///
    /// Examples:
    ///   clawd tasks list
    ///   clawd tasks list --status active --limit 20
    ///   clawd tasks list --repo /path/to/repo --json
    List {
        #[arg(long, short)]
        repo: Option<String>,
        #[arg(long, short)]
        status: Option<String>,
        #[arg(long, short = 'p')]
        phase: Option<String>,
        #[arg(long, short = 'n', default_value = "50")]
        limit: i64,
        /// Output as JSON array (for piping)
        #[arg(long)]
        json: bool,
    },
    /// Get the full detail of a task by ID.
    ///
    /// Prints all fields: title, status, severity, phase, notes, block reason,
    /// claimed-by, file, repo path, and timestamps.
    ///
    /// Examples:
    ///   clawd tasks get SP1.T3
    ///   clawd tasks get --task SP1.T3
    Get {
        /// Task ID (positional or --task)
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Claim a task atomically and mark it in-progress.
    ///
    /// Uses a DB-level atomic compare-and-set to prevent two agents from
    /// claiming the same task. Fails with exit 2 if the task is already claimed.
    ///
    /// Examples:
    ///   clawd tasks claim SP1.T3
    ///   clawd tasks claim SP1.T3 --agent codex
    Claim {
        /// Task ID (positional or --task)
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Release a task back to pending (unclaim it).
    ///
    /// Reverses a claim. Use when an agent must hand off an in-progress task
    /// or when a claim was made by mistake.
    ///
    /// Examples:
    ///   clawd tasks release SP1.T3
    Release {
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
    },
    /// Mark a task done. Completion notes are required.
    ///
    /// The daemon enforces non-empty notes — a task cannot be marked done
    /// without a brief description of what was completed. This creates an
    /// audit trail for every finished task.
    ///
    /// Examples:
    ///   clawd tasks done SP1.T3 --notes "implemented and all tests pass"
    Done {
        /// Task ID (positional or --task)
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        /// Completion notes (required — daemon enforces non-empty)
        #[arg(long)]
        notes: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Mark a task blocked with a reason.
    ///
    /// Use when work cannot proceed due to an external dependency, missing
    /// information, or a cross-project inbox message. Blocked tasks are
    /// visible in `clawd tasks list` and highlighted in summary views.
    ///
    /// Examples:
    ///   clawd tasks blocked SP1.T3 --notes "waiting on nself CLI fix"
    Blocked {
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long)]
        notes: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
    },
    /// Send a heartbeat for a running task.
    ///
    /// Called periodically by agents to signal that a claimed task is still
    /// actively being worked on. Tasks without a heartbeat for 90 seconds
    /// are automatically released back to pending.
    ///
    /// Examples:
    ///   clawd tasks heartbeat SP1.T3
    ///   clawd tasks heartbeat SP1.T3 --agent codex
    Heartbeat {
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Add a new task to the database.
    ///
    /// Creates a task with the given title and optional metadata. The task
    /// starts in pending status. Use `tasks claim` to start work on it.
    ///
    /// Examples:
    ///   clawd tasks add --title "Fix session reconnect on network drop"
    ///   clawd tasks add --title "Add --json to pack list" --phase SP55 --severity high
    Add {
        #[arg(long)]
        title: String,
        #[arg(long)]
        repo: Option<String>,
        #[arg(long)]
        phase: Option<String>,
        #[arg(long, default_value = "medium")]
        severity: String,
        #[arg(long)]
        file: Option<String>,
    },
    /// Log an activity entry for a task (called by PostToolUse hook).
    ///
    /// Records a structured activity entry in the database. Called automatically
    /// by the Claude Code PostToolUse hook. Can also be called manually to log
    /// important decisions or discoveries against a task.
    ///
    /// Examples:
    ///   clawd tasks log SP1.T3 --action "file_edit" --detail "updated session.rs"
    ///   clawd tasks log --action "decision" --detail "chose sqlx over diesel"
    Log {
        /// Task ID (positional or --task; optional)
        id: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
        #[arg(long)]
        action: String,
        /// Detail text (alias: --notes)
        #[arg(long)]
        detail: Option<String>,
        #[arg(long)]
        notes: Option<String>,
        #[arg(long, default_value = "auto", name = "entry-type")]
        entry_type: String,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Post a narrative note for a task or for an entire phase.
    ///
    /// Notes are free-text and appear in activity views alongside structured
    /// log entries. Useful for recording observations, risks, or rationale
    /// that do not fit the action/detail structure.
    ///
    /// Examples:
    ///   clawd tasks note SP1.T3 "discovered that sqlx requires --offline in CI"
    ///   clawd tasks note --phase SP1 "phase complete — all tests green"
    Note {
        /// Task ID (positional or --task; omit for phase-level note)
        id: Option<String>,
        #[arg(long, conflicts_with = "phase")]
        task: Option<String>,
        /// Phase name for a phase-level note
        #[arg(long)]
        phase: Option<String>,
        /// Note text (positional or --note)
        text: Option<String>,
        #[arg(long)]
        note: Option<String>,
        #[arg(long, default_value = "cli")]
        agent: String,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Import tasks from a planning markdown file.
    ///
    /// Parses a .claude/planning/*.md file in active.md format and inserts
    /// any new tasks into the database. Existing tasks (matched by ID) are
    /// not duplicated. Use `tasks sync` to also update the queue.json file.
    ///
    /// Examples:
    ///   clawd tasks from-planning .claude/planning/55-cli-ux.md
    ///   clawd tasks from-planning .claude/planning/55-cli-ux.md --repo /path/to/repo
    FromPlanning {
        /// Path to a planning .md file (e.g. .claude/planning/41-feature.md)
        file: std::path::PathBuf,
        #[arg(long)]
        repo: Option<String>,
    },
    /// Sync active.md to the DB and regenerate queue.json.
    ///
    /// Reads the active.md file, upserts tasks into the database, and
    /// regenerates the queue.json file used by agent tooling. Run this
    /// after manually editing active.md to keep the DB in sync.
    ///
    /// Examples:
    ///   clawd tasks sync
    ///   clawd tasks sync --repo /path/to/repo
    ///   clawd tasks sync --active-md /custom/path/active.md
    Sync {
        #[arg(long)]
        repo: Option<String>,
        /// Path to active.md (default: {repo}/.claude/tasks/active.md)
        #[arg(long)]
        active_md: Option<std::path::PathBuf>,
    },
    /// Show a task counts summary for a project.
    ///
    /// Prints totals for done, in-progress, pending, and blocked tasks.
    /// Includes average task duration. Use --json for machine-readable output.
    ///
    /// Examples:
    ///   clawd tasks summary
    ///   clawd tasks summary --repo /path/to/repo
    ///   clawd tasks summary --json
    Summary {
        #[arg(long)]
        repo: Option<String>,
        /// Output raw JSON instead of formatted table
        #[arg(long, default_value_t = false)]
        json: bool,
    },
    /// Show the recent activity log.
    ///
    /// Displays structured activity entries (file edits, decisions, notes)
    /// across all tasks or filtered to a specific task or phase. Use --limit
    /// to control how many entries are returned.
    ///
    /// Examples:
    ///   clawd tasks activity
    ///   clawd tasks activity --task SP1.T3 --limit 50
    ///   clawd tasks activity --phase SP55
    Activity {
        #[arg(long)]
        repo: Option<String>,
        #[arg(long)]
        task: Option<String>,
        #[arg(long)]
        phase: Option<String>,
        #[arg(long, default_value = "20")]
        limit: i64,
    },
}

#[derive(Subcommand)]
enum ServiceAction {
    /// Install and start clawd as a platform service.
    ///
    /// Registers the daemon with the OS service manager (launchd on macOS,
    /// systemd on Linux, SCM on Windows). The service starts automatically
    /// on login/boot.
    ///
    /// Examples:
    ///   clawd service install
    Install,
    /// Stop and remove the platform service.
    ///
    /// Unloads and removes the service from the OS service manager. Does not
    /// delete data or config — only removes the service registration.
    ///
    /// Examples:
    ///   clawd service uninstall
    Uninstall,
    /// Show the service status.
    ///
    /// Queries the OS service manager for the current state of the clawd service.
    /// Reports whether the service is installed, running, stopped, or failed.
    ///
    /// Examples:
    ///   clawd service status
    Status,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();

    // ── Logging setup ────────────────────────────────────────────────────────
    // Init once — must happen before any tracing calls.
    let log_level = args.log.as_deref().unwrap_or("info").to_owned();
    let log_format = std::env::var("CLAWD_LOG_FORMAT").unwrap_or_else(|_| "pretty".to_string());
    let _file_guard = setup_logging(&log_level, args.log_file.as_deref(), &log_format);

    let quiet = args.quiet;
    match args.command {
        Some(Command::Service { action }) => match action {
            ServiceAction::Install => service::install()?,
            ServiceAction::Uninstall => service::uninstall()?,
            ServiceAction::Status => service::status()?,
        },
        Some(Command::Init { path, template }) => {
            let path = match path {
                Some(p) => p,
                None => std::env::current_dir().context("failed to determine current directory")?,
            };
            run_init(&path, template.as_deref(), quiet).await?;
        }
        Some(Command::Tasks { action }) => {
            run_tasks(action, args.data_dir, quiet).await?;
        }
        Some(Command::Update { check, apply }) => {
            run_update(check, apply, quiet, args.data_dir).await?;
        }
        Some(Command::Start) => service::start()?,
        Some(Command::Stop) => service::stop()?,
        Some(Command::Restart) => service::restart()?,
        Some(Command::Doctor) => {
            let results = doctor::run_doctor();
            doctor::print_doctor_results(&results);
            let failed = results.iter().filter(|r| !r.passed).count();
            std::process::exit(if failed == 0 { 0 } else { 1 });
        }
        Some(Command::Pair) => {
            println!("To pair a device, open the ClawDE desktop app and go to:");
            println!("  Settings > Remote Access > Add Device");
            println!();
            println!("Or run the daemon and use: clawd pair --daemon");
            println!("(Requires daemon to be running to generate a one-time PIN)");
        }
        Some(Command::Token { cmd }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            match cmd {
                TokenCmd::Show => run_token_show(&config)?,
                TokenCmd::Qr { relay } => run_token_qr(&config, relay)?,
            }
        }
        Some(Command::Project(cmd)) => {
            let _ = cmd; // suppress unused warning — full RPC wiring is a future task
            eprintln!("project commands require the daemon to be running.");
            eprintln!("Start the daemon with: clawd start");
            std::process::exit(1);
        }
        Some(Command::Status { json }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let exit_code = run_status(&config, json).await;
            std::process::exit(exit_code);
        }
        Some(Command::Logs {
            follow,
            lines,
            filter,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            run_logs(&config, follow, lines, filter.as_deref())?;
        }
        Some(Command::Account { cmd }) => {
            let config = DaemonConfig::new(
                args.port,
                args.data_dir,
                Some("error".to_string()),
                None,
                None,
            );
            run_account(&config, cmd).await?;
        }
        Some(Command::SignRun {
            task_id,
            sha,
            notes,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::sign_run::run_sign_run_cli(&task_id, &sha, &notes, &config.data_dir)?;
        }
        Some(Command::Chat {
            resume,
            session_list,
            non_interactive,
            provider,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let opts = clawd::cli::chat::ChatOpts {
                resume,
                session_list,
                non_interactive,
                provider: Some(provider),
            };
            clawd::cli::chat::run_chat(opts, &config).await?;
        }
        Some(Command::Explain {
            file,
            line,
            lines,
            stdin,
            error,
            format,
            provider,
        }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let fmt = if format == "json" {
                clawd::cli::explain::ExplainFormat::Json
            } else {
                clawd::cli::explain::ExplainFormat::Text
            };
            let opts = clawd::cli::explain::ExplainOpts {
                file,
                line,
                lines,
                stdin,
                error,
                format: fmt,
                provider: Some(provider),
            };
            clawd::cli::explain::run_explain(opts, &config).await?;
        }
        Some(Command::Instructions { action }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                InstructionsAction::Compile {
                    target,
                    project,
                    dry_run,
                } => {
                    let opts = clawd::cli::instructions::CompileOpts {
                        target,
                        project,
                        dry_run,
                    };
                    clawd::cli::instructions::compile(opts, &data_dir, port).await?;
                }
                InstructionsAction::Explain { path } => {
                    clawd::cli::instructions::explain(path, &data_dir, port).await?;
                }
                InstructionsAction::Lint { project, ci } => {
                    clawd::cli::instructions::lint(project, ci, &data_dir, port).await?;
                }
                InstructionsAction::Import { project } => {
                    clawd::cli::instructions::import(project, &data_dir, port).await?;
                }
                InstructionsAction::Snapshot {
                    path,
                    output,
                    check,
                } => {
                    clawd::cli::instructions::snapshot(path, output, check, &data_dir, port)
                        .await?;
                }
                InstructionsAction::Doctor { project } => {
                    clawd::cli::instructions::doctor(project, &data_dir, port).await?;
                }
            }
        }
        Some(Command::Policy { action }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                PolicyAction::Test {
                    file,
                    project: _,
                    ci,
                } => {
                    clawd::cli::policy::test(
                        file.map(std::path::PathBuf::from),
                        ci,
                        &data_dir,
                        port,
                    )
                    .await?;
                }
                PolicyAction::Seed { project } => {
                    clawd::cli::policy::install_seed_tests(&project).await?;
                }
            }
        }
        Some(Command::Bench { action }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let port = config.port;
            let data_dir = config.data_dir.clone();
            match action {
                BenchAction::Run { task, provider } => {
                    clawd::cli::bench::run(Some(task), Some(provider), &data_dir, port).await?;
                }
                BenchAction::Compare {
                    base_ref,
                    provider: _,
                } => {
                    let br = base_ref.unwrap_or_else(|| "HEAD~1".to_string());
                    clawd::cli::bench::compare(br, &data_dir, port).await?;
                }
                BenchAction::Seed => {
                    // Seed via RPC
                    let token = clawd::cli::client::read_auth_token(&data_dir)?;
                    let client = clawd::cli::client::DaemonClient::new(port, token);
                    let res = client
                        .call_once("bench.seedTasks", serde_json::json!({}))
                        .await?;
                    let created = res["created"].as_u64().unwrap_or(0);
                    let skipped = res["skipped"].as_u64().unwrap_or(0);
                    println!("Seed complete: {created} tasks created, {skipped} already present.");
                }
            }
        }
        Some(Command::Observe { session }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::observe::observe(session, &config.data_dir, config.port).await?;
        }
        Some(Command::Providers) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            clawd::cli::providers::list_capabilities(&config.data_dir, config.port).await?;
        }
        Some(Command::DiffRisk { path }) => {
            let config =
                DaemonConfig::new(None, args.data_dir, Some("error".to_string()), None, None);
            let worktree = path.map(|p| p.to_string_lossy().into_owned());
            clawd::cli::diff_risk::diff_risk_score(worktree, &config.data_dir, config.port).await?;
        }
        None | Some(Command::Serve) => {
            run_server(
                args.port,
                args.data_dir,
                args.log,
                args.max_sessions,
                args.bind_address,
                args.no_migrate,
            )
            .await?;
        }
    }

    Ok(())
}

/// Initialize the tracing subscriber.
/// If `log_file` is set, logs go to both stdout and a daily-rolling file.
/// Returns a `WorkerGuard` that must stay alive for the process lifetime.
///
/// `log_format` may be `"pretty"` (default, human-readable compact format) or
/// `"json"` (structured JSON for log aggregators like Loki/Elasticsearch).
///
/// If the log directory cannot be created, falls back to stdout-only logging
/// with a warning — never panics.
fn setup_logging(
    log_level: &str,
    log_file: Option<&std::path::Path>,
    log_format: &str,
) -> Option<tracing_appender::non_blocking::WorkerGuard> {
    use tracing_subscriber::{fmt, layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

    let use_json = log_format == "json";

    if let Some(path) = log_file {
        let dir = path.parent().unwrap_or_else(|| std::path::Path::new("."));
        let filename = path
            .file_name()
            .unwrap_or_else(|| std::ffi::OsStr::new("clawd.log"));

        // Ensure the directory exists before tracing-appender tries to open it.
        if let Err(e) = std::fs::create_dir_all(dir) {
            // Fall back to stdout-only — don't panic on a bad log path.
            eprintln!(
                "warn: could not create log directory '{}': {e} — falling back to stdout",
                dir.display()
            );
            if use_json {
                tracing_subscriber::fmt()
                    .json()
                    .with_env_filter(log_level)
                    .init();
            } else {
                tracing_subscriber::fmt()
                    .with_env_filter(log_level)
                    .compact()
                    .init();
            }
            return None;
        }

        let appender = tracing_appender::rolling::daily(dir, filename);
        let (non_blocking, guard) = tracing_appender::non_blocking(appender);

        if use_json {
            tracing_subscriber::registry()
                .with(EnvFilter::new(log_level))
                .with(fmt::layer().json())
                .with(fmt::layer().json().with_writer(non_blocking))
                .init();
        } else {
            tracing_subscriber::registry()
                .with(EnvFilter::new(log_level))
                .with(fmt::layer().compact())
                .with(fmt::layer().with_writer(non_blocking))
                .init();
        }

        Some(guard)
    } else if use_json {
        tracing_subscriber::fmt()
            .json()
            .with_env_filter(log_level)
            .init();
        None
    } else {
        tracing_subscriber::fmt()
            .with_env_filter(log_level)
            .compact()
            .init();
        None
    }
}

// ── Panic hook + crash log (DC.T51) ───────────────────────────────────────────

/// Install a custom panic hook that writes panic info + backtrace to `{data_dir}/crash.log`.
///
/// The crash log is checked and removed on the next startup (`check_crash_log`).
/// This works alongside the rollback sentinel — the sentinel handles binary corruption;
/// the crash log captures application-level panics.
fn install_panic_hook(data_dir: std::path::PathBuf) {
    let original = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        // Call the original hook first (prints to stderr).
        original(info);

        let crash_path = data_dir.join("crash.log");
        let msg = info
            .payload()
            .downcast_ref::<&str>()
            .copied()
            .or_else(|| info.payload().downcast_ref::<String>().map(|s| s.as_str()))
            .unwrap_or("unknown panic");

        let location = info
            .location()
            .map(|l| format!("{}:{}", l.file(), l.line()))
            .unwrap_or_else(|| "unknown location".to_string());

        let backtrace = std::backtrace::Backtrace::capture();
        let content = format!(
            "clawd panic at {location}\n\
             message: {msg}\n\
             version: {}\n\
             backtrace:\n{backtrace:#}\n",
            env!("CARGO_PKG_VERSION")
        );

        // Best-effort write — if this fails, we can't do much.
        let _ = std::fs::write(&crash_path, &content);
    }));
}

/// Check for a crash log from the previous run, log it at error level, then delete it.
///
/// Called early in `run_serve()` after logging is initialized.
fn check_crash_log(data_dir: &std::path::Path) {
    let crash_path = data_dir.join("crash.log");
    match std::fs::read_to_string(&crash_path) {
        Ok(content) => {
            tracing::error!(
                crash_report = %content.trim(),
                "previous daemon run ended with a panic — see crash report above"
            );
            let _ = std::fs::remove_file(&crash_path);
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(e) => {
            tracing::warn!(err = %e, "could not read crash.log");
        }
    }
}

// ── clawd init ────────────────────────────────────────────────────────────────

async fn run_init(path: &std::path::Path, template: Option<&str>, quiet: bool) -> Result<()> {
    use clawd::init_templates::{detect_stack, template_for, Stack};
    use tokio::fs;

    // Determine stack — override or auto-detect.
    let stack = if let Some(t) = template {
        t.parse::<Stack>().unwrap_or_else(|_| {
            if !quiet {
                eprintln!("warn: unknown template '{}' — using generic", t);
            }
            Stack::Generic
        })
    } else {
        detect_stack(path)
    };

    let tmpl = template_for(stack);
    let claude_dir = path.join(".claude");
    let mut created: Vec<String> = Vec::new();

    for dir in &[
        ".claude",
        ".claude/rules",
        ".claude/agents",
        ".claude/skills",
        ".claude/memory",
        ".claude/tasks",
        ".claude/planning",
        ".claude/qa",
        ".claude/docs",
        ".claude/archive/inbox",
        ".claude/inbox",
        ".claude/ideas",
        ".claude/temp",
    ] {
        let full = path.join(dir);
        if !full.exists() {
            fs::create_dir_all(&full).await?;
            created.push(dir.to_string());
        }
    }

    let claude_md = claude_dir.join("CLAUDE.md");
    if !claude_md.exists() {
        fs::write(&claude_md, tmpl.claude_md).await?;
        created.push(".claude/CLAUDE.md".to_string());
    }

    let decisions_md = claude_dir.join("memory/decisions.md");
    if !decisions_md.exists() {
        fs::write(&decisions_md, tmpl.decisions_md).await?;
        created.push(".claude/memory/decisions.md".to_string());
    }

    let active_md = claude_dir.join("tasks/active.md");
    if !active_md.exists() {
        fs::write(&active_md, clawd::ipc::handlers::afs::ACTIVE_MD_TEMPLATE).await?;
        created.push(".claude/tasks/active.md".to_string());
    }

    let settings = claude_dir.join("settings.json");
    if !settings.exists() {
        fs::write(&settings, clawd::ipc::handlers::afs::SETTINGS_JSON_TEMPLATE).await?;
        created.push(".claude/settings.json".to_string());
    }

    // Ensure .claude/ is in .gitignore (D64.T22)
    let gitignore = path.join(".gitignore");
    let mut gitignore_updated = false;
    if gitignore.exists() {
        let content = fs::read_to_string(&gitignore).await.unwrap_or_default();
        let missing_entry = !content.contains(".claude/");
        let missing_stack = !tmpl.gitignore_additions.is_empty()
            && !content.contains(tmpl.gitignore_additions.trim());
        if missing_entry || missing_stack {
            let mut updated = content.trim_end().to_string();
            if missing_entry {
                updated.push_str("\n\n# AI agent directories\n.claude/\n");
            }
            if missing_stack && !tmpl.gitignore_additions.trim().is_empty() {
                updated.push_str(tmpl.gitignore_additions);
            }
            fs::write(&gitignore, updated).await?;
            gitignore_updated = true;
        }
    } else {
        let mut content = ".claude/\n".to_string();
        if !tmpl.gitignore_additions.trim().is_empty() {
            content.push_str(tmpl.gitignore_additions);
        }
        fs::write(&gitignore, content).await?;
        created.push(".gitignore".to_string());
        gitignore_updated = true;
    }

    if !quiet {
        if created.is_empty() && !gitignore_updated {
            println!("Already initialized: {}", path.display());
        } else {
            println!("Initialized AFS at: {} (stack: {})", path.display(), stack);
            for item in &created {
                println!("  created   {item}");
            }
            if gitignore_updated {
                println!("  updated   .gitignore");
            }
        }
    }
    Ok(())
}

// ── clawd update ──────────────────────────────────────────────────────────────

async fn run_update(
    check_only: bool,
    apply_only: bool,
    quiet: bool,
    data_dir: Option<std::path::PathBuf>,
) -> Result<()> {
    let config = Arc::new(DaemonConfig::new(
        None,
        data_dir,
        Some("error".to_string()),
        None,
        None,
    ));

    // If daemon is running, route through it so it controls the update lifecycle.
    if !apply_only {
        if let Ok(token) = read_auth_token(&config.data_dir) {
            let client = DaemonClient::new(config.port, token);
            if client.is_reachable().await {
                if !quiet {
                    println!("Checking for updates...");
                }
                match client
                    .call_once("daemon.checkUpdate", serde_json::json!({}))
                    .await
                {
                    Ok(result) => {
                        let available = result["available"].as_bool().unwrap_or(false);
                        let latest = result["latest"].as_str().unwrap_or("?");
                        let current = result["current"].as_str().unwrap_or("?");
                        if !available {
                            if !quiet {
                                println!("clawd {current} is up to date (latest: {latest}).");
                            }
                            return Ok(());
                        }
                        if !quiet {
                            println!("Update available: {current} -> {latest}");
                        }
                        if check_only {
                            return Ok(());
                        }
                        if !quiet {
                            println!("Applying update via daemon...");
                        }
                        let _ = client
                            .call_once("daemon.applyUpdate", serde_json::json!({}))
                            .await;
                        if !quiet {
                            println!("Update initiated — daemon will restart when complete.");
                        }
                        return Ok(());
                    }
                    Err(_) => {
                        // daemon doesn't support checkUpdate RPC — fall through to in-process
                    }
                }
            }
        }
    }

    // In-process path: daemon not running or token not found
    let broadcaster = Arc::new(EventBroadcaster::new());
    let updater = update::Updater::new(config, broadcaster);

    if apply_only {
        match updater.apply_if_ready().await? {
            true => {
                if !quiet {
                    println!("Update applied — restarting.");
                }
            }
            false => {
                if !quiet {
                    println!("No pending update to apply.");
                }
            }
        }
        return Ok(());
    }

    if !quiet {
        println!("Checking for updates...");
    }
    let (current, latest, available) = updater.check().await?;
    if !available {
        if !quiet {
            println!("clawd {current} is up to date (latest: {latest}).");
        }
        return Ok(());
    }

    if !quiet {
        println!("Update available: {current} -> {latest}");
    }

    if check_only {
        return Ok(());
    }

    if !quiet {
        println!("Downloading...");
    }
    updater.check_and_download().await?;
    if !quiet {
        println!("Download complete. Applying update...");
    }
    match updater.apply_if_ready().await? {
        true => {
            if !quiet {
                println!("Update applied — restarting.");
            }
        }
        false => {
            if !quiet {
                println!("Update downloaded but could not be applied yet.");
            }
        }
    }

    Ok(())
}

// ── clawd status ──────────────────────────────────────────────────────────────

/// Returns exit code: 0 = healthy, 1 = stopped/unresponsive.
async fn run_status(config: &DaemonConfig, json: bool) -> i32 {
    let token = match read_auth_token(&config.data_dir) {
        Ok(t) => t,
        Err(_) => {
            if json {
                println!(r#"{{"status":"not_installed"}}"#);
            } else {
                println!("clawd: not installed (run `clawd service install`)");
            }
            return 1;
        }
    };

    let client = DaemonClient::new(config.port, token);
    match client
        .call_once("daemon.status", serde_json::json!({}))
        .await
    {
        Ok(result) => {
            let version = result["version"].as_str().unwrap_or("?");
            let sessions = result["activeSessions"].as_u64().unwrap_or(0);
            let uptime_secs = result["uptime"].as_u64().unwrap_or(0);
            let uptime_str = format_uptime(uptime_secs);

            if json {
                println!("{}", serde_json::to_string(&result).unwrap_or_default());
            } else {
                println!(
                    "clawd {version} — Running ({sessions} active sessions, uptime {uptime_str})"
                );
            }
            0
        }
        Err(_) => {
            if json {
                println!(r#"{{"status":"not_running"}}"#);
            } else {
                println!("clawd: not running");
            }
            1
        }
    }
}

/// Format uptime seconds as "2h 14m" or "45m 3s".
fn format_uptime(secs: u64) -> String {
    let h = secs / 3600;
    let m = (secs % 3600) / 60;
    let s = secs % 60;
    if h > 0 {
        format!("{h}h {m}m")
    } else if m > 0 {
        format!("{m}m {s}s")
    } else {
        format!("{s}s")
    }
}

// ── clawd logs ────────────────────────────────────────────────────────────────

fn run_logs(config: &DaemonConfig, follow: bool, lines: u64, filter: Option<&str>) -> Result<()> {
    use std::fs::File;
    use std::io::{Read, Seek, SeekFrom};

    // Resolve log path: CLAWD_LOG_FILE env → default {data_dir}/clawd.log
    let log_path = std::env::var("CLAWD_LOG_FILE")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|_| config.data_dir.join("clawd.log"));

    // Validate: must be within data_dir or an absolute path explicitly set
    if !log_path.exists() {
        anyhow::bail!(
            "log file not found: {}\n  Start the daemon first: clawd start",
            log_path.display()
        );
    }

    let content = std::fs::read_to_string(&log_path)
        .with_context(|| format!("cannot read log file: {}", log_path.display()))?;

    let all_lines: Vec<&str> = content.lines().collect();

    let min_level = filter.map(|f| f.to_ascii_lowercase());

    // Apply level filter (heuristic: check for level strings in each line)
    let filtered: Vec<&&str> = if let Some(ref level) = min_level {
        let levels = log_level_order(level);
        all_lines
            .iter()
            .filter(|line| {
                let l = line.to_ascii_lowercase();
                levels.iter().any(|lvl| l.contains(lvl))
            })
            .collect()
    } else {
        all_lines.iter().collect()
    };

    // Print last N lines (0 = all)
    let start = if lines == 0 || lines as usize >= filtered.len() {
        0
    } else {
        filtered.len() - lines as usize
    };

    for line in &filtered[start..] {
        println!("{line}");
    }

    if !follow {
        return Ok(());
    }

    // Follow mode: poll file every 250ms, print new content as it appears
    let mut file = File::open(&log_path)
        .with_context(|| format!("cannot open log file: {}", log_path.display()))?;
    let mut pos = file
        .seek(SeekFrom::End(0))
        .context("cannot seek log file")?;

    loop {
        std::thread::sleep(std::time::Duration::from_millis(250));

        // Handle log rotation: if file shrunk, reopen from start
        let meta = std::fs::metadata(&log_path);
        let new_size = meta.map(|m| m.len()).unwrap_or(0);
        if new_size < pos {
            if let Ok(f) = File::open(&log_path) {
                file = f;
                pos = 0;
            }
        }

        file.seek(SeekFrom::Start(pos))
            .context("cannot seek log file")?;
        let mut buf = String::new();
        file.read_to_string(&mut buf)
            .context("cannot read log file")?;

        if !buf.is_empty() {
            let should_print = if let Some(ref level) = min_level {
                let levels = log_level_order(level);
                levels
                    .iter()
                    .any(|lvl| buf.to_ascii_lowercase().contains(lvl))
            } else {
                true
            };
            if should_print {
                print!("{buf}");
            }
            pos += buf.len() as u64;
        }
    }
}

/// Return all log levels at or above `min_level` (for line filtering).
fn log_level_order(min_level: &str) -> Vec<&'static str> {
    match min_level {
        "error" => vec!["error"],
        "warn" | "warning" => vec!["warn", "error"],
        "info" => vec!["info", "warn", "error"],
        "debug" => vec!["debug", "info", "warn", "error"],
        _ => vec!["trace", "debug", "info", "warn", "error"],
    }
}

// ── clawd account ─────────────────────────────────────────────────────────────

async fn run_account(config: &DaemonConfig, cmd: AccountCmd) -> Result<()> {
    let token = read_auth_token(&config.data_dir)?;
    let client = DaemonClient::new(config.port, token);

    match cmd {
        AccountCmd::Add {
            provider,
            credentials,
            name,
            priority,
        } => {
            // Validate credentials path
            if !credentials.exists() {
                anyhow::bail!("credentials file not found: {}", credentials.display());
            }
            let creds_path = credentials
                .canonicalize()
                .context("cannot resolve credentials path")?;

            let mut params = serde_json::json!({
                "provider": provider,
                "credentials_path": creds_path.to_string_lossy(),
            });
            if let Some(n) = name {
                params["name"] = serde_json::json!(n);
            }
            if let Some(p) = priority {
                params["priority"] = serde_json::json!(p);
            }

            let result = client.call_once("account.create", params).await?;
            let id = result["id"].as_str().unwrap_or("?");
            println!("Account added: {id}");
        }

        AccountCmd::List { json } => {
            let result = client
                .call_once("account.list", serde_json::json!({}))
                .await?;
            let accounts = result.as_array().cloned().unwrap_or_default();

            if json {
                println!("{}", serde_json::to_string(&accounts)?);
                return Ok(());
            }

            if accounts.is_empty() {
                println!("No accounts configured.");
                return Ok(());
            }

            // Plain ASCII table
            println!(
                "{:<36}  {:<20}  {:<12}  {:<8}  Status",
                "ID", "Name", "Provider", "Priority"
            );
            println!("{}", "-".repeat(90));
            for acc in &accounts {
                let id = acc["id"].as_str().unwrap_or("-");
                let name = acc["name"].as_str().unwrap_or("-");
                let provider = acc["provider"].as_str().unwrap_or("-");
                let priority = acc["priority"].as_i64().unwrap_or(0);
                let status = acc["status"].as_str().unwrap_or("-");
                println!("{id:<36}  {name:<20}  {provider:<12}  {priority:<8}  {status}");
            }
        }

        AccountCmd::Remove { id, yes } => {
            if !yes {
                use std::io::Write;
                print!("Remove account {id}? [y/N] ");
                std::io::stdout().flush().ok();
                let mut input = String::new();
                std::io::stdin().read_line(&mut input).ok();
                if !matches!(input.trim().to_ascii_lowercase().as_str(), "y" | "yes") {
                    println!("Aborted.");
                    return Ok(());
                }
            }
            client
                .call_once("account.delete", serde_json::json!({ "id": id }))
                .await?;
            println!("Account removed: {id}");
        }
    }

    Ok(())
}

// ── clawd tasks ───────────────────────────────────────────────────────────────

/// Open the task DB for CLI commands (no server — just storage access).
async fn open_task_storage(data_dir: Option<std::path::PathBuf>) -> Result<TaskStorage> {
    let config = DaemonConfig::new(None, data_dir, Some("error".to_string()), None, None);
    let storage = Storage::new(&config.data_dir).await?;
    Ok(TaskStorage::new(storage.clone_pool()))
}

/// Resolve task ID from positional arg or --task flag.
fn resolve_task_id(id: Option<String>, task: Option<String>) -> Result<String> {
    id.or(task)
        .ok_or_else(|| anyhow::anyhow!("task ID required (positional or --task)"))
}

async fn run_tasks(
    action: TasksAction,
    data_dir: Option<std::path::PathBuf>,
    quiet: bool,
) -> Result<()> {
    let ts = open_task_storage(data_dir).await?;

    match action {
        TasksAction::List {
            repo,
            status,
            phase,
            limit,
            json,
        } => {
            let tasks = ts
                .list_tasks(&TaskListParams {
                    repo_path: repo,
                    status,
                    phase,
                    limit: Some(limit),
                    ..Default::default()
                })
                .await?;
            if json {
                println!("{}", serde_json::to_string(&tasks)?);
            } else if tasks.is_empty() {
                println!("No tasks found.");
            } else {
                println!("{:<12} {:<10} {:<10} TITLE", "STATUS", "SEVERITY", "PHASE");
                println!("{}", "-".repeat(72));
                for t in &tasks {
                    println!(
                        "{:<12} {:<10} {:<10} {}",
                        t.status,
                        t.severity.as_deref().unwrap_or("-"),
                        t.phase.as_deref().unwrap_or("-"),
                        t.title
                    );
                }
                println!("\n{} task(s)", tasks.len());
            }
        }

        TasksAction::Get { id, task, .. } => {
            let task_id = resolve_task_id(id, task)?;
            match ts.get_task(&task_id).await? {
                None => {
                    eprintln!("Task not found: {task_id}");
                    std::process::exit(1);
                }
                Some(t) => print_task_detail(&t),
            }
        }

        TasksAction::Claim {
            id, task, agent, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let t = ts.claim_task(&task_id, &agent, None).await?;
            if !quiet {
                println!("Claimed: {} — {}", t.id, t.title);
                println!(
                    "Status: {} by {}",
                    t.status,
                    t.claimed_by.as_deref().unwrap_or("?")
                );
            }
        }

        TasksAction::Release { id, task, agent } => {
            let task_id = resolve_task_id(id, task)?;
            ts.release_task(&task_id, &agent).await?;
            if !quiet {
                println!("Released: {task_id}");
            }
        }

        TasksAction::Done {
            id,
            task,
            notes,
            agent: _,
            ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let notes_text = notes.ok_or_else(|| anyhow::anyhow!("--notes required for done"))?;
            let t = ts
                .update_status(&task_id, "done", Some(&notes_text), None)
                .await?;
            if !quiet {
                println!("Done: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Blocked {
            id, task, notes, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            let t = ts
                .update_status(&task_id, "blocked", None, notes.as_deref())
                .await?;
            if !quiet {
                println!("Blocked: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Heartbeat {
            id, task, agent, ..
        } => {
            let task_id = resolve_task_id(id, task)?;
            ts.heartbeat_task(&task_id, &agent).await?;
            // Silent success — hook calls this fire-and-forget
        }

        TasksAction::Add {
            title,
            repo,
            phase,
            severity,
            file,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let id = format!("{:x}", rand_u64());
            let t = ts
                .add_task(
                    &id,
                    &title,
                    None,
                    phase.as_deref(),
                    None,
                    None,
                    Some(&severity),
                    file.as_deref(),
                    None,
                    None,
                    None,
                    None,
                    repo_path,
                )
                .await?;
            if !quiet {
                println!("Added: {} — {}", t.id, t.title);
            }
        }

        TasksAction::Log {
            id,
            task,
            agent,
            action,
            detail,
            notes,
            entry_type,
            repo,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let task_id = id.or(task);
            // Accept --detail or --notes as the detail field
            let detail_text = detail.or(notes);
            ts.log_activity(
                &agent,
                task_id.as_deref(),
                None,
                &action,
                &entry_type,
                detail_text.as_deref(),
                None,
                repo_path,
            )
            .await?;
            // Silent — called by PostToolUse hook fire-and-forget
        }

        TasksAction::Note {
            id,
            task,
            phase,
            text,
            note,
            agent,
            repo,
        } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let task_id = id.or(task);
            let note_text = text
                .or(note)
                .ok_or_else(|| anyhow::anyhow!("note text required (positional or --note)"))?;
            ts.post_note(
                &agent,
                task_id.as_deref(),
                phase.as_deref(),
                &note_text,
                repo_path,
            )
            .await?;
            if !quiet {
                println!("Note posted.");
            }
        }

        TasksAction::FromPlanning { file, repo } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let content = tokio::fs::read_to_string(&file)
                .await
                .map_err(|e| anyhow::anyhow!("Cannot read file {}: {e}", file.display()))?;
            let parsed = clawd::tasks::markdown_parser::parse_active_md(&content);
            if parsed.is_empty() {
                if !quiet {
                    println!("No tasks found in {}", file.display());
                }
            } else {
                let count = ts.backfill_from_tasks(parsed, repo_path).await?;
                if !quiet {
                    println!("Imported {count} new task(s) from {}", file.display());
                }
            }
        }

        TasksAction::Sync { repo, active_md } => {
            let repo_path = repo.as_deref().unwrap_or(".");
            let md_path = active_md.unwrap_or_else(|| {
                std::path::PathBuf::from(repo_path).join(".claude/tasks/active.md")
            });
            let content = tokio::fs::read_to_string(&md_path)
                .await
                .map_err(|e| anyhow::anyhow!("Cannot read {}: {e}", md_path.display()))?;
            let parsed = clawd::tasks::markdown_parser::parse_active_md(&content);
            let count = ts.backfill_from_tasks(parsed, repo_path).await?;
            clawd::tasks::queue_serializer::flush_queue(&ts, repo_path).await?;
            if !quiet {
                println!("Synced: {count} new task(s), queue.json updated.");
            }
        }

        TasksAction::Summary { repo, json } => {
            let summary = ts.summary(repo.as_deref()).await?;
            if json {
                println!("{}", serde_json::to_string_pretty(&summary)?);
            } else {
                let done = summary["done"].as_i64().unwrap_or(0);
                let in_progress = summary["in_progress"].as_i64().unwrap_or(0);
                let pending = summary["pending"].as_i64().unwrap_or(0);
                let blocked = summary["blocked"].as_i64().unwrap_or(0);
                let total = summary["total"].as_i64().unwrap_or(0);
                let avg = summary["avg_duration_minutes"].as_f64();
                let bar = "━".repeat(40);
                println!("Task Summary");
                println!("{bar}");
                if let Some(r) = &repo {
                    println!("Project:     {r}");
                }
                println!("Total:       {total}");
                println!("Done:        {done}");
                println!("In Progress: {in_progress}");
                println!("Pending:     {pending}");
                println!("Blocked:     {blocked}");
                if let Some(m) = avg {
                    println!("Avg time:    {m:.1}m per task");
                }
            }
        }

        TasksAction::Activity {
            repo,
            task,
            phase,
            limit,
        } => {
            let rows = ts
                .query_activity(&ActivityQueryParams {
                    repo_path: repo,
                    task_id: task,
                    phase,
                    limit: Some(limit),
                    ..Default::default()
                })
                .await?;
            if rows.is_empty() {
                println!("No activity found.");
            } else {
                for r in &rows {
                    let task_label = r.task_id.as_deref().unwrap_or("-");
                    println!(
                        "[{}] {} | {} | {} | {}",
                        r.ts,
                        r.agent,
                        r.action,
                        task_label,
                        r.detail.as_deref().unwrap_or("")
                    );
                }
            }
        }
    }

    Ok(())
}

fn print_task_detail(t: &AgentTaskRow) {
    println!("ID:       {}", t.id);
    println!("Title:    {}", t.title);
    println!("Status:   {}", t.status);
    println!("Severity: {}", t.severity.as_deref().unwrap_or("-"));
    println!("Phase:    {}", t.phase.as_deref().unwrap_or("-"));
    println!("File:     {}", t.file.as_deref().unwrap_or("-"));
    if let Some(ref a) = t.claimed_by {
        println!("Claimed:  {a}");
    }
    if let Some(ref n) = t.notes {
        println!("Notes:    {n}");
    }
    if let Some(ref b) = t.block_reason {
        println!("Blocked:  {b}");
    }
    println!("Repo:     {}", t.repo_path);
    println!("Created:  {}", t.created_at);
}

fn rand_u64() -> u64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    let ns = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .subsec_nanos();
    let pid = std::process::id() as u64;
    // simple non-crypto ID
    (ns as u64).wrapping_mul(1_000_003).wrapping_add(pid)
}

// ── clawd token show ──────────────────────────────────────────────────────────

fn run_token_show(config: &DaemonConfig) -> Result<()> {
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

// ── clawd token qr ────────────────────────────────────────────────────────────

fn run_token_qr(config: &DaemonConfig, use_relay: bool) -> Result<()> {
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

async fn run_server(
    port: Option<u16>,
    data_dir: Option<std::path::PathBuf>,
    log: Option<String>,
    max_sessions: Option<usize>,
    bind_address: Option<String>,
    no_migrate: bool,
) -> Result<()> {
    // Warn when a non-default port is used (dev-only scenario per F55.5.01).
    if let Some(p) = port {
        if p != 4300 {
            eprintln!(
                "warning: non-default port {p}. \n  This is for development only. \
                \n  Two daemons in production mode are unsupported."
            );
        }
    }
    info!(version = env!("CARGO_PKG_VERSION"), "clawd starting");

    let config = Arc::new(DaemonConfig::new(
        port,
        data_dir,
        log,
        max_sessions,
        bind_address,
    ));
    info!(
        data_dir = %config.data_dir.display(),
        port = config.port,
        max_sessions = config.max_sessions,
        "config loaded"
    );

    // ── Panic hook: write crash.log on panic (DC.T51) ────────────────────────
    install_panic_hook(config.data_dir.clone());
    // If previous run panicked, log the crash report and delete it.
    check_crash_log(&config.data_dir);

    // ── Rollback-on-crash detection (DC.T29) ─────────────────────────────────
    // If the previous binary crashed immediately after applying an update, restore
    // the backup automatically before proceeding.
    if update::check_and_rollback(&config.data_dir) {
        warn!("previous update was rolled back — running on restored binary");
    }
    // Delete the sentinel so a clean shutdown doesn't trigger rollback next time.
    update::delete_rollback_sentinel(&config.data_dir);

    // ── Provider CLI availability check ──────────────────────────────────────
    for binary in &["claude", "codex"] {
        let available = std::process::Command::new(binary)
            .arg("--version")
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .is_ok();
        if available {
            info!(binary = %binary, "provider CLI found");
        } else {
            warn!(
                binary = %binary,
                "provider CLI not found on PATH — sessions using this provider will fail"
            );
        }
    }

    let storage = Arc::new(if no_migrate {
        clawd::storage::Storage::new_no_migrate(&config.data_dir).await?
    } else {
        Storage::new_with_slow_query(
            &config.data_dir,
            config.observability.slow_query_threshold_ms,
        )
        .await?
    });

    // ── Apply SQLite WAL tuning (Sprint Z — Z.3) ─────────────────────────────
    if let Err(e) = clawd::perf::wal_tuning::apply_wal_tuning(storage.pool()).await {
        warn!(err = %e, "SQLite WAL tuning failed (non-fatal)");
    }

    let daemon_id = match identity::get_or_create(&storage).await {
        Ok(id) => {
            info!(daemon_id = %id, "daemon identity ready");
            id
        }
        Err(e) => {
            warn!("failed to get daemon_id: {e:#}; proceeding without identity");
            String::new()
        }
    };

    let broadcaster = Arc::new(EventBroadcaster::new());
    let repo_registry = Arc::new(RepoRegistry::new(broadcaster.clone()));
    let session_manager = Arc::new(SessionManager::new(
        storage.clone(),
        broadcaster.clone(),
        config.data_dir.clone(),
    ));

    let recovered = storage.recover_stale_sessions().await.unwrap_or(0);
    if recovered > 0 {
        info!(
            count = recovered,
            "recovered stale sessions from previous run"
        );
    }

    let license_info = license::verify_and_cache(&storage, &config, &daemon_id).await;
    let tier = license_info.tier.clone();
    let license = Arc::new(tokio::sync::RwLock::new(license_info));

    {
        let storage = storage.clone();
        let config = config.clone();
        let daemon_id = daemon_id.clone();
        let license = license.clone();
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(std::time::Duration::from_secs(24 * 60 * 60));
            interval.tick().await;
            loop {
                interval.tick().await;
                let info = license::verify_and_cache(&storage, &config, &daemon_id).await;
                *license.write().await = info;
            }
        });
    }

    // ── DB pruning + vacuum (daily, offset 1 h to stagger with license check) ─
    {
        let storage = storage.clone();
        let prune_days = config.session_prune_days;
        tokio::spawn(async move {
            // First run after 1 hour, then every 24 hours
            tokio::time::sleep(std::time::Duration::from_secs(60 * 60)).await;
            let mut consecutive_prune_failures: u32 = 0;
            loop {
                match storage.prune_old_sessions(prune_days).await {
                    Ok(n) if n > 0 => {
                        consecutive_prune_failures = 0;
                        info!(pruned = n, days = prune_days, "pruned old sessions");
                    }
                    Ok(_) => {
                        consecutive_prune_failures = 0;
                    }
                    Err(e) => {
                        consecutive_prune_failures += 1;
                        if consecutive_prune_failures >= 3 {
                            warn!(
                                err = %e,
                                failures = consecutive_prune_failures,
                                "session pruning failing repeatedly"
                            );
                        } else {
                            warn!(err = %e, "session pruning failed");
                        }
                    }
                }
                if let Err(e) = storage.vacuum().await {
                    warn!(err = %e, "sqlite vacuum failed");
                }
                tokio::time::sleep(std::time::Duration::from_secs(24 * 60 * 60)).await;
            }
        });
    }

    let telemetry = Arc::new(telemetry::spawn(config.clone(), daemon_id.clone(), tier));

    let account_registry = Arc::new(AccountRegistry::new(storage.clone(), broadcaster.clone()));
    let updater = Arc::new(update::spawn(config.clone(), broadcaster.clone()));

    let auth_token = match auth::get_or_create_token(&config.data_dir) {
        Ok(t) => {
            info!("auth token ready");
            t
        }
        Err(e) => {
            // Auth token is required — running without it leaves the daemon fully open.
            // This is a startup configuration error, not a recoverable condition.
            eprintln!("FATAL: failed to generate auth token: {e:#}");
            std::process::exit(1);
        }
    };
    // Warn if auth_token file has incorrect permissions (DC.T42).
    auth::check_token_permissions(&config.data_dir);

    // ── Task storage (shared pool from main storage) ──────────────────────────
    let task_storage = Arc::new(TaskStorage::new(storage.clone_pool()));

    // ── Token tracker (Phase 61 MI.T05) ──────────────────────────────────────
    let token_tracker = TokenTracker::new(storage.clone());

    // ── Phase 43c/43m: worktree manager + scheduler components ───────────────
    let worktree_manager =
        std::sync::Arc::new(clawd::worktree::WorktreeManager::new(&config.data_dir));
    let account_pool = std::sync::Arc::new(clawd::scheduler::accounts::AccountPool::new());
    let rate_limit_tracker =
        std::sync::Arc::new(clawd::scheduler::rate_limits::RateLimitTracker::new());
    let fallback_engine = std::sync::Arc::new(clawd::scheduler::fallback::FallbackEngine::new(
        std::sync::Arc::clone(&account_pool),
        std::sync::Arc::clone(&rate_limit_tracker),
    ));
    let scheduler_queue = std::sync::Arc::new(clawd::scheduler::queue::SchedulerQueue::new());

    // ── Version bump watcher (D64.T16) ───────────────────────────────────────
    let version_watcher = std::sync::Arc::new(clawd::doctor::version_watcher::VersionWatcher::new(
        broadcaster.clone(),
    ));

    // Retain a handle for post-shutdown WAL checkpoint (Sprint Z).
    let storage_for_shutdown = storage.clone();

    // ── Stores for memory and metrics (Sprint OO/PP) ─────────────────────────
    let memory_store = clawd::memory::MemoryStore::new(storage.clone_pool());
    let metrics_store = clawd::metrics::MetricsStore::new(storage.clone_pool());
    if let Err(e) = memory_store.migrate().await {
        warn!(err = %e, "memory store migration failed");
    }
    if let Err(e) = metrics_store.migrate().await {
        warn!(err = %e, "metrics store migration failed");
    }

    // ── Connectivity (Sprint JJ) ──────────────────────────────────────────────
    let quality = clawd::connectivity::new_shared_quality();
    let peer_registry = clawd::connectivity::direct::new_registry();

    let ctx = Arc::new(AppContext {
        config: config.clone(),
        storage,
        broadcaster: broadcaster.clone(),
        repo_registry,
        session_manager,
        daemon_id: daemon_id.clone(),
        license: license.clone(),
        telemetry,
        account_registry,
        updater,
        auth_token,
        started_at: std::time::Instant::now(),
        task_storage: task_storage.clone(),
        worktree_manager,
        account_pool,
        rate_limit_tracker,
        fallback_engine,
        scheduler_queue,
        orchestrator: std::sync::Arc::new(clawd::agents::orchestrator::Orchestrator::new()),
        token_tracker,
        metrics: std::sync::Arc::new(clawd::metrics::DaemonMetrics::new()),
        version_watcher: version_watcher.clone(),
        ide_bridge: clawd::ide::new_shared_bridge(),
        provider_sessions: clawd::agents::provider_session::new_shared_registry(),
        recovery_mode: no_migrate,
        automation_engine: clawd::automations::engine::AutomationEngine::new(
            clawd::automations::builtins::all(),
        ),
        quality,
        peer_registry,
        memory_store,
        metrics_store,
    });

    // ── Spawn automation engine dispatcher (Sprint CC CA.1) ──────────────────
    {
        let engine = Arc::clone(&ctx.automation_engine);
        let ctx_for_auto = (*ctx).clone();
        clawd::automations::engine::AutomationEngine::start_dispatcher(engine, ctx_for_auto);
    }

    // ── Spawn version bump watcher (D64.T16) ─────────────────────────────────
    version_watcher.spawn();

    // ── Spawn task background jobs ────────────────────────────────────────────
    {
        let ts = task_storage.clone();
        let bc = broadcaster.clone();
        tokio::spawn(clawd::tasks::jobs::run_heartbeat_checker(ts, bc, 90));
    }
    {
        let ts = task_storage.clone();
        tokio::spawn(clawd::tasks::jobs::run_done_task_archiver(ts, 24));
    }
    {
        let ts = task_storage.clone();
        tokio::spawn(clawd::tasks::jobs::run_activity_log_pruner(ts, 30));
    }

    // ── Lease janitor — release expired task leases every 30s (LH.T03) ─────
    {
        let storage = ctx.storage.clone();
        tokio::spawn(clawd::tasks::janitor::run_lease_janitor(storage));
    }

    // ── Background drift scanner (V02.T25) ───────────────────────────────────
    clawd::drift::background::spawn(ctx.storage.clone(), broadcaster.clone());

    // ── mDNS advertisement ────────────────────────────────────────────────────
    // Non-blocking: if mDNS fails (e.g. system restriction), daemon continues.
    let _mdns_guard = mdns::advertise(&daemon_id, config.port);

    // ── .claw/ AFS structure validation (Phase 43i) ───────────────────────────
    // Validate the .claw/ directory structure in the daemon's data dir.
    // Missing items are warned but never fatal — the daemon starts regardless.
    {
        let missing = clawd::claw_init::validate_claw_dir(&ctx.config.data_dir).await;
        if !missing.is_empty() {
            warn!(
                missing = ?missing,
                "missing .claw/ structure — run `clawd init-claw` to fix"
            );
        }
    }

    // Spawn relay AFTER ctx is built so it can dispatch inbound RPC frames
    // through the full IPC handler and forward push events to remote clients.
    {
        let lic = license.read().await;
        relay::spawn_if_enabled(config, &lic, daemon_id, ctx.clone()).await;
    }

    let run_result = clawd::ipc::run(ctx).await;

    // ── WAL checkpoint on clean shutdown (Sprint Z — Z.3) ────────────────────
    if let Err(e) =
        clawd::perf::wal_tuning::checkpoint_wal(storage_for_shutdown.pool(), "TRUNCATE").await
    {
        tracing::warn!(err = %e, "WAL checkpoint on shutdown failed (non-fatal)");
    }

    run_result
}
