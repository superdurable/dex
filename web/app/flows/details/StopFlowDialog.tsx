// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';
import { webURL } from '@/lib/basePath';
import { useModalFocus } from '../../components/useModalFocus';

const stopTypes = [
  { value: 1, label: 'Cancel', needsReason: false, completes: false, hint: 'Requests cooperative cancellation, so running Steps can clean up.' },
  { value: 2, label: 'Terminate', needsReason: true, completes: false, hint: 'Stops the run at once, without cleanup.' },
  { value: 3, label: 'Fail', needsReason: true, completes: false, hint: 'Closes the run as failed, with the reason as its error.' },
  { value: 4, label: 'Graceful complete', needsReason: false, completes: true, hint: 'Completes the Flow after active Steps finish; their decisions still apply.' },
  { value: 5, label: 'Force complete', needsReason: false, completes: true, hint: 'Completes the Flow at once and cancels active Steps.' },
];

/** Returns the JSON parse error message for text, or an empty string when text is empty or valid JSON. */
function stopOutputError(text: string): string {
  if (text.trim() === '') return '';
  try {
    JSON.parse(text);
    return '';
  } catch (parseError) {
    return parseError instanceof Error ? `Invalid JSON: ${parseError.message}` : 'Invalid JSON';
  }
}

export function StopFlowDialog({
  open,
  summary,
  onClose,
  onStopped,
}: {
  open: boolean;
  summary: FlowSummary;
  onClose: () => void;
  onStopped: () => void;
}) {
  const [stopType, setStopType] = useState(1);
  const [reason, setReason] = useState('');
  const [output, setOutput] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const selected = stopTypes.find((item) => item.value === stopType) || stopTypes[0];
  const reasonRequired = selected.needsReason;
  const outputError = selected.completes ? stopOutputError(output) : '';
  const canSubmit = !submitting && (!reasonRequired || reason.trim() !== '') && outputError === '';

  const modalRef = useModalFocus<HTMLDivElement>(open, onClose);
  if (!open) return null;

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      // The raw text keeps large integers exact; the server decodes it.
      const response = await fetch(webURL('/api/flows/stop'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowId: summary.flowId,
          runId: summary.runId,
          stopType,
          reason: selected.completes ? '' : reason,
          output: selected.completes ? output.trim() : '',
        }),
      });
      await readResponseJSON(response);
      onStopped();
      onClose();
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : 'Stop failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="modal" ref={modalRef} role="dialog" aria-modal="true" tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title-danger">Stop flow</h2>
          <button className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>Stop ends the current run. The stop type decides how it ends.</p>
        <label>
          Stop type
          <select value={stopType} onChange={(event) => setStopType(Number(event.target.value))}>
            {stopTypes.map((item) => (
              <option value={item.value} key={item.value}>{item.label}</option>
            ))}
          </select>
          <small className="muted">{selected.hint}</small>
        </label>
        {selected.completes ? (
          <label>
            Output (optional JSON)
            <textarea
              aria-invalid={outputError ? true : undefined}
              placeholder={'{"summary": "ended by operator"}'}
              rows={3}
              spellCheck={false}
              value={output}
              onChange={(event) => setOutput(event.target.value)}
            />
            {outputError
              ? <small className="field-error">{outputError}</small>
              : <small className="muted">Added to the Flow results. Leave empty for no output.</small>}
          </label>
        ) : (
          <label>
            Reason{reasonRequired ? '' : ' (optional)'}
            <textarea rows={3} value={reason} onChange={(event) => setReason(event.target.value)} />
          </label>
        )}
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Close</button>
          <button
            className="button danger"
            disabled={!canSubmit}
            onClick={() => void submit()}
          >
            {submitting ? 'Stopping…' : 'Stop flow'}
          </button>
        </div>
      </div>
    </div>
  );
}
