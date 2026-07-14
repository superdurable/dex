import type {
  ActiveStepExecutionLive,
  ConditionNode,
  HistoryEvent,
  WaitForConditionTree,
} from '../../api/_grpc/mappers';
import { graphHistoryEvents } from '../../api/_grpc/mappers';
import { STOP_DECISION_FAIL } from '../../components/utils';

// One graph node per unique step_exe_id (plus two virtual nodes for the run
// start/end). Status is derived from a TWO-source merge:
//   1. History events (StepWaitForCompleted, StepExecuteCompleted, …) —
//      retired/durable view of every step that ever ran.
//   2. The live active_step_executions map from RunsService.GetRun — used
//      to overlay any in-flight step that has NOT YET written a history
//      event. The classic case: a step in INVOKING_EXECUTE running a long
//      sleep — without this overlay it would be invisible on the graph
//      until Execute returns.
// History always wins on conflict (a Completed/Cancelled node never gets
// downgraded back to Running). The waitFor->execute transition is captured
// as `hadWaitFor=true` on the same node so users see it without an extra
// graph node.

export const START_NODE_ID = '__start';
export const END_NODE_ID = '__end';

export type StepStatus = 'Pending' | 'Waiting' | 'Running' | 'Retrying' | 'Completed' | 'Cancelled' | 'Virtual';

// pb.StepExecutionStatus values (mirrors dex.proto enum). Only the
// INVOKING_EXECUTE value is the load-bearing one for the graph overlay
// (WAITING_FOR_CONDITION steps already get a node from
// StepWaitForCompleted; INVOKING_WAIT_FOR is brief enough to ignore).
const ACTIVE_STATUS_INVOKING_EXECUTE = 2;

export interface StepNodeData {
  stepExeId: string;
  stepId: string;
  status: StepStatus;
  // Only set when status='Completed'. Mirrors pb.StopDecision.
  stopDecision?: number;
  hadWaitFor: boolean;
  occurredAtMs: number;
  // True for __start / __end. Renderer keys off this for a simpler body.
  isVirtual: boolean;
  // For RunStart: flow_type / task_list_name. For RunStop: runStatus.
  meta?: Record<string, unknown>;
  // The most recent WaitForCondition tree the worker reported for this step
  // (from StepWaitForCompleted). When status='Waiting' this is the live
  // condition the worker is currently blocked on; when status='Completed'
  // it's the last condition that was evaluated. Drives the graph node badge
  // and the side-panel WaitForConditionPanel.
  waitFor?: WaitForConditionTree;
  // The most recent condition_results echoed back on StepExecuteCompleted
  // (only present when the step was previously waiting). Tells the user
  // which conditions actually fired/satisfied.
  conditionResults?: ConditionNode[];
  // Set when status='Cancelled'. The exe_id of the step whose
  // StepDecision.WithCancelingSiblingStepExecution caused this step to be
  // deleted from ActiveStepExecutions. Surfaced as a tooltip on the
  // graph node so the user can trace back to the canceller's history
  // event without leaving the graph view.
  cancelledByExeId?: string;
  retryState?: import('../../api/_grpc/mappers').StepRetryStateLive | null;
  waitForMethod?: import('../../api/_grpc/mappers').StepMethodReportLive | null;
  executeMethod?: import('../../api/_grpc/mappers').StepMethodReportLive | null;
  // True when a method failed after retries but the run continued via next_steps.
  proceededAfterFailure?: boolean;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}

export interface GraphResult {
  nodes: StepNodeData[];
  edges: GraphEdge[];
}

// stepIdFromExeId strips the trailing -N counter the engine appends to a
// step's name to form a step_exe_id (e.g. "main.sequentialLoopStep-3" ->
// "main.sequentialLoopStep"). Falls back to the input if no counter.
export function stepIdFromExeId(stepExeId: string): string {
  const idx = stepExeId.lastIndexOf('-');
  if (idx <= 0) return stepExeId;
  const tail = stepExeId.slice(idx + 1);
  if (/^\d+$/.test(tail)) return stepExeId.slice(0, idx);
  return stepExeId;
}

