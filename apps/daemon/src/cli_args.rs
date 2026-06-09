//! CLI argument definitions for the clawd binary.
//!
//! Purpose: All Clap-derived structs and enums that define the CLI surface.
//! Inputs:  Command-line arguments (std::env::args).
//! Outputs: Parsed [`Args`] and variant enums consumed by `main()`.
//! Constraints: No async, no I/O — pure parse-time types only.

use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(
    name = "clawd",
    about = "ClawDE Host — always-on background daemon",
    version
)]
pub struct Args {
    #[command(subcommand)]
    pub command: Option<Command>,

    /// JSON-RPC WebSocket server port
    #[arg(long, env = "CLAWD_PORT", global = true)]
    pub port: Option<u16>,

    /// Data directory for sessions, config, and SQLite database
    #[arg(long, env = "CLAWD_DATA_DIR", global = true)]
    pub data_dir: Option<std::path::PathBuf>,

    /// Log level (trace, debug, info, warn, error)
    #[arg(long, env = "CLAWD_LOG")]
    pub log: Option<String>,

    /// Maximum concurrent sessions (0 = unlimited)
    #[arg(long, env = "CLAWD_MAX_SESSIONS")]
    pub max_sessions: Option<usize>,

    /// Bind address for the WebSocket server (default: 127.0.0.1; use 0.0.0.0 for LAN access)
    #[arg(long, env = "CLAWD_BIND")]
    pub bind_address: Option<String>,

    /// Write logs to this file path (rotated daily). Optional.
    #[arg(long, env = "CLAWD_LOG_FILE")]
    pub log_file: Option<std::path::PathBuf>,

    /// Suppress progress and informational output.
    ///
    /// Errors are still printed to stderr. JSON output (--json flags) is
    /// unaffected. Use this flag when piping output to other tools.
    #[arg(long, short = 'q', global = true)]
    pub quiet: bool,

    /// Skip database migrations and start in recovery mode (UX.2 — Sprint BB).
    ///
    /// Use when a migration failure prevents the daemon from starting normally.
    /// `daemon.status` reports `recoveryMode: true`; Flutter clients show a
    /// recovery overlay with retry / rollback options.
    ///
    /// Pre-migration backups live in `{data_dir}/backups/`.
    #[arg(long, env = "CLAWD_NO_MIGRATE")]
    pub no_migrate: bool,
}

#[derive(Subcommand)]
pub enum Command {
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
pub enum InstructionsAction {
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
pub enum PolicyAction {
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
pub enum BenchAction {
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
pub enum TokenCmd {
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
pub enum AccountCmd {
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
pub enum ProjectCommands {
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
pub enum TasksAction {
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
pub enum ServiceAction {
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
