// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { SEARCH_COPY } from './searchCopy';
import {
  SINCE_WINDOWS,
  filterableAttributes,
  keywordAttribute,
  statusOptions,
  type RunQuery,
  type SinceWindow,
} from './runQuery';

/**
 * Find a run using only what Dex can search: a keyword over the Flow's one full-text
 * Attribute, a status, a time window, and one exact Attribute value.
 *
 * The keyword box is absent when the Flow declares no full-text Attribute, so the UI never
 * offers a search it cannot perform — and the placeholder names the Attribute it searches
 * rather than relying on help text that can drift from the contract.
 */
export function RunSearch({
  definition,
  query,
  busy,
  onChange,
  onSubmit,
  onClear,
}: {
  definition: FlowV2Definition;
  query: RunQuery;
  busy: boolean;
  onChange: (query: RunQuery) => void;
  onSubmit: () => void;
  onClear: () => void;
}) {
  const fulltext = keywordAttribute(definition);
  const attributes = filterableAttributes(definition);
  const chosen = attributes.find((a) => a.attributeKey === query.attributeKey);
  const set = (patch: Partial<RunQuery>) => onChange({ ...query, ...patch });
  return (
    <form
      className="rsq"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      {fulltext !== null && (
        <input
          aria-label={SEARCH_COPY.keywordLabel(fulltext.description)}
          className="rsq-keyword"
          placeholder={SEARCH_COPY.keywordPlaceholder(fulltext.description)}
          type="search"
          value={query.keyword}
          onChange={(event) => set({ keyword: event.target.value })}
        />
      )}
      <div className="rsq-row">
        <select
          aria-label={SEARCH_COPY.statusLabel}
          value={query.status}
          onChange={(event) => set({ status: event.target.value })}
        >
          <option value="">{SEARCH_COPY.anyStatus}</option>
          {statusOptions().map((status) => <option key={status} value={status}>{status}</option>)}
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
      {attributes.length > 0 && (
        <div className="rsq-row">
          <select
            aria-label={SEARCH_COPY.attributeLabel}
            value={query.attributeKey}
            onChange={(event) => set({ attributeKey: event.target.value, attributeValue: '' })}
          >
            <option value="">{SEARCH_COPY.anyAttribute}</option>
            {attributes.map((attribute) => (
              <option key={attribute.attributeKey} value={attribute.attributeKey}>
                {attribute.description}
              </option>
            ))}
          </select>
          <input
            aria-label={SEARCH_COPY.attributeValueLabel}
            disabled={query.attributeKey === ''}
            placeholder={chosen === undefined ? SEARCH_COPY.pickAttribute : SEARCH_COPY.exactValue}
            type={chosen?.indexType === 'double' || chosen?.indexType === 'int' ? 'number' : 'text'}
            value={query.attributeValue}
            onChange={(event) => set({ attributeValue: event.target.value })}
          />
        </div>
      )}
      <div className="rsq-actions">
        <button className="v2-primary" disabled={busy} type="submit">
          {busy ? SEARCH_COPY.searching : SEARCH_COPY.search}
        </button>
        <button className="v2-ghost" onClick={onClear} type="button">{SEARCH_COPY.clear}</button>
      </div>
    </form>
  );
}