export function buildGraph(
  events: HistoryEvent[],
  activeStepExecutions: Record<string, ActiveStepExecutionLive> = {},
): GraphResult {
  const graphEvents = graphHistoryEvents(events);
  const stepNodes = new Map<string, StepNodeData>();
  const edges: GraphEdge[] = [];
  const edgeKeys = new Set<string>();
  let runStartMeta: Record<string, unknown> | null = null;
  let runEndMeta: Record<string, unknown> | null = null;
  let runStartedAtMs = 0;
  let runEndedAtMs = 0;

  const addEdge = (source: string, target: string) => {
    if (source === target) return;
    const id = `${source}->${target}`;
    if (edgeKeys.has(id)) return;
    edgeKeys.add(id);
    edges.push({ id, source, target });
  };

  const upsertStep = (
    stepExeId: string,
    patch: Partial<StepNodeData> & { occurredAtMs: number },
  ): StepNodeData => {
    let node = stepNodes.get(stepExeId);
    if (!node) {
      node = {
        stepExeId,
        stepId: stepIdFromExeId(stepExeId),
        status: 'Pending',
        hadWaitFor: false,
        occurredAtMs: patch.occurredAtMs,
        isVirtual: false,
      };
      stepNodes.set(stepExeId, node);
    }
    Object.assign(node, patch);
    return node;
  };

  for (const e of graphEvents) {
    switch (e.payload.type) {
      case 'RunStart': {
        const data = e.payload.data as Record<string, unknown>;
        const startingSteps = (data.starting_steps as unknown[] | undefined) ?? [];
        runStartMeta = {
          flow_type: data.flow_type ?? '',
          task_list_name: data.task_list_name ?? '',
          starting_steps: startingSteps,
        };
        runStartedAtMs = e.occurredAtMs;
        // Pre-seed the __start → starting-step edges from the registered
        // starting_steps. Without this, a starting step that is still in
        // WAITING_FOR_CONDITION (no StepExecuteCompleted yet) renders
        // floating with no incoming edge — see "anyOfRaceFlow" in
        // dev-stack.sh which spends most of its life waiting on a
        // notify-channel publish before ever completing Execute.
        //
        // The engine deterministically assigns step_exe_id "<step_id>-1"
        // to the first invocation of each starting step (counter starts at
        // 1; see stepExeIDFromCounter in run_engine.go).
        for (const ns of startingSteps) {
          const obj = ns as Record<string, unknown>;
          const sid = String(obj.step_id ?? '');
          if (!sid) continue;
          addEdge(START_NODE_ID, `${sid}-1`);
        }
        break;
      }
      case 'RunStop': {
        runEndMeta = { runStatus: e.payload.data.runStatus ?? null };
        runEndedAtMs = e.occurredAtMs;
        break;
      }
      case 'StepWaitForCompleted': {
        const data = e.payload.data;
        const sid = String(data.step_exe_id ?? '');
        if (!sid) break;
        const existing = stepNodes.get(sid);
        const waitTree = data.wait_for_condition as WaitForConditionTree | null;
        const waitForMethod = data.wait_for_method as import('../../api/_grpc/mappers').StepMethodReportLive | null | undefined;
        const nextSteps = (data.next_steps as unknown[] | undefined) ?? [];
        const methodFailed = waitForMethod?.outcome === 'Failed';
        const proceeded = methodFailed && nextSteps.length > 0;
        upsertStep(sid, {
          hadWaitFor: true,
          status: methodFailed ? 'Completed' : existing?.status === 'Completed' ? 'Completed' : 'Waiting',
          stopDecision: proceeded ? 0 : methodFailed ? STOP_DECISION_FAIL : existing?.stopDecision,
          occurredAtMs: e.occurredAtMs,
          waitFor: waitTree ?? existing?.waitFor,
          waitForMethod: waitForMethod ?? null,
          proceededAfterFailure: proceeded,
        });
        const fromId = String(data.from_step_exe_id ?? '');
        if (fromId === '') {
          addEdge(START_NODE_ID, sid);
        } else {
          if (!stepNodes.has(fromId)) {
            upsertStep(fromId, { occurredAtMs: e.occurredAtMs });
          }
          addEdge(fromId, sid);
        }
        // Proceed edges: handler steps spawned after retry exhaustion.
        for (const ns of nextSteps) {
          const obj = ns as Record<string, unknown>;
          const handlerStepId = String(obj.step_id ?? '');
          if (!handlerStepId) continue;
          addEdge(sid, `${handlerStepId}-1`);
        }
        break;
      }
      case 'StepExecuteCompleted': {
        const data = e.payload.data;
        const sid = String(data.step_exe_id ?? '');
        if (!sid) break;
        const fromId = String(data.from_step_exe_id ?? '');
        const stop = typeof data.stop_decision === 'number' ? data.stop_decision : 0;
        const nextSteps = (data.next_steps as unknown[] | undefined) ?? [];
        const condResults = data.condition_results as ConditionNode[] | undefined;
        const executeMethod = data.execute_method as import('../../api/_grpc/mappers').StepMethodReportLive | null | undefined;
        const executeFailed = executeMethod?.outcome === 'Failed';
        const proceeded = executeFailed && stop === 0 && nextSteps.length > 0;
        upsertStep(sid, {
          status: 'Completed',
          stopDecision: stop,
          occurredAtMs: e.occurredAtMs,
          conditionResults: condResults && condResults.length > 0 ? condResults : undefined,
          executeMethod: executeMethod ?? null,
          proceededAfterFailure: proceeded,
        });
        if (fromId === '') {
          addEdge(START_NODE_ID, sid);
        } else {
          if (!stepNodes.has(fromId)) {
            upsertStep(fromId, { occurredAtMs: e.occurredAtMs });
          }
          addEdge(fromId, sid);
        }
        for (const ns of nextSteps) {
          const obj = ns as Record<string, unknown>;
          const handlerStepId = String(obj.step_id ?? '');
          if (!handlerStepId) continue;
          addEdge(sid, `${handlerStepId}-1`);
        }
        // Apply sibling cancellations carried by THIS event. Each entry is
        // a step_exe_id the engine just deleted from ActiveStepExecutions
        // because of the SDK's StepDecision.WithCancelingSiblingStepExecution.
        // We don't downgrade nodes that already reached Completed (the
        // engine ignores cancel IDs for absent steps too — see
        // tryProcessStepExecuteCompleted), and we attach the canceller's
        // exe_id so the StepNode can render a "cancelled by ..." tooltip.
        //
        // Edge wiring for cancelled nodes: the victim and the canceller
        // share the same from_step_exe_id (same-parent rule in the SDK).
        // For victims that never wrote StepWaitForCompleted (NoWaitFor
        // steps dispatched directly into INVOKING_EXECUTE, e.g.
        // slowLLMStep), the cancel list is the ONLY signal of their
        // existence — and without a parent edge they'd float with no
        // connection to the graph. We use the canceller's own
        // from_step_exe_id as the shared parent to draw the edge.
        const cancelled = data.canceled_step_executions as string[] | undefined;
        if (Array.isArray(cancelled) && cancelled.length > 0) {
          for (const cid of cancelled) {
            if (!cid) continue;
            const existing = stepNodes.get(cid);
            if (existing?.status === 'Completed') continue;
            upsertStep(cid, {
              status: 'Cancelled',
              cancelledByExeId: sid,
              occurredAtMs: e.occurredAtMs,
            });
            // Draw the parent edge if the victim node doesn't already
            // have one. The canceller's from_step_exe_id is the shared
            // parent (same-parent rule: siblings share fromStepExeId).
            if (!edgeKeys.has(`${fromId}->${cid}`) && !edgeKeys.has(`${START_NODE_ID}->${cid}`)) {
              if (fromId === '') {
                addEdge(START_NODE_ID, cid);
              } else {
                if (!stepNodes.has(fromId)) {
                  upsertStep(fromId, { occurredAtMs: e.occurredAtMs });
                }
                addEdge(fromId, cid);
              }
            }
          }
        }
        break;
      }
      // ChannelPublish and Unknown are intentionally ignored in the graph.
      default:
        break;
    }
  }

  // Live overlay: every step the engine considers active that has NOT
  // already been retired by a history event in the loop above gets a
  // synthetic node so the graph reflects what's actually in flight RIGHT
  // NOW. The most important case: a step in INVOKING_EXECUTE running a
  // long sleep. Without this overlay the user sees nothing for it until
  // Execute returns (could be 60s for slowLLMStep, 24h for a WaitFor on a
  // multi-day timer that the engine has resumed into Execute, etc).
  //
  // Conflict policy:
  //   - Completed / Cancelled history wins (never downgrade — those are
  //     terminal/durable for the step's lifecycle).
  //   - Waiting (from StepWaitForCompleted) wins over the active map's
  //     WAITING_FOR_CONDITION because the history version carries the
  //     full waitFor tree authored by the worker.
  //   - INVOKING_EXECUTE only paints a node if there isn't one yet (no
  //     history event referenced this exe-id) — that's the gap we're
  //     filling. Engine writes from_step_exe_id on every active entry so
  //     we can also draw the parent edge.
  const liveOverlayOccurredAtMs = Date.now();
  for (const [exeId, live] of Object.entries(activeStepExecutions)) {
    if (!exeId) continue;
    const existing = stepNodes.get(exeId);
    if (existing && (existing.status === 'Completed' || existing.status === 'Cancelled')) {
      continue;
    }
    if (live.status !== ACTIVE_STATUS_INVOKING_EXECUTE) {
      // INVOKING_WAIT_FOR is too brief to bother with; WAITING_FOR_CONDITION
      // is already handled by the history-event branch above (and carries
      // a richer waitFor tree). Skip both.
      continue;
    }
    if (existing && existing.status === 'Waiting') {
      // Tricky transient: the engine just promoted this step from
      // WAITING_FOR_CONDITION to INVOKING_EXECUTE but the corresponding
      // StepExecuteCompleted hasn't landed yet. Keep the Waiting badge —
      // it's accurate-enough until the next poll, and downgrading would
      // lose the waitFor tree we already painted.
      continue;
    }
    const retryState = live.executeRetryState ?? live.waitForRetryState;
    const isRetrying =
      retryState != null &&
      (retryState.currentAttempts > 1 || retryState.lastError != null || retryState.lastErrorStackTrace != null);
    upsertStep(exeId, {
      status: isRetrying ? 'Retrying' : 'Running',
      occurredAtMs: existing?.occurredAtMs ?? liveOverlayOccurredAtMs,
      hadWaitFor: existing?.hadWaitFor ?? false,
      retryState,
    });
    // Provenance edge from the parent. Empty fromStepExeId means this is
    // a starting step — anchor it to __start so it doesn't float.
    if (live.fromStepExeId === '') {
      addEdge(START_NODE_ID, exeId);
    } else {
      if (!stepNodes.has(live.fromStepExeId)) {
        upsertStep(live.fromStepExeId, { occurredAtMs: liveOverlayOccurredAtMs });
      }
      addEdge(live.fromStepExeId, exeId);
    }
  }

  // Virtual __start node when we ever saw a RunStart.
  const nodes: StepNodeData[] = [];
  if (runStartMeta) {
    nodes.push({
      stepExeId: START_NODE_ID,
      stepId: 'RunStart',
      status: 'Virtual',
      hadWaitFor: false,
      occurredAtMs: runStartedAtMs,
      isVirtual: true,
      meta: runStartMeta,
    });
  }
  for (const n of stepNodes.values()) nodes.push(n);

  // Virtual __end node + edges from leaf steps when we ever saw a RunStop.
  if (runEndMeta) {
    nodes.push({
      stepExeId: END_NODE_ID,
      stepId: 'RunStop',
      status: 'Virtual',
      hadWaitFor: false,
      occurredAtMs: runEndedAtMs,
      isVirtual: true,
      meta: runEndMeta,
    });
    const sourcesWithOutgoing = new Set(edges.map((e) => e.source));
    for (const n of stepNodes.values()) {
      if (!sourcesWithOutgoing.has(n.stepExeId)) {
        addEdge(n.stepExeId, END_NODE_ID);
      }
    }
  }

  return { nodes, edges };
}
