// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react';
import { Bookmark, BookmarkPlus, Copy, History, Play, Trash2, X } from 'lucide-react';
import { exactTimeText, relativeTimeText } from '../connectors/connectionPresentation';

/** A named query this browser keeps for one Dex Server. */
export interface SavedQuery {
  name: string;
  query: string;
  /** When it was saved, as ISO 8601; queries saved before 1.45.9 have none. */
  savedAt?: string;
}

/** A query this browser ran, newest first. */
export interface RecentQuery {
  query: string;
  /** When it last ran, as ISO 8601; queries run before 1.45.9 have none. */
  lastRunAt?: string;
}

/** Moves focus among the entries of a list with the arrow, Home, and End keys; returns whether it handled the key. */
export function moveFocusAmongEntries(event: KeyboardEvent<HTMLElement>, entrySelector: string): boolean {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return false;
  const entries = [...event.currentTarget.querySelectorAll<HTMLElement>(entrySelector)];
  if (entries.length === 0) return false;
  const current = entries.indexOf(document.activeElement as HTMLElement);
  let target: number;
  if (event.key === 'Home') target = 0;
  else if (event.key === 'End') target = entries.length - 1;
  else if (event.key === 'ArrowDown') target = current < 0 ? 0 : Math.min(current + 1, entries.length - 1);
  else target = current < 0 ? entries.length - 1 : Math.max(current - 1, 0);
  event.preventDefault();
  entries[target].focus();
  return true;
}

interface QueryLibraryPanelProps {
  saved: readonly SavedQuery[];
  recent: readonly RecentQuery[];
  /** The query that Save names, or null while no save is in progress. */
  saveDraft: string | null;
  /** A failure to show, such as storage that refused a write. */
  storageError: string;
  onRunQuery: (query: string) => void;
  onStartSave: (query: string) => void;
  onCancelSave: () => void;
  onSaveQuery: (name: string, query: string) => void;
  onDeleteSaved: (name: string) => void;
  onRemoveRecent: (query: string) => void;
  onClearRecent: () => void;
  onClose: () => void;
}

/** The Queries panel: saved and recent queries, each with Run, Copy, and Save or Delete. */
export function QueryLibraryPanel({
  saved,
  recent,
  saveDraft,
  storageError,
  onRunQuery,
  onStartSave,
  onCancelSave,
  onSaveQuery,
  onDeleteSaved,
  onRemoveRecent,
  onClearRecent,
  onClose,
}: QueryLibraryPanelProps) {
  const [notice, setNotice] = useState<{ text: string; isError: boolean } | null>(null);
  const now = new Date();

  async function copyQuery(query: string) {
    try {
      // An embedding page may deny clipboard access, which rejects or leaves the API undefined.
      await navigator.clipboard.writeText(query);
      setNotice({ text: 'Copied the query.', isError: false });
    } catch {
      setNotice({ text: 'This page cannot use the clipboard. Select the query text to copy it.', isError: true });
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
      return;
    }
    if ((event.target as HTMLElement).closest('form')) return;
    moveFocusAmongEntries(event, '[data-query-entry]');
  }

  return (
    <div aria-label="Queries" className="query-panel query-library" onKeyDown={handleKeyDown} role="region">
      {saveDraft !== null && (
        <SaveQueryForm
          existingNames={saved.map((item) => item.name)}
          onCancel={onCancelSave}
          onSave={onSaveQuery}
          query={saveDraft}
        />
      )}
      <section className="query-library-section">
        <QuerySectionHeader count={saved.length} icon={<Bookmark aria-hidden="true" size={14} strokeWidth={2} />} title="Saved" />
        {saved.length === 0 ? (
          <QueryEmptyState hint="Run a search, then choose Save query to keep it here." title="No saved queries" />
        ) : (
          <ul className="query-entry-list">
            {saved.map((item) => (
              <QueryEntry
                key={item.name}
                name={item.name}
                now={now}
                onRun={() => onRunQuery(item.query)}
                query={item.query}
                time={item.savedAt}
                timeLabel="Saved"
              >
                <QueryEntryAction label={`Copy ${item.name}`} onClick={() => void copyQuery(item.query)}>
                  <Copy aria-hidden="true" size={14} strokeWidth={2} />
                </QueryEntryAction>
                <QueryEntryAction label={`Delete saved query ${item.name}`} onClick={() => onDeleteSaved(item.name)}>
                  <Trash2 aria-hidden="true" size={14} strokeWidth={2} />
                </QueryEntryAction>
              </QueryEntry>
            ))}
          </ul>
        )}
      </section>
      <section className="query-library-section">
        <QuerySectionHeader
          action={recent.length > 0 && (
            <button aria-label="Clear recent queries" className="text-button" onClick={onClearRecent} type="button">Clear</button>
          )}
          count={recent.length}
          icon={<History aria-hidden="true" size={14} strokeWidth={2} />}
          title="Recent"
        />
        {recent.length === 0 ? (
          <QueryEmptyState hint="Searches you run in this browser appear here." title="No recent queries" />
        ) : (
          <ul className="query-entry-list">
            {recent.map((item) => (
              <QueryEntry
                key={item.query}
                now={now}
                onRun={() => onRunQuery(item.query)}
                query={item.query}
                time={item.lastRunAt}
                timeLabel="Ran"
              >
                <QueryEntryAction label={`Copy ${item.query}`} onClick={() => void copyQuery(item.query)}>
                  <Copy aria-hidden="true" size={14} strokeWidth={2} />
                </QueryEntryAction>
                <QueryEntryAction label={`Save ${item.query}`} onClick={() => onStartSave(item.query)}>
                  <BookmarkPlus aria-hidden="true" size={14} strokeWidth={2} />
                </QueryEntryAction>
                <QueryEntryAction label={`Remove ${item.query} from Recent`} onClick={() => onRemoveRecent(item.query)}>
                  <X aria-hidden="true" size={14} strokeWidth={2} />
                </QueryEntryAction>
              </QueryEntry>
            ))}
          </ul>
        )}
      </section>
      <p
        aria-live="polite"
        className={`query-panel-notice${storageError || notice?.isError ? ' is-error' : ''}`}
        role="status"
      >
        {storageError || notice?.text}
      </p>
    </div>
  );
}

