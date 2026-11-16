// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { KeyboardEvent } from 'react';
import { BookOpen, ExternalLink } from 'lucide-react';
import {
  BUILT_IN_SEARCH_FIELDS,
  SEARCH_QUERY_SYNTAX_URL,
  type DeclaredSearchField,
  type ObservedSearchField,
  type SearchField,
  type SearchFieldCatalogState,
} from './flowSearchFields';
import { moveFocusAmongEntries, QueryEmptyState } from './FlowSearchQueries';

interface SearchFieldsPanelProps {
  catalogState: SearchFieldCatalogState;
  declaredFields: readonly DeclaredSearchField[];
  observedFields: readonly ObservedSearchField[];
  /** Adds the field to the search: a filter row on Filters, a clause on Query. */
  onPickField: (field: SearchField) => void;
  onClose: () => void;
}

/** The Fields panel: the built-in fields and the indexed Attributes a query can name, with their types. */
export function SearchFieldsPanel({ catalogState, declaredFields, observedFields, onPickField, onClose }: SearchFieldsPanelProps) {
  const declaredByFlowType = new Map<string, DeclaredSearchField[]>();
  for (const field of declaredFields) {
    declaredByFlowType.set(field.flowType, [...(declaredByFlowType.get(field.flowType) ?? []), field]);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
      return;
    }
    moveFocusAmongEntries(event, '[data-query-entry]');
  }

  return (
    <div aria-label="Searchable fields" className="query-panel query-fields" onKeyDown={handleKeyDown} role="region">
      <div className="query-fields-intro">
        <span>Choose a field to add it to the search.</span>
        <QuerySyntaxLink />
      </div>
      <section className="query-library-section">
        <header className="query-panel-header"><h3>Built-in fields</h3></header>
        <ul className="query-field-list">
          {BUILT_IN_SEARCH_FIELDS.map((field) => (
            <SearchFieldEntry field={field} key={field.name} onPick={onPickField} />
          ))}
        </ul>
      </section>
      <section className="query-library-section">
        <header className="query-panel-header"><h3>Indexed Attributes</h3></header>
        {catalogState.status === 'loading' || catalogState.status === 'idle' ? (
          <p className="query-empty"><span className="query-empty-hint">Reading Flow definitions…</span></p>
        ) : catalogState.status === 'failed' ? (
          <p className="query-panel-notice is-error" role="alert">
            Flow definitions did not load: {catalogState.error}. Built-in fields and fields seen in results still work.
          </p>
        ) : declaredByFlowType.size === 0 ? (
          <QueryEmptyState
            hint="Indexed Attributes appear here once a loaded Flow definition declares them."
            title="No declared indexed Attributes"
          />
        ) : (
          [...declaredByFlowType].map(([flowType, fields]) => (
            <div className="query-field-group" key={flowType}>
              <h4>{flowType}</h4>
              <ul className="query-field-list">
                {fields.map((field) => (
                  <SearchFieldEntry
                    detail={field.attributeKey === field.name ? undefined : `Attribute ${field.attributeKey}`}
                    field={field}
                    key={field.name}
                    onPick={onPickField}
                  />
                ))}
              </ul>
            </div>
          ))
        )}
      </section>
      {observedFields.length > 0 && (
        <section className="query-library-section">
          <header className="query-panel-header"><h3>Seen in these results</h3></header>
          <ul className="query-field-list">
            {observedFields.map((field) => (
              <SearchFieldEntry field={field} isTypeGuessed key={field.name} onPick={onPickField} />
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

interface SearchFieldEntryProps {
  field: SearchField;
  detail?: string;
  isTypeGuessed?: boolean;
  onPick: (field: SearchField) => void;
}

function SearchFieldEntry({ field, detail, isTypeGuessed = false, onPick }: SearchFieldEntryProps) {
  const typeLabel = isTypeGuessed ? `${field.type}, guessed` : field.type;
  return (
    <li>
      <button
        aria-label={`Add ${field.name}, ${typeLabel}`}
        className="query-field-entry"
        data-query-entry
        onClick={() => onPick(field)}
        title={isTypeGuessed ? 'Type guessed from the values in these results' : field.description}
        type="button"
      >
        <code className="query-field-name">{field.name}</code>
        <span className="query-field-type">{typeLabel}</span>
        <span className="query-field-description">
          {[detail, field.description].filter(Boolean).join(' · ')}
        </span>
      </button>
    </li>
  );
}

/** Opens the query syntax reference in a new tab. */
export function QuerySyntaxLink() {
  return (
    <a className="query-syntax-link" href={SEARCH_QUERY_SYNTAX_URL} rel="noreferrer" target="_blank">
      <BookOpen aria-hidden="true" size={14} strokeWidth={2} />
      Query syntax
      <ExternalLink aria-hidden="true" size={12} strokeWidth={2} />
    </a>
  );
}
