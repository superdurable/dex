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

export type HistorySettingsUpdate =
  | { update: { historyWindowEvents?: number; deleteExpiredHistoryObjects?: boolean } }
  | { error: string };

/** Returns only changed fields; every sent field pins an override that cannot be cleared. */
export function buildHistorySettingsUpdate(
  current: HistorySettings,
  windowText: string,
  deleteChoice: DeleteExpiredChoice,
): HistorySettingsUpdate {
  const update: { historyWindowEvents?: number; deleteExpiredHistoryObjects?: boolean } = {};
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
  return windowEvents === undefined ? 'Server default' : `${windowEvents.toLocaleString()} events`;
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
  onSaved?: () => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [windowText, setWindowText] = useState('');
  const [deleteChoice, setDeleteChoice] = useState<DeleteExpiredChoice>('unchanged');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

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
    setEditing(true);
  };

  async function save() {
    if (!pending || !onSaved) return;
    if ('error' in pending) {
      setError(pending.error);
      return;
    }
    setSaving(true);
    setError('');
    try {
      await readResponseJSON(await fetch('/api/flows/config/history', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ flowId: summary.flowId, runId: summary.runId, ...pending.update }),
      }));
      await onSaved();
      setEditing(false);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : 'History settings update failed');
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="card overview-card history-settings-card">
      <div className="section-heading history-settings-heading">
        <div><p className="eyebrow">Run settings</p><h2>History</h2></div>
        {canEdit && !editing && (
          <button className="button ghost live-state-write-button" onClick={startEditing}>
            Edit
          </button>
        )}
      </div>
      {!current && (
        <p className="muted">History settings are shown and edited only while this run is active.</p>
      )}
      {current && !editing && (
        <dl className="definition-list">
          <div><dt>History window</dt><dd>{windowLabel(current.historyWindowEvents)}</dd></div>
          <div>
            <dt>Delete expired history objects</dt>
            <dd>{deleteExpiredLabel(current.deleteExpiredHistoryObjects)}</dd>
          </div>
        </dl>
      )}
      {current && editing && (
        <div className="history-settings-form">
          <label>
            History window (events)
            <input
              inputMode="numeric"
              placeholder={`Unchanged: ${windowLabel(current.historyWindowEvents)}`}
              value={windowText}
              onChange={(event) => setWindowText(event.target.value)}
            />
          </label>
          <label>
            Delete expired history objects
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
          {error && <div className="error-banner">{error}</div>}
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
      {current && (
        <p className="muted history-settings-note">
          A saved value overrides the server default for this run. It can be changed later but not
          cleared back to the server default. A smaller window prunes older events for good, and
          turning deletion on deletes history objects the window already passed.
        </p>
      )}
    </section>
  );
}
