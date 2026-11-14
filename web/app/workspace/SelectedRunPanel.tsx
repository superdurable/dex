// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { CircleSlash, Lock, Pencil, Zap } from 'lucide-react';
import type {
  FlowV2Action,
  FlowV2ActionInputField,
  FlowV2Definition,
  V2ValueType,
} from '@superdurable/flow-definition-renderer';
import { displayValue } from '@/lib/format';
import { definitionRevisionHeaders, readResponseJSON } from '@/lib/http';
import type { V2Display } from '@/lib/types';
import { parseTypedValue, v2ActionUserFields, v2ActionUserInput, visibleV2Actions } from '../catalog/definitionContract';
import { SESSION_COPY } from '../agents/copy';
import { ActingAsLine, BlockedReason } from '../agents/WorkingAsChoice';
import { WORK_QUEUE_COPY } from '../work-queue/copy';
import { absorb, classifyReadFailure, nothingHeld, readFailureReason } from '../work-queue/liveness';
import { RUN_COPY } from './runCopy';
import { statusTone } from './statusTone';
import { leadFields } from './uiSlots';
import { useWebCatalog } from '../catalog/WebCatalogProvider';
import { QRCodeScannerDialog } from './QRCodeScannerDialog';
import { webURL } from '@/lib/basePath';

interface ActiveQRScanner {
  actionRPCName: string;
  fieldName: string;
  fieldDescription: string;
}

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
  workQueuePermissions = [],
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
  workQueuePermissions?: readonly string[];
}) {
  const { revisionFor, definitionUpdateKey, handleDefinitionError } = useWebCatalog();
  const definitionRevision = revisionFor([flowType]);
  const [held, setHeld] = useState(() => nothingHeld<V2Display>());
  /** A write that failed is not a stale read, so it does not touch held. */
  const [actionError, setActionError] = useState('');
  const [busyKey, setBusyKey] = useState('');
  const [editingKey, setEditingKey] = useState('');
  const [editValue, setEditValue] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [actionValues, setActionValues] = useState<Record<string, Record<string, string>>>({});
  const [activeQRScanner, setActiveQRScanner] = useState<ActiveQRScanner | null>(null);

  const loadDisplay = useCallback(async () => {
    try {
      const query = new URLSearchParams({ flowType, flowId });
      const response = await fetch(webURL(`/api/v2/display?${query}`), {
        headers: definitionRevisionHeaders(definitionRevision),
      });
      const display = await readResponseJSON<V2Display>(response);
      setHeld((prior) => absorb(prior, { state: 'ok', value: display }));
    } catch (loadError) {
      if (handleDefinitionError(loadError)) {
        setHeld(nothingHeld<V2Display>());
        return;
      }
      // Only knowable once somebody opens the run: a search says nothing about its worker.
      const outcome = classifyReadFailure<V2Display>(loadError, flowStatusCode);
      if (outcome.state === 'stranded') onStranded?.(flowId);
      setHeld((prior) => absorb(prior, outcome));
    }
  }, [definitionRevision, flowId, flowStatusCode, flowType, handleDefinitionError, onStranded]);

  // A new run must not inherit the previous run's values while its own read is in flight.
  useEffect(() => {
    setHeld(nothingHeld<V2Display>());
    setActionValues({});
    setActiveQRScanner(null);
    setEditingKey('');
  }, [definitionUpdateKey, flowId, flowType]);

  useEffect(() => { void loadDisplay(); }, [loadDisplay, reloadKey]);

  const result = held.value;
  const isStranded = held.liveness === 'stranded';

  async function saveField(attributeKey: string, valueType: V2ValueType) {
    setBusyKey(attributeKey);
    setActionError('');
    setFieldErrors((current) => ({ ...current, [attributeKey]: '' }));
    try {
      const response = await fetch(webURL('/api/v2/display'), {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
          ...definitionRevisionHeaders(definitionRevision),
        },
        body: JSON.stringify({
          flowType, flowId, attributeKey,
          value: parseTypedValue(editValue, valueType),
        }),
      });
      await readResponseJSON(response);
      setEditingKey('');
      await loadDisplay();
    } catch (saveError) {
      if (handleDefinitionError(saveError)) return;
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
      const response = await fetch(webURL('/api/v2/actions'), {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...definitionRevisionHeaders(definitionRevision),
        },
        body: JSON.stringify({
          flowType, flowId, rpcName: action.rpcName,
          workQueuePermissions,
          input, attributeSnapshot: result?.attributeSnapshot ?? {},
        }),
      });
      await readResponseJSON(response);
      await loadDisplay();
      onActed?.();
    } catch (actionError) {
      if (handleDefinitionError(actionError)) return;
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
          const value = result.display[field.attributeKey];
          const text = displayValue(value);
          const kind = isStatusField(field) ? 'status' : text.length > 60 ? 'long' : 'short';
          return (
            <div className="sc-fact" data-kind={kind} key={field.attributeKey}>
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
                    {kind === 'status'
                      ? <span className="sc-chip" data-tone={statusTone(text)}>{text}</span>
                      : <span className="sc-ftext">{text}</span>}
                    {field.editable && result.isActive && (
                      <button
                        aria-label={`Edit ${field.description}`}
                        className="v2-ghost sc-edit"
                        onClick={() => {
                          setEditingKey(field.attributeKey);
                          setFieldErrors((current) => ({ ...current, [field.attributeKey]: '' }));
                          setEditValue(editableValue(value, field.valueType));
                        }}
                        type="button"
                      >
                        <Pencil aria-hidden="true" size={13} strokeWidth={2} />
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
        const eligible = new Set(visibleV2Actions(definition.actions, result.eligibleActions).map((action) => action.rpcName));

        const actionsBlock = definition.actions.length === 0 ? null : (
          <section className="sc-block sc-actions-block" aria-label={RUN_COPY.actions}>
            <h4 className="sc-blockhead">{RUN_COPY.actions}</h4>
            <ActingAsLine />
            <div className="sc-action-list">
              {definition.actions.map((action) => {
                const state = actionState(action, eligible, workQueuePermissions, result.isActive);
                const userFields = v2ActionUserFields(action);
                const StateIcon = state.kind === 'ready' ? Zap : state.kind === 'needs-permission' ? Lock : CircleSlash;
                return (
                  <form
                    className="sc-action-card"
                    data-state={state.kind}
                    key={action.rpcName}
                    onSubmit={(event) => {
                      event.preventDefault();
                      if (state.kind === 'ready') void invokeAction(action);
                    }}
                  >
                    <div className="sc-action-head">
                      <span className="sc-action-icon"><StateIcon aria-hidden="true" size={16} strokeWidth={2} /></span>
                      <div className="sc-action-words">
                        <b>{action.label}</b>
                        {state.kind === 'needs-permission'
                          ? <BlockedReason reason={state.note} />
                          : <small>{state.note}</small>}
                      </div>
                    </div>
                    {state.kind === 'ready' && userFields.map((field) => (
                      <label key={field.fieldName}>
                        <span className="sc-fname">{field.description}</span>
                        <ActionInput
                          field={field}
                          value={actionValues[action.rpcName]?.[field.fieldName] ?? ''}
                          onChange={(fieldValue) => setActionValues((current) => ({
                            ...current,
                            [action.rpcName]: {
                              ...current[action.rpcName],
                              [field.fieldName]: fieldValue,
                            },
                          }))}
                          onScanRequested={() => setActiveQRScanner({
                            actionRPCName: action.rpcName,
                            fieldName: field.fieldName,
                            fieldDescription: field.description,
                          })}
                        />
                      </label>
                    ))}
                    {state.kind === 'ready' && (
                      <button
                        className="v2-primary sc-action-submit"
                        disabled={busyKey === action.rpcName}
                        type="submit"
                      >
                        {busyKey === action.rpcName ? 'Working…' : action.label}
                      </button>
                    )}
                  </form>
                );
              })}
            </div>
          </section>
        );

        const displayBlock = (
          <section className="sc-block sc-display-block">
            {lead.length > 0 && <dl className="sc-facts" data-lead="true">{lead.map(fieldFact)}</dl>}
            {detail.length > 0 && <dl className="sc-facts">{detail.map(fieldFact)}</dl>}
          </section>
        );

        return order === 'actions-first'
          ? <>{actionsBlock}{displayBlock}</>
          : <>{displayBlock}{actionsBlock}</>;
      })()}
      {activeQRScanner && (
        <QRCodeScannerDialog
          fieldDescription={activeQRScanner.fieldDescription}
          onCancel={() => setActiveQRScanner(null)}
          onScan={(value) => {
            setActionValues((current) => ({
              ...current,
              [activeQRScanner.actionRPCName]: {
                ...current[activeQRScanner.actionRPCName],
                [activeQRScanner.fieldName]: value,
              },
            }));
            setActiveQRScanner(null);
          }}
        />
      )}
    </div>
  );
}

