// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { SEARCH_COPY } from './searchCopy';
import {
  SINCE_WINDOWS,
  attributeByKey,
  defaultOperator,
  filterableAttributes,
  hasAdvancedQuery,
  operatorsFor,
  searchedFlowTypes,
  statusOptions,
  toggleFlowType,
  toggleStepType,
  valueInputKind,
  type RunOperator,
  type RunQuery,
  type SinceWindow,
} from './runQuery';

/**
 * Status and time in front; attribute comparisons and an exact Flow ID behind a disclosure.
 *
 * Two controls answer most questions about a list of runs, and they are the two every Flow has
 * whether or not it declared anything indexable. What only some Flows can answer, and what needs a
 * reader to choose an operator, goes one click away rather than into the default view.
 */
export function RunSearch({
  definition,
  flowType,
  flowTypes,
  stepTypes,
  query,
  busy,
  onChange,
  onSubmit,
  onClear,
}: {
  definition: FlowV2Definition;
  /** The page's own Flow type, always part of the search. */
  flowType: string;
  /** Every Flow type the search can widen to. */
  flowTypes: readonly string[];
  /** The Step types the current selection can be waiting at. */
  stepTypes: readonly string[];
  query: RunQuery;
  busy: boolean;
  onChange: (query: RunQuery) => void;
  onSubmit: () => void;
  onClear: () => void;
}) {
  /**
   * The disclosure owns its own open state after the first render.
   *
   * Deriving `open` from the query collapsed the panel the moment somebody chose an Attribute,
   * because a chosen Attribute with no value yet is not an active filter.
   */
  const [advancedOpen, setAdvancedOpen] = useState(() => hasAdvancedQuery(query));
  const attributes = filterableAttributes(definition);
  const chosen = attributeByKey(definition, query.attributeKey);
  const operators = chosen === null ? [] : operatorsFor(chosen.indexType);
  const kind = valueInputKind(chosen?.valueType);
  const set = (patch: Partial<RunQuery>) => onChange({ ...query, ...patch });
  const searched = searchedFlowTypes(query.flowTypes, flowType);
  return (
    <form
      className="rsq"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <div className="rsq-basic">
        <select
          aria-label={SEARCH_COPY.statusLabel}
          value={query.status}
          onChange={(event) => set({ status: event.target.value })}
        >
          <option value="">{SEARCH_COPY.anyStatus}</option>
          {statusOptions().map((status) => <option key={status.value} value={status.value}>{status.label}</option>)}
        </select>
        <select
          aria-label={SEARCH_COPY.sinceLabel}
          value={query.since}
          onChange={(event) => set({ since: event.target.value as SinceWindow })}
        >
          {SINCE_WINDOWS.map((window) => (
            <option key={window.value || 'any'} value={window.value}>{window.label}</option>
          ))}
        </select>
      </div>

      <details
        className="rsq-adv"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary className="rsq-advhead">{SEARCH_COPY.advanced}</summary>

        <label className="rsq-field">
          <span className="rsq-label">{SEARCH_COPY.flowIdLabel}</span>
          <input
            className="t-mono"
            placeholder={SEARCH_COPY.flowIdPlaceholder}
            type="search"
            value={query.flowId}
            onChange={(event) => set({ flowId: event.target.value })}
          />
        </label>

        {flowTypes.length > 1 && (
          <CheckList
            label={SEARCH_COPY.flowTypesLabel}
            lockedOption={flowType}
            options={flowTypes}
            selected={searched}
            onToggle={(candidate) => set({ flowTypes: toggleFlowType(query.flowTypes, flowType, candidate) })}
          />
        )}

        {stepTypes.length > 0 && (
          <CheckList
            label={SEARCH_COPY.activeStepLabel}
            note={SEARCH_COPY.activeStepNote}
            options={stepTypes}
            selected={query.activeStepTypes}
            onToggle={(candidate) => set({ activeStepTypes: toggleStepType(query.activeStepTypes, candidate) })}
          />
        )}

        {attributes.length > 0 && searched.length > 1 && (
          <small className="rsw-permission-note">{SEARCH_COPY.attributeNeedsOneFlowType}</small>
        )}
        {attributes.length > 0 && searched.length <= 1 && (
          <div className="rsq-field">
            <span className="rsq-label">{SEARCH_COPY.attributeLabel}</span>
            <select
              aria-label={SEARCH_COPY.attributeLabel}
              value={query.attributeKey}
              onChange={(event) => {
                const next = attributeByKey(definition, event.target.value);
                set({
                  attributeKey: event.target.value,
                  attributeOperator: next === null ? 'eq' : defaultOperator(next.indexType),
                  attributeValue: '',
                });
              }}
            >
              <option value="">{SEARCH_COPY.anyAttribute}</option>
              {attributes.map((attribute) => (
                <option key={attribute.attributeKey} value={attribute.attributeKey}>
                  {attribute.description}
                </option>
              ))}
            </select>
            <div className="rsq-compare">
              <select
                aria-label={SEARCH_COPY.operatorLabel}
                disabled={chosen === null}
                value={query.attributeOperator}
                onChange={(event) => set({ attributeOperator: event.target.value as RunOperator })}
              >
                {operators.map((operator) => (
                  <option key={operator.value} value={operator.value}>{operator.label}</option>
                ))}
              </select>
              <ValueInput
                disabled={chosen === null}
                kind={kind}
                value={query.attributeValue}
                onChange={(value) => set({ attributeValue: value })}
              />
            </div>
          </div>
        )}
      </details>

      <div className="rsq-actions">
        <button className="v2-primary" disabled={busy} type="submit">
          {busy ? SEARCH_COPY.searching : SEARCH_COPY.search}
        </button>
        <button className="v2-ghost" onClick={onClear} type="button">{SEARCH_COPY.clear}</button>
      </div>
    </form>
  );
}

/** Several exact names to choose from, because a Step or Flow type is not something to retype. */
function CheckList({ label, note, options, selected, lockedOption, onToggle }: {
  label: string;
  note?: string;
  options: readonly string[];
  selected: readonly string[];
  /** Stays ticked and cannot be changed. */
  lockedOption?: string;
  onToggle: (option: string) => void;
}) {
  return (
    <fieldset className="rsq-field rsq-checks">
      <legend className="rsq-label">{label}</legend>
      <div className="rsq-checklist">
        {options.map((option) => (
          <label key={option} title={option}>
            <input
              checked={selected.includes(option)}
              disabled={option === lockedOption}
              type="checkbox"
              onChange={() => onToggle(option)}
            />
            <span>{option}</span>
          </label>
        ))}
      </div>
      {note !== undefined && <small className="rsw-permission-note">{note}</small>}
    </fieldset>
  );
}

/** The value control the Attribute's own type calls for, so an impossible value cannot be sent. */
function ValueInput({ disabled, kind, value, onChange }: {
  disabled: boolean;
  kind: 'text' | 'number' | 'datetime' | 'bool';
  value: string;
  onChange: (value: string) => void;
}) {
  if (kind === 'bool') {
    return (
      <select
        aria-label={SEARCH_COPY.attributeValueLabel}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        <option value="">{SEARCH_COPY.anyValue}</option>
        <option value="true">{SEARCH_COPY.boolTrue}</option>
        <option value="false">{SEARCH_COPY.boolFalse}</option>
      </select>
    );
  }
  return (
    <input
      aria-label={SEARCH_COPY.attributeValueLabel}
      disabled={disabled}
      placeholder={disabled ? SEARCH_COPY.pickAttribute : SEARCH_COPY.value}
      type={kind === 'number' ? 'number' : kind === 'datetime' ? 'datetime-local' : 'text'}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}
