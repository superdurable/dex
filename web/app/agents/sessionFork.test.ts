// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment node

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  SESSION_FORK_DELETED_ATTRIBUTES,
  sessionForkPayload,
  sessionForkTaskQueue,
  sessionTaskQueue,
  suggestedForkSessionId,
} from './sessionFork';

interface TaskQueueCase {
  name: string;
  session_id: string;
  task_queue: string;
}

function taskQueueCases(): TaskQueueCase[] {
  const path = new URL('../../../agents/conformance/cli_session_task_queues.json', import.meta.url);
  return (JSON.parse(readFileSync(path, 'utf8')) as { task_queue_cases: TaskQueueCase[] }).task_queue_cases;
}

function sessionGraphAttributeNames(): string[] {
  const path = new URL('../../../agents/conformance/session_graph.json', import.meta.url);
  const graph = JSON.parse(readFileSync(path, 'utf8')) as { nodes: Array<{ kind: string; name: string }> };
  return graph.nodes.filter((node) => node.kind === 'attribute').map((node) => node.name);
}

describe('sessionTaskQueue', () => {
  it('matches every conformance vector', async () => {
    const cases = taskQueueCases();
    expect(cases.length).toBeGreaterThan(0);
    for (const testCase of cases) {
      expect(await sessionTaskQueue(testCase.session_id), testCase.name).toBe(testCase.task_queue);
    }
  });
});

describe('suggestedForkSessionId', () => {
  const now = new Date('2026-11-15T09:08:07.654Z');

  it('keeps the harness name of a CLI session ID', () => {
    expect(suggestedForkSessionId('builtin-20261113-203123-3efeb4', now, 'a1b2c3')).toBe('builtin-20261115-090807-a1b2c3');
    expect(suggestedForkSessionId('claude-sdk-20261114-090000-a1b2c3', now, '0f0f0f')).toBe('claude-sdk-20261115-090807-0f0f0f');
  });

  it('uses another ID whole as the prefix', () => {
    expect(suggestedForkSessionId('demo-agent-session-1', now, 'a1b2c3')).toBe('demo-agent-session-1-20261115-090807-a1b2c3');
  });
});

describe('sessionForkPayload', () => {
  it('deletes only Attributes the session graph declares', () => {
    const declared = sessionGraphAttributeNames();
    for (const name of SESSION_FORK_DELETED_ATTRIBUTES) expect(declared).toContain(name);
  });

  it('moves a fork of a session on its own task queue to the new session task queue', async () => {
    const [source, vector] = taskQueueCases();
    expect(await sessionForkPayload({
      sourceSessionId: source.session_id,
      sourceRunId: 'run-1',
      sourceTaskQueue: source.task_queue,
      sessionId: vector.session_id,
      point: { kind: 'event', eventId: 14 },
      reason: 'try another answer',
      requestId: 'request-1',
    })).toEqual({
      sourceFlowId: source.session_id,
      sourceRunId: 'run-1',
      flowId: vector.session_id,
      requestId: 'request-1',
      pointType: 6,
      eventId: '14',
      taskQueue: vector.task_queue,
      deleteAttributeKeys: ['SessionTerminalLease', 'SessionLastTerminalEnvironment'],
      idReusePolicy: 3,
      reason: 'try another answer',
    });
  });

  it('lets a fork of a session on another task queue inherit it, and still deletes the lease', async () => {
    const [source, other] = taskQueueCases();
    // The default task queue, a shared queue, and another session's own queue are not the source's own.
    for (const sourceTaskQueue of ['', 'agents', other.task_queue]) {
      const payload = await sessionForkPayload({
        sourceSessionId: source.session_id, sourceRunId: 'run-1', sourceTaskQueue,
        sessionId: 'x', point: { kind: 'current' }, reason: 'r', requestId: 'q',
      });
      expect(payload, sourceTaskQueue).toMatchObject({
        pointType: 1, deleteAttributeKeys: ['SessionTerminalLease', 'SessionLastTerminalEnvironment'],
      });
      expect(payload, sourceTaskQueue).not.toHaveProperty('taskQueue');
      expect(payload, sourceTaskQueue).not.toHaveProperty('eventId');
    }
  });

  it('derives the fork task queue for every conformance vector from a source on its own queue', async () => {
    const cases = taskQueueCases();
    for (const [index, vector] of cases.entries()) {
      const source = cases[(index + 1) % cases.length];
      expect(await sessionForkTaskQueue(source.session_id, source.task_queue, vector.session_id), vector.name)
        .toBe(vector.task_queue);
      expect(await sessionForkTaskQueue(source.session_id, '', vector.session_id), vector.name).toBeNull();
    }
  });
});
