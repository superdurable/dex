// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useState, type ReactNode } from 'react';
import type {
  FlowV2Action,
  FlowV2ActionInputField,
  FlowV2Definition,
  V2ValueType,
} from '@superdurable/flow-definition-renderer';
import { displayValue } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import type { V2Display } from '@/lib/types';
import { parseTypedValue, v2ActionUserFields, v2ActionUserInput, visibleV2Actions } from '../contract';
import { WORK_QUEUE_COPY } from '../work-queue/copy';
import { absorb, classifyReadFailure, nothingHeld, readFailureReason } from '../work-queue/liveness';
import { RUN_COPY } from '../run/copy';
import { leadFields } from './uiSlots';

export function SelectedRunPanel({
  flowType,
  flowId,
  definition,
  flowStatusCode,
  footer,
  reloadKey = 0,
  order,
  showHeading = true,
  onActed,
  onStranded,
}: {
  flowType: string;
  flowId: string;
  definition: FlowV2Definition;
  /** From the search row, so a dead worker can be told apart from a closed run. */
  flowStatusCode?: number;
  footer?: ReactNode;
  /** Bumped by the view's shared clock, so the fields and the canvas move together. */
  reloadKey?: number;
  /**
   * Admin already knows the case, so Run puts the decision first. A participant needs the
   * evidence before the decision, so the Queue reads the other way.
   */
  order: 'actions-first' | 'evidence-first';
  /** False when the host already names the run, so it is not named twice. */
  showHeading?: boolean;
  /** An Action succeeded, so whatever the run was waiting for has moved on. */
  onActed?: () => void;
  /** Reported up so the list can mark the row; a search cannot discover this. */
  onStranded?: (flowID: string) => void;
}) {
  const [held, setHeld] = useState(() => nothingHeld<V2Display>());
  /** A write that failed is not a stale read, so it does not touch held. */
  const [actionError, setActionError] = useState('');
  const [busyKey, setBusyKey] = useState('');
  const [editingKey, setEditingKey] = useState('');
  const [editValue, setEditValue] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [actionValues, setActionValues] = useState<Record<string, Record<string, string>>>({});

  const loadDisplay = useCallback(async () => {
    try {
      const query = new URLSearchParams({ flowType, flowId });
      const response = await fetch(`/api/v2/display?${query}`);
      const display = await readResponseJSON<V2Display>(response);
      setHeld((prior) => absorb(prior, { state: 'ok', value: display }));
    } catch (loadError) {
      // Only knowable once somebody opens the run: a search says nothing about its worker.
      const outcome = classifyReadFailure<V2Display>(loadError, flowStatusCode);
      if (outcome.state === 'stranded') onStranded?.(flowId);
      setHeld((prior) => absorb(prior, outcome));
    }
  }, [flowId, flowStatusCode, flowType, onStranded]);

  // A new run must not inherit the previous run's values while its own read is in flight.
  useEffect(() => { setHeld(nothingHeld<V2Display>()); }, [flowId, flowType]);

  useEffect(() => { void loadDisplay(); }, [loadDisplay, reloadKey]);

  const result = held.value;
  const isStranded = held.liveness === 'stranded';

  async function saveField(attributeKey: string, valueType: V2ValueType) {
    setBusyKey(attributeKey);
    setActionError('');
    setFieldErrors((current) => ({ ...current, [attributeKey]: '' }));
    try {
      const response = await fetch('/api/v2/display', {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType, flowId, attributeKey,
          value: parseTypedValue(editValue, valueType),
        }),
      });
      await readResponseJSON(response);
      setEditingKey('');
      await loadDisplay();
    } catch (saveError) {
      setFieldErrors((current) => ({
        ...current,
        [attributeKey]: saveError instanceof Error ? saveError.message : 'Edit failed',
      }));
    } finally {
      setBusyKey('');
    }
  }

  async function invokeAction(action: FlowV2Action) {
    setBusyKey(action.rpcName);
    setActionError('');
    try {
      const input = v2ActionUserInput(action, actionValues[action.rpcName] ?? {});
      const response = await fetch('/api/v2/actions', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType, flowId, rpcName: action.rpcName,
          input, attributeSnapshot: result?.attributeSnapshot ?? {},
        }),
      });
      await readResponseJSON(response);
      await loadDisplay();
      onActed?.();
    } catch (actionError) {
      setActionError(readFailureReason(actionError));
    } finally {
      setBusyKey('');
    }
  }

  return (
    <div className="v2-case sc">
      {(showHeading || footer || held.liveness === 'stale') && (
        <div className="sc-head">
          {showHeading && <span className="sc-title">{flowId}</span>}
          {showHeading && result && <span className="sc-status">{result.flowStatus}</span>}
          {held.liveness === 'stale' && <span className="sc-stale">{WORK_QUEUE_COPY.staleShort}</span>}
          {footer}
        </div>
      )}
      {isStranded && <p className="sc-state" data-liveness="stranded">{WORK_QUEUE_COPY.stranded}</p>}
      {held.liveness === 'unreachable' && <p className="v2-error">{held.reason}</p>}
      {held.liveness === 'stale' && <p className="sc-why">{held.reason}</p>}
      {held.liveness === 'loading' && <p className="sc-state">{WORK_QUEUE_COPY.loading}</p>}
      {actionError && <p className="v2-error">{actionError}</p>}
      {result && !isStranded && (() => {
        const fieldFact = (field: FlowV2Definition['display']['fields'][number]) => {
          const isEditing = editingKey === field.attributeKey;
          return (
            <div className="sc-fact" key={field.attributeKey}>
              <dt className="sc-fname">{field.description}</dt>
              <dd className="sc-fvalue">
                {isEditing ? (
                  <>
                    <TypedInput field={field} value={editValue} onChange={setEditValue} />
                    <button
                      className="v2-primary"
                      disabled={busyKey === field.attributeKey}
                      onClick={() => void saveField(field.attributeKey, field.valueType)}
                      type="button"
                    >
                      Save
                    </button>
                    <button className="v2-ghost" onClick={() => setEditingKey('')} type="button">Cancel</button>
                    {fieldErrors[field.attributeKey] && (
                      <small className="v2-error">{fieldErrors[field.attributeKey]}</small>
                    )}
                  </>
                ) : (
                  <>
                    <span>{displayValue(result.display[field.attributeKey])}</span>
                    {field.editable && result.isActive && (
                      <button
                        className="v2-ghost"
                        onClick={() => {
                          setEditingKey(field.attributeKey);
                          setFieldErrors((current) => ({ ...current, [field.attributeKey]: '' }));
                          setEditValue(editableValue(
                            result.display[field.attributeKey],
                            field.valueType,
                          ));
                        }}
                        type="button"
                      >
                        Edit
                      </button>
                    )}
                  </>
                )}
              </dd>
            </div>
          );
        };

        const lead = leadFields(definition);
        const detail = definition.display.fields.filter((field) => !lead.includes(field));

        const actionsBlock = (
          <div className="sc-block">
            <div className="sc-blockhead">{RUN_COPY.actions}</div>
            {visibleV2Actions(definition.actions, result.eligibleActions).map((action) => {
              const userFields = v2ActionUserFields(action);
              return (
                <form
                  className="sc-actions"
                  key={action.rpcName}
                  onSubmit={(event) => {
                    event.preventDefault();
                    void invokeAction(action);
                  }}
                >
                  {userFields.map((field) => (
                    <label key={field.fieldName}>
                      <span className="sc-fname">{field.description}</span>
                      <ActionInput
                        field={field}
                        value={actionValues[action.rpcName]?.[field.fieldName] ?? ''}
                        onChange={(value) => setActionValues((current) => ({
                          ...current,
                          [action.rpcName]: {
                            ...current[action.rpcName],
                            [field.fieldName]: value,
                          },
                        }))}
                      />
                    </label>
                  ))}
                  <button
                    className="v2-primary"
                    disabled={!result.isActive || busyKey === action.rpcName}
                    type="submit"
                  >
                    {busyKey === action.rpcName ? 'Working…' : action.label}
                  </button>
                </form>
              );
            })}
            {definition.actions.length > 0 && result.eligibleActions.length === 0 && (
              <p className="sc-state">No Actions are available in the current state.</p>
            )}
          </div>
        );

        const displayBlock = (
          <div className="sc-block">
            {lead.length > 0 && <dl className="sc-facts" data-lead="true">{lead.map(fieldFact)}</dl>}
            {detail.length > 0 && <dl className="sc-facts">{detail.map(fieldFact)}</dl>}
          </div>
        );

        return order === 'actions-first'
          ? <>{actionsBlock}{displayBlock}</>
          : <>{displayBlock}{actionsBlock}</>;
      })()}
    </div>
  );
}

function editableValue(value: unknown, valueType: V2ValueType): string {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'string' ? value : String(value);
  if (valueType !== 'datetime') return text;
  const date = new Date(text);
  if (Number.isNaN(date.getTime())) return '';
  const localDate = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return localDate.toISOString().slice(0, 16);
}

function TypedInput({ field, value, onChange, required = false }: {
  field: { valueType: V2ValueType; description: string };
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
}) {
  if (field.valueType === 'bool') {
    return (
      <select
        aria-label={field.description}
        required={required}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {!required && <option value="">Unset</option>}
        <option value="true">True</option>
        <option value="false">False</option>
      </select>
    );
  }
  return (
    <input
      aria-label={field.description}
      required={required}
      type={field.valueType === 'datetime'
        ? 'datetime-local'
        : field.valueType === 'int64' || field.valueType === 'double'
          ? 'number'
          : 'text'}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function ActionInput({ field, value, onChange }: {
  field: FlowV2ActionInputField;
  value: string;
  onChange: (value: string) => void;
}) {
  return <TypedInput field={field} required={field.required} value={value} onChange={onChange} />;
}