interface ActionState {
  kind: 'ready' | 'needs-permission' | 'unavailable';
  note: string;
}

/** Whether an Action can be taken now, and if not, why, in words a reader can act on. */
function actionState(
  action: FlowV2Action,
  eligible: ReadonlySet<string>,
  workQueuePermissions: readonly string[],
  isActive: boolean,
): ActionState {
  if (!isActive) return { kind: 'unavailable', note: 'The run is no longer active' };
  if (!eligible.has(action.rpcName)) return { kind: 'unavailable', note: 'Not offered in the current state' };
  if (!workQueuePermissions.includes(action.requiredPermission)) {
    return { kind: 'needs-permission', note: SESSION_COPY.chooseWorkingAs(action.requiredPermission) };
  }
  return { kind: 'ready', note: `Available to ${action.requiredPermission}` };
}

/** A field that names where the run stands reads as a chip. */
function isStatusField(field: { uiSlot?: string; description: string }): boolean {
  return field.uiSlot === 'status' || /\bstatus$/i.test(field.description.trim());
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

export function ActionInput({ field, value, onChange, onScanRequested }: {
  field: FlowV2ActionInputField;
  value: string;
  onChange: (value: string) => void;
  onScanRequested: () => void;
}) {
  return (
    <span className="qr-capture-control">
      <TypedInput field={field} required={field.required} value={value} onChange={onChange} />
      {field.capture === 'qr-code' && (
        <button className="v2-ghost" onClick={onScanRequested} type="button">Scan QR code</button>
      )}
    </span>
  );
}
