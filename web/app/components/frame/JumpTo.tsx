// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, History, Search } from 'lucide-react';
import { webURL } from '@/lib/basePath';
import { readResponseJSON } from '@/lib/http';
import type { SearchFlowsResult } from '@/lib/types';
import { runPath } from '../../routes';
import { StatusBadge } from '../StatusBadge';
import { readRecentRuns } from './recentRuns';
import type { AppSection } from './appSections';
import { usePopover } from './usePopover';

/** Returns the run path of a pasted run link, or null when the text is not one. */
export function linkedRunPathOf(input: string): string | null {
  const linkedRun = input.trim().match(/\/v1\/flows\/([^/?#\s]+)(?:\/([^/?#\s]+))?/);
  if (!linkedRun) return null;
  const [, flowId, runId] = linkedRun;
  return runPath(decodeURIComponent(flowId), runId ? decodeURIComponent(runId) : undefined);
}

/** The search for Flows whose ID starts with the typed text. */
export function flowIdPrefixQuery(prefix: string): string {
  return `WorkflowId STARTS_WITH ${JSON.stringify(prefix)}`;
}

interface JumpOption {
  key: string;
  label: string;
  detail: string;
  status?: string;
  kind: 'run' | 'recent' | 'section';
  path: string;
}

const SUGGESTION_LIMIT = 8;

/** Flows whose ID starts with the query, read 200 ms after typing stops. */
function useFlowIdSuggestions(query: string): { options: JumpOption[]; isSearching: boolean } {
  const [options, setOptions] = useState<JumpOption[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  useEffect(() => {
    const prefix = query.trim();
    if (!prefix || linkedRunPathOf(prefix)) {
      setOptions([]);
      setIsSearching(false);
      return undefined;
    }
    const controller = new AbortController();
    setIsSearching(true);
    const timer = window.setTimeout(() => {
      fetch(webURL('/api/flows/search'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: flowIdPrefixQuery(prefix), pageSize: SUGGESTION_LIMIT, nextPageToken: '' }),
        signal: controller.signal,
      })
        .then((response) => readResponseJSON<SearchFlowsResult>(response))
        .then((result) => setOptions(result.flows.map((flow) => ({
          key: `run:${flow.flowId}`, label: flow.flowId, detail: flow.flowType, status: flow.flowStatus,
          kind: 'run', path: runPath(flow.flowId, flow.runId),
        }))))
        .catch(() => { if (!controller.signal.aborted) setOptions([]); })
        .finally(() => { if (!controller.signal.aborted) setIsSearching(false); });
    }, 200);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [query]);
  return { options, isSearching };
}

/** ⌘K or Ctrl+K: open a run by Flow ID prefix, recent run or pasted link, or switch section. */
export function JumpTo({ sections }: { sections: readonly AppSection[] }) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const dialogId = useId();
  const listId = useId();
  const suggestions = useFlowIdSuggestions(query);
  const linkedPath = linkedRunPathOf(query);
  const recentRuns = useMemo(() => (isOpen ? readRecentRuns() : []), [isOpen]);

  const options: JumpOption[] = useMemo(() => {
    if (linkedPath) {
      return [{ key: 'link', label: `Open ${query.trim()}`, detail: 'The linked run', kind: 'run', path: linkedPath }];
    }
    if (query.trim()) return suggestions.options;
    return [
      ...recentRuns.map((run) => ({
        key: `recent:${run.flowId}`, label: run.flowId, detail: run.flowType, status: run.flowStatus,
        kind: 'recent' as const, path: runPath(run.flowId, run.runId),
      })),
      ...sections.map((section) => ({
        key: `section:${section.path}`, label: section.label, detail: section.description,
        kind: 'section' as const, path: section.path,
      })),
    ];
  }, [linkedPath, query, recentRuns, sections, suggestions.options]);

  useEffect(() => {
    const openOnShortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setIsOpen(true);
      }
    };
    document.addEventListener('keydown', openOnShortcut);
    return () => document.removeEventListener('keydown', openOnShortcut);
  }, [setIsOpen]);

  useEffect(() => {
    if (isOpen) inputRef.current?.focus();
    else setQuery('');
  }, [isOpen]);

  useEffect(() => { setActiveIndex(0); }, [query, options.length]);

  const go = (path: string) => {
    setIsOpen(false);
    navigate(path);
  };
  const moveWithKeys = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (options.length === 0) return;
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setActiveIndex((index) => (index + step + options.length) % options.length);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const option = options[activeIndex];
      if (option) go(option.path);
    }
  };
  const isNoMatch = query.trim() !== '' && !linkedPath && !suggestions.isSearching && options.length === 0;

  return (
    <div className="app-bar-jump" ref={rootRef}>
      <button
        aria-controls={dialogId}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
        aria-label="Jump to a Flow"
        className="app-bar-icon-button app-bar-jump-button"
        onClick={() => setIsOpen((open) => !open)}
        title="Jump to a Flow (⌘K)"
        type="button"
      >
        <Search aria-hidden="true" size={15} strokeWidth={2} />
        <kbd>⌘K</kbd>
      </button>
      {isOpen && (
        <div aria-label="Jump to" className="app-bar-popover app-bar-jump-popover" id={dialogId} role="dialog">
          <input
            aria-activedescendant={options[activeIndex] ? `${listId}-${activeIndex}` : undefined}
            aria-autocomplete="list"
            aria-controls={listId}
            aria-expanded="true"
            aria-label="Flow ID or run link"
            className="app-bar-jump-input"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={moveWithKeys}
            placeholder="Type the start of a Flow ID, or paste a run link"
            ref={inputRef}
            role="combobox"
            value={query}
          />
          <div className="app-bar-jump-list" id={listId} role="listbox" aria-label="Matches">
            {!query.trim() && recentRuns.length > 0 && <p className="app-bar-jump-heading">Recent runs</p>}
            {options.map((option, index) => {
              const isSectionHead = option.kind === 'section' && options[index - 1]?.kind !== 'section';
              return (
                <div key={option.key}>
                  {isSectionHead && <p className="app-bar-jump-heading">Pages</p>}
                  <button
                    aria-selected={index === activeIndex}
                    className="app-bar-menu-item app-bar-jump-option"
                    id={`${listId}-${index}`}
                    onClick={() => go(option.path)}
                    onMouseEnter={() => setActiveIndex(index)}
                    role="option"
                    tabIndex={-1}
                    type="button"
                  >
                    <span className="app-bar-menu-icon">
                      {option.kind === 'recent'
                        ? <History aria-hidden="true" size={15} strokeWidth={2} />
                        : <ArrowRight aria-hidden="true" size={15} strokeWidth={2} />}
                    </span>
                    <span className="app-bar-menu-words">
                      <b className={option.kind === 'section' ? undefined : 'mono'}>{option.label}</b>
                      <small>{option.detail}</small>
                    </span>
                    {option.status && <StatusBadge status={option.status} />}
                  </button>
                </div>
              );
            })}
            {suggestions.isSearching && <p className="app-bar-jump-note">Searching…</p>}
            {isNoMatch && <p className="app-bar-jump-note">No Flow ID starts with “{query.trim()}”.</p>}
          </div>
        </div>
      )}
    </div>
  );
}
