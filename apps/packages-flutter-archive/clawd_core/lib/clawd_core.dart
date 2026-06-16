/// ClawDE core — shared Riverpod providers for daemon connection, sessions,
/// messages, and tool calls. Both desktop and mobile import this package.
library clawd_core;

export 'src/providers/daemon_provider.dart';
export 'src/providers/session_provider.dart';
export 'src/providers/message_provider.dart';
export 'src/providers/tool_call_provider.dart';
export 'src/providers/repo_provider.dart';
export 'src/providers/settings_provider.dart';
export 'src/providers/task_provider.dart';
export 'src/utils/paths.dart';
export 'src/session_export.dart';

// Phase 43l — multi-agent UX providers
export 'src/providers/agent_provider.dart';
export 'src/providers/task_summary_provider.dart';
export 'src/providers/worktree_provider.dart';

// Phase 57 — resource governor + system stats
export 'src/providers/resource_stats_provider.dart';

// V02 Sprint B — session indicator providers (standards, provider knowledge, drift)
export 'src/providers/session_indicators_provider.dart';

// Device pairing, project management, and connection state providers
export 'src/providers/project_provider.dart';
export 'src/providers/device_provider.dart';
export 'src/providers/connection_state_provider.dart';

// Phase D64 — doctor provider
export 'src/providers/doctor_provider.dart';

// Sprint G — session intelligence providers
export 'src/providers/session_health_provider.dart';

// Sprint H — model intelligence providers
export 'src/providers/token_usage_provider.dart';

// Dunning — license + grace period
export 'src/providers/license_provider.dart';

// Relay resilience
export 'src/providers/relay_provider.dart';

// Sprint DD — semantic intelligence providers
export 'src/providers/workflow_provider.dart';
export 'src/providers/pulse_provider.dart';

// Sprint JJ — connectivity + LAN peer discovery
export 'src/providers/connectivity_provider.dart';

// Sprint ZZ — instruction graph + evidence pack providers
export 'src/providers/instruction_provider.dart';
