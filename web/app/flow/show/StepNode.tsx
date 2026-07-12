'use client';

import { useContext, type MouseEvent } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';
import { stopDecisionName, runStatusName } from '../../components/utils';
import type { StepNodeData } from './buildGraph';
import { SectionSelectionContext, type Section } from './SectionSelectionContext';

const STATUS_COLORS: Record<string, string> = {
  Pending: 'bg-gray-100 text-gray-700 ring-gray-300',
  Waiting: 'bg-yellow-100 text-yellow-800 ring-yellow-300',
  // Animated blue badge — the only live-pulsing state. Driven by the
  // active_step_executions overlay in buildGraph.ts: a step in
  // INVOKING_EXECUTE that hasn't written StepExecuteCompleted yet (e.g.
  // a long sleep or LLM call). Pulse signals "this is happening NOW".
  Running: 'bg-blue-100 text-blue-800 ring-blue-300 animate-pulse',
  Retrying: 'bg-amber-100 text-amber-900 ring-amber-300',
  Completed: 'bg-green-100 text-green-800 ring-green-300',
  // Rose to match the EventCard's CancelledSiblingsSection so users can
  // visually link a graph node back to the StepExecuteCompleted timeline
  // event that owns the cancellation.
  Cancelled: 'bg-rose-100 text-rose-800 ring-rose-300',
  Virtual: 'bg-blue-100 text-blue-800 ring-blue-300',
};

// Override the Completed color when the stop_decision tells us more.
function statusBadgeClass(d: StepNodeData): string {
  if (d.status !== 'Completed') return STATUS_COLORS[d.status] ?? STATUS_COLORS.Pending;
  switch (d.stopDecision) {
    case 1: // COMPLETE
      return 'bg-green-100 text-green-800 ring-green-300';
    case 2: // FAIL
      return 'bg-red-100 text-red-800 ring-red-300';
    case 3: // DEAD_END
      return 'bg-gray-200 text-gray-800 ring-gray-400';
    default: // NONE / unknown
      return 'bg-purple-100 text-purple-800 ring-purple-300';
  }
}

function statusLabel(d: StepNodeData): string {
  if (d.status === 'Retrying' && d.retryState) {
    return `Retrying · #${d.retryState.currentAttempts}`;
  }
  if (d.status !== 'Completed') return d.status;
  return `${stopDecisionName(d.stopDecision)}`;
}

function methodBarClass(failed: boolean, recovered: boolean, selected: boolean, base: string, selectedClass: string): string {
  if (failed) {
    return `${base} bg-red-100 text-red-800 ring-1 ring-inset ring-red-300`;
  }
  if (recovered) {
    return `${base} bg-amber-100 text-amber-900 ring-1 ring-inset ring-amber-300`;
  }
  return selected ? selectedClass : base;
}

function methodBarLabel(kind: 'WaitFor' | 'Execute', report: import('../../api/_grpc/mappers').StepMethodReportLive | null | undefined): string {
  if (!report) return kind;
  if (report.outcome === 'Failed') return `${kind} Failed`;
  if (report.attemptCount > 1) return `${kind} · recovered #${report.attemptCount}`;
  return kind;
}

