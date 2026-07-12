'use client';

import type {
  ConditionNode,
  HistoryEvent,
  HistoryEventPayload,
  StepMethodReportLive,
  StepUnblockedEntry,
  WaitForConditionTree,
} from '../api/_grpc/mappers';
import { formatTimestamp, runStatusName, stopDecisionName } from './utils';
import WaitForConditionPanel from './WaitForConditionPanel';
import StackTracePre from './StackTracePre';

const TYPE_COLORS: Record<HistoryEventPayload['type'], string> = {
  RunStart: 'bg-blue-100 text-blue-800 ring-blue-300',
  RunStop: 'bg-green-100 text-green-800 ring-green-300',
  StepExecuteCompleted: 'bg-purple-100 text-purple-800 ring-purple-300',
  StepWaitForCompleted: 'bg-yellow-100 text-yellow-800 ring-yellow-300',
  ChannelPublish: 'bg-pink-100 text-pink-800 ring-pink-300',
  StepsUnblocked: 'bg-indigo-100 text-indigo-800 ring-indigo-300',
  Unknown: 'bg-gray-100 text-gray-800 ring-gray-300',
};

function MethodOutcomeSection({ label, report }: { label: string; report: StepMethodReportLive | null | undefined }) {
  if (!report) return null;
  const failed = report.outcome === 'Failed';
  const recovered = report.outcome === 'Succeeded' && report.attemptCount > 1;
  if (!failed && !recovered) return null;
  return (
    <div
      data-testid="method-outcome-section"
      className={`rounded border p-2 text-sm ${failed ? 'border-red-200 bg-red-50' : 'border-amber-200 bg-amber-50'}`}
    >
      <div className="text-xs font-medium uppercase tracking-wide text-gray-600 mb-1">
        {label} · {failed ? 'Failed' : `Recovered after ${report.attemptCount} attempts`}
      </div>
      {report.error && <div className="font-mono text-xs break-all text-gray-900">{report.error}</div>}
      {report.errorStackTrace && (
        <StackTracePre text={report.errorStackTrace} className="mt-2 text-gray-700" />
      )}
    </div>
  );
}

function MethodFailedProceededBadge() {
  return (
    <span
      data-testid="method-failed-proceeded-badge"
      className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-900 ring-1 ring-amber-300"
    >
      Failed → proceeded
    </span>
  );
}

function PayloadDetails({ payload }: { payload: HistoryEventPayload }) {
  switch (payload.type) {
    case 'RunStart': {
      const data = payload.data as { flow_type?: string; task_list_name?: string; starting_steps?: unknown[] };
      return (
        <dl className="grid grid-cols-1 md:grid-cols-3 gap-x-6 gap-y-1 text-sm">
          <Field label="Flow type" value={data.flow_type ?? '—'} />
          <Field label="Task list" value={data.task_list_name ?? '—'} />
          <NextStepsSection steps={data.starting_steps} label="Starting steps" />
        </dl>
      );
    }
    case 'RunStop':
      return (
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-1 text-sm">
          <Field label="Run status" value={runStatusName(payload.data.runStatus)} />
          {payload.data.reason ? (
            <Field label="Reason" value={String(payload.data.reason)} />
          ) : null}
        </dl>
      );
    case 'StepExecuteCompleted': {
      const data = payload.data;
      const stop = data.stop_decision as number | undefined;
      const nextSteps = data.next_steps as unknown[] | undefined;
      const condResults = data.condition_results as ConditionNode[] | undefined;
      const unblocks = data.steps_unblocked as StepUnblockedEntry[] | undefined;
      const cancelledSiblings = data.canceled_step_executions as string[] | undefined;
      const executeMethod = data.execute_method as StepMethodReportLive | null | undefined;
      return (
        <div className="space-y-3">
          <dl className="grid grid-cols-1 md:grid-cols-3 gap-x-6 gap-y-1 text-sm">
            <Field label="Step exe id" value={String(data.step_exe_id ?? '—')} mono />
            <Field label="From step" value={String(data.from_step_exe_id ?? '—')} mono />
            <Field label="Stop decision" value={stopDecisionName(stop)} />
            <NextStepsSection steps={nextSteps} label="Next steps" />
            <Field
              label="State keys"
              value={`${Object.keys((data.state_to_upsert as object) ?? {}).length}`}
            />
          </dl>
          {executeMethod?.outcome === 'Failed' && Array.isArray(nextSteps) && nextSteps.length > 0 && stop === 0 && (
            <MethodFailedProceededBadge />
          )}
          <MethodOutcomeSection label="Execute method" report={executeMethod} />
          {Array.isArray(condResults) && condResults.length > 0 && (
            <div>
              <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
                Condition results
              </div>
              <WaitForConditionPanel results={condResults} />
            </div>
          )}
          {Array.isArray(cancelledSiblings) && cancelledSiblings.length > 0 && (
            <CancelledSiblingsSection cancelled={cancelledSiblings} />
          )}
          {Array.isArray(unblocks) && unblocks.length > 0 && (
            <UnblocksSection unblocks={unblocks} />
          )}
        </div>
      );
    }
    case 'StepWaitForCompleted': {
      const data = payload.data;
      const cond = data.wait_for_condition as WaitForConditionTree | null;
      const unblocks = data.steps_unblocked as StepUnblockedEntry[] | undefined;
      const nextSteps = data.next_steps as unknown[] | undefined;
      const waitForMethod = data.wait_for_method as StepMethodReportLive | null | undefined;
      return (
        <div className="space-y-3">
          <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-1 text-sm">
            <Field label="Step exe id" value={String(data.step_exe_id ?? '—')} mono />
            <NextStepsSection steps={nextSteps} label="Next steps" />
          </dl>
          {waitForMethod?.outcome === 'Failed' && Array.isArray(nextSteps) && nextSteps.length > 0 && (
            <MethodFailedProceededBadge />
          )}
          <MethodOutcomeSection label="WaitFor method" report={waitForMethod} />
          <div>
            <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
              Wait for
            </div>
            <WaitForConditionPanel tree={cond} emptyMessage="No condition reported." />
          </div>
          {Array.isArray(unblocks) && unblocks.length > 0 && (
            <UnblocksSection unblocks={unblocks} />
          )}
        </div>
      );
    }
    case 'ChannelPublish': {
      const data = payload.data;
      const values = data.values as unknown[] | undefined;
      return (
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-1 text-sm">
          <Field label="Channel" value={String(data.channel_name ?? '—')} />
          <Field label="Messages" value={`${Array.isArray(values) ? values.length : 0}`} />
        </dl>
      );
    }
    case 'StepsUnblocked': {
      const data = payload.data;
      return (
        <div className="space-y-3">
          <div className="flex items-center gap-2 text-sm">
            <span className="text-gray-700">{data.steps_unblocked.length} step{data.steps_unblocked.length === 1 ? '' : 's'} promoted</span>
          </div>
          <UnblocksSection unblocks={data.steps_unblocked} />
        </div>
      );
    }
    default:
      return null;
  }
}

