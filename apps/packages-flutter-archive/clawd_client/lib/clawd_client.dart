/// clawd_client — typed WebSocket/JSON-RPC 2.0 client for the clawd daemon.
///
/// Connects to ws://127.0.0.1:4300 (local) or wss://relay.clawde.io (remote).
library clawd_client;

export 'src/client.dart';
export 'src/direct_client.dart';
export 'src/exceptions.dart';
export 'src/relay_crypto.dart';
export 'src/relay_reconnect.dart';
