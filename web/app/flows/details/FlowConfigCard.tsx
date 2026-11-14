// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useState } from 'react';
import { Info, Pencil } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';
import { webURL } from '@/lib/basePath';

const maxInt32 = 2147483647;
const CHANGE_NOT_CLEAR = 'Once set, it can be changed but not cleared.';

/** What each field does for the run, then whether it can change; from FlowConfig in protos/dex.proto. */
export const flowConfigFieldHints = {
  historyWindowEvents: 'About how many of the newest history events this run keeps for history reads and Time Travel. '
    + `Older events are pruned. ${CHANGE_NOT_CLEAR}`,
  stepDurability: 'The durability of Step methods that set none of their own. '
    + 'Sync waits for each result to commit before the run moves on; Async moves on first. '
    + `A change applies to method calls scheduled after it. ${CHANGE_NOT_CLEAR}`,
  activeStepSearchMode: 'Which active Steps this run indexes for search: every active Step, only Steps that ran WaitFor, '
    + `or none. ${CHANGE_NOT_CLEAR}`,
  attributeStoreNames: 'Which Dex Server Attribute stores receive this run\'s Attributes that opt in to sync. '
    + `Empty turns sync off. ${CHANGE_NOT_CLEAR}`,
  offloadHistory: 'Whether Dex moves this run\'s history to object storage and keeps only recent history on local disk. '
    + 'Chosen when the run starts and fixed for the run.',
} as const;
const SERVER_DEFAULT = 'Server default';
/** The server's history.windowEvents default, from FlowConfig.history_window_events in protos/dex.proto. */
const BUILT_IN_DEFAULT_WINDOW_EVENTS = 10_000;

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

