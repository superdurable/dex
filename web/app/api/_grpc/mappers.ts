import type {
  RunSummaryWire,
  GetRunResponseWire,
  HistoryEventWire,
} from './client';

// JSON shapes the React layer consumes. We deliberately rename snake_case
// proto fields to camelCase here so the UI never has to think about the wire
// format, and we flatten the HistoryEvent oneof to a discriminated union.

export interface RunSummary {
  runId: string;
  namespace: string;
  flowType: string;
  taskListName: string;
  status: number;
  startTimeMs: number;
  updatedAtMs: number;
}

// Typed WaitFor condition tree (mirrors pb.WaitForCondition + ConditionResult).
// Surfaced both inside historical StepWaitForCompleted / StepExecuteCompleted
// payloads AND on live ActiveStepExecution objects (see GET /api/runs/get).
export type ConditionNode =
  | { kind: 'Timer'; fireAtUnixMs: number; fired?: boolean }
  | { kind: 'Channel'; channelName: string; min: number; max: number; satisfied?: boolean; consumedCount?: number };

export type WaitForConditionTree = { type: 'AnyOf' | 'AllOf'; conditions: ConditionNode[] };

export interface StepUnblockedEntry {
  step_exe_id: string;
  condition_results: ConditionNode[];
}

export type HistoryEventPayload =
  | { type: 'RunStart'; data: unknown }
  | { type: 'RunStop'; data: { runStatus?: number; reason?: string } & Record<string, unknown> }
  | { type: 'StepExecuteCompleted'; data: Record<string, unknown> }
  | { type: 'StepWaitForCompleted'; data: Record<string, unknown> }
  | { type: 'ChannelPublish'; data: Record<string, unknown> }
  | {
      type: 'StepsUnblocked';
      data: {
        worker_request_counter: number;
        steps_unblocked: StepUnblockedEntry[];
        has_snapshot?: boolean;
      };
    }
  | {
      type: 'RunFork';
      data: {
        fork_to_event_id: number;
        reason?: string;
      };
    }
  | { type: 'Unknown'; data: Record<string, unknown> };

export interface HistoryEvent {
  id: number;
  occurredAtMs: number;
  workerId: string;
  payload: HistoryEventPayload;
}

// Live (current) view for a step the engine considers active. Returned
// by GET /api/runs/get under activeStepExecutions[stepExeId]. Drives two
// independent UI consumers:
//   - The graph's "Running" overlay (buildGraph.ts): a step with
//     status=INVOKING_EXECUTE has no history event between dispatch and
//     completion, so without this field a long-running Execute would
//     render invisibly until it finishes. fromStepExeId is the parent
//     edge; status=2 → 'Running' badge.
//   - The live-state panel: surfaces the WaitFor tree + condition results
//     so the user can see what a WAITING step is currently blocked on.
export interface ActiveStepExecutionLive {
  // pb.StepExecutionStatus: 0=INVOKING_WAIT_FOR, 1=WAITING_FOR_CONDITION,
  // 2=INVOKING_EXECUTE. The graph layer maps these to its own StepStatus.
  status: number;
  // Provenance edge used by the graph to attach a live Running node to
  // its parent. Empty string for starting steps.
  fromStepExeId: string;
  waitForCondition: WaitForConditionTree | null;
  conditionResults: ConditionNode[];
  waitForRetryState: StepRetryStateLive | null;
  executeRetryState: StepRetryStateLive | null;
}

export interface StepRetryStateLive {
  firstAttemptTimeMs: number;
  currentAttempts: number;
  lastError: string | null;
  lastErrorStackTrace: string | null;
}

export interface StepMethodReportLive {
  outcome: 'Succeeded' | 'Failed';
  error: string | null;
  errorStackTrace: string | null;
  attemptCount: number;
}

export interface StepOptionsSnapshotLive {
  waitForMethodTimeoutMs: number;
  executeMethodTimeoutMs: number;
  waitForMethodRetryPolicy: StepRetryPolicySnapshotLive | null;
  executeMethodRetryPolicy: StepRetryPolicySnapshotLive | null;
  waitForMethodProceedToAfterRetryExhaustedStepId: string | null;
  executeMethodProceedToAfterRetryExhaustedStepId: string | null;
}