function QuerySectionHeader({ title, count, icon, action }: { title: string; count: number; icon: ReactNode; action?: ReactNode }) {
  return (
    <header className="query-panel-header">
      <h3>
        {icon}
        {title}
        {count > 0 && <span className="query-panel-count">{count}</span>}
      </h3>
      {action}
    </header>
  );
}

/** An empty list: what is missing, then one line on how to fill it. */
export function QueryEmptyState({ title, hint }: { title: string; hint: string }) {
  return (
    <p className="query-empty">
      <span className="query-empty-title">{title}</span>
      <span className="query-empty-hint">{hint}</span>
    </p>
  );
}

interface QueryEntryProps {
  query: string;
  name?: string;
  time: string | undefined;
  timeLabel: string;
  now: Date;
  onRun: () => void;
  children: ReactNode;
}

function QueryEntry({ query, name, time, timeLabel, now, onRun, children }: QueryEntryProps) {
  return (
    <li className="query-entry">
      <button
        aria-label={`Run ${name ? `${name}: ` : ''}${query}`}
        className="query-entry-run"
        data-query-entry
        onClick={onRun}
        title={query}
        type="button"
      >
        {name && <span className="query-entry-name">{name}</span>}
        <code className="query-entry-text">{query}</code>
        {time && (
          <span className="query-entry-meta">
            {timeLabel} <time dateTime={time} title={exactTimeText(time)}>{relativeTimeText(time, now)}</time>
          </span>
        )}
      </button>
      <span className="query-entry-actions">
        <span aria-hidden="true" className="query-entry-run-hint"><Play size={12} strokeWidth={2.25} /> Run</span>
        {children}
      </span>
    </li>
  );
}

function QueryEntryAction({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button aria-label={label} className="query-entry-action" onClick={onClick} title={label} type="button">
      {children}
    </button>
  );
}

interface SaveQueryFormProps {
  query: string;
  existingNames: readonly string[];
  onSave: (name: string, query: string) => void;
  onCancel: () => void;
}

/** Names a query inline; a browser prompt would not open inside a sandboxed embedding frame. */
function SaveQueryForm({ query, existingNames, onSave, onCancel }: SaveQueryFormProps) {
  const [name, setName] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => inputRef.current?.focus(), [query]);
  const trimmedName = name.trim();
  const isReplacing = existingNames.includes(trimmedName);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!trimmedName || !query.trim()) return;
    onSave(trimmedName, query);
    setName('');
  }

  // Escape leaves the form only; the panel stays open.
  function cancelOnEscape(event: KeyboardEvent<HTMLFormElement>) {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    onCancel();
  }

  if (!query.trim()) {
    return (
      <div className="query-save-form">
        <QueryEmptyState hint="Add a filter or write a query, then save it." title="Nothing to save yet" />
        <button className="button quiet" onClick={onCancel} type="button">Close</button>
      </div>
    );
  }
  return (
    <form className="query-save-form" onKeyDown={cancelOnEscape} onSubmit={submit}>
      <label>
        <span>Query name</span>
        <input onChange={(event) => setName(event.target.value)} ref={inputRef} value={name} />
      </label>
      <code className="query-entry-text" title={query}>{query}</code>
      {isReplacing && <span className="query-save-replace">Replaces the saved query with this name.</span>}
      <div className="query-save-buttons">
        <button className="button ghost" onClick={onCancel} type="button">Cancel</button>
        <button className="button primary" disabled={!trimmedName} type="submit">Save</button>
      </div>
    </form>
  );
}
