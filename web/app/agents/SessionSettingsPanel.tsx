// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { BlockedReason } from './WorkingAsChoice';
import { useEffect, useRef, useState } from 'react';
import { Settings2, ShieldAlert } from 'lucide-react';
import { SESSION_COPY } from './copy';
import { parseSessionSettings, updateSessionSettings, type SessionRouteContext } from './sessionRequests';

/** Settings as the editor shows them: indented JSON, or empty when the session has none. */
export function sessionSettingsText(settings: Record<string, unknown> | null): string {
  return settings === null ? '' : JSON.stringify(settings, null, 2);
}

/** One line naming the settings, such as "model: gpt-5-mini · 2 more". */
export function sessionSettingsSummary(settings: Record<string, unknown> | null): string {
  const keys = settings === null ? [] : Object.keys(settings);
  if (keys.length === 0) return SESSION_COPY.settingsNone;
  const model = settings?.model;
  if (typeof model === 'string') {
    return keys.length === 1 ? `model: ${model}` : `model: ${model} · ${keys.length - 1} more`;
  }
  return keys.join(', ');
}

/** The SessionSettings Attribute as editable JSON, saved through update_session_settings. */
export function SessionSettingsPanel({
  settings,
  routeContext,
  blockedReason,
  isBlockedReasonShownAbove = false,
  onSaved,
}: {
  settings: Record<string, unknown> | null;
  routeContext: SessionRouteContext;
  /** Why saving cannot run now, such as a closed session; null when it can. */
  blockedReason: string | null;
  /** True when the tab already shows blockedReason at its top, as the Working as hint. */
  isBlockedReasonShownAbove?: boolean;
  onSaved: () => void;
}) {
  const stored = sessionSettingsText(settings);
  const [draft, setDraft] = useState(stored);
  /** The stored text the draft started from. */
  const [editBase, setEditBase] = useState(stored);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ tone: 'note' | 'error'; text: string } | null>(null);
  const isMounted = useRef(true);
  useEffect(() => {
    isMounted.current = true;
    return () => { isMounted.current = false; };
  }, []);

  // A reload that brings new settings replaces an unedited draft; an edited one keeps the edit.
  useEffect(() => {
    if (stored === editBase) return;
    if (draft === editBase || draft === stored) {
      setDraft(stored);
      setEditBase(stored);
    }
  }, [stored, editBase, draft]);

  const isEdited = draft !== stored;
  const hasStoredChangedUnderEdit = isEdited && editBase !== stored;
  const save = async () => {
    const parsed = parseSessionSettings(draft, true);
    if (!parsed.ok) {
      setMessage({ tone: 'error', text: parsed.message });
      return;
    }
    setSaving(true);
    setMessage(null);
    try {
      const outcome = await updateSessionSettings(routeContext, parsed.value);
      if (!isMounted.current) return;
      if (outcome.kind === 'done') {
        setMessage({ tone: 'note', text: SESSION_COPY.settingsSaved });
        onSaved();
      } else if (outcome.kind !== 'already_handled') {
        setMessage({ tone: 'error', text: outcome.message });
      }
    } finally {
      if (isMounted.current) setSaving(false);
    }
  };

  return (
    <details className="ses-settings">
      <summary>
        <Settings2 aria-hidden="true" size={14} strokeWidth={2} />
        <span className="ses-settings-title">{SESSION_COPY.settingsHeading}</span>
        <span className="ses-settings-summary">{sessionSettingsSummary(settings)}</span>
      </summary>
      <form
        className="ses-settings-body"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <p className="ses-settings-warning">
          <ShieldAlert aria-hidden="true" size={14} />
          <span>{SESSION_COPY.settingsNoSecrets}</span>
        </p>
        <textarea
          aria-label={SESSION_COPY.settingsLabel}
          className="ses-settings-json"
          disabled={saving}
          placeholder="{}"
          rows={Math.min(14, Math.max(4, draft.split('\n').length + 1))}
          spellCheck={false}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
            setMessage(null);
          }}
        />
        <p className="agc-note">{SESSION_COPY.settingsReplace} {SESSION_COPY.settingsNextTurn}</p>
        {hasStoredChangedUnderEdit && <p className="agc-blocked">{SESSION_COPY.settingsChangedElsewhere}</p>}
        {blockedReason !== null && !isBlockedReasonShownAbove && <BlockedReason reason={blockedReason} />}
        <div className="ses-settings-buttons">
          <button
            className="agc-secondary"
            disabled={saving || !isEdited}
            onClick={() => {
              setDraft(stored);
              setEditBase(stored);
              setMessage(null);
            }}
            type="button"
          >
            {SESSION_COPY.settingsReset}
          </button>
          <button className="agc-primary" disabled={saving || blockedReason !== null || !isEdited} type="submit">
            {saving ? SESSION_COPY.settingsSaving : SESSION_COPY.settingsSave}
          </button>
        </div>
        {message && <p className={message.tone === 'error' ? 'agc-error' : 'agc-note'} role="status">{message.text}</p>}
      </form>
    </details>
  );
}