export interface StepRetryPolicySnapshotLive {
  maxAttempts: number;
  initialIntervalMs: number;
  backoffCoefficient: number;
  maximumIntervalMs: number;
  totalTimeoutMs: number;
}

// One channel's pending message backlog as surfaced by RunsService.GetRun.
// `values` is a list of dex.Value flattened to JSON-displayable form
// (see flattenValue). Empty channels are filtered out by the mapper so the
// UI doesn't have to.
export interface ChannelMessagesEntry {
  channelName: string;
  values: unknown[];
}

// RunDetail is the live, authoritative view of a run as returned by
// RunsService.GetRun. The show page polls this every 2s while the run is
// non-terminal so the status badge, state map, and pending channel messages
// stay in sync with what the server actually has — replacing the previous
// "infer status from RunStop history event" hack that produced "Running"
// for runs sitting in AllStepsWaitingForConditions.
export interface RunDetail {
  found: boolean;
  runId: string;
  namespace: string;
  flowType: string;
  taskListName: string;
  status: number;
  version: number;
  serverTimestampMs: number;
  durableTimerFired: boolean;
  externalChannelMessageCounter: number;
  workerRequestCounter: number;
  state: Record<string, unknown>;
  unconsumedChannelMessages: ChannelMessagesEntry[];
  // Live snapshot of every step the engine considers active. Empty when
  // the run is fully terminal. Keys are step_exe_ids; values carry
  // status + provenance + WaitFor tree. The graph layer overlays this
  // on top of history-derived nodes so INVOKING_EXECUTE steps show up
  // as "Running" before their StepExecuteCompleted history event lands.
  activeStepExecutions: Record<string, ActiveStepExecutionLive>;
}

// mapGetRun lifts the wire GetRunResponse into the UI-facing RunDetail.
// Channels with empty values are dropped (the server returns the map with
// every channel ever published to during the run; an empty list means
// everything has been consumed and there's nothing actionable to show).
export function mapGetRun(wire: GetRunResponseWire): RunDetail {
  const channels: ChannelMessagesEntry[] = [];
  if (wire.unconsumed_channel_messages) {
    for (const [channelName, msgs] of Object.entries(wire.unconsumed_channel_messages)) {
      const raw = msgs?.values ?? [];
      if (raw.length === 0) continue;
      channels.push({
        channelName,
        values: raw.map(flattenValue),
      });
    }
    // Stable ordering by channel name so the panel doesn't reshuffle on every
    // poll — each refetch otherwise comes back with arbitrary map iteration
    // order from grpc-js.
    channels.sort((a, b) => a.channelName.localeCompare(b.channelName));
  }
  return {
    found: wire.found,
    runId: wire.run_id,
    namespace: wire.namespace,
    flowType: wire.flow_type,
    taskListName: wire.task_list_name,
    status: wire.status ?? 0,
    version: toNum(wire.version),
    serverTimestampMs: toNum(wire.server_timestamp_ms),
    durableTimerFired: Boolean(wire.durable_timer_fired),
    externalChannelMessageCounter: toNum(wire.external_channel_message_counter),
    workerRequestCounter: toNum(wire.worker_request_counter),
    state: flattenStateMap(wire.state),
    unconsumedChannelMessages: channels,
    activeStepExecutions: mapActiveStepExecutions(wire.active_step_executions),
  };
}

// mapActiveStepExecutions converts the wire `map<stepExeId, ActiveStepExecution>`
// into the typed `Record<stepExeId, ActiveStepExecutionLive>` the UI consumes.
// Treats a missing/empty wire field as the empty map (a fully terminal run
// returns no active executions).
export function mapActiveStepExecutions(
  raw: Record<string, unknown> | undefined,
): Record<string, ActiveStepExecutionLive> {
  const out: Record<string, ActiveStepExecutionLive> = {};
  if (!raw) return out;
  for (const [stepExeId, v] of Object.entries(raw)) {
    if (!v || typeof v !== 'object') continue;
    const obj = v as Record<string, unknown>;
    out[stepExeId] = {
      status: typeof obj.status === 'number' ? obj.status : 0,
      fromStepExeId: typeof obj.from_step_exe_id === 'string' ? obj.from_step_exe_id : '',
      waitForCondition: mapWaitForCondition(obj.wait_for_condition),
      conditionResults: mapConditionResults(obj.condition_results),
      waitForRetryState: mapStepRetryState(obj.wait_for_retry_state),
      executeRetryState: mapStepRetryState(obj.execute_retry_state),
    };
  }
  return out;
}

