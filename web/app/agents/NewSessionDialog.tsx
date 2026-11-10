// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ShieldAlert } from 'lucide-react';
import { DexAPIError } from '@/lib/http';
import type { FlowDefinitionCatalog, V2Catalog } from '@/lib/types';
import { sessionSchemaFor } from '../catalog/applicability';
import { SESSION_COPY } from './copy';
import {
  checkSessionSettingsSize,
  newSessionId,
  parseSessionSettings,
  startAgentSession,
} from './sessionRequests';
import './session.css';

/** The catalog Flow types whose session graph starts at StartAgentSession, in catalog order. */
export function agentSessionFlowTypes(
  catalog: V2Catalog | null | undefined,
  definitions: FlowDefinitionCatalog | null | undefined,
): string[] {
  return (catalog?.flows ?? [])
    .filter((entry) => sessionSchemaFor(definitions, entry)?.canStartSession === true)
    .map((entry) => entry.flowType);
}

export interface NewSessionDraft {
  sessionId: string;
  firstMessage: string;
  model: string;
  systemPrompt: string;
  otherSettings: string;
}

export type NewSessionSettings =
  | { ok: true; value: Record<string, unknown> | null }
  | { ok: false; message: string };

/** The start settings: the other settings object, with model and system_prompt set over it; null when empty. */
export function newSessionSettings(draft: NewSessionDraft): NewSessionSettings {
  const other = parseSessionSettings(draft.otherSettings, true);
  if (!other.ok) return other;
  const settings: Record<string, unknown> = { ...other.value };
  if (draft.model.trim() !== '') settings.model = draft.model.trim();
  if (draft.systemPrompt.trim() !== '') settings.system_prompt = draft.systemPrompt;
  if (Object.keys(settings).length === 0) return { ok: true, value: null };
  return checkSessionSettingsSize(settings);
}

/** Why a session ID cannot start a session, or null when it can. */
export function sessionIdProblem(sessionId: string): string | null {
  if (sessionId.trim() === '') return SESSION_COPY.sessionIdRequired;
  if (/[/$:]/.test(sessionId)) return SESSION_COPY.sessionIdReserved;
  return null;
}

