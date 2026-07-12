import { test, expect } from '@playwright/test';
import {
  buildGraph,
  stepIdFromExeId,
  START_NODE_ID,
  END_NODE_ID,
} from '../app/flow/show/buildGraph';
import type { HistoryEvent } from '../app/api/_grpc/mappers';

// Pure unit-style coverage of the graph derivation. No browser needed; the
// Playwright worker just runs the TS in Node.

test.describe('buildGraph', () => {
  test('stepIdFromExeId strips trailing -N counter', () => {
    expect(stepIdFromExeId('main.sequentialLoopStep-1')).toBe('main.sequentialLoopStep');
    expect(stepIdFromExeId('main.sequentialLoopStep-42')).toBe('main.sequentialLoopStep');
    expect(stepIdFromExeId('foo')).toBe('foo');
    expect(stepIdFromExeId('foo-bar')).toBe('foo-bar');
  });

  test('builds start -> A -> B -> end with hadWaitFor=true on A', () => {
    const events: HistoryEvent[] = [
      {
        id: 1,
        occurredAtMs: 100,
        workerId: '',
        payload: {
          type: 'RunStart',
          data: { flow_type: 'main.testFlow', task_list_name: 'g', starting_steps: [] },
        },
      },
      {
        id: 2,
        occurredAtMs: 110,
        workerId: '',
        payload: {
          type: 'StepWaitForCompleted',
          data: { step_exe_id: 'A-1' },
        },
      },
      {
        id: 3,
        occurredAtMs: 120,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'A-1', from_step_exe_id: '', stop_decision: 0 },
        },
      },
      {
        id: 4,
        occurredAtMs: 130,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'B-1', from_step_exe_id: 'A-1', stop_decision: 1 },
        },
      },
      {
        id: 5,
        occurredAtMs: 140,
        workerId: '',
        payload: {
          type: 'RunStop',
          data: { runStatus: 4 },
        },
      },
    ];

    const { nodes, edges } = buildGraph(events);

    const ids = nodes.map((n) => n.stepExeId);
    expect(ids).toEqual([START_NODE_ID, 'A-1', 'B-1', END_NODE_ID]);

    const edgeIds = edges.map((e) => `${e.source}->${e.target}`);
    expect(edgeIds).toEqual([`${START_NODE_ID}->A-1`, 'A-1->B-1', `B-1->${END_NODE_ID}`]);

    const a = nodes.find((n) => n.stepExeId === 'A-1')!;
    expect(a.hadWaitFor).toBe(true);
    expect(a.status).toBe('Completed');
    expect(a.stopDecision).toBe(0);
    expect(a.stepId).toBe('A');

    const b = nodes.find((n) => n.stepExeId === 'B-1')!;
    expect(b.hadWaitFor).toBe(false);
    expect(b.status).toBe('Completed');
    expect(b.stopDecision).toBe(1);
  });

  test('Waiting status when only StepWaitForCompleted seen so far', () => {
    const events: HistoryEvent[] = [
      {
        id: 1,
        occurredAtMs: 100,
        workerId: '',
        payload: { type: 'RunStart', data: {} },
      },
      {
        id: 2,
        occurredAtMs: 110,
        workerId: '',
        payload: { type: 'StepWaitForCompleted', data: { step_exe_id: 'A-1' } },
      },
    ];
    const { nodes, edges } = buildGraph(events);
    const a = nodes.find((n) => n.stepExeId === 'A-1')!;
    expect(a.status).toBe('Waiting');
    expect(a.hadWaitFor).toBe(true);
    // Empty from_step_exe_id anchors the waiting node to __start so it
    // doesn't float before any StepExecuteCompleted lands.
    expect(edges).toEqual([{ id: `${START_NODE_ID}->A-1`, source: START_NODE_ID, target: 'A-1' }]);
  });

  test('parallel fan-out: one parent -> multiple children', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'init-1', from_step_exe_id: '', stop_decision: 0 },
        },
      },
      {
        id: 3,
        occurredAtMs: 3,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'worker-1', from_step_exe_id: 'init-1', stop_decision: 3 },
        },
      },
      {
        id: 4,
        occurredAtMs: 4,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'worker-2', from_step_exe_id: 'init-1', stop_decision: 3 },
        },
      },
      { id: 5, occurredAtMs: 5, workerId: '', payload: { type: 'RunStop', data: { runStatus: 4 } } },
    ];
    const { edges } = buildGraph(events);
    const set = new Set(edges.map((e) => `${e.source}->${e.target}`));
    expect(set.has(`${START_NODE_ID}->init-1`)).toBe(true);
    expect(set.has('init-1->worker-1')).toBe(true);
    expect(set.has('init-1->worker-2')).toBe(true);
    // Both leaves connect to __end.
    expect(set.has(`worker-1->${END_NODE_ID}`)).toBe(true);
    expect(set.has(`worker-2->${END_NODE_ID}`)).toBe(true);
  });

  test('returns empty graph when there are no events', () => {
    expect(buildGraph([])).toEqual({ nodes: [], edges: [] });
  });

  // CancelSiblingStepExecution coverage. The cancelling step's
  // StepExecuteCompleted event carries a `canceled_step_executions`
  // list; buildGraph must mark each target node as 'Cancelled' and
  // attach the canceller's exe_id for the StepNode tooltip. Already-
  // Completed targets must NOT be downgraded (the engine ignores
  // cancel IDs for absent / done steps).
  test('marks canceled_step_executions targets as Cancelled with canceller link', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'parent-1', from_step_exe_id: '', stop_decision: 0 },
        },
      },
      {
        id: 3,
        occurredAtMs: 3,
        workerId: '',
        payload: {
          type: 'StepWaitForCompleted',
          data: { step_exe_id: 'sibling-1', from_step_exe_id: 'parent-1' },
        },
      },
      {
        id: 4,
        occurredAtMs: 4,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: {
            step_exe_id: 'caller-1',
            from_step_exe_id: 'parent-1',
            stop_decision: 3,
            canceled_step_executions: ['sibling-1'],
          },
        },
      },
      { id: 5, occurredAtMs: 5, workerId: '', payload: { type: 'RunStop', data: { runStatus: 4 } } },
    ];

    const { nodes } = buildGraph(events);
    const sibling = nodes.find((n) => n.stepExeId === 'sibling-1')!;
    expect(sibling.status).toBe('Cancelled');
    expect(sibling.cancelledByExeId).toBe('caller-1');
    // hadWaitFor must persist from the earlier StepWaitForCompleted —
    // upsertStep merges patches rather than overwriting.
    expect(sibling.hadWaitFor).toBe(true);

    const caller = nodes.find((n) => n.stepExeId === 'caller-1')!;
    expect(caller.status).toBe('Completed');
  });

  test('Cancelled never downgrades an already-Completed target', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'sibling-1', from_step_exe_id: 'parent-1', stop_decision: 1 },
        },
      },
      {
        id: 3,
        occurredAtMs: 3,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: {
            step_exe_id: 'caller-1',
            from_step_exe_id: 'parent-1',
            stop_decision: 3,
            // sibling-1 already completed before this RPC was committed —
            // engine treats cancel as a no-op; graph must not regress
            // its visible status.
            canceled_step_executions: ['sibling-1'],
          },
        },
      },
    ];
    const { nodes } = buildGraph(events);
    const sibling = nodes.find((n) => n.stepExeId === 'sibling-1')!;
    expect(sibling.status).toBe('Completed');
    expect(sibling.cancelledByExeId).toBeUndefined();
  });

  // ---- active_step_executions overlay ----------------------------------
  // These tests pin the live-overlay path that surfaces in-flight
  // INVOKING_EXECUTE steps as "Running" nodes BEFORE their
  // StepExecuteCompleted history event lands. Without this overlay a
  // long Sleep inside Execute would render invisibly until completion
  // (e.g. slowLLMStep in the multi-agent benchmark sleeps 60s — the
  // graph would have a 60s gap with no visible node).

  test('active overlay: INVOKING_EXECUTE without history event renders Running with parent edge', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'parent-1', from_step_exe_id: '', stop_decision: 0 },
        },
      },
    ];
    const active = {
      'slow-1': {
        status: 2, // INVOKING_EXECUTE
        fromStepExeId: 'parent-1',
        waitForCondition: null,
        conditionResults: [],
      },
    };
    const { nodes, edges } = buildGraph(events, active);
    const slow = nodes.find((n) => n.stepExeId === 'slow-1')!;
    expect(slow).toBeDefined();
    expect(slow.status).toBe('Running');
    expect(edges.some((e) => e.source === 'parent-1' && e.target === 'slow-1')).toBe(true);
  });

  test('active overlay: Completed history wins over INVOKING_EXECUTE (no downgrade)', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: { step_exe_id: 'fast-1', from_step_exe_id: '', stop_decision: 3 },
        },
      },
    ];
    // Hypothetical race where active map still references a step that
    // already wrote StepExecuteCompleted (transient between worker
    // commit and engine cleanup). Overlay must not regress the node.
    const active = {
      'fast-1': { status: 2, fromStepExeId: '', waitForCondition: null, conditionResults: [] },
    };
    const { nodes } = buildGraph(events, active);
    const fast = nodes.find((n) => n.stepExeId === 'fast-1')!;
    expect(fast.status).toBe('Completed');
  });

  test('active overlay: Waiting (with rich tree) wins over WAITING_FOR_CONDITION active entry', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepWaitForCompleted',
          data: {
            step_exe_id: 'waiter-1',
            from_step_exe_id: '',
            wait_for_condition: { type: 'AnyOf', conditions: [] },
          },
        },
      },
    ];
    const active = {
      'waiter-1': {
        status: 1, // WAITING_FOR_CONDITION
        fromStepExeId: '',
        waitForCondition: null,
        conditionResults: [],
      },
    };
    const { nodes } = buildGraph(events, active);
    const w = nodes.find((n) => n.stepExeId === 'waiter-1')!;
    // Must remain Waiting (with the history-derived waitFor tree intact),
    // NOT downgraded to Running.
    expect(w.status).toBe('Waiting');
    expect(w.waitFor).toEqual({ type: 'AnyOf', conditions: [] });
  });

  test('active overlay: Cancelled history wins over INVOKING_EXECUTE (no downgrade)', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: {
            step_exe_id: 'caller-1',
            from_step_exe_id: '',
            stop_decision: 3,
            canceled_step_executions: ['victim-1'],
          },
        },
      },
    ];
    const active = {
      'victim-1': { status: 2, fromStepExeId: '', waitForCondition: null, conditionResults: [] },
    };
    const { nodes } = buildGraph(events, active);
    const v = nodes.find((n) => n.stepExeId === 'victim-1')!;
    expect(v.status).toBe('Cancelled');
  });

  test('active overlay: starting Running step anchors to __start when fromStepExeId is empty', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
    ];
    const active = {
      'starter-1': {
        status: 2,
        fromStepExeId: '',
        waitForCondition: null,
        conditionResults: [],
      },
    };
    const { nodes, edges } = buildGraph(events, active);
    const s = nodes.find((n) => n.stepExeId === 'starter-1')!;
    expect(s.status).toBe('Running');
    expect(edges.some((e) => e.source === START_NODE_ID && e.target === 'starter-1')).toBe(true);
  });

  test('active overlay: INVOKING_WAIT_FOR (status=0) is skipped (too brief to render)', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
    ];
    const active = {
      'invoking-wait-1': {
        status: 0, // INVOKING_WAIT_FOR — transient, skip.
        fromStepExeId: '',
        waitForCondition: null,
        conditionResults: [],
      },
    };
    const { nodes } = buildGraph(events, active);
    expect(nodes.find((n) => n.stepExeId === 'invoking-wait-1')).toBeUndefined();
  });

  test('cancellation creates a node for a target that never had its own event (NoWaitFor step)', () => {
    // Edge case: the canceller fires before the target step ever produces
    // a StepWaitForCompleted (e.g. cancelled in INVOKING_EXECUTE before
    // any history event landed for it). The cancellation list is then
    // the only signal of its existence; the graph must still surface
    // the node so the user can see what was killed.
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: {
            step_exe_id: 'caller-1',
            from_step_exe_id: 'parent-1',
            stop_decision: 3,
            canceled_step_executions: ['victim-1'],
          },
        },
      },
    ];
    const { nodes, edges } = buildGraph(events);
    const victim = nodes.find((n) => n.stepExeId === 'victim-1')!;
    expect(victim).toBeDefined();
    expect(victim.status).toBe('Cancelled');
    expect(victim.cancelledByExeId).toBe('caller-1');
    expect(victim.hadWaitFor).toBe(false);
    // The victim must have a parent edge from the shared parent
    // (same-parent rule: canceller's from_step_exe_id = victim's
    // from_step_exe_id). Without this edge the Cancelled node would
    // float disconnected from the rest of the graph.
    expect(
      edges.some((e) => e.source === 'parent-1' && e.target === 'victim-1'),
    ).toBe(true);
  });

  test('failed execute with next_steps marks proceeded and draws handler edge', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepExecuteCompleted',
          data: {
            step_exe_id: 'failing-1',
            stop_decision: 0,
            next_steps: [{ step_id: 'handler' }],
            execute_method: { outcome: 'Failed', attemptCount: 2, error: 'boom' },
          },
        },
      },
    ];
    const { nodes, edges } = buildGraph(events);
    const failing = nodes.find((n) => n.stepExeId === 'failing-1')!;
    expect(failing.proceededAfterFailure).toBe(true);
    expect(failing.stopDecision).toBe(0);
    expect(edges.some((e) => e.source === 'failing-1' && e.target === 'handler-1')).toBe(true);
  });

  test('failed wait_for with next_steps marks proceeded and sets stop NONE', () => {
    const events: HistoryEvent[] = [
      { id: 1, occurredAtMs: 1, workerId: '', payload: { type: 'RunStart', data: {} } },
      {
        id: 2,
        occurredAtMs: 2,
        workerId: '',
        payload: {
          type: 'StepWaitForCompleted',
          data: {
            step_exe_id: 'failing-1',
            next_steps: [{ step_id: 'handler' }],
            wait_for_method: { outcome: 'Failed', attemptCount: 2, error: 'boom' },
          },
        },
      },
    ];
    const { nodes, edges } = buildGraph(events);
    const failing = nodes.find((n) => n.stepExeId === 'failing-1')!;
    expect(failing.proceededAfterFailure).toBe(true);
    expect(failing.stopDecision).toBe(0);
    expect(edges.some((e) => e.source === 'failing-1' && e.target === 'handler-1')).toBe(true);
  });
});
