// SPDX-License-Identifier: MIT
/// Sprint ZZ IG — Riverpod providers for the instruction graph.
///
/// Provides async access to the instruction scope tree, budget report,
/// and lint results for a given project path.
library;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_proto/clawd_proto.dart';

import 'daemon_provider.dart';

// ─── Instruction scope / explain ──────────────────────────────────────────────

/// Notifier that loads and caches the instruction scope tree for a path.
class InstructionScopeNotifier
    extends AutoDisposeFamilyAsyncNotifier<InstructionExplainResult?, String> {
  @override
  Future<InstructionExplainResult?> build(String arg) async => null;

  /// Load the instruction scope tree for this provider's path.
  Future<void> load() async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final raw = await client.instructionsExplain(arg);
      if (raw.isEmpty) {
        state = const AsyncValue.data(null);
      } else {
        state = AsyncValue.data(InstructionExplainResult.fromJson(raw));
      }
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

/// Provider family keyed by filesystem path.
final instructionScopeProvider = AsyncNotifierProvider.autoDispose
    .family<InstructionScopeNotifier, InstructionExplainResult?, String>(
  InstructionScopeNotifier.new,
);

// ─── Budget report ────────────────────────────────────────────────────────────

/// Notifier that loads the instruction budget report for a project.
class InstructionBudgetNotifier extends AutoDisposeFamilyAsyncNotifier<
    InstructionBudgetReport?, String> {
  @override
  Future<InstructionBudgetReport?> build(String arg) async => null;

  Future<void> load() async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final raw = await client.instructionsBudgetReport(arg);
      if (raw.isEmpty) {
        state = const AsyncValue.data(null);
      } else {
        state = AsyncValue.data(InstructionBudgetReport.fromJson(raw));
      }
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

/// Provider family keyed by project path.
final instructionBudgetProvider = AsyncNotifierProvider.autoDispose
    .family<InstructionBudgetNotifier, InstructionBudgetReport?, String>(
  InstructionBudgetNotifier.new,
);

// ─── Lint report ─────────────────────────────────────────────────────────────

/// Notifier that loads and caches the instruction lint report for a project.
class InstructionLintNotifier
    extends AutoDisposeFamilyAsyncNotifier<InstructionLintReport?, String> {
  @override
  Future<InstructionLintReport?> build(String arg) async => null;

  Future<void> load() async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final raw = await client.instructionsLint(arg);
      state = AsyncValue.data(InstructionLintReport.fromJson(raw));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

/// Provider family keyed by project path.
final instructionLintProvider = AsyncNotifierProvider.autoDispose
    .family<InstructionLintNotifier, InstructionLintReport?, String>(
  InstructionLintNotifier.new,
);

// ─── Evidence pack ────────────────────────────────────────────────────────────

/// Notifier that loads the evidence pack for a completed task.
class EvidencePackNotifier
    extends AutoDisposeFamilyAsyncNotifier<EvidencePack?, String> {
  @override
  Future<EvidencePack?> build(String arg) async => null;

  Future<void> load() async {
    state = const AsyncValue.loading();
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final raw = await client.artifactsEvidencePack(arg);
      if (raw == null) {
        state = const AsyncValue.data(null);
      } else {
        state = AsyncValue.data(EvidencePack.fromJson(raw));
      }
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }
}

/// Provider family keyed by task ID.
final evidencePackProvider = AsyncNotifierProvider.autoDispose
    .family<EvidencePackNotifier, EvidencePack?, String>(
  EvidencePackNotifier.new,
);
