import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';

const resetTypes = [
  { value: 2, label: 'Beginning' },
  { value: 1, label: 'History event ID' },
  { value: 3, label: 'History event time' },
  { value: 4, label: 'Step type' },
  { value: 5, label: 'Step execution ID' },
];

export function ResetFlowDialog({
  open,
  summary,
  events,
  onClose,
}: {
  open: boolean;
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const [resetType, setResetType] = useState(2);
  const [target, setTarget] = useState('');
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const stepTypes = useMemo(() => {
    const values = new Set<string>();
    events.forEach((event) => {
      const execution = event.payload.execution as Record<string, unknown> | undefined;
      if (typeof execution?.stepType === 'string') values.add(execution.stepType);
    });
    return [...values];
  }, [events]);

  if (!open) return null;

  async function submit() {
    setSubmitting(true);
    setError('');
    const payload: Record<string, unknown> = {
      flowId: summary.flowId,
      runId: summary.runId,
      resetType,
      reason,
    };
    if (resetType === 1) payload.historyEventId = Number(target);
    if (resetType === 3) payload.historyEventTime = target;
    if (resetType === 4) payload.stepType = target;
    if (resetType === 5) payload.stepExecutionId = target;
    try {
      const response = await fetch('/api/flows/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await response.json() as { runId?: string; error?: string };
      if (!response.ok) throw new Error(data.error || 'Reset failed');
      navigate(`/flows/${encodeURIComponent(summary.flowId)}/${encodeURIComponent(data.runId || '')}`);
      onClose();
    } catch (resetError) {
      setError(resetError instanceof Error ? resetError.message : 'Reset failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <div><p className="eyebrow">Destructive operation</p><h2>Reset flow</h2></div>
          <button className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          Reset creates a new run from a selected Dex point. The current run remains available in history.
        </p>
        <label>
          Reset point
          <select value={resetType} onChange={(event) => {
            setResetType(Number(event.target.value));
            setTarget('');
          }}>
            {resetTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}
          </select>
        </label>
        {resetType !== 2 && (
          <label>
            Target
            {resetType === 4 ? (
              <>
                <input list="reset-step-types" value={target} onChange={(event) => setTarget(event.target.value)} />
                <datalist id="reset-step-types">{stepTypes.map((value) => <option value={value} key={value} />)}</datalist>
              </>
            ) : (
              <input
                type={resetType === 1 ? 'number' : resetType === 3 ? 'datetime-local' : 'text'}
                value={target}
                onChange={(event) => setTarget(event.target.value)}
              />
            )}
          </label>
        )}
        <label>
          Reason
          <textarea rows={3} value={reason} onChange={(event) => setReason(event.target.value)} />
        </label>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button
            className="button danger"
            disabled={submitting || !reason.trim() || (resetType !== 2 && !target)}
            onClick={() => void submit()}
          >
            {submitting ? 'Resetting…' : 'Reset flow'}
          </button>
        </div>
      </div>
    </div>
  );
}
