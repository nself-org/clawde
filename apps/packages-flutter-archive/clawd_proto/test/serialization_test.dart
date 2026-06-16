// QA-01: Serialization tests for all clawd_proto types.
// Tests fromJson round-trips, enum coverage, and edge cases.
import 'package:clawd_proto/clawd_proto.dart';
import 'package:test/test.dart';

void main() {
  // ── Session ──────────────────────────────────────────────────────────────

  group('Session.fromJson', () {
    final baseJson = {
      'id': 'sess-1',
      'repoPath': '/home/user/myapp',
      'title': 'My Session',
      'provider': 'claude',
      'status': 'running',
      'createdAt': '2024-01-15T10:00:00.000Z',
      'updatedAt': '2024-01-15T10:00:01.000Z',
      'messageCount': 3,
    };

    test('parses required fields', () {
      final s = Session.fromJson(baseJson);
      expect(s.id, 'sess-1');
      expect(s.repoPath, '/home/user/myapp');
      expect(s.title, 'My Session');
      expect(s.provider, ProviderType.claude);
      expect(s.status, SessionStatus.running);
      expect(s.messageCount, 3);
    });

    test('parses timestamps', () {
      final s = Session.fromJson(baseJson);
      expect(s.createdAt, DateTime.parse('2024-01-15T10:00:00.000Z'));
      expect(s.updatedAt, DateTime.parse('2024-01-15T10:00:01.000Z'));
    });

    test('title defaults to empty when absent', () {
      final json = Map<String, dynamic>.from(baseJson)..remove('title');
      expect(Session.fromJson(json).title, '');
    });

    test('messageCount defaults to 0 when absent', () {
      final json = Map<String, dynamic>.from(baseJson)..remove('messageCount');
      expect(Session.fromJson(json).messageCount, 0);
    });

    test('all SessionStatus values parse', () {
      for (final status in SessionStatus.values) {
        final json = Map<String, dynamic>.from(baseJson)
          ..['status'] = status.name;
        expect(Session.fromJson(json).status, status);
      }
    });

    test('unknown status falls back to idle', () {
      final json = Map<String, dynamic>.from(baseJson)
        ..['status'] = 'unknown_future_status';
      expect(Session.fromJson(json).status, SessionStatus.idle);
    });

    test('all ProviderType values parse', () {
      for (final provider in ProviderType.values) {
        final json = Map<String, dynamic>.from(baseJson)
          ..['provider'] = provider.name;
        expect(Session.fromJson(json).provider, provider);
      }
    });

    test('unknown provider falls back to claude', () {
      final json = Map<String, dynamic>.from(baseJson)
        ..['provider'] = 'unknown_provider';
      expect(Session.fromJson(json).provider, ProviderType.claude);
    });
  });

  // ── Message ───────────────────────────────────────────────────────────────

  group('Message.fromJson', () {
    final baseJson = {
      'id': 'msg-1',
      'sessionId': 'sess-1',
      'role': 'user',
      'content': 'Hello, AI!',
      'status': 'done',
      'createdAt': '2024-01-15T10:01:00.000Z',
      'metadata': <String, dynamic>{},
    };

    test('parses required fields', () {
      final m = Message.fromJson(baseJson);
      expect(m.id, 'msg-1');
      expect(m.sessionId, 'sess-1');
      expect(m.role, MessageRole.user);
      expect(m.content, 'Hello, AI!');
      expect(m.status, 'done');
    });

    test('all MessageRole values parse', () {
      for (final role in MessageRole.values) {
        final json = Map<String, dynamic>.from(baseJson)..['role'] = role.name;
        expect(Message.fromJson(json).role, role);
      }
    });

    test('status defaults to done when absent', () {
      final json = Map<String, dynamic>.from(baseJson)..remove('status');
      expect(Message.fromJson(json).status, 'done');
    });

    test('metadata defaults to empty when absent', () {
      final json = Map<String, dynamic>.from(baseJson)..remove('metadata');
      expect(Message.fromJson(json).metadata, isEmpty);
    });

    test('assistant message content preserved', () {
      final json = Map<String, dynamic>.from(baseJson)
        ..['role'] = 'assistant'
        ..['content'] = '## Answer\n\nHere is the code:\n\n```dart\nprint("hi");\n```';
      final m = Message.fromJson(json);
      expect(m.role, MessageRole.assistant);
      expect(m.content, contains('```dart'));
    });
  });

  // ── ToolCall ──────────────────────────────────────────────────────────────

  group('ToolCall.fromJson', () {
    final baseJson = {
      'id': 'tc-1',
      'sessionId': 'sess-1',
      'messageId': 'msg-1',
      'name': 'bash',
      'input': {'command': 'ls -la'},
      'status': 'pending',
      'createdAt': '2024-01-15T10:02:00.000Z',
      'completedAt': null,
    };

    test('parses required fields', () {
      final tc = ToolCall.fromJson(baseJson);
      expect(tc.id, 'tc-1');
      expect(tc.sessionId, 'sess-1');
      expect(tc.toolName, 'bash');
      expect(tc.status, ToolCallStatus.pending);
    });

    test('accepts name field (daemon snake_case alias)', () {
      final tc = ToolCall.fromJson(baseJson);
      expect(tc.toolName, 'bash');
    });

    test('accepts toolName field directly', () {
      final json = Map<String, dynamic>.from(baseJson)
        ..remove('name')
        ..['toolName'] = 'write_file';
      expect(ToolCall.fromJson(json).toolName, 'write_file');
    });

    test('maps done status to completed', () {
      final json = Map<String, dynamic>.from(baseJson)..['status'] = 'done';
      expect(ToolCall.fromJson(json).status, ToolCallStatus.completed);
    });

    test('all ToolCallStatus values parse (except done alias)', () {
      for (final status in ToolCallStatus.values) {
        final json = Map<String, dynamic>.from(baseJson)
          ..['status'] = status.name;
        expect(ToolCall.fromJson(json).status, status);
      }
    });

    test('completedAt is null when absent', () {
      final json = Map<String, dynamic>.from(baseJson)..['completedAt'] = null;
      expect(ToolCall.fromJson(json).completedAt, isNull);
    });

    test('completedAt parses when present', () {
      final json = Map<String, dynamic>.from(baseJson)
        ..['completedAt'] = '2024-01-15T10:02:05.000Z';
      expect(ToolCall.fromJson(json).completedAt, isNotNull);
    });
  });

  // ── RepoStatus ────────────────────────────────────────────────────────────

  group('RepoStatus.fromJson', () {
    final baseJson = {
      'repoPath': '/home/user/myapp',
      'branch': 'main',
      'ahead': 2,
      'behind': 0,
      'hasConflicts': false,
      'files': [
        {'path': 'lib/main.dart', 'status': 'modified', 'oldPath': null},
        {'path': 'lib/new.dart', 'status': 'untracked', 'oldPath': null},
      ],
    };

    test('parses required fields', () {
      final rs = RepoStatus.fromJson(baseJson);
      expect(rs.repoPath, '/home/user/myapp');
      expect(rs.branch, 'main');
      expect(rs.files.isNotEmpty, isTrue);
      expect(rs.ahead, 2);
      expect(rs.behind, 0);
    });

    test('parses files list', () {
      final rs = RepoStatus.fromJson(baseJson);
      expect(rs.files, hasLength(2));
      expect(rs.files[0].state, FileState.modified);
      expect(rs.files[1].state, FileState.untracked);
    });

    test('branch can be null', () {
      final json = Map<String, dynamic>.from(baseJson)..['branch'] = null;
      expect(RepoStatus.fromJson(json).branch, isNull);
    });

    test('all FileState values parse', () {
      for (final state in FileState.values) {
        final fileJson = {
          'path': 'test.dart',
          'status': state.name,
          'oldPath': null,
        };
        expect(FileStatus.fromJson(fileJson).state, state);
      }
    });

    test('FileStatus oldPath parses for moved files', () {
      final fileJson = {
        'path': 'lib/new_name.dart',
        'status': 'modified',
        'oldPath': 'lib/old_name.dart',
      };
      final fs = FileStatus.fromJson(fileJson);
      expect(fs.oldPath, 'lib/old_name.dart');
    });

    test('empty files list', () {
      final json = Map<String, dynamic>.from(baseJson)..['files'] = <dynamic>[];
      expect(RepoStatus.fromJson(json).files, isEmpty);
    });
  });

  // ── RpcRequest (toJson) ───────────────────────────────────────────────────

  group('RpcRequest.toJson', () {
    test('includes required fields', () {
      final req = RpcRequest(method: 'session.list', id: 1);
      final json = req.toJson();
      expect(json['jsonrpc'], '2.0');
      expect(json['method'], 'session.list');
      expect(json['id'], 1);
    });

    test('omits params when null', () {
      final json = RpcRequest(method: 'daemon.status', id: 2).toJson();
      expect(json.containsKey('params'), isFalse);
    });

    test('includes params when provided', () {
      final json = RpcRequest(
        method: 'session.create',
        params: {'repoPath': '/tmp/test', 'provider': 'claude'},
        id: 3,
      ).toJson();
      expect(json['params'], {'repoPath': '/tmp/test', 'provider': 'claude'});
    });
  });

  // ── RpcResponse / RpcError ────────────────────────────────────────────────

  group('RpcResponse.fromJson', () {
    test('success response', () {
      final r = RpcResponse.fromJson({
        'jsonrpc': '2.0',
        'result': {'id': 'sess-1'},
        'id': 1,
      });
      expect(r.isError, isFalse);
      expect(r.result, {'id': 'sess-1'});
    });

    test('error response', () {
      final r = RpcResponse.fromJson({
        'jsonrpc': '2.0',
        'error': {'code': -32001, 'message': 'Session not found'},
        'id': 2,
      });
      expect(r.isError, isTrue);
      expect(r.error!.code, -32001);
      expect(r.error!.message, 'Session not found');
    });

    test('null result for void responses', () {
      final r = RpcResponse.fromJson({
        'jsonrpc': '2.0',
        'result': null,
        'id': 3,
      });
      expect(r.result, isNull);
      expect(r.isError, isFalse);
    });
  });

  group('RpcError.fromJson', () {
    test('parses all fields', () {
      final err = RpcError.fromJson({
        'code': -32002,
        'message': 'Provider not available',
        'data': {'provider': 'codex'},
      });
      expect(err.code, -32002);
      expect(err.message, 'Provider not available');
      expect(err.data, {'provider': 'codex'});
    });

    test('data can be null', () {
      final err = RpcError.fromJson({
        'code': -32000,
        'message': 'Unknown error',
      });
      expect(err.data, isNull);
    });

    test('toString includes code and message', () {
      final err = RpcError(code: -32001, message: 'Not found');
      expect(err.toString(), contains('-32001'));
      expect(err.toString(), contains('Not found'));
    });
  });

  // ── Phase 41: AgentTask ────────────────────────────────────────────────────

  group('AgentTask.fromJson / toJson (Phase 41)', () {
    final baseJson = <String, dynamic>{
      'id': 'task-41-a',
      'title': 'Implement activity dashboard',
      'status': 'in_progress',
      'type': 'feature',
      'severity': 'high',
      'phase': '41',
      'claimed_by': 'agent-claude-1',
      'tags': '["dashboard","ui"]',
      'files': '["src/dashboard.dart","src/api.dart"]',
      'notes': null,
      'repo_path': '/home/user/clawde',
      'created_at': '2026-02-22T00:00:00Z',
    };

    test('parses all key fields from snake_case JSON', () {
      final task = AgentTask.fromJson(baseJson);
      expect(task.id, 'task-41-a');
      expect(task.title, 'Implement activity dashboard');
      expect(task.status, TaskStatus.inProgress);
      expect(task.taskType, TaskType.feature);
      expect(task.severity, TaskSeverity.high);
      expect(task.phase, '41');
      expect(task.claimedBy, 'agent-claude-1');
    });

    test('parses tags from JSON string', () {
      final task = AgentTask.fromJson(baseJson);
      expect(task.tags, containsAll(['dashboard', 'ui']));
    });

    test('parses files from JSON string', () {
      final task = AgentTask.fromJson(baseJson);
      expect(task.files, hasLength(2));
      expect(task.files, contains('src/dashboard.dart'));
    });

    test('toJson round-trips key fields', () {
      final task = AgentTask.fromJson(baseJson);
      final json = task.toJson();
      expect(json['id'], 'task-41-a');
      expect(json['title'], 'Implement activity dashboard');
      expect(json['status'], 'in_progress');
      expect(json['severity'], 'high');
      expect(json['phase'], '41');
      expect(json['claimedBy'], 'agent-claude-1');
    });

    test('full round-trip preserves status and type', () {
      final task = AgentTask.fromJson(baseJson);
      final rebuilt = AgentTask.fromJson(task.toJson());
      expect(rebuilt.status, task.status);
      expect(rebuilt.taskType, task.taskType);
      expect(rebuilt.severity, task.severity);
      expect(rebuilt.id, task.id);
    });
  });

  // ── Phase 41: TaskStatus enum ─────────────────────────────────────────────

  group('TaskStatus enum (Phase 41)', () {
    test('parses in_progress', () {
      expect(TaskStatus.fromString('in_progress'), TaskStatus.inProgress);
    });

    test('parses pending', () {
      expect(TaskStatus.fromString('pending'), TaskStatus.pending);
    });

    test('parses done', () {
      expect(TaskStatus.fromString('done'), TaskStatus.done);
    });

    test('parses blocked', () {
      expect(TaskStatus.fromString('blocked'), TaskStatus.blocked);
    });

    test('parses deferred', () {
      expect(TaskStatus.fromString('deferred'), TaskStatus.deferred);
    });

    test('parses interrupted', () {
      expect(TaskStatus.fromString('interrupted'), TaskStatus.interrupted);
    });

    test('parses in_qa', () {
      expect(TaskStatus.fromString('in_qa'), TaskStatus.inQa);
    });

    test('unknown string falls back to pending', () {
      expect(TaskStatus.fromString('unknown_xyz'), TaskStatus.pending);
    });

    test('toJsonStr round-trips for all values', () {
      for (final s in TaskStatus.values) {
        final str = s.toJsonStr();
        expect(TaskStatus.fromString(str), s,
            reason: '$s.toJsonStr() = "$str" did not round-trip');
      }
    });
  });

  // ── Phase 41: ActivityLogEntry ────────────────────────────────────────────

  group('ActivityLogEntry.fromJson / toJson (Phase 41)', () {
    test('round-trip with camelCase entryType', () {
      final json = <String, dynamic>{
        'id': 'ald-1',
        'agent': 'agent-claude-1',
        'action': 'task_started',
        'entryType': 'auto',
        'ts': 1700000000,
        'taskId': 'task-41-a',
        'phase': '41',
        'detail': 'task execution began',
        'repoPath': '/home/user/clawde',
      };
      final entry = ActivityLogEntry.fromJson(json);
      expect(entry.id, 'ald-1');
      expect(entry.agent, 'agent-claude-1');
      expect(entry.action, 'task_started');
      expect(entry.entryType, ActivityEntryType.auto);
      expect(entry.ts, 1700000000);
      expect(entry.taskId, 'task-41-a');
      expect(entry.phase, '41');
      expect(entry.detail, 'task execution began');

      final rebuilt = ActivityLogEntry.fromJson(entry.toJson());
      expect(rebuilt.id, entry.id);
      expect(rebuilt.entryType, entry.entryType);
      expect(rebuilt.ts, entry.ts);
    });

    test('round-trip with snake_case entry_type', () {
      final json = <String, dynamic>{
        'id': 'ald-2',
        'agent': 'agent-bot',
        'action': 'agent_note',
        'entry_type': 'note',
        'ts': 1700000001,
        'task_id': 'task-41-b',
        'repo_path': '/tmp/repo',
      };
      final entry = ActivityLogEntry.fromJson(json);
      expect(entry.entryType, ActivityEntryType.note);
      expect(entry.taskId, 'task-41-b');
      expect(entry.repoPath, '/tmp/repo');
    });

    test('unknown entry_type falls back to auto', () {
      final json = <String, dynamic>{
        'id': 'ald-3',
        'agent': 'bot',
        'action': 'x',
        'entry_type': 'unknown_type',
        'ts': 0,
        'repo_path': '/tmp',
      };
      expect(ActivityLogEntry.fromJson(json).entryType, ActivityEntryType.auto);
    });
  });

  // ── Phase 41: AgentView ───────────────────────────────────────────────────

  group('AgentView.fromJson / toJson (Phase 41)', () {
    test('parses camelCase agentId', () {
      final json = <String, dynamic>{
        'agentId': 'agent-claude-1',
        'status': 'active',
        'repoPath': '/home/user/clawde',
        'agentType': 'claude',
        'sessionId': 'sess-1',
        'currentTaskId': 'task-41-a',
        'lastSeen': 1700000000,
        'connectedAt': 1699999000,
      };
      final view = AgentView.fromJson(json);
      expect(view.agentId, 'agent-claude-1');
      expect(view.status, AgentViewStatus.active);
      expect(view.repoPath, '/home/user/clawde');
      expect(view.agentType, 'claude');
      expect(view.sessionId, 'sess-1');
      expect(view.currentTaskId, 'task-41-a');
      expect(view.lastSeen, 1700000000);
      expect(view.connectedAt, 1699999000);
    });

    test('parses snake_case agent_id', () {
      final json = <String, dynamic>{
        'agent_id': 'agent-snake-1',
        'status': 'idle',
        'repo_path': '/tmp/repo',
        'agent_type': 'codex',
        'last_seen': 1700000002,
        'connected_at': 1700000000,
      };
      final view = AgentView.fromJson(json);
      expect(view.agentId, 'agent-snake-1');
      expect(view.status, AgentViewStatus.idle);
      expect(view.repoPath, '/tmp/repo');
      expect(view.agentType, 'codex');
      expect(view.lastSeen, 1700000002);
      expect(view.connectedAt, 1700000000);
    });

    test('toJson round-trip', () {
      final json = <String, dynamic>{
        'agentId': 'agent-rt',
        'status': 'active',
        'repoPath': '/tmp',
        'agentType': 'claude',
      };
      final view = AgentView.fromJson(json);
      final rebuilt = AgentView.fromJson(view.toJson());
      expect(rebuilt.agentId, view.agentId);
      expect(rebuilt.status, view.status);
    });

    test('unknown status falls back to offline', () {
      final json = <String, dynamic>{
        'agentId': 'a1',
        'status': 'unknown_status',
        'repoPath': '/tmp',
      };
      expect(AgentView.fromJson(json).status, AgentViewStatus.offline);
    });
  });

  // ── Phase 41: Task push events ────────────────────────────────────────────

  group('TaskClaimedEvent.fromJson (Phase 41)', () {
    test('parses snake_case keys', () {
      final json = <String, dynamic>{
        'task_id': 'task-41-a',
        'agent_id': 'agent-claude-1',
        'is_resume': false,
      };
      final ev = TaskClaimedEvent.fromJson(json);
      expect(ev.taskId, 'task-41-a');
      expect(ev.agentId, 'agent-claude-1');
      expect(ev.isResume, isFalse);
    });

    test('parses camelCase keys', () {
      final json = <String, dynamic>{
        'taskId': 'task-41-b',
        'agentId': 'agent-codex-2',
        'is_resume': true,
      };
      final ev = TaskClaimedEvent.fromJson(json);
      expect(ev.taskId, 'task-41-b');
      expect(ev.agentId, 'agent-codex-2');
      expect(ev.isResume, isTrue);
    });

    test('isResume defaults to false when absent', () {
      final json = <String, dynamic>{
        'taskId': 'task-x',
        'agentId': 'agent-y',
      };
      expect(TaskClaimedEvent.fromJson(json).isResume, isFalse);
    });
  });

  group('TaskStatusChangedEvent.fromJson (Phase 41)', () {
    test('parses snake_case task_id and status', () {
      final json = <String, dynamic>{
        'task_id': 'task-41-a',
        'status': 'done',
        'notes': 'task completed successfully',
      };
      final ev = TaskStatusChangedEvent.fromJson(json);
      expect(ev.taskId, 'task-41-a');
      expect(ev.status, TaskStatus.done);
      expect(ev.notes, 'task completed successfully');
    });

    test('parses camelCase taskId', () {
      final json = <String, dynamic>{
        'taskId': 'task-41-b',
        'status': 'blocked',
      };
      final ev = TaskStatusChangedEvent.fromJson(json);
      expect(ev.taskId, 'task-41-b');
      expect(ev.status, TaskStatus.blocked);
      expect(ev.notes, isNull);
    });

    test('unknown status falls back to pending', () {
      final json = <String, dynamic>{
        'taskId': 'task-z',
        'status': 'future_unknown_status',
      };
      expect(
        TaskStatusChangedEvent.fromJson(json).status,
        TaskStatus.pending,
      );
    });
  });
}
