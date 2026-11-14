// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type ReactNode } from 'react';
import {
  ChevronDown,
  CircleCheck,
  CircleDot,
  CircleMinus,
  CircleX,
  Clock,
  Ellipsis,
  Layers,
  LoaderCircle,
  Search,
  SlidersHorizontal,
  type LucideIcon,
} from 'lucide-react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { WORK_QUEUE_COPY } from '../work-queue/copy';
import { SEARCH_COPY } from './searchCopy';
import {
  SINCE_WINDOWS,
  attributeByKey,
  defaultOperator,
  filterableAttributes,
  flowStatusQueryValue,
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

/** The statuses a queue is read by most; the rest sit behind the More chip. */
const STATUS_CHIPS: { label: string; value: string; icon: LucideIcon; tone: string }[] = [
  { label: 'Running', value: flowStatusQueryValue('Running'), icon: CircleDot, tone: 'waiting' },
  { label: 'Completed', value: flowStatusQueryValue('Completed'), icon: CircleCheck, tone: 'success' },
  { label: 'Failed', value: flowStatusQueryValue('Failed'), icon: CircleX, tone: 'danger' },
  { label: 'All', value: '', icon: Layers, tone: 'neutral' },
];

/**
 * Status and time in front; attribute comparisons and an exact Flow ID behind a disclosure.
 *
 * Two controls answer most questions about a list of runs, and they are the two every Flow has
 * whether or not it declared anything indexable. What only some Flows can answer, and what needs a
 * reader to choose an operator, goes one click away rather than into the default view.
 *
 * The chips search as soon as they are chosen. The disclosure still searches on Search.
 */
export function RunSearch({
  definition,
  flowType,
  flowTypes,
  stepTypes,
  query,
  busy,
  permissionControl,
  onChange,
  onApplyChip,
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
  /** Which permission scopes the Work Queue. Drawn beside the time chip. */
  permissionControl?: ReactNode;
  onChange: (query: RunQuery) => void;
  /** Changes one chip of the last searched query and searches with it at once. */
  onApplyChip: (chip: Pick<Partial<RunQuery>, 'status' | 'since'>) => void;
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
  const chipValues = new Set(STATUS_CHIPS.map((chip) => chip.value));
  const moreStatuses = statusOptions().filter((status) => !chipValues.has(status.value));
  const isMoreStatus = moreStatuses.some((status) => status.value === query.status);
  return (
    <form
      className="wq-filters"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <div className="wq-status" role="radiogroup" aria-label={SEARCH_COPY.statusLabel}>
        {STATUS_CHIPS.map(({ label, value, icon: Icon, tone }) => (
          <button
            aria-checked={query.status === value}
            className="wq-status-chip"
            data-tone={tone}
            key={label}
            role="radio"
            type="button"
            onClick={() => onApplyChip({ status: value })}
          >
            <Icon aria-hidden="true" size={14} strokeWidth={2.2} />
            {label}
          </button>
        ))}
        {/* An invisible native select over the chip: the browser keeps the menu and the keyboard. */}
        <label
          className="wq-status-chip wq-status-more"
          data-checked={isMoreStatus ? 'true' : undefined}
          title={WORK_QUEUE_COPY.moreStatuses}
        >
          {isMoreStatus ? (
            <>
              <CircleMinus aria-hidden="true" size={14} strokeWidth={2.2} />
              {moreStatuses.find((status) => status.value === query.status)?.label}
            </>
          ) : <Ellipsis aria-hidden="true" size={16} />}
          <select
            aria-label={`${SEARCH_COPY.statusLabel}: ${WORK_QUEUE_COPY.moreStatuses}`}
            value={isMoreStatus ? query.status : ''}
            onChange={(event) => onApplyChip({ status: event.target.value })}
          >
            <option disabled value="">{WORK_QUEUE_COPY.moreStatuses}</option>
            {moreStatuses.map((status) => (
              <option key={status.value} value={status.value}>{status.label}</option>
            ))}
          </select>
        </label>
      </div>

      <div className="wq-chips">
        <label className="wq-chip" data-active={query.since !== '' ? 'true' : undefined}>
          <Clock aria-hidden="true" size={14} />
          <select
            aria-label={SEARCH_COPY.sinceLabel}
            className="wq-chip-select"
            value={query.since}
            onChange={(event) => onApplyChip({ since: event.target.value as SinceWindow })}
          >
            {SINCE_WINDOWS.map((window) => (
              <option key={window.value || 'any'} value={window.value}>{window.label}</option>
            ))}
          </select>
          <ChevronDown aria-hidden="true" className="wq-chip-chevron" size={13} />
        </label>
        {permissionControl}
      </div>

      <details
        className="wq-adv"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary className="wq-adv-toggle">
          <SlidersHorizontal aria-hidden="true" size={14} />
          <span>{SEARCH_COPY.advanced}</span>
          {hasAdvancedQuery(query) && <span aria-hidden="true" className="wq-adv-dot" />}
          <ChevronDown aria-hidden="true" className="wq-adv-chevron" size={14} />
        </summary>

        <div className="wq-adv-body">
          <label className="wq-field">
            <span className="wq-field-label">{SEARCH_COPY.flowIdLabel}</span>
            <input
              className="wq-input t-mono"
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
            <small className="wq-note">{SEARCH_COPY.attributeNeedsOneFlowType}</small>
          )}
          {attributes.length > 0 && searched.length <= 1 && (
            <div className="wq-field">
              <span className="wq-field-label">{SEARCH_COPY.attributeLabel}</span>
              <select
                aria-label={SEARCH_COPY.attributeLabel}
                className="wq-input"
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
              <div className="wq-compare">
                <select
                  aria-label={SEARCH_COPY.operatorLabel}
                  className="wq-input"
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

          <div className="wq-adv-actions">
            <button className="wq-button wq-button-strong" disabled={busy} type="submit">
              {busy
                ? <LoaderCircle aria-hidden="true" className="wq-spin" size={14} />
                : <Search aria-hidden="true" size={14} />}
              {busy ? SEARCH_COPY.searching : SEARCH_COPY.search}
            </button>
            <button className="wq-button wq-button-quiet" onClick={onClear} type="button">{SEARCH_COPY.clear}</button>
          </div>
        </div>
      </details>
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
    <fieldset className="wq-field wq-checks">
      <legend className="wq-field-label">{label}</legend>
      <div className="wq-checklist">
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
      {note !== undefined && <small className="wq-note">{note}</small>}
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
        className="wq-input"
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
      className="wq-input"
      aria-label={SEARCH_COPY.attributeValueLabel}
      disabled={disabled}
      placeholder={disabled ? SEARCH_COPY.pickAttribute : SEARCH_COPY.value}
      type={kind === 'number' ? 'number' : kind === 'datetime' ? 'datetime-local' : 'text'}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}