/** Starts a Dex Agents session with a first message and settings, then opens it. */
export function NewSessionDialog({
  flowTypes,
  initialFlowType,
  definitionRevision,
  onClose,
  onDefinitionChanged,
  onStarted,
}: {
  /** Agent session Flow types to choose from; at least one. */
  flowTypes: readonly string[];
  initialFlowType?: string;
  definitionRevision: string;
  onClose: () => void;
  onDefinitionChanged: (error: unknown) => boolean;
  onStarted: (flowType: string, sessionId: string) => void;
}) {
  const [flowType, setFlowType] = useState(
    initialFlowType && flowTypes.includes(initialFlowType) ? initialFlowType : flowTypes[0] ?? '',
  );
  const [draft, setDraft] = useState<NewSessionDraft>(() => ({
    sessionId: newSessionId(), firstMessage: '', model: '', systemPrompt: '', otherSettings: '',
  }));
  const [fieldErrors, setFieldErrors] = useState<{ sessionId?: string; otherSettings?: string }>({});
  const [submitError, setSubmitError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const isOpen = useRef(true);

  useEffect(() => {
    isOpen.current = true;
    if (dialogRef.current && !dialogRef.current.open) dialogRef.current.showModal();
    dialogRef.current?.querySelector<HTMLTextAreaElement>('textarea')?.focus();
    return () => { isOpen.current = false; };
  }, []);

  const update = (field: keyof NewSessionDraft) => (value: string) => setDraft((current) => ({ ...current, [field]: value }));
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const idProblem = sessionIdProblem(draft.sessionId);
    const settings = newSessionSettings(draft);
    setFieldErrors({
      sessionId: idProblem ?? undefined,
      otherSettings: settings.ok ? undefined : settings.message,
    });
    setSubmitError('');
    if (idProblem !== null || !settings.ok) return;
    setSubmitting(true);
    try {
      const sessionId = await startAgentSession({
        flowType,
        flowId: draft.sessionId.trim(),
        initialMessage: draft.firstMessage,
        sessionSettings: settings.value,
        definitionRevision,
      });
      if (isOpen.current) onStarted(flowType, sessionId);
    } catch (error) {
      if (!isOpen.current) return;
      if (onDefinitionChanged(error)) {
        onClose();
        return;
      }
      setSubmitError(error instanceof DexAPIError || error instanceof Error ? error.message : SESSION_COPY.startFailed);
    } finally {
      if (isOpen.current) setSubmitting(false);
    }
  };

  return (
    <dialog
      aria-labelledby="new-session-title"
      className="sfd nsd"
      ref={dialogRef}
      onCancel={(event) => {
        event.preventDefault();
        if (!submitting) onClose();
      }}
    >
      <form onSubmit={(event) => void submit(event)}>
        <header className="sfd-head">
          <div>
            <h2 id="new-session-title">{SESSION_COPY.newSessionTitle(flowType)}</h2>
            <p>StartAgentSession</p>
          </div>
          <button aria-label={SESSION_COPY.newSessionClose} disabled={submitting} onClick={onClose} type="button">×</button>
        </header>
        <div className="sfd-scroll">
          <fieldset className="nsd-fields" disabled={submitting}>
            {flowTypes.length > 1 && (
              <label className="sfd-field">
                <span>{SESSION_COPY.agentFlowType}</span>
                <select value={flowType} onChange={(event) => setFlowType(event.target.value)}>
                  {flowTypes.map((candidate) => <option key={candidate} value={candidate}>{candidate}</option>)}
                </select>
              </label>
            )}
            <label className="sfd-field">
              <span>{SESSION_COPY.sessionId}</span>
              <input value={draft.sessionId} onChange={(event) => update('sessionId')(event.target.value)} />
              <small className="nsd-hint">{SESSION_COPY.sessionIdHint}</small>
              {fieldErrors.sessionId && <small className="sfd-error" role="alert">{fieldErrors.sessionId}</small>}
            </label>
            <label className="sfd-field">
              <span>{SESSION_COPY.firstMessage}</span>
              <textarea rows={3} value={draft.firstMessage} onChange={(event) => update('firstMessage')(event.target.value)} />
              <small className="nsd-hint">{SESSION_COPY.firstMessageHint}</small>
            </label>
            <p className="ses-settings-warning">
              <ShieldAlert aria-hidden="true" size={14} />
              <span>{SESSION_COPY.settingsNoSecrets}</span>
            </p>
            <label className="sfd-field">
              <span><code>{SESSION_COPY.modelSetting}</code></span>
              <input value={draft.model} onChange={(event) => update('model')(event.target.value)} />
              <small className="nsd-hint">{SESSION_COPY.modelHint}</small>
            </label>
            <label className="sfd-field">
              <span><code>{SESSION_COPY.systemPromptSetting}</code></span>
              <textarea rows={3} value={draft.systemPrompt} onChange={(event) => update('systemPrompt')(event.target.value)} />
              <small className="nsd-hint">{SESSION_COPY.systemPromptHint}</small>
            </label>
            <label className="sfd-field">
              <span>{SESSION_COPY.otherSettings}</span>
              <textarea
                className="ses-settings-json"
                placeholder="{}"
                rows={4}
                spellCheck={false}
                value={draft.otherSettings}
                onChange={(event) => update('otherSettings')(event.target.value)}
              />
              <small className="nsd-hint">{SESSION_COPY.otherSettingsHint}</small>
              {fieldErrors.otherSettings && <small className="sfd-error" role="alert">{fieldErrors.otherSettings}</small>}
            </label>
            {submitError && <div className="error-banner" role="alert">{submitError}</div>}
          </fieldset>
        </div>
        <footer className="sfd-actions">
          <button disabled={submitting} onClick={onClose} type="button">{SESSION_COPY.cancel}</button>
          <button className="button primary" disabled={submitting || flowType === ''} type="submit">
            {submitting ? SESSION_COPY.startingSession : SESSION_COPY.startSession}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
