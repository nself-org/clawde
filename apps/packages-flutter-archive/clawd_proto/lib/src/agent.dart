/// Agent types for multi-agent UX — Phase 43l.

/// The role of an agent in the multi-agent pipeline.
enum AgentRole {
  router,
  planner,
  implementer,
  reviewer,
  qaExecutor;

  static AgentRole fromString(String s) {
    return AgentRole.values.firstWhere(
      (r) => r.name == s,
      orElse: () => AgentRole.implementer,
    );
  }

  String get displayName {
    switch (this) {
      case AgentRole.router:
        return 'Router';
      case AgentRole.planner:
        return 'Planner';
      case AgentRole.implementer:
        return 'Implementer';
      case AgentRole.reviewer:
        return 'Reviewer';
      case AgentRole.qaExecutor:
        return 'QA';
    }
  }
}

/// Runtime status of an agent process.
enum AgentStatus {
  pending,
  running,
  paused,
  completed,
  failed,
  crashed;

  static AgentStatus fromString(String s) {
    return AgentStatus.values.firstWhere(
      (r) => r.name == s,
      orElse: () => AgentStatus.pending,
    );
  }
}

/// A running or completed agent record returned by the daemon.
class AgentRecord {
  final String agentId;
  final AgentRole role;
  final String taskId;
  final String provider; // "claude" | "codex"
  final String model;
  final String? worktreePath;
  final AgentStatus status;
  final DateTime createdAt;
  final DateTime lastHeartbeat;
  final int tokensUsed;
  final double costUsdEst;
  final String? result;
  final String? error;

  const AgentRecord({
    required this.agentId,
    required this.role,
    required this.taskId,
    required this.provider,
    required this.model,
    this.worktreePath,
    required this.status,
    required this.createdAt,
    required this.lastHeartbeat,
    this.tokensUsed = 0,
    this.costUsdEst = 0.0,
    this.result,
    this.error,
  });

  factory AgentRecord.fromJson(Map<String, dynamic> json) => AgentRecord(
        agentId: json['agent_id'] as String? ?? json['agentId'] as String? ?? '',
        role: AgentRole.fromString(json['role'] as String? ?? 'implementer'),
        taskId: json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        provider: json['provider'] as String? ?? '',
        model: json['model'] as String? ?? '',
        worktreePath: json['worktree_path'] as String? ?? json['worktreePath'] as String?,
        status: AgentStatus.fromString(json['status'] as String? ?? 'pending'),
        createdAt: DateTime.parse(json['created_at'] as String? ?? json['createdAt'] as String? ?? DateTime.now().toIso8601String()),
        lastHeartbeat: DateTime.parse(json['last_heartbeat'] as String? ?? json['lastHeartbeat'] as String? ?? DateTime.now().toIso8601String()),
        tokensUsed: (json['tokens_used'] as num?)?.toInt() ?? (json['tokensUsed'] as num?)?.toInt() ?? 0,
        costUsdEst: (json['cost_usd_est'] as num?)?.toDouble() ?? (json['costUsdEst'] as num?)?.toDouble() ?? 0.0,
        result: json['result'] as String?,
        error: json['error'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'agent_id': agentId,
        'role': role.name,
        'task_id': taskId,
        'provider': provider,
        'model': model,
        if (worktreePath != null) 'worktree_path': worktreePath,
        'status': status.name,
        'created_at': createdAt.toIso8601String(),
        'last_heartbeat': lastHeartbeat.toIso8601String(),
        'tokens_used': tokensUsed,
        'cost_usd_est': costUsdEst,
        if (result != null) 'result': result,
        if (error != null) 'error': error,
      };
}

/// An approval request raised by an agent that requires user confirmation.
class ApprovalRequest {
  final String approvalId;
  final String taskId;
  final String agentId;
  final String tool;
  final String argsSummary;
  final String risk; // "low" | "medium" | "high" | "critical"
  final DateTime requestedAt;

  const ApprovalRequest({
    required this.approvalId,
    required this.taskId,
    required this.agentId,
    required this.tool,
    required this.argsSummary,
    required this.risk,
    required this.requestedAt,
  });

  factory ApprovalRequest.fromJson(Map<String, dynamic> json) => ApprovalRequest(
        approvalId: json['approval_id'] as String? ?? json['approvalId'] as String? ?? '',
        taskId: json['task_id'] as String? ?? json['taskId'] as String? ?? '',
        agentId: json['agent_id'] as String? ?? json['agentId'] as String? ?? '',
        tool: json['tool'] as String? ?? '',
        argsSummary: json['args_summary'] as String? ?? json['argsSummary'] as String? ?? '',
        risk: json['risk'] as String? ?? 'medium',
        requestedAt: DateTime.parse(
          json['requested_at'] as String? ?? json['requestedAt'] as String? ?? DateTime.now().toIso8601String(),
        ),
      );

  Map<String, dynamic> toJson() => {
        'approval_id': approvalId,
        'task_id': taskId,
        'agent_id': agentId,
        'tool': tool,
        'args_summary': argsSummary,
        'risk': risk,
        'requested_at': requestedAt.toIso8601String(),
      };
}