export function mapStepRetryState(raw: unknown): StepRetryStateLive | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  return {
    firstAttemptTimeMs: toNum(obj.first_attempt_time_ms as string | number),
    currentAttempts: typeof obj.current_attempts === 'number' ? obj.current_attempts : 0,
    lastError: typeof obj.last_error === 'string' ? obj.last_error : null,
    lastErrorStackTrace: typeof obj.last_error_stack_trace === 'string' ? obj.last_error_stack_trace : null,
  };
}

export function mapStepMethodReport(raw: unknown): StepMethodReportLive | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  const outcomeNum = typeof obj.outcome === 'number' ? obj.outcome : 0;
  const attemptCount = typeof obj.attempt_count === 'number' ? obj.attempt_count : 0;
  if (outcomeNum === 0 && attemptCount <= 1 && !obj.error) {
    return null;
  }
  return {
    outcome: outcomeNum === 1 ? 'Failed' : 'Succeeded',
    error: typeof obj.error === 'string' && obj.error !== '' ? obj.error : null,
    errorStackTrace:
      typeof obj.error_stack_trace === 'string' && obj.error_stack_trace !== ''
        ? obj.error_stack_trace
        : null,
    attemptCount,
  };
}

function mapStepOptionsSnapshot(raw: unknown): StepOptionsSnapshotLive | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  return {
    waitForMethodTimeoutMs: toNum(obj.wait_for_method_timeout_ms as string | number),
    executeMethodTimeoutMs: toNum(obj.execute_method_timeout_ms as string | number),
    waitForMethodRetryPolicy: mapRetryPolicySnapshot(obj.wait_for_method_retry_policy),
    executeMethodRetryPolicy: mapRetryPolicySnapshot(obj.execute_method_retry_policy),
    waitForMethodProceedToAfterRetryExhaustedStepId:
      typeof obj.wait_for_method_proceed_to_after_retry_exhausted_step_id === 'string' &&
      obj.wait_for_method_proceed_to_after_retry_exhausted_step_id !== ''
        ? obj.wait_for_method_proceed_to_after_retry_exhausted_step_id
        : null,
    executeMethodProceedToAfterRetryExhaustedStepId:
      typeof obj.execute_method_proceed_to_after_retry_exhausted_step_id === 'string' &&
      obj.execute_method_proceed_to_after_retry_exhausted_step_id !== ''
        ? obj.execute_method_proceed_to_after_retry_exhausted_step_id
        : null,
  };
}

function mapRetryPolicySnapshot(raw: unknown): StepRetryPolicySnapshotLive | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  return {
    maxAttempts: typeof obj.max_attempts === 'number' ? obj.max_attempts : 0,
    initialIntervalMs: toNum(obj.initial_interval_ms as string | number),
    backoffCoefficient: typeof obj.backoff_coefficient === 'number' ? obj.backoff_coefficient : 0,
    maximumIntervalMs: toNum(obj.maximum_interval_ms as string | number),
    totalTimeoutMs: toNum(obj.total_timeout_ms as string | number),
  };
}

function toNum(v: string | number | undefined | null): number {
  if (v === undefined || v === null || v === '') return 0;
  if (typeof v === 'number') return v;
  // proto-loader gives longs as strings when longs: String. Numbers fit safely
  // for ms timestamps and small ints.
  const n = Number(v);
  return Number.isFinite(n) ? n : 0;
}

export function mapRun(r: RunSummaryWire): RunSummary {
  return {
    runId: r.run_id,
    namespace: r.namespace,
    flowType: r.flow_type,
    taskListName: r.task_list_name,
    status: r.status ?? 0,
    startTimeMs: toNum(r.start_time_ms),
    updatedAtMs: toNum(r.updated_at_ms),
  };
}

