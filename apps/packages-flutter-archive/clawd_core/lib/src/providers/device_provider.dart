import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';
import 'daemon_provider.dart';

// ─── Paired device list ───────────────────────────────────────────────────────

/// All paired devices known to the daemon.
/// Re-fetches on daemon connect. Mutated on revoke/rename.
class PairedDevicesNotifier extends AsyncNotifier<List<PairedDevice>> {
  @override
  Future<List<PairedDevice>> build() async {
    ref.listen(daemonProvider, (prev, next) {
      if (next.isConnected) refresh();
    });

    return _fetch();
  }

  Future<List<PairedDevice>> _fetch() async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<List<dynamic>>('device.list');
    return result
        .map((j) => PairedDevice.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }

  /// Revoke a device by ID. The daemon marks it revoked and it can no longer
  /// connect. Refreshes the list after success.
  Future<void> revoke(String deviceId) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('device.revoke', {'id': deviceId});
    await refresh();
  }

  /// Rename a device. Refreshes the list after success.
  Future<void> rename(String deviceId, String name) async {
    final client = ref.read(daemonProvider.notifier).client;
    await client.call<void>('device.rename', {'id': deviceId, 'name': name});
    await refresh();
  }
}

final pairedDevicesProvider =
    AsyncNotifierProvider<PairedDevicesNotifier, List<PairedDevice>>(
  PairedDevicesNotifier.new,
);

// ─── Pair info ────────────────────────────────────────────────────────────────

/// Fetches a pairing PIN from the daemon (`daemon.pairPin` RPC).
///
/// This is a one-shot async provider. Invalidate it to generate a fresh PIN:
///   ref.invalidate(pairInfoProvider);
class PairInfoNotifier extends AsyncNotifier<PairInfo> {
  @override
  Future<PairInfo> build() async {
    final client = ref.read(daemonProvider.notifier).client;
    final result = await client.call<Map<String, dynamic>>('daemon.pairPin');
    return PairInfo.fromJson(result);
  }

  /// Regenerate the pairing PIN (invalidates and rebuilds the provider).
  Future<void> regenerate() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(() async {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>('daemon.pairPin');
      return PairInfo.fromJson(result);
    });
  }
}

final pairInfoProvider = AsyncNotifierProvider<PairInfoNotifier, PairInfo>(
  PairInfoNotifier.new,
);