function enumLabels(field: 'stepDurability' | 'activeStepSearchMode'): Record<number, string> {
  return field === 'stepDurability' ? stepDurabilityLabels : activeStepSearchModeLabels;
}

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
  loadError,
  onRetryLoad,
  onSaved,
}: {
  summary: FlowSummary;
  /** The run's flowConfig, or undefined while it is not loaded. */
  flowConfig: Record<string, unknown> | undefined;
  /** Why the flowConfig could not be loaded; shown with Retry while flowConfig is undefined. */
  loadError?: string;
  onRetryLoad?: () => void;
  /** Receives the changed fields and the run they were saved to. */
  onSaved?: (saved: FlowConfigChange, savedRun: FlowSummary) => Promise<void>;
}) {
  const [editing, setEditing] = useState<EditableField | null>(null);
  const [draft, setDraft] = useState('');
  const [isTurningSyncOff, setIsTurningSyncOff] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const hintId = useId();
  const nameIdPrefix = useId();
  const nameIdOf = (field: EditableField | 'offloadHistory') => `${nameIdPrefix}-${field}`;
  const isRunning = summary.flowStatus === 'Running';
  const canEdit = isRunning && flowConfig !== undefined && onSaved !== undefined;

  // A run that stops running drops any open editor and its draft.
  useEffect(() => {
    if (!canEdit) setEditing(null);
  }, [canEdit]);

  if (!flowConfig) {
    return (
      <section className="overview-section history-settings-card">
        <header className="overview-section-head"><h2>Configuration</h2></header>
        {loadError ? (
          <>
            <div className="error-banner" role="alert">{loadError}</div>
            {onRetryLoad && (
              <div className="history-settings-actions">
                <button className="button ghost" onClick={onRetryLoad} type="button">Retry</button>
              </div>
            )}
          </>
        ) : <p className="overview-quiet">The run&apos;s configuration is not loaded.</p>}
      </section>
    );
  }

  const windowEvents = historySettingsFromFlowConfig(flowConfig).historyWindowEvents;
  const stepDurability = enumValue(flowConfig, 'stepDurability');
  const activeStepSearchMode = enumValue(flowConfig, 'activeStepSearchMode');
  const attributeStoreNames = attributeStoreNamesOf(flowConfig);

  // A new draft clears the last save's error, which named the old value.
  const changeDraft = (value: string) => {
    setDraft(value);
    setError('');
  };

  const startEditing = (field: EditableField) => {
    // Each editor opens on the current value; an unset field opens with nothing chosen.
    const initialDrafts: Record<EditableField, string> = {
      historyWindowEvents: windowEvents === undefined ? '' : String(windowEvents),
      stepDurability: stepDurability === undefined ? '' : String(stepDurability),
      activeStepSearchMode: activeStepSearchMode === undefined ? '' : String(activeStepSearchMode),
      attributeStoreNames: attributeStoreNames?.join(', ') ?? '',
    };
    setDraft(initialDrafts[field]);
    setIsTurningSyncOff(attributeStoreNames?.length === 0);
    setError('');
    setEditing(field);
  };

  /** Returns the change the draft makes, null while nothing changed, or why the draft cannot be saved. */
  const pendingChange = (field: EditableField): FlowConfigChange | null | { error: string } => {
    if (field === 'historyWindowEvents') {
      if (draft.trim() === '') return null;
      const result = buildHistorySettingsUpdate({ historyWindowEvents: windowEvents }, draft);
      if ('error' in result) return result;
      return result.update.historyWindowEvents === undefined ? null : result.update;
    }
    if (field === 'stepDurability' || field === 'activeStepSearchMode') {
      if (draft === '') return null;
      const value = Number(draft);
      const current = field === 'stepDurability' ? stepDurability : activeStepSearchMode;
      if (value === current) return null;
      return field === 'stepDurability' ? { stepDurability: value } : { activeStepSearchMode: value };
    }
    if (isTurningSyncOff) {
      return attributeStoreNames?.length === 0 ? null : { attributeStoreNames: { names: [] } };
    }
    const names = draft.split(',').map((name) => name.trim()).filter(Boolean);
    if (names.length === 0) {
      return draft.trim() === '' && attributeStoreNames === undefined
        ? null
        : { error: 'Enter at least one store name, or choose Turn sync off.' };
    }
    if (new Set(names).size !== names.length) return { error: 'Each store name may appear once.' };
    if (attributeStoreNames !== undefined && names.join(',') === attributeStoreNames.join(',')) return null;
    return { attributeStoreNames: { names } };
  };

  async function save(field: EditableField) {
    const change = pendingChange(field);
    if (!onSaved || change === null || 'error' in change) return;
    const savedRun = summary;
    const { attributeStoreNames: stores, ...scalarFields } = change;
    setSaving(true);
    setError('');
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
      setError(saveError instanceof Error ? saveError.message : 'Configuration update failed');
      setSaving(false);
      return;
    }
    setSaving(false);
    setEditing(null);
    await onSaved(change, savedRun);
  }

  const editor = (field: EditableField, currentValue: string) => {
    const change = pendingChange(field);
    const invalid = change !== null && 'error' in change ? change.error : '';
    const canSave = canEdit && !saving && change !== null && !invalid;
    const draftWindow = Number(draft.trim());
    // An unset window uses the server default, which is 10,000 events unless the server sets another.
    const isShrinkingWindow = field === 'historyWindowEvents' && canSave && Number.isFinite(draftWindow)
      && draftWindow < (windowEvents ?? BUILT_IN_DEFAULT_WINDOW_EVENTS);
    return (
      <div className="history-settings-form flow-config-editor">
        {field === 'historyWindowEvents' && (
          <input
            aria-describedby={hintId}
            aria-invalid={invalid ? true : undefined}
            aria-labelledby={nameIdOf(field)}
            inputMode="numeric"
            placeholder="e.g. 10000"
            value={draft}
            onChange={(event) => changeDraft(event.target.value)}
          />
        )}
        {(field === 'stepDurability' || field === 'activeStepSearchMode') && (
          <select
            aria-describedby={hintId}
            aria-labelledby={nameIdOf(field)}
            value={draft}
            onChange={(event) => changeDraft(event.target.value)}
          >
            {draft === '' && <option disabled value="">Choose a value</option>}
            {draft !== '' && !(draft in enumLabels(field)) && (
              <option disabled value={draft}>Unknown ({draft})</option>
            )}
            {Object.entries(enumLabels(field))
              .map(([value, optionLabel]) => <option key={value} value={value}>{optionLabel}</option>)}
          </select>
        )}
        {field === 'attributeStoreNames' && (
          <>
            <input
              aria-describedby={hintId}
              aria-invalid={invalid ? true : undefined}
              aria-labelledby={nameIdOf(field)}
              disabled={isTurningSyncOff}
              placeholder="e.g. primary, audit"
              value={draft}
              onChange={(event) => changeDraft(event.target.value)}
            />
            <label className="flow-config-check">
              <input
                checked={isTurningSyncOff}
                onChange={(event) => {
                  setIsTurningSyncOff(event.target.checked);
                  setError('');
                }}
                type="checkbox"
              />
              Turn sync off for this run
            </label>
          </>
        )}
        <div className="flow-config-editor-notes" id={hintId}>
          {field === 'historyWindowEvents' && <p className="overview-note">Number of events.</p>}
          {field === 'attributeStoreNames' && <p className="overview-note">Comma-separated store names.</p>}
          {invalid && <p className="flow-config-invalid">{invalid}</p>}
          {isShrinkingWindow && (
            <p className="flow-config-warning">
              {windowEvents === undefined
                ? `This window is smaller than the built-in server default of ${BUILT_IN_DEFAULT_WINDOW_EVENTS.toLocaleString()} events, so older events may be deleted for good.`
                : `This window is smaller than ${windowEvents.toLocaleString()} events, so older events are deleted for good.`}
            </p>
          )}
          {field === 'attributeStoreNames' && isTurningSyncOff && (
            <p className="flow-config-warning">This run&apos;s Attributes stop syncing to any store.</p>
          )}
          <p className="overview-note">Current value: {currentValue}. A saved value cannot return to Server default later.</p>
        </div>
        {error && <div className="error-banner" role="alert">{error}</div>}
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
    const isEditing = field !== 'offloadHistory' && canEdit && editing === field;
    return (
      <div className={isEditing ? 'is-editing' : undefined} key={field}>
        <dt><span id={nameIdOf(field)}>{label}</span>{hint && <> <InfoHint text={hint} /></>}</dt>
        <dd>
          {isEditing ? editor(field, value) : (
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
      <header className="overview-section-head"><h2>Configuration</h2></header>
      <dl className="definition-list flow-config-list">
        {row('historyWindowEvents', windowLabel(windowEvents), flowConfigFieldHints.historyWindowEvents)}
        {row(
          'stepDurability',
          stepDurability ? stepDurabilityLabels[stepDurability] ?? String(stepDurability) : SERVER_DEFAULT,
          flowConfigFieldHints.stepDurability,
        )}
        {row(
          'activeStepSearchMode',
          activeStepSearchMode ? activeStepSearchModeLabels[activeStepSearchMode] ?? String(activeStepSearchMode) : SERVER_DEFAULT,
          flowConfigFieldHints.activeStepSearchMode,
        )}
        {row('attributeStoreNames', attributeStoresLabel(attributeStoreNames), flowConfigFieldHints.attributeStoreNames)}
        {row('offloadHistory', offloadHistoryLabel(flowConfig), flowConfigFieldHints.offloadHistory)}
      </dl>
      {!isRunning && <p className="overview-note">The run is closed, so its configuration no longer changes.</p>}
    </section>
  );
}
