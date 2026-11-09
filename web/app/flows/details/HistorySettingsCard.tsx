// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useState } from 'react';
import { Info, Pencil } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';

const maxInt32 = 2147483647;
const retentionWindowHint = 'Older events are deleted.';

export interface HistorySettings {
  historyWindowEvents: number | undefined;
}

/** Reads the history window; an absent field means the run uses the server default. */
export function historySettingsFromFlowConfig(flowConfig: Record<string, unknown>): HistorySettings {
  const windowEvents = flowConfig.historyWindowEvents;
  return {
    historyWindowEvents: typeof windowEvents === 'number' ? windowEvents : undefined,
  };
}

/** The history fields one save changes; an absent field is unchanged. */
export interface HistorySettingsChange {
  historyWindowEvents?: number;
}

export type HistorySettingsUpdate = { update: HistorySettingsChange } | { error: string };

/** Returns only changed fields; every sent field pins an override that cannot be cleared. */
export function buildHistorySettingsUpdate(
  current: HistorySettings,
  windowText: string,
): HistorySettingsUpdate {
  const update: HistorySettingsChange = {};
  const trimmedWindow = windowText.trim();
  if (trimmedWindow !== '') {
    const windowEvents = /^\d+$/.test(trimmedWindow) ? Number(trimmedWindow) : Number.NaN;
    if (!Number.isSafeInteger(windowEvents) || windowEvents <= 0 || windowEvents > maxInt32) {
      return { error: 'Enter a positive whole number of events.' };
    }
    if (windowEvents !== current.historyWindowEvents) update.historyWindowEvents = windowEvents;
  }
  return { update };
}

function windowLabel(windowEvents: number | undefined): string {
  return windowEvents === undefined ? 'Server default' : `Last ${windowEvents.toLocaleString()} events`;
}

/** An info icon whose hint shows on hover and keyboard focus. */
function InfoHint({ text }: { text: string }) {
  return (
    <span aria-label={text} className="info-hint" data-hint={text} role="img" tabIndex={0}>
      <Info aria-hidden="true" size={14} strokeWidth={2} />
    </span>
  );
}

export function HistorySettingsCard({
  summary,
  flowConfig,
  onSaved,
}: {
  summary: FlowSummary;
  flowConfig: Record<string, unknown> | undefined;
  /** Receives the changed fields and the run they were saved to. */
  onSaved?: (saved: HistorySettingsChange, savedRun: FlowSummary) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [windowText, setWindowText] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [isValidationError, setIsValidationError] = useState(false);
  const errorId = useId();

  const isRunning = summary.flowStatus === 'Running';
  const current = flowConfig ? historySettingsFromFlowConfig(flowConfig) : undefined;
  const canEdit = isRunning && current !== undefined && onSaved !== undefined;
  const pending = current ? buildHistorySettingsUpdate(current, windowText) : undefined;
  const canSave = !saving && pending !== undefined
    && ('error' in pending || Object.keys(pending.update).length > 0);

  const startEditing = () => {
    setWindowText('');
    setError('');
    setIsValidationError(false);
    setEditing(true);
  };

  async function save() {
    if (!pending || !onSaved) return;
    const savedRun = summary;
    if ('error' in pending) {
      setError(pending.error);
      setIsValidationError(true);
      return;
    }
    setSaving(true);
    setError('');
    setIsValidationError(false);
    try {
      await readResponseJSON(await fetch('/api/flows/config/history', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ flowId: savedRun.flowId, runId: savedRun.runId, ...pending.update }),
      }));
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : 'History settings update failed');
      setSaving(false);
      return;
    }
    setSaving(false);
    setEditing(false);
    await onSaved(pending.update, savedRun);
  }

  return (
    <section className="overview-section history-settings-card">
      <header className="overview-section-head">
        <h2>History settings</h2>
        {canEdit && !editing && (
          <div className="overview-section-actions">
            <button className="button quiet" onClick={startEditing} type="button">
              <Pencil aria-hidden="true" size={14} strokeWidth={2} />
              Edit
            </button>
          </div>
        )}
      </header>
      {!current && (
        <p className="overview-quiet">History settings are shown and edited only while this run is active.</p>
      )}
      {current && !editing && (
        <dl className="definition-list">
          <div>
            <dt>Retention window <InfoHint text={retentionWindowHint} /></dt>
            <dd>{windowLabel(current.historyWindowEvents)}</dd>
          </div>
        </dl>
      )}
      {current && editing && (
        <div className="history-settings-form">
          <label>
            Retention window (events)
            <input
              inputMode="numeric"
              aria-invalid={isValidationError || undefined}
              aria-describedby={isValidationError ? errorId : undefined}
              placeholder={`Unchanged: ${windowLabel(current.historyWindowEvents)}`}
              value={windowText}
              onChange={(event) => setWindowText(event.target.value)}
            />
          </label>
          <p className="overview-note history-settings-note">
            A saved value overrides the server default and cannot be cleared. A smaller window
            deletes older events for good.
          </p>
          {error && <div className="error-banner" id={errorId} role="alert">{error}</div>}
          <div className="history-settings-actions">
            <button className="button ghost" disabled={saving} onClick={() => setEditing(false)} type="button">
              Cancel
            </button>
            <button className="button primary" disabled={!canSave} onClick={() => void save()} type="button">
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
