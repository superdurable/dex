import type {
  ActiveStepExecution,
  FlowExecution,
  FlowHistoryEvent,
  FlowState,
  FlowSummary,
  HistoryEventType,
  KeyValue,
} from '@/lib/types';
import { FLOW_STATUS } from '@/lib/types';

const eventFields: Array<[string, HistoryEventType]> = [
  ['flow_started_or_continued', 'FlowStartedOrContinued'],
  ['flow_closed', 'FlowClosed'],
  ['step_wait_for_completed', 'StepWaitForCompleted'],
  ['step_wait_for_failed', 'StepWaitForFailed'],
  ['step_execute_completed', 'StepExecuteCompleted'],
  ['step_execute_failed', 'StepExecuteFailed'],
  ['rpc_execution_completed', 'RpcExecutionCompleted'],
  ['channel_external_publish', 'ChannelExternalPublish'],
];

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? value as Record<string, unknown> : {};
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function numberValue(value: unknown): number {
  if (typeof value === 'number') return value;
  if (typeof value === 'string') return Number(value) || 0;
  return 0;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function camelKey(key: string): string {
  return key.replace(/_([a-z])/g, (_, letter: string) => letter.toUpperCase());
}

export function jsonValue(value: unknown, fieldName = ''): unknown {
  if (Buffer.isBuffer(value)) return value.toString('base64');
  if (Array.isArray(value)) return value.map((nested) => jsonValue(nested, fieldName));
  if (!value || typeof value !== 'object') return value;
  const object = value as Record<string, unknown>;
  if ('seconds' in object && 'nanos' in object && Object.keys(object).length <= 3) {
    if (/(time|timestamp)$/i.test(fieldName)) return timestampValue(object);
    return {
      seconds: numberValue(object.seconds),
      nanos: numberValue(object.nanos),
    };
  }
  const result: Record<string, unknown> = {};
  for (const [key, nested] of Object.entries(object)) {
    if (key === 'payload' || key === 'start_or_continue' || key === 'kind') continue;
    if (typeof nested === 'string' && nested in object) continue;
    result[camelKey(key)] = jsonValue(nested, key);
  }
  return result;
}

function timestampValue(value: unknown): string | null {
  const timestamp = asRecord(value);
  const seconds = numberValue(timestamp.seconds);
  const nanos = numberValue(timestamp.nanos);
  if (!seconds && !nanos) return null;
  return new Date(seconds * 1000 + Math.floor(nanos / 1_000_000)).toISOString();
}

function statusValue(value: unknown) {
  const code = numberValue(value);
  return {
    code,
    label: FLOW_STATUS[code as keyof typeof FLOW_STATUS] ?? 'Unknown',
  };
}

function mapKeyValues(value: unknown): KeyValue[] {
  return asArray(value).map((entry) => {
    const item = asRecord(entry);
    return { key: stringValue(item.key), value: dexValue(item.value) };
  });
}

function dexValue(value: unknown): unknown {
  const message = asRecord(value);
  if ('string_value' in message) return message.string_value;
  if ('int_value' in message) return numberValue(message.int_value);
  if ('double_value' in message) return numberValue(message.double_value);
  if ('bool_value' in message) return Boolean(message.bool_value);
  if ('null_value' in message) return null;
  if ('obj_value' in message) {
    const object = asRecord(message.obj_value);
    const encoding = stringValue(object.encoding);
    if (Buffer.isBuffer(object.payload)) {
      const text = object.payload.toString('utf8');
      if (encoding === 'json') {
        try {
          return JSON.parse(text) as unknown;
        } catch {
          return { encoding, payload: text };
        }
      }
      return { encoding, payload: object.payload.toString('base64') };
    }
    return { encoding, payload: stringValue(object.payload) };
  }
  if ('internal_blob_id_for_string_value' in message) {
    return { blobId: message.internal_blob_id_for_string_value, kind: 'string' };
  }
  if ('internal_blob_id_for_obj_value' in message) {
    return { blobId: message.internal_blob_id_for_obj_value, kind: 'object' };
  }
  return jsonValue(value);
}

export function mapSearchEntry(value: unknown): FlowExecution {
  const entry = asRecord(value);
  const status = statusValue(entry.flow_status);
  return {
    flowId: stringValue(entry.flow_id),
    runId: stringValue(entry.run_id),
    flowType: stringValue(entry.flow_type),
    flowStatus: status.label,
    flowStatusCode: status.code,
    startTime: timestampValue(entry.start_time),
    closeTime: timestampValue(entry.close_time),
    searchAttributes: mapKeyValues(entry.search_attributes),
  };
}

export function mapSummary(value: unknown): FlowSummary {
  const summary = asRecord(value);
  const execution = asRecord(summary.flow_execution_id);
  const status = statusValue(summary.flow_status);
  return {
    flowId: stringValue(execution.flow_id),
    runId: stringValue(execution.run_id),
    firstRunId: stringValue(summary.first_run_id),
    requestId: stringValue(summary.request_id),
    flowType: stringValue(summary.flow_type),
    flowStatus: status.label,
    flowStatusCode: status.code,
    startTime: timestampValue(summary.start_time),
    closeTime: timestampValue(summary.close_time),
  };
}

export function mapHistoryEvent(value: unknown): FlowHistoryEvent {
  const event = asRecord(value);
  const selected = eventFields.find(([field]) => event[field] !== undefined);
  if (!selected) throw new Error(`history event ${stringValue(event.event_id)} has no Dex payload`);
  return {
    eventId: numberValue(event.event_id),
    eventTime: timestampValue(event.event_time),
    type: selected[1],
    payload: jsonValue(event[selected[0]]) as Record<string, unknown>,
  };
}

function phaseValue(value: unknown): ActiveStepExecution['phase'] {
  if (numberValue(value) === 1) return 'Active';
  if (numberValue(value) === 2) return 'Waiting';
  return 'Unspecified';
}

function mapActiveStep(value: unknown): ActiveStepExecution {
  const step = asRecord(value);
  return {
    stepExecutionId: stringValue(step.step_execution_id),
    fromStepExecutionId: stringValue(step.from_step_execution_id),
    stepType: stringValue(step.step_type),
    phase: phaseValue(step.phase),
    movement: jsonValue(step.movement) as Record<string, unknown>,
    waitingCondition: jsonValue(step.waiting_condition) as Record<string, unknown>,
    completedConditions: jsonValue(step.completed_conditions) as Record<string, unknown>,
    stepExecutionLocals: mapKeyValues(step.step_execution_locals),
    timers: asArray(step.timers).map((timer) => jsonValue(timer) as Record<string, unknown>),
  };
}

export function mapFlowState(value: unknown): FlowState {
  const state = asRecord(value);
  return {
    flowConfig: jsonValue(state.flow_config) as Record<string, unknown>,
    attributes: mapKeyValues(state.attributes),
    activeStepExecutions: asArray(state.active_step_executions).map(mapActiveStep),
    queuedSteps: asArray(state.queued_steps).map(
      (step) => jsonValue(step) as Record<string, unknown>,
    ),
    pendingChannelMessages: jsonValue(state.pending_channel_messages) as Record<string, unknown>,
    completedSteps: asArray(state.completed_steps).map(
      (step) => jsonValue(step) as Record<string, unknown>,
    ),
  };
}