function CancelledSiblingsSection({ cancelled }: { cancelled: string[] }) {
  return (
    <div data-testid="cancelled-siblings">
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
        Cancelled siblings
      </div>
      <ul className="space-y-1">
        {cancelled.map((id) => (
          <li
            key={id}
            className="font-mono text-xs break-all border border-rose-200 bg-rose-50 text-rose-800 rounded px-2 py-1"
          >
            {id}
          </li>
        ))}
      </ul>
    </div>
  );
}

function UnblocksSection({ unblocks }: { unblocks: StepUnblockedEntry[] }) {
  return (
    <div>
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
        Unblocked siblings
      </div>
      <ul className="space-y-2">
        {unblocks.map((u) => (
          <li key={u.step_exe_id} className="border border-gray-200 rounded p-2">
            <div className="font-mono text-xs break-all mb-1">{u.step_exe_id}</div>
            <WaitForConditionPanel results={u.condition_results} />
          </li>
        ))}
      </ul>
    </div>
  );
}

function NextStepsSection({ steps, label }: { steps: unknown; label: string }) {
  if (!Array.isArray(steps) || steps.length === 0) {
    return <Field label={label} value="0" />;
  }
  return (
    <div className="md:col-span-3 space-y-2" data-testid="next-step-options">
      <div className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</div>
      <ul className="space-y-2">
        {steps.map((raw, index) => {
          const step = raw as Record<string, unknown>;
          const snapshot = step.step_options_snapshot as Record<string, unknown> | null | undefined;
          const executePolicy = snapshot?.execute_method_retry_policy as Record<string, unknown> | undefined;
          return (
            <li key={index} className="text-xs border border-gray-200 rounded p-2 bg-gray-50">
              <div className="font-mono break-all">{String(step.step_id ?? '—')}</div>
              <div className="text-gray-600 mt-1">
                skip_wait_for: {String(step.skip_wait_for ?? false)}
              </div>
              {snapshot && (
                <div className="text-gray-600 mt-1">
                  execute timeout: {String(snapshot.execute_method_timeout_ms ?? '—')}ms
                  {executePolicy && (
                    <span>
                      {' '}
                      · max attempts: {String(executePolicy.max_attempts ?? '—')}
                    </span>
                  )}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</dt>
      <dd className={`mt-0.5 text-gray-900 ${mono ? 'font-mono text-xs break-all' : ''}`}>{value}</dd>
    </div>
  );
}

export default function EventCard({ event }: { event: HistoryEvent }) {
  const cls = TYPE_COLORS[event.payload.type];
  return (
    <li
      data-testid="event-card"
      data-event-type={event.payload.type}
      data-event-id={event.id}
      className="relative pl-8 pb-6"
    >
      <span className="absolute left-2 top-2 w-3 h-3 rounded-full bg-white border-2 border-blue-500" />
      <span className="absolute left-3.5 top-5 bottom-0 w-px bg-gray-200" />
      <div className="bg-white shadow-sm border border-gray-200 rounded-lg p-4">
        <div className="flex items-center gap-2 mb-2">
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ring-1 ring-inset ${cls}`}
          >
            {event.payload.type}
          </span>
          <span className="text-xs font-mono text-gray-500">#{event.id}</span>
          <span className="text-xs text-gray-500">{formatTimestamp(event.occurredAtMs)}</span>
          {event.workerId && (
            <span className="text-xs text-gray-400 font-mono ml-auto">worker {event.workerId}</span>
          )}
        </div>
        <PayloadDetails payload={event.payload} />
        <details className="mt-3 group">
          <summary
            data-testid="event-raw-toggle"
            className="cursor-pointer text-xs text-gray-500 hover:text-gray-700"
          >
            Raw payload JSON
          </summary>
          <pre
            data-testid="event-raw-pre"
            className="mt-2 text-xs bg-gray-50 border border-gray-200 rounded p-2 overflow-x-auto"
          >
            {JSON.stringify(event.payload.data, null, 2)}
          </pre>
        </details>
      </div>
    </li>
  );
}
