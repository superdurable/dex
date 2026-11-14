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
import { webURL } from '@/lib/basePath';

const maxInt32 = 2147483647;
const retentionWindowHint = 'Older events are deleted.';
const SERVER_DEFAULT = 'Server default';

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

/** The Flow config fields one save changes, keyed as in flowConfig; UpdateFlowConfig applies these four. */
export interface FlowConfigChange {
  historyWindowEvents?: number;
  stepDurability?: number;
  activeStepSearchMode?: number;
  attributeStoreNames?: { names: string[] };
}

export type HistorySettingsUpdate = { update: FlowConfigChange } | { error: string };

/** Returns only changed fields; every sent field pins an override that cannot be cleared. */
export function buildHistorySettingsUpdate(
  current: HistorySettings,
  windowText: string,
): HistorySettingsUpdate {
  const update: FlowConfigChange = {};
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

const stepDurabilityLabels: Record<number, string> = { 1: 'Sync', 2: 'Async' };
const activeStepSearchModeLabels: Record<number, string> = {
  1: 'All Steps',
  2: 'Steps with WaitFor',
  3: 'Off',
};

function windowLabel(windowEvents: number | undefined): string {
  return windowEvents === undefined ? SERVER_DEFAULT : `Last ${windowEvents.toLocaleString()} events`;
}

/** Reads an enum field; protojson omits an unset field, and 0 is the unspecified value. */
function enumValue(flowConfig: Record<string, unknown>, key: string): number | undefined {
  const value = flowConfig[key];
  return typeof value === 'number' && value > 0 ? value : undefined;
}

/** Reads the Attribute Store names; an empty list turns synchronization off, and absent means the server default. */
function attributeStoreNamesOf(flowConfig: Record<string, unknown>): string[] | undefined {
  const value = flowConfig.attributeStoreNames;
  if (!value || typeof value !== 'object') return undefined;
  const names = (value as { names?: unknown }).names;
  return Array.isArray(names) ? names.filter((name): name is string => typeof name === 'string') : [];
}

function attributeStoresLabel(names: string[] | undefined): string {
  if (names === undefined) return SERVER_DEFAULT;
  return names.length === 0 ? 'None, synchronization off' : names.join(', ');
}

function offloadHistoryLabel(flowConfig: Record<string, unknown>): string {
  const value = flowConfig.offloadHistory;
  if (value === false) return 'Off';
  return value === true ? 'On' : 'On (default)';
}

/** An info icon whose hint shows on hover and keyboard focus. */
function InfoHint({ text }: { text: string }) {
  return (
    <span aria-label={text} className="info-hint" data-hint={text} role="img" tabIndex={0}>
      <Info aria-hidden="true" size={14} strokeWidth={2} />
    </span>
  );
}

type EditableField = 'historyWindowEvents' | 'stepDurability' | 'activeStepSearchMode' | 'attributeStoreNames';

const fieldLabels: Record<EditableField, string> = {
  historyWindowEvents: 'Retention window',
  stepDurability: 'Step durability',
  activeStepSearchMode: 'Active step search',
  attributeStoreNames: 'Attribute stores',
};

/** The run's Flow config in plain words; fields UpdateFlowConfig changes carry Edit while the run is active. */
export function FlowConfigCard({
  summary,
  flowConfig,
  onSaved,
}: {
  summary: FlowSummary;
  /** The run's flowConfig, or undefined while it is not loaded. */
  flowConfig: Record<string, unknown> | undefined;
  /** Receives the changed fields and the run they were saved to. */
  onSaved?: (saved: FlowConfigChange, savedRun: FlowSummary) => Promise<void>;
}) {
  const [editing, setEditing] = useState<EditableField | null>(null);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [isValidationError, setIsValidationError] = useState(false);
  const errorId = useId();
  const isRunning = summary.flowStatus === 'Running';
  const canEdit = isRunning && flowConfig !== undefined && onSaved !== undefined;

  if (!flowConfig) {
    return (
      <section className="overview-section history-settings-card">
        <header className="overview-section-head"><h2>Flow config</h2></header>
        <p className="overview-quiet">The run&apos;s Flow config is not loaded.</p>
      </section>
    );
  }

  const windowEvents = historySettingsFromFlowConfig(flowConfig).historyWindowEvents;
  const stepDurability = enumValue(flowConfig, 'stepDurability');
  const activeStepSearchMode = enumValue(flowConfig, 'activeStepSearchMode');
  const attributeStoreNames = attributeStoreNamesOf(flowConfig);

  const startEditing = (field: EditableField) => {
    const initialDrafts: Record<EditableField, string> = {
      historyWindowEvents: '',
      stepDurability: String(stepDurability ?? 1),
      activeStepSearchMode: String(activeStepSearchMode ?? 1),
      attributeStoreNames: attributeStoreNames?.join(', ') ?? '',
    };
    setDraft(initialDrafts[field]);
    setError('');
    setIsValidationError(false);
    setEditing(field);
  };

  /** Returns the change the draft makes, null for no change, or an error. */
  const pendingChange = (field: EditableField): FlowConfigChange | null | { error: string } => {
    if (field === 'historyWindowEvents') {
      const result = buildHistorySettingsUpdate({ historyWindowEvents: windowEvents }, draft);
      if ('error' in result) return result;
      return result.update.historyWindowEvents === undefined ? null : result.update;
    }
    if (field === 'stepDurability') {
      const value = Number(draft);
      return value === stepDurability ? null : { stepDurability: value };
    }
    if (field === 'activeStepSearchMode') {
      const value = Number(draft);
      return value === activeStepSearchMode ? null : { activeStepSearchMode: value };
    }
    const names = draft.split(',').map((name) => name.trim()).filter(Boolean);
    if (attributeStoreNames !== undefined && names.join(',') === attributeStoreNames.join(',')) return null;
    return { attributeStoreNames: { names } };
  };

  async function save(field: EditableField) {
    if (!onSaved) return;
    const change = pendingChange(field);
    if (change === null) return;
    if ('error' in change) {
      setError(change.error);
      setIsValidationError(true);
      return;
    }
    const savedRun = summary;
    const { attributeStoreNames: stores, ...scalarFields } = change;
    setSaving(true);
    setError('');
    setIsValidationError(false);
    try {
      await readResponseJSON(await fetch(webURL('/api/flows/config'), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowId: savedRun.flowId,
          runId: savedRun.runId,
          ...scalarFields,
          ...(stores ? { attributeStoreNames: stores.names } : {}),
        }),
      }));
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : 'Flow config update failed');
      setSaving(false);
      return;
    }
    setSaving(false);
    setEditing(null);
    await onSaved(change, savedRun);
  }

  const editor = (field: EditableField) => {
    const label = fieldLabels[field];
    const change = pendingChange(field);
    const canSave = !saving && change !== null;
    const describedBy = isValidationError ? errorId : undefined;
    return (
      <div className="history-settings-form">
        {field === 'historyWindowEvents' && (
          <label>
            Retention window (events)
            <input
              inputMode="numeric"
              aria-invalid={isValidationError || undefined}
              aria-describedby={describedBy}
              placeholder={`Unchanged: ${windowLabel(windowEvents)}`}
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
            />
          </label>
        )}
        {(field === 'stepDurability' || field === 'activeStepSearchMode') && (
          <label>
            {label}
            <select value={draft} onChange={(event) => setDraft(event.target.value)}>
              {Object.entries(field === 'stepDurability' ? stepDurabilityLabels : activeStepSearchModeLabels)
                .map(([value, optionLabel]) => <option key={value} value={value}>{optionLabel}</option>)}
            </select>
          </label>
        )}
        {field === 'attributeStoreNames' && (
          <label>
            Attribute stores (comma-separated)
            <input
              placeholder="Empty turns synchronization off"
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
            />
          </label>
        )}
        <p className="overview-note history-settings-note">
          {field === 'historyWindowEvents'
            ? 'A saved value overrides the server default and cannot be cleared. A smaller window deletes older events for good.'
            : 'A saved value overrides the server default and cannot be cleared.'}
        </p>
        {error && <div className="error-banner" id={errorId} role="alert">{error}</div>}
        <div className="history-settings-actions">
          <button className="button ghost" disabled={saving} onClick={() => setEditing(null)} type="button">
            Cancel
          </button>
          <button className="button primary" disabled={!canSave} onClick={() => void save(field)} type="button">
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    );
  };

  const row = (field: EditableField | 'offloadHistory', value: string, hint?: string) => {
    const label = field === 'offloadHistory' ? 'Offload history' : fieldLabels[field];
    const isEditable = field !== 'offloadHistory' && canEdit;
    return (
      <div key={field}>
        <dt>{label}{hint && <> <InfoHint text={hint} /></>}</dt>
        <dd>
          {field !== 'offloadHistory' && editing === field ? editor(field) : (
            <span className="flow-config-value">
              {value}
              {isEditable && editing === null && (
                <button
                  aria-label={`Edit ${label}`}
                  className="button quiet flow-config-edit"
                  onClick={() => startEditing(field)}
                  title={`Edit ${label}`}
                  type="button"
                >
                  <Pencil aria-hidden="true" size={13} strokeWidth={2} />
                  Edit
                </button>
              )}
            </span>
          )}
        </dd>
      </div>
    );
  };

  return (
    <section className="overview-section history-settings-card">
      <header className="overview-section-head"><h2>Flow config</h2></header>
      <dl className="definition-list flow-config-list">
        {row('historyWindowEvents', windowLabel(windowEvents), retentionWindowHint)}
        {row('stepDurability', stepDurability ? stepDurabilityLabels[stepDurability] ?? String(stepDurability) : SERVER_DEFAULT)}
        {row(
          'activeStepSearchMode',
          activeStepSearchMode ? activeStepSearchModeLabels[activeStepSearchMode] ?? String(activeStepSearchMode) : SERVER_DEFAULT,
        )}
        {row('attributeStoreNames', attributeStoresLabel(attributeStoreNames))}
        {row('offloadHistory', offloadHistoryLabel(flowConfig), 'Set when the run starts.')}
      </dl>
      {!isRunning && <p className="overview-note">The run is closed, so its Flow config no longer changes.</p>}
    </section>
  );
}
