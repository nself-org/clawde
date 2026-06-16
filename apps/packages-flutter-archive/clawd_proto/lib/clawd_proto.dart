/// clawd_proto — JSON-RPC 2.0 protocol types for the clawd daemon.
///
/// Mirrors the 17 RPC methods and 7 push event types defined in the
/// ClawDE system specification. Also includes Phase 41 agent task types.
library clawd_proto;

export 'src/session.dart';
export 'src/message.dart';
export 'src/repo_status.dart';
export 'src/tool_call.dart';
export 'src/rpc.dart';
export 'src/agent_task.dart';
export 'src/agent_activity.dart';
export 'src/task_dtos.dart';
export 'src/task_events.dart';

// Phase 43l — multi-agent UX types
export 'src/agent.dart';
export 'src/worktree_status.dart';
export 'src/worktree_events.dart';

// Phase 57 resource governor types
export 'src/resource_stats.dart';

// Device pairing and project types
export 'src/project.dart';
export 'src/device.dart';

// Phase D64 — doctor types
export 'src/doctor.dart';

// Sprint DD — semantic intelligence types
export 'src/workflow.dart';
export 'src/sovereignty.dart';
export 'src/semantic_event.dart';
export 'src/session_bundle.dart';
export 'src/git_query.dart';

// Sprint EE — ecosystem + session sharing types
export 'src/ci.dart';
export 'src/session_share.dart';
export 'src/digest.dart';

// Sprint FF — plugin architecture types
export 'src/plugin.dart';

// Sprint GG — code completion + session search types
export 'src/completion.dart';
export 'src/search.dart';

// Sprint OO — AI memory types
export 'src/memory.dart';

// Sprint ZZ — instruction graph + evidence pack types
export 'src/instruction.dart';
export 'src/evidence_pack.dart';
