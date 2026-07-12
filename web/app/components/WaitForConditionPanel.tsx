'use client';

import type { ConditionNode, WaitForConditionTree } from '../api/_grpc/mappers';

// Compact, theme-friendly renderer for a WaitForCondition tree (live or
// historical). Shows the parent AnyOf/AllOf chip plus a list of children.
// Per-condition status (timer fired, channel satisfied + consumed_count)
// is rendered when present, so the same component works for both
// "current waiting state" (no result fields) and "historical evaluated
// result" (with result fields).
//
// Used by:
//   - WorkflowGraph side panel (live state on a Waiting node)
//   - EventCard for StepWaitForCompleted (the condition the worker armed)
//   - EventCard for StepExecuteCompleted (condition_results — what fired)
//   - EventCard for StepsUnblocked (per-step condition_results)
export default function WaitForConditionPanel({
  tree,
  results,
  emptyMessage,
}: {
  tree?: WaitForConditionTree | null;
  results?: ConditionNode[];
  emptyMessage?: string;
}) {
  // If we only have a result list (e.g. StepExecuteCompleted.condition_results
  // with no separate condition tree), synthesize a parent label from result
  // length so the layout stays consistent.
  const parentLabel = tree?.type ?? (results && results.length > 0 ? 'Result' : null);
  const items = tree?.conditions ?? results ?? [];

  if (!items || items.length === 0) {
    return <div className="text-xs text-gray-500">{emptyMessage ?? 'No conditions.'}</div>;
  }

  return (
    <div data-testid="wait-for-condition-panel" className="text-sm">
      {parentLabel && (
        <div className="mb-1 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wide ring-1 ring-inset bg-indigo-50 text-indigo-700 ring-indigo-200">
          {parentLabel}
        </div>
      )}
      <ul className="space-y-1.5">
        {items.map((c, i) => (
          <ConditionRow key={i} cond={c} result={results?.[i]} />
        ))}
      </ul>
    </div>
  );
}

function ConditionRow({ cond, result }: { cond: ConditionNode; result?: ConditionNode }) {
  // Prefer the condition tree as the structural source; merge in the matching
  // result entry's fired/satisfied/consumedCount when present.
  const merged: ConditionNode =
    cond.kind === 'Timer'
      ? { ...cond, fired: result && result.kind === 'Timer' ? result.fired : cond.fired }
      : cond.kind === 'Channel'
        ? {
            ...cond,
            satisfied: result && result.kind === 'Channel' ? result.satisfied : cond.satisfied,
            consumedCount: result && result.kind === 'Channel' ? result.consumedCount : cond.consumedCount,
          }
        : cond;

  if (merged.kind === 'Timer') {
    const status = statusPill(merged.fired === true, merged.fired === false ? 'pending' : undefined);
    return (
      <li className="flex items-center gap-2 text-sm">
        <span aria-hidden className="text-gray-400">⏱</span>
        <span className="text-gray-700">{relativeTimerLabel(merged.fireAtUnixMs, merged.fired === true)}</span>
        {status}
      </li>
    );
  }
  // Channel
  const range = merged.max && merged.max > merged.min ? `${merged.min}..${merged.max}` : `${merged.min}`;
  const status = statusPill(merged.satisfied === true, merged.satisfied === false ? 'pending' : undefined);
  return (
    <li className="flex items-center gap-2 text-sm">
      <span aria-hidden className="text-gray-400">≋</span>
      <code className="text-xs font-mono bg-gray-100 px-1 rounded">{merged.channelName}</code>
      <span className="text-xs text-gray-500">need {range}</span>
      {typeof merged.consumedCount === 'number' && merged.consumedCount > 0 && (
        <span className="text-xs text-gray-500">consumed {merged.consumedCount}</span>
      )}
      {status}
    </li>
  );
}

function statusPill(positive: boolean, fallback?: 'pending') {
  if (positive) {
    return (
      <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ring-1 ring-inset bg-green-50 text-green-700 ring-green-200">
        satisfied
      </span>
    );
  }
  if (fallback === 'pending') {
    return (
      <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ring-1 ring-inset bg-gray-50 text-gray-700 ring-gray-200">
        pending
      </span>
    );
  }
  return null;
}

function relativeTimerLabel(fireAtUnixMs: number, fired: boolean): string {
  if (!fireAtUnixMs) return 'timer';
  const absolute = formatLocalTime(fireAtUnixMs);
  const delta = fireAtUnixMs - Date.now();
  if (fired) {
    const ago = Math.max(0, -delta);
    // Past + we know it fired: stamp the actual fire time and a short
    // "how long ago" hint for context.
    return `fired at ${absolute} (${formatDuration(ago)} ago)`;
  }
  if (delta <= 0) {
    // Past but the worker hasn't reported it as fired yet (engine evaluate-only
    // path, mid-flight to the worker, or AllOf still gated on a sibling
    // channel). Stamp the fire time and call out that it's overdue.
    return `fires at ${absolute} (overdue ${formatDuration(-delta)})`;
  }
  // Future: anchor on the absolute time so the user can correlate with logs,
  // append a relative hint so "in 12s" is obvious without doing math.
  return `fires at ${absolute} (in ${formatDuration(delta)})`;
}

// formatLocalTime renders an absolute Unix-millis timestamp as the viewer's
// local clock time. Includes the date prefix when the fire time is more
// than ~12h away from now so cross-day timers don't read as misleading
// "fires at 09:15" entries.
function formatLocalTime(ms: number): string {
  const d = new Date(ms);
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  const time = d.toLocaleTimeString(undefined, { hour12: false });
  if (sameDay) return time;
  // Short locale date + time for cross-day; consistent across envs because
  // we don't pass a locale (uses runtime default).
  return `${d.toLocaleDateString()} ${time}`;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}
