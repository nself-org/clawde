/// ClawDE shared widget library. Both desktop and mobile import this.
/// Widgets here are platform-agnostic — layout adaptation is the app's job.
library clawd_ui;

export 'src/theme/clawd_theme.dart';
export 'src/widgets/chat_bubble.dart';
export 'src/widgets/session_list_tile.dart';
export 'src/widgets/tool_call_card.dart';
export 'src/widgets/message_input.dart';
export 'src/widgets/connection_status_indicator.dart';
export 'src/widgets/provider_badge.dart';
export 'src/widgets/markdown_message.dart';
export 'src/widgets/error_state.dart';
export 'src/widgets/empty_state.dart';
export 'src/widgets/task_status_badge.dart';
export 'src/widgets/agent_chip.dart';
export 'src/widgets/task_card.dart';
export 'src/widgets/kanban_column.dart';
export 'src/widgets/kanban_board.dart';
export 'src/widgets/activity_feed_item.dart';
export 'src/widgets/activity_feed.dart';
export 'src/widgets/task_detail_panel.dart';
// Phase 41 — agent dashboard widgets
export 'src/widgets/agent_swimlane_row.dart';
export 'src/widgets/filter_bar.dart';
export 'src/widgets/add_task_dialog.dart';
export 'src/widgets/project_selector.dart';
export 'src/widgets/phase_log_view.dart';
export 'src/widgets/phase_indicator.dart';
export 'src/widgets/file_edit_card.dart';
export 'src/widgets/context_budget_bar.dart';

// Phase 43l — multi-agent UX widgets
export 'src/widgets/agent_feed.dart';
export 'src/widgets/worktree_status.dart';
export 'src/widgets/approval_card.dart';

// Phase 57 — mode badge + resource status
export 'src/widgets/mode_badge.dart';

// V02 Sprint B — task progress + promoted idea widgets
export 'src/widgets/task_progress_bar.dart';
export 'src/widgets/standards_chip.dart';
export 'src/widgets/provider_knowledge_chip.dart';
export 'src/widgets/drift_badge.dart';

// Phase D64 — doctor UI widgets
export 'src/widgets/doctor_badge.dart';
export 'src/widgets/doctor_panel.dart';
export 'src/widgets/release_plan_tile.dart';

// Sprint G — session intelligence widgets
export 'src/widgets/health_chip.dart';
export 'src/widgets/split_proposal_dialog.dart';

// Sprint H — model intelligence widgets
export 'src/widgets/model_chip.dart';
export 'src/widgets/token_usage_panel.dart';