// flattenValue converts a dex.Value oneof to a JSON-displayable form so
// the UI doesn't need to dispatch on field names. JSON-encoded blobs are
// decoded inline (the most common case in practice — both the SDK and the
// benchmark worker default to JSON encoding for inputs / state). Other
// encodings fall back to a verbose object the show page can render raw.
//
// Exported so the GetRun mapper (and any future mapper that needs to surface
// raw dex.Value into the UI layer) can reuse it.
export function flattenValue(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v !== 'object') return v;
  const obj = v as Record<string, unknown>;
  if ('int_value' in obj && obj.int_value !== undefined) return toNum(obj.int_value as string | number);
  if ('double_value' in obj && obj.double_value !== undefined) return obj.double_value;
  if ('bool_value' in obj && obj.bool_value !== undefined) return obj.bool_value;
  if ('null_value' in obj && obj.null_value !== undefined) return null;
  if ('encoded_object' in obj && obj.encoded_object) {
    const eo = obj.encoded_object as { encoding?: string; payload?: Buffer | Uint8Array | string };
    return decodeEncodedObject(eo);
  }
  if ('encoded_object_blob_id_internal_only' in obj && obj.encoded_object_blob_id_internal_only) {
    return { __blobRef: true, blob_id: obj.encoded_object_blob_id_internal_only };
  }
  return null;
}

function decodeEncodedObject(eo: {
  encoding?: string;
  payload?: Buffer | Uint8Array | string;
}): unknown {
  const encoding = eo.encoding ?? '';
  // Normalize payload to a Buffer so the same decode path handles every
  // wire form (proto-loader gives Uint8Array; gRPC tests sometimes pass
  // strings).
  let buf: Buffer | null = null;
  if (eo.payload) {
    if (typeof eo.payload === 'string') {
      // Best-effort: if proto-loader gave us a base64 string, decode it.
      buf = Buffer.from(eo.payload, 'base64');
    } else if (eo.payload instanceof Uint8Array) {
      buf = Buffer.from(eo.payload);
    }
  }
  // JSON: decode and parse so the UI sees the actual object.
  if (encoding.toLowerCase() === 'json' && buf) {
    try {
      return JSON.parse(buf.toString('utf8'));
    } catch {
      // Fall through to the raw representation below.
    }
  }
  // Anything else: surface the encoding + base64 so it's at least inspectable.
  return {
    __encoded: true,
    encoding,
    payload_b64: buf ? buf.toString('base64') : '',
  };
}

// flattenStateMap walks a `map<string, Value>` and applies flattenValue.
// Exported for the same reason as flattenValue.
export function flattenStateMap(m: unknown): Record<string, unknown> {
  if (!m || typeof m !== 'object') return {};
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(m as Record<string, unknown>)) {
    out[k] = flattenValue(v);
  }
  return out;
}

function normalizeNextSteps(steps: unknown): unknown {
  if (!Array.isArray(steps)) return [];
  return steps.map((s) => {
    const obj = s as Record<string, unknown>;
    return {
      step_id: obj.step_id,
      input: flattenValue(obj.input),
      skip_wait_for: obj.skip_wait_for ?? false,
      step_options_snapshot: mapStepOptionsSnapshot(obj.step_options_snapshot),
    };
  });
}

function normalizeChannelPublish(items: unknown): unknown {
  if (!Array.isArray(items)) return [];
  return items.map((p) => {
    const obj = p as Record<string, unknown>;
    return {
      channel_name: obj.channel_name,
      values: Array.isArray(obj.values) ? (obj.values as unknown[]).map(flattenValue) : [],
    };
  });
}

// mapWaitType lifts the proto-loader pb.WaitType (numeric or string) into the
// typed UI form. Returns null on garbage input so callers can render a
// fallback rather than crash.
function mapWaitType(raw: unknown): 'AnyOf' | 'AllOf' | null {
  if (raw === 0 || raw === 'WAIT_TYPE_ANY_OF') return 'AnyOf';
  if (raw === 1 || raw === 'WAIT_TYPE_ALL_OF') return 'AllOf';
  return null;
}

