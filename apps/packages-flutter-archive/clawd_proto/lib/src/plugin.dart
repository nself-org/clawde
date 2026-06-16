// Sprint FF PL.15 — Plugin types for clawd_proto.

/// Runtime type for a ClawDE plugin.
enum PluginRuntime {
  dylib,
  wasm;

  static PluginRuntime fromString(String s) => switch (s) {
        'dylib' => dylib,
        'wasm' => wasm,
        _ => dylib,
      };

  String get displayName => switch (this) {
        dylib => 'Native (dylib)',
        wasm => 'WebAssembly',
      };
}

/// Status of an installed plugin.
enum PluginStatus {
  enabled,
  disabled,
  failed,
  unknown;

  static PluginStatus fromString(String s) => switch (s) {
        'enabled' => enabled,
        'disabled' => disabled,
        'failed' => failed,
        _ => unknown,
      };
}

/// Capability a plugin may hold.
enum PluginCapability {
  fsRead,
  fsWrite,
  networkRelay,
  daemonRpc;

  static PluginCapability? fromString(String s) => switch (s) {
        'fs.read' => fsRead,
        'fs.write' => fsWrite,
        'network.relay' => networkRelay,
        'daemon.rpc' => daemonRpc,
        _ => null,
      };
}

/// Metadata for an installed plugin.
class PluginInfo {
  const PluginInfo({
    required this.name,
    required this.version,
    required this.runtime,
    required this.status,
    required this.path,
    required this.isSigned,
    this.description = '',
    this.capabilities = const [],
  });

  final String name;
  final String version;
  final PluginRuntime runtime;
  final PluginStatus status;
  final String path;
  final bool isSigned;
  final String description;
  final List<PluginCapability> capabilities;

  factory PluginInfo.fromJson(Map<String, dynamic> json) => PluginInfo(
        name: json['name'] as String? ?? '',
        version: json['version'] as String? ?? '0.0.0',
        runtime: PluginRuntime.fromString(json['runtime'] as String? ?? ''),
        status: PluginStatus.fromString(json['status'] as String? ?? ''),
        path: json['path'] as String? ?? '',
        isSigned: json['is_signed'] as bool? ?? false,
        description: json['description'] as String? ?? '',
        capabilities: (json['capabilities'] as List<dynamic>? ?? [])
            .whereType<String>()
            .map(PluginCapability.fromString)
            .whereType<PluginCapability>()
            .toList(),
      );

  Map<String, dynamic> toJson() => {
        'name': name,
        'version': version,
        'runtime': runtime.name,
        'status': status.name,
        'path': path,
        'is_signed': isSigned,
        'description': description,
        'capabilities': capabilities.map((c) => c.name).toList(),
      };
}