const StepNode = ({ data, selected }: NodeProps) => {
  // React Flow types the data as Record<string,unknown>; we know our shape.
  const d = data as unknown as StepNodeData;
  const { selection, setSelection } = useContext(SectionSelectionContext);

  if (d.isVirtual) {
    const isStart = d.stepId === 'RunStart';
    const labelDetail = isStart
      ? `flow=${(d.meta?.flow_type as string) ?? '—'}`
      : `status=${runStatusName((d.meta?.runStatus as number) ?? null)}`;
    return (
      <>
        {!isStart && (
          <Handle type="target" position={Position.Top} id="in" className="!bg-blue-500" />
        )}
        <div
          data-testid="step-node"
          data-step-exe-id={d.stepExeId}
          data-status="Virtual"
          className={`px-3 py-2 rounded-md border-2 shadow-sm w-[220px] text-center ${
            selected ? 'border-blue-500' : 'border-gray-300'
          } bg-white`}
        >
          <div className="text-xs font-bold uppercase tracking-wide text-gray-700">
            {isStart ? 'Run start' : 'Run end'}
          </div>
          <div className="text-xs text-gray-500 mt-0.5 truncate">{labelDetail}</div>
        </div>
        {isStart && (
          <Handle type="source" position={Position.Bottom} id="out" className="!bg-blue-500" />
        )}
      </>
    );
  }

  // selectSection sets the side-panel filter without bubbling up to ReactFlow's
  // own node-selection (which would strip the section). Click on the same
  // sub-section twice toggles it off so the user can collapse back to overview.
  const selectSection = (section: Exclude<Section, null>) => (e: MouseEvent) => {
    e.stopPropagation();
    const isSameNode = selection && selection.stepExeId === d.stepExeId;
    const nextSection = isSameNode && selection.section === section ? null : section;
    setSelection({ stepExeId: d.stepExeId, section: nextSection });
  };

  const isThisNodeSelected = selection?.stepExeId === d.stepExeId;
  const waitSelected = isThisNodeSelected && selection?.section === 'wait';
  const executeSelected = isThisNodeSelected && selection?.section === 'execute';

  const subSectionBase =
    'flex-1 px-2 py-1 text-[10px] font-medium uppercase tracking-wide text-center cursor-pointer transition-colors';

  // Cancelled nodes get a faded body + dashed border so they read as
  // "deleted from ActiveStepExecutions" at a glance, not as a regular
  // completed/waiting step. The header still shows the canceller's
  // exe_id via tooltip so users can pivot to the timeline event.
  const isCancelled = d.status === 'Cancelled';
  const waitFailed = d.waitForMethod?.outcome === 'Failed';
  const executeFailed = d.executeMethod?.outcome === 'Failed';
  const waitRecovered = d.waitForMethod?.outcome === 'Succeeded' && (d.waitForMethod?.attemptCount ?? 0) > 1;
  const executeRecovered = d.executeMethod?.outcome === 'Succeeded' && (d.executeMethod?.attemptCount ?? 0) > 1;
  const containerClass = `rounded-md border-2 shadow-sm w-[240px] overflow-hidden ${
    isCancelled
      ? 'bg-rose-50/40 border-dashed border-rose-300 opacity-80'
      : 'bg-white'
  } ${selected ? 'border-blue-500' : isCancelled ? '' : 'border-gray-300'}`;

  return (
    <>
      <Handle type="target" position={Position.Top} id="in" className="!bg-blue-500" />
      <div
        data-testid="step-node"
        data-step-exe-id={d.stepExeId}
        data-status={d.status}
        data-cancelled-by={d.cancelledByExeId ?? ''}
        data-stop-decision={d.status === 'Completed' ? stopDecisionName(d.stopDecision) : ''}
        className={containerClass}
        title={
          isCancelled && d.cancelledByExeId
            ? `Cancelled by ${d.cancelledByExeId}`
            : undefined
        }
      >
        {/* Header: status + step ids. Clicking the header (but outside the
            sub-sections) clears the section so the overview is shown. */}
        <div className="px-3 pt-2 pb-1.5">
          <div className="flex items-center gap-2 mb-1">
            <span
              className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ring-1 ring-inset ${statusBadgeClass(d)}`}
              data-testid={d.status === 'Retrying' ? 'step-retry-badge' : undefined}
            >
              {statusLabel(d)}
            </span>
            {d.hadWaitFor && d.status === 'Waiting' && d.waitFor && (
              <span
                data-testid="live-wait"
                className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ring-1 ring-inset bg-indigo-50 text-indigo-700 ring-indigo-200"
                title={`Waiting on ${d.waitFor.type} of ${d.waitFor.conditions.length} condition${d.waitFor.conditions.length === 1 ? '' : 's'}`}
              >
                {d.waitFor.type} · {d.waitFor.conditions.length}
              </span>
            )}
            {d.proceededAfterFailure && (
              <span
                data-testid="method-failed-proceeded-badge"
                className="inline-flex items-center rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-900 ring-1 ring-amber-300"
              >
                Failed → proceeded
              </span>
            )}
          </div>
          <div className="text-[11px] font-mono text-gray-900 break-all leading-tight">
            {d.stepExeId}
          </div>
          <div className="text-[10px] text-gray-500 truncate mt-0.5">{d.stepId}</div>
          {isCancelled && d.cancelledByExeId && (
            <div className="text-[10px] text-rose-700 mt-1 break-all">
              ↳ cancelled by{' '}
              <span className="font-mono">{d.cancelledByExeId}</span>
            </div>
          )}
        </div>

        {/* Method bar: separate WaitFor / Execute sub-boxes when applicable.
            Steps without a WaitFor phase show only Execute. Cancelled steps
            never produced an Execute event, so we drop that sub-box. */}
        {d.hadWaitFor || d.status === 'Waiting' || (isCancelled && d.hadWaitFor) ? (
          <div className="flex border-t border-gray-200 divide-x divide-gray-200">
            <div
              data-testid="step-node-section-wait"
              data-section="wait"
              data-selected={waitSelected ? 'true' : 'false'}
              data-method-failed={waitFailed ? 'true' : 'false'}
              onClick={selectSection('wait')}
              className={methodBarClass(
                waitFailed,
                waitRecovered,
                waitSelected,
                `${subSectionBase} bg-indigo-50/60 text-indigo-700 hover:bg-indigo-100`,
                `${subSectionBase} bg-indigo-100 text-indigo-800 ring-1 ring-inset ring-indigo-300`,
              )}
              title={d.waitForMethod?.error ?? 'Show WaitFor details'}
            >
              {methodBarLabel('WaitFor', d.waitForMethod)}
            </div>
            {(d.status === 'Completed' || waitFailed) && (
              <div
                data-testid="step-node-section-execute"
                data-section="execute"
                data-selected={executeSelected ? 'true' : 'false'}
                data-method-failed={executeFailed ? 'true' : 'false'}
                onClick={selectSection('execute')}
                className={methodBarClass(
                  executeFailed,
                  executeRecovered,
                  executeSelected,
                  `${subSectionBase} bg-purple-50/60 text-purple-700 hover:bg-purple-100`,
                  `${subSectionBase} bg-purple-100 text-purple-800 ring-1 ring-inset ring-purple-300`,
                )}
                title={d.executeMethod?.error ?? 'Show Execute details'}
              >
                {methodBarLabel('Execute', d.executeMethod)}
              </div>
            )}
          </div>
        ) : isCancelled ? (
          // Cancelled-only step that never reached WaitFor: render a thin
          // strip indicating the cancellation rather than an empty footer.
          <div className="border-t border-rose-200 bg-rose-50/60 px-2 py-1 text-[10px] font-medium uppercase tracking-wide text-center text-rose-700">
            Deleted by sibling
          </div>
        ) : (
          // Steps with no WaitFor: a single Execute strip. Still clickable so
          // the panel surfaces just the StepExecuteCompleted event.
          <div className="flex border-t border-gray-200">
            <div
              data-testid="step-node-section-execute"
              data-section="execute"
              data-selected={executeSelected ? 'true' : 'false'}
              data-method-failed={executeFailed ? 'true' : 'false'}
              onClick={selectSection('execute')}
              className={methodBarClass(
                executeFailed,
                executeRecovered,
                executeSelected,
                `${subSectionBase} bg-purple-50/60 text-purple-700 hover:bg-purple-100`,
                `${subSectionBase} bg-purple-100 text-purple-800 ring-1 ring-inset ring-purple-300`,
              )}
              title={d.executeMethod?.error ?? 'Show Execute details'}
            >
              {methodBarLabel('Execute', d.executeMethod)}
            </div>
          </div>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} id="out" className="!bg-blue-500" />
    </>
  );
};

export default StepNode;