// mapWaitForCondition turns a wire pb.WaitForCondition (proto-loader oneof
// shape) into the typed tree the UI components consume. The proto-loader
// enables `oneofs: true` so SingleCondition has a `condition` discriminator
// holding 'timer' | 'channel'.
export function mapWaitForCondition(raw: unknown): WaitForConditionTree | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  const type = mapWaitType(obj.type);
  if (type === null) return null;
  const conds = Array.isArray(obj.conditions) ? (obj.conditions as unknown[]) : [];
  const out: ConditionNode[] = [];
  for (const c of conds) {
    if (!c || typeof c !== 'object') continue;
    const co = c as Record<string, unknown>;
    if (co.timer && typeof co.timer === 'object') {
      const t = co.timer as Record<string, unknown>;
      out.push({ kind: 'Timer', fireAtUnixMs: toNum(t.fire_at_unix_ms as string | number) });
      continue;
    }
    if (co.channel && typeof co.channel === 'object') {
      const ch = co.channel as Record<string, unknown>;
      out.push({
        kind: 'Channel',
        channelName: String(ch.channel_name ?? ''),
        min: toNum(ch.min as string | number),
        max: toNum(ch.max as string | number),
      });
    }
  }
  return { type, conditions: out };
}

// mapConditionResults turns the wire `repeated ConditionResult` (with the
// timer/channel oneof on each entry) into a flat ConditionNode array with
// the per-result satisfied/fired/consumedCount fields populated. Used both
// for StepExecute/StepWaitFor history payloads and for StepsUnblocked.
export function mapConditionResults(raw: unknown): ConditionNode[] {
  if (!Array.isArray(raw)) return [];
  const out: ConditionNode[] = [];
  for (const r of raw) {
    if (!r || typeof r !== 'object') continue;
    const ro = r as Record<string, unknown>;
    if (ro.timer && typeof ro.timer === 'object') {
      const t = ro.timer as Record<string, unknown>;
      out.push({
        kind: 'Timer',
        fireAtUnixMs: toNum(t.fire_at_unix_ms as string | number),
        fired: Boolean(t.fired),
      });
      continue;
    }
    if (ro.channel && typeof ro.channel === 'object') {
      const ch = ro.channel as Record<string, unknown>;
      out.push({
        kind: 'Channel',
        channelName: String(ch.channel_name ?? ''),
        min: 0, // not present on result; only on condition itself
        max: 0,
        satisfied: Boolean(ch.satisfied),
        consumedCount: toNum(ch.consumed_count as string | number),
      });
    }
  }
  return out;
}

// mapStepUnblockedEntries normalizes the wire `repeated StepUnblocked` field
// (used on StepExecuteCompletedRequest, StepWaitForCompletedRequest, and
// HistoryStepsUnblockedPayload) into the typed UI shape.
export function mapStepUnblockedEntries(raw: unknown): StepUnblockedEntry[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((u) => {
    const obj = (u ?? {}) as Record<string, unknown>;
    return {
      step_exe_id: String(obj.step_exe_id ?? ''),
      condition_results: mapConditionResults(obj.condition_results),
    };
  });
}

