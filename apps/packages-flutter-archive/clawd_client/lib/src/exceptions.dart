/// Exception types thrown by ClawdClient.
library;

/// Thrown when the daemon returns a JSON-RPC error response.
class ClawdRpcError implements Exception {
  const ClawdRpcError({required this.code, required this.message});

  final int code;
  final String message;

  @override
  String toString() => 'ClawdRpcError($code): $message';
}

/// Thrown when a call is made on a disconnected client, or when the
/// connection drops while waiting for a response.
class ClawdDisconnectedError implements Exception {
  const ClawdDisconnectedError();

  @override
  String toString() => 'ClawdDisconnectedError: connection lost';
}

/// Thrown when a JSON-RPC call exceeds the configured timeout.
class ClawdTimeoutError implements Exception {
  const ClawdTimeoutError(this.method);

  final String method;

  @override
  String toString() => 'ClawdTimeoutError: $method timed out';
}
