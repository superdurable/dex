// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, Search } from 'lucide-react';
import { runPath } from '../../routes';
import type { AppSection } from './SectionMenu';
import { usePopover } from './usePopover';

/** Returns the Dex Web path a pasted run link or bare Flow ID opens, or null for blank input. */
export function jumpTargetOf(input: string): string | null {
  const text = input.trim();
  if (!text) return null;
  const linkedRun = text.match(/\/v1\/flows\/([^/?#\s]+)(?:\/([^/?#\s]+))?/);
  if (linkedRun) {
    const [, flowId, runId] = linkedRun;
    return runPath(decodeURIComponent(flowId), runId ? decodeURIComponent(runId) : undefined);
  }
  return runPath(text);
}

/** ⌘K or Ctrl+K: open a Flow by its ID or a pasted run link, or switch section. */
export function JumpTo({ sections }: { sections: readonly AppSection[] }) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const dialogId = useId();
  const target = jumpTargetOf(query);

  useEffect(() => {
    const openOnShortcut = (event: KeyboardEvent) => {
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

  const go = (path: string) => {
    setIsOpen(false);
    navigate(path);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (target) go(target);
  };

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
          <form onSubmit={submit}>
            <input
              aria-label="Flow ID or run link"
              className="app-bar-jump-input"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Paste a Flow ID or a run link"
              ref={inputRef}
              value={query}
            />
          </form>
          {target && (
            <button className="app-bar-menu-item" onClick={() => go(target)} type="button">
              <span className="app-bar-menu-icon"><ArrowRight aria-hidden="true" size={16} strokeWidth={2} /></span>
              <span className="app-bar-menu-words">
                <b>Open {query.trim()}</b>
                <small>The Flow's current run, or the linked run</small>
              </span>
            </button>
          )}
          {sections.map((section) => (
            <button className="app-bar-menu-item" key={section.path} onClick={() => go(section.path)} type="button">
              <span className="app-bar-menu-icon"><section.Icon aria-hidden="true" size={16} strokeWidth={2} /></span>
              <span className="app-bar-menu-words"><b>{section.label}</b></span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
