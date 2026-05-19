import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:clawd_core/clawd_core.dart';
import 'package:clawd_ui/clawd_ui.dart';

// ─── Providers ───────────────────────────────────────────────────────────────

final _replayMessagesProvider =
    StateProvider<List<Map<String, dynamic>>>((ref) => []);

// ─── Screen ──────────────────────────────────────────────────────────────────

/// Sprint DD SR.7 — Session Replay screen.
///
/// Displays a timeline scrubber for replay sessions. Users can play, pause,
/// and control replay speed. Messages appear in sequence during playback.
class SessionReplayScreen extends ConsumerStatefulWidget {
  const SessionReplayScreen({super.key, required this.sessionId});

  final String sessionId;

  @override
  ConsumerState<SessionReplayScreen> createState() =>
      _SessionReplayScreenState();
}

class _SessionReplayScreenState extends ConsumerState<SessionReplayScreen> {
  bool _isPlaying = false;
  double _speed = 1.0;

  Future<void> _startReplay() async {
    setState(() => _isPlaying = true);
    ref.read(_replayMessagesProvider.notifier).state = [];
    try {
      final client = ref.read(daemonProvider.notifier).client;
      await client.call<Map<String, dynamic>>('session.replay', {
        'sessionId': widget.sessionId,
        'speed': _speed,
      });
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Replay failed: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _isPlaying = false);
    }
  }

  Future<void> _exportBundle() async {
    try {
      final client = ref.read(daemonProvider.notifier).client;
      final result = await client.call<Map<String, dynamic>>(
          'session.export', {'sessionId': widget.sessionId});
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
                'Exported ${result['messageCount']} messages. Bundle ready.'),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Export failed: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // ── Header ──────────────────────────────────────────────────────────
          Row(
            children: [
              const Icon(Icons.replay, color: ClawdTheme.clawLight, size: 20),
              const SizedBox(width: 8),
              Text(
                'Session Replay',
                style: Theme.of(context)
                    .textTheme
                    .titleLarge
                    ?.copyWith(fontWeight: FontWeight.w700),
              ),
              const Spacer(),
              OutlinedButton.icon(
                onPressed: _exportBundle,
                icon: const Icon(Icons.download, size: 14),
                label: const Text('Export Bundle'),
                style: OutlinedButton.styleFrom(
                  foregroundColor: Colors.white60,
                  side: const BorderSide(color: ClawdTheme.surfaceBorder),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'Session: ${widget.sessionId}',
            style: const TextStyle(
                fontSize: 11,
                color: Colors.white38,
                fontFamily: 'monospace'),
          ),
          const SizedBox(height: 24),

          // ── Controls ────────────────────────────────────────────────────────
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  // Play/Pause button.
                  FilledButton.icon(
                    onPressed: _isPlaying ? null : _startReplay,
                    icon: Icon(
                      _isPlaying ? Icons.pause : Icons.play_arrow,
                      size: 18,
                    ),
                    label: Text(_isPlaying ? 'Replaying…' : 'Play'),
                    style: FilledButton.styleFrom(
                        backgroundColor: ClawdTheme.claw),
                  ),
                  const SizedBox(width: 16),

                  // Speed selector.
                  const Text('Speed:',
                      style: TextStyle(
                          fontSize: 13, color: Colors.white54)),
                  const SizedBox(width: 8),
                  DropdownButton<double>(
                    value: _speed,
                    items: const [
                      DropdownMenuItem(value: 0.5, child: Text('0.5×')),
                      DropdownMenuItem(value: 1.0, child: Text('1×')),
                      DropdownMenuItem(value: 2.0, child: Text('2×')),
                      DropdownMenuItem(value: 5.0, child: Text('5×')),
                    ],
                    onChanged: _isPlaying
                        ? null
                        : (v) {
                            if (v != null) {
                              setState(() => _speed = v);
                            }
                          },
                    underline: const SizedBox.shrink(),
                  ),

                  if (_isPlaying) ...[
                    const SizedBox(width: 16),
                    const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                    const SizedBox(width: 8),
                    const Text('Playing back…',
                        style: TextStyle(
                            fontSize: 12, color: Colors.white54)),
                  ],
                ],
              ),
            ),
          ),
          const SizedBox(height: 16),

          // ── Message Timeline ─────────────────────────────────────────────
          Expanded(
            child: Consumer(
              builder: (context, ref, _) {
                final messages = ref.watch(_replayMessagesProvider);
                if (messages.isEmpty) {
                  return Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(Icons.play_circle_outline,
                            size: 48, color: Colors.white12),
                        const SizedBox(height: 12),
                        Text(
                          _isPlaying
                              ? 'Waiting for messages…'
                              : 'Press Play to start the replay.',
                          style: const TextStyle(color: Colors.white38),
                        ),
                      ],
                    ),
                  );
                }
                return ListView.builder(
                  itemCount: messages.length,
                  scrollCacheExtent: const ScrollCacheExtent.pixels(500),
                  itemBuilder: (context, i) {
                    final msg = messages[i];
                    final isUser = msg['role'] == 'user';
                    return Padding(
                      padding: const EdgeInsets.symmetric(vertical: 4),
                      child: Row(
                        mainAxisAlignment: isUser
                            ? MainAxisAlignment.end
                            : MainAxisAlignment.start,
                        children: [
                          Container(
                            constraints:
                                const BoxConstraints(maxWidth: 500),
                            padding: const EdgeInsets.symmetric(
                                horizontal: 12, vertical: 8),
                            decoration: BoxDecoration(
                              color: isUser
                                  ? ClawdTheme.userBubble
                                  : ClawdTheme.assistantBubble,
                              borderRadius: BorderRadius.circular(12),
                            ),
                            child: Text(
                              (msg['content'] as String?) ?? '',
                              style: const TextStyle(fontSize: 13),
                            ),
                          ),
                        ],
                      ),
                    );
                  },
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}
