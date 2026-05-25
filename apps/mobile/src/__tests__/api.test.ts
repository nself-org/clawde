/**
 * Purpose: Unit tests for the API façade — verifies RPC method names and param shapes.
 * Inputs:  Mock daemonClient.call; api module functions.
 * Outputs: Jest assertions on method strings and param objects passed to daemonClient.
 * Constraints: No actual WebSocket used.
 * SPORT: T-E1-06 — React Native Expo migration
 */

// Mock the daemon client
const mockCall = jest.fn().mockResolvedValue([]);

jest.mock('../lib/daemon', () => ({
  daemonClient: {
    call: mockCall,
    setUrl: jest.fn(),
    reconnect: jest.fn(),
    onConnectionChange: jest.fn(),
  },
}));

import * as api from '../lib/api';

beforeEach(() => {
  mockCall.mockClear();
  mockCall.mockResolvedValue([]);
});

describe('API façade', () => {
  it('listSessions calls session.list', async () => {
    await api.listSessions();
    expect(mockCall).toHaveBeenCalledWith('session.list');
  });

  it('createSession calls session.create with repoPath', async () => {
    mockCall.mockResolvedValueOnce({ id: '1', repoPath: '/repo', status: 'running', createdAt: '', updatedAt: '' });
    await api.createSession('/repo');
    expect(mockCall).toHaveBeenCalledWith('session.create', { repoPath: '/repo' });
  });

  it('pauseSession calls session.pause with sessionId', async () => {
    mockCall.mockResolvedValueOnce(undefined);
    await api.pauseSession('abc');
    expect(mockCall).toHaveBeenCalledWith('session.pause', { sessionId: 'abc' });
  });

  it('sendMessage calls message.send with sessionId and content', async () => {
    mockCall.mockResolvedValueOnce({ id: 'm1', sessionId: 'abc', role: 'user', content: 'hi', metadata: {}, createdAt: '' });
    await api.sendMessage('abc', 'hi');
    expect(mockCall).toHaveBeenCalledWith('message.send', { sessionId: 'abc', content: 'hi' });
  });

  it('approveToolCall calls toolCall.approve with toolCallId', async () => {
    mockCall.mockResolvedValueOnce(undefined);
    await api.approveToolCall('tc1');
    expect(mockCall).toHaveBeenCalledWith('toolCall.approve', { toolCallId: 'tc1' });
  });

  it('rejectToolCall calls toolCall.reject with toolCallId', async () => {
    mockCall.mockResolvedValueOnce(undefined);
    await api.rejectToolCall('tc2');
    expect(mockCall).toHaveBeenCalledWith('toolCall.reject', { toolCallId: 'tc2' });
  });

  it('updateTaskStatus passes notes when provided', async () => {
    mockCall.mockResolvedValueOnce(undefined);
    await api.updateTaskStatus('t1', 'done', 'all good');
    expect(mockCall).toHaveBeenCalledWith('task.updateStatus', {
      taskId: 't1',
      status: 'done',
      notes: 'all good',
    });
  });
});