export function mapHistoryEvent(e: HistoryEventWire): HistoryEvent {
  const base = {
    id: toNum(e.id),
    occurredAtMs: toNum(e.occurred_at_ms),
    workerId: e.worker_id ?? '',
  };

  if (e.run_start) {
    const data = e.run_start as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'RunStart',
        data: {
          flow_type: data.flow_type,
          task_list_name: data.task_list_name,
          starting_steps: normalizeNextSteps(data.starting_steps),
        },
      },
    };
  }
  if (e.run_stop) {
    const data = e.run_stop as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'RunStop',
        data: {
          runStatus: toNum(data.run_status as string | number),
          reason: typeof data.reason === 'string' ? data.reason : '',
        },
      },
    };
  }
  if (e.step_execute_completed) {
    const data = e.step_execute_completed as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'StepExecuteCompleted',
        data: {
          step_exe_id: data.step_exe_id,
          from_step_exe_id: data.from_step_exe_id,
          worker_request_counter: toNum(data.worker_request_counter as string | number),
          request_to_drain_channels: data.request_to_drain_channels ?? false,
          stop_decision: data.stop_decision ?? 0,
          state_to_upsert: flattenStateMap(data.state_to_upsert),
          next_steps: normalizeNextSteps(data.next_steps),
          canceled_step_executions: data.canceled_step_executions ?? [],
          condition_results: mapConditionResults(data.condition_results),
          channel_publish: normalizeChannelPublish(data.channel_publish),
          steps_unblocked: mapStepUnblockedEntries(data.steps_unblocked),
          execute_method: mapStepMethodReport(data.execute_method),
          has_snapshot: data.snapshot != null,
        },
      },
    };
  }
  if (e.step_wait_for_completed) {
    const data = e.step_wait_for_completed as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'StepWaitForCompleted',
        data: {
          step_exe_id: data.step_exe_id,
          // from_step_exe_id is server-authoritative provenance for graph
          // rendering. Surfaced on WaitFor (not just Execute) so that a
          // child step which never reaches Execute (stays in
          // WAITING_FOR_CONDITION indefinitely) can still be drawn with
          // an incoming edge from its parent.
          from_step_exe_id: data.from_step_exe_id ?? '',
          worker_request_counter: toNum(data.worker_request_counter as string | number),
          wait_for_condition: mapWaitForCondition(data.wait_for_condition),
          state_to_upsert: flattenStateMap(data.state_to_upsert),
          channel_publish: normalizeChannelPublish(data.channel_publish),
          steps_unblocked: mapStepUnblockedEntries(data.steps_unblocked),
          wait_for_method: mapStepMethodReport(data.wait_for_method),
          next_steps: normalizeNextSteps(data.next_steps),
          has_snapshot: data.snapshot != null,
        },
      },
    };
  }
  if (e.steps_unblocked) {
    const data = e.steps_unblocked as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'StepsUnblocked',
        data: {
          worker_request_counter: toNum(data.worker_request_counter as string | number),
          steps_unblocked: mapStepUnblockedEntries(data.steps_unblocked),
          has_snapshot: data.snapshot != null,
        },
      },
    };
  }
  if (e.run_fork) {
    const data = e.run_fork as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'RunFork',
        data: {
          fork_to_event_id: toNum(data.fork_to_event_id as string | number),
          reason: typeof data.reason === 'string' ? data.reason : '',
        },
      },
    };
  }
  if (e.channel_publish) {
    const data = e.channel_publish as Record<string, unknown>;
    return {
      ...base,
      payload: {
        type: 'ChannelPublish',
        data: {
          channel_name: data.channel_name,
          values: Array.isArray(data.values) ? (data.values as unknown[]).map(flattenValue) : [],
        },
      },
    };
  }
  return { ...base, payload: { type: 'Unknown', data: {} } };
}

const STOP_DECISION_COMPLETE = 1;
const STOP_DECISION_FAIL = 2;

/** True when ForkRun is allowed to restore to this history event. */
export function isForkableHistoryEvent(event: HistoryEvent): boolean {
  switch (event.payload.type) {
    case 'RunStart':
      return true;
    case 'RunFork':
    case 'RunStop':
    case 'ChannelPublish':
      return false;
    case 'StepExecuteCompleted': {
      const stop = (event.payload.data as { stop_decision?: number }).stop_decision ?? 0;
      if (stop === STOP_DECISION_COMPLETE || stop === STOP_DECISION_FAIL) return false;
      return Boolean((event.payload.data as { has_snapshot?: boolean }).has_snapshot);
    }
    case 'StepWaitForCompleted':
    case 'StepsUnblocked':
      return Boolean((event.payload.data as { has_snapshot?: boolean }).has_snapshot);
    default:
      return false;
  }
}

/** Events that drive the post-fork graph (excludes pre-fork branch). */
export function graphHistoryEvents(events: HistoryEvent[]): HistoryEvent[] {
  let forkMarkerId = 0;
  let forkToEventId = 0;
  for (let index = events.length - 1; index >= 0; index--) {
    const event = events[index];
    if (event.payload.type === 'RunFork') {
      forkMarkerId = event.id;
      forkToEventId = (event.payload.data as { fork_to_event_id: number }).fork_to_event_id;
      break;
    }
  }
  if (forkMarkerId === 0) return events;
  return events.filter((event) => event.id === forkToEventId || event.id > forkMarkerId);
}
