// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useState } from 'react';
import { Pencil } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';

const maxInt32 = 2147483647;

type DeleteExpiredChoice = 'unchanged' | 'on' | 'off';

export interface HistorySettings {
  historyWindowEvents: number | undefined;
  deleteExpiredHistoryObjects: boolean | undefined;
}

/** Reads the two history fields; an absent field means the run uses the server default. */
export function historySettingsFromFlowConfig(flowConfig: Record<string, unknown>): HistorySettings {
  const windowEvents = flowConfig.historyWindowEvents;
  const deleteExpired = flowConfig.deleteExpiredHistoryObjects;
  return {
    historyWindowEvents: typeof windowEvents === 'number' ? windowEvents : undefined,
    deleteExpiredHistoryObjects: typeof deleteExpired === 'boolean' ? deleteExpired : undefined,
  };
}

/** The history fields one save changes; an absent field is unchanged. */
export interface HistorySettingsChange {
  historyWindowEvents?: number;
  deleteExpiredHistoryObjects?: boolean;
}

export type HistorySettingsUpdate = { update: HistorySettingsChange } | { error: string };

/** Returns only changed fields; every sent field pins an override that cannot be cleared. */
export function buildHistorySettingsUpdate(
  current: HistorySettings,
  windowText: string,
  deleteChoice: DeleteExpiredChoice,
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
  if (deleteChoice !== 'unchanged') {
    const deleteExpired = deleteChoice === 'on';
    if (deleteExpired !== current.deleteExpiredHistoryObjects) {
      update.deleteExpiredHistoryObjects = deleteExpired;
    }
  }
  return { update };
}

function windowLabel(windowEvents: number | undefined): string {
  return windowEvents === undefined ? 'Server default' : `Last ${windowEvents.toLocaleString()} events`;
}

function deleteExpiredLabel(deleteExpired: boolean | undefined): string {
  if (deleteExpired === undefined) return 'Server default';
  return deleteExpired ? 'On' : 'Off';
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
  const [deleteChoice, setDeleteChoice] = useState<DeleteExpiredChoice>('unchanged');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [isValidationError, setIsValidationError] = useState(false);
  const errorId = useId();

  const isRunning = summary.flowStatus === 'Running';
  const current = flowConfig ? historySettingsFromFlowConfig(flowConfig) : undefined;
  const canEdit = isRunning && current !== undefined && onSaved !== undefined;
  const pending = current ? buildHistorySettingsUpdate(current, windowText, deleteChoice) : undefined;
  const canSave = !saving && pending !== undefined
    && ('error' in pending || Object.keys(pending.update).length > 0);

  const startEditing = () => {
    setWindowText('');
    setDeleteChoice('unchanged');
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
          <div><dt>Retention window</dt><dd>{windowLabel(current.historyWindowEvents)}</dd></div>
          <div>
            <dt>Delete expired history from object storage</dt>
            <dd>{deleteExpiredLabel(current.deleteExpiredHistoryObjects)}</dd>
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
          <label>
            Delete expired history from object storage
            <select
              value={deleteChoice}
              onChange={(event) => setDeleteChoice(event.target.value as DeleteExpiredChoice)}
            >
              <option value="unchanged">
                Unchanged: {deleteExpiredLabel(current.deleteExpiredHistoryObjects)}
              </option>
              <option value="on">On</option>
              <option value="off">Off</option>
            </select>
          </label>
          <p className="overview-note history-settings-note">
            A saved value overrides the server default for this run. It can be changed later but not
            cleared back to the server default. A smaller window prunes older events for good.
            Expired history is never readable either way; turning deletion on only deletes the
            object storage files of history the window already passed.
          </p>
          {error && <div className="error-banner" id={errorId} role="alert">{error}</div>}
          <div className="history-settings-actions">
            <button className="button ghost" disabled={saving} onClick={() => setEditing(false)}>
              Cancel
            </button>
            <button className="button" disabled={!canSave} onClick={() => void save()}>
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      )}
      {current && !editing && (
        <p className="overview-note history-settings-note">
          Saved values override the server default and cannot be cleared. Expired history is
          never readable; the switch only deletes its object storage files.
        </p>
      )}
    </section>
  );
}
