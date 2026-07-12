// Mirrors persistence.RunStatus in server/internal/persistence/interfaces.go.
export const RUN_STATUSES: { value: number; label: string }[] = [
  { value: 0, label: 'Pending' },
  { value: 1, label: 'WaitingForWorker' },
  { value: 2, label: 'Running' },
  { value: 3, label: 'AllStepsWaiting' },
  { value: 4, label: 'Completed' },
  { value: 5, label: 'Failed' },
];

export function runStatusName(n: number | undefined | null): string {
  const found = RUN_STATUSES.find((s) => s.value === n);
  return found ? found.label : `Unknown(${n ?? '?'})`;
}

// Mirrors pb.StopDecision.
const STOP_DECISIONS = ['NONE', 'COMPLETE', 'FAIL', 'DEAD_END'];

export const STOP_DECISION_FAIL = 2;

export function stopDecisionName(n: number | undefined | null): string {
  if (typeof n !== 'number' || n < 0 || n >= STOP_DECISIONS.length) return `Unknown(${n ?? '?'})`;
  return STOP_DECISIONS[n];
}

export function formatTimestamp(ms: number | undefined | null): string {
  if (!ms || !Number.isFinite(ms)) return '—';
  const d = new Date(ms);
  // Show local time in a readable form. We avoid Intl.DateTimeFormat options
  // that would force a specific locale to keep output stable across machines.
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
