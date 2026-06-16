/// JSON-RPC 2.0 envelope types.

/// A JSON-RPC 2.0 request.
class RpcRequest {
  final String jsonrpc = '2.0';
  final String method;
  final Map<String, dynamic>? params;
  final dynamic id;

  const RpcRequest({required this.method, this.params, this.id});

  Map<String, dynamic> toJson() => {
        'jsonrpc': jsonrpc,
        'method': method,
        if (params != null) 'params': params,
        if (id != null) 'id': id,
      };
}

/// A JSON-RPC 2.0 response.
class RpcResponse {
  final String jsonrpc;
  final dynamic result;
  final RpcError? error;
  final dynamic id;

  const RpcResponse({
    required this.jsonrpc,
    this.result,
    this.error,
    this.id,
  });

  bool get isError => error != null;

  factory RpcResponse.fromJson(Map<String, dynamic> json) => RpcResponse(
        jsonrpc: json['jsonrpc'] as String,
        result: json['result'],
        error: json['error'] != null
            ? RpcError.fromJson(json['error'] as Map<String, dynamic>)
            : null,
        id: json['id'],
      );
}

/// A JSON-RPC 2.0 error object.
class RpcError {
  final int code;
  final String message;
  final dynamic data;

  const RpcError({required this.code, required this.message, this.data});

  factory RpcError.fromJson(Map<String, dynamic> json) => RpcError(
        code: json['code'] as int,
        message: json['message'] as String,
        data: json['data'],
      );

  @override
  String toString() => 'RpcError($code): $message';
}

/// Standard clawd error codes.
abstract final class ClawdError {
  // ── Session errors (-32001..-32007) ──────────────────────────────────────
  static const int sessionNotFound = -32001;
  static const int providerNotAvailable = -32002;
  static const int rateLimited = -32003;
  static const int unauthorized = -32004;
  static const int repoNotFound = -32005;
  static const int sessionPaused = -32006;
  static const int sessionLimitReached = -32007;

  // ── Task system errors (-32010..-32015) ──────────────────────────────────
  static const int taskNotFound = -32010;
  static const int taskAlreadyClaimed = -32011;
  static const int taskAlreadyDone = -32012;
  static const int agentNotFound = -32013;
  static const int missingCompletionNotes = -32014;
  static const int taskNotResumable = -32015;

  /// Human-readable label for any known error code.
  static String label(int code) {
    switch (code) {
      case sessionNotFound:
        return 'Session not found';
      case providerNotAvailable:
        return 'Provider not available';
      case rateLimited:
        return 'Rate limited';
      case unauthorized:
        return 'Unauthorized';
      case repoNotFound:
        return 'Repository not found';
      case sessionPaused:
        return 'Session paused';
      case sessionLimitReached:
        return 'Session limit reached';
      case taskNotFound:
        return 'Task not found';
      case taskAlreadyClaimed:
        return 'Task already claimed';
      case taskAlreadyDone:
        return 'Task already done';
      case agentNotFound:
        return 'Agent not found';
      case missingCompletionNotes:
        return 'Completion notes required';
      case taskNotResumable:
        return 'Task not resumable';
      default:
        return 'Unknown error ($code)';
    }
  }
}
