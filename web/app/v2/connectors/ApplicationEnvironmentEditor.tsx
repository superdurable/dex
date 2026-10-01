// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { readResponseJSON } from '@/lib/http';
import { dexFetch } from '@/lib/webConfig';

export interface ApplicationEnvironmentField {
  name: string;
  required: boolean;
  secret: boolean;
  minLength: number;
  enum: string[];
  value?: string;
  configured: boolean;
}
export interface ApplicationEnvironmentSnapshot {
  appManifestRevision: number;
  configurationRevision: number;
  csrfToken: string;
  fields: ApplicationEnvironmentField[];
  obsolete: string[];
}
interface EnvironmentWrite {
  values: Record<string, string>;
  secrets: Record<string, string>;
  remove: string[];
}

export function applicationEnvironmentWrite(snapshot: ApplicationEnvironmentSnapshot, changes: Record<string, string>, removed: string[]): EnvironmentWrite {
  const result: EnvironmentWrite = { values: {}, secrets: {}, remove: [...removed] };
  for (const field of snapshot.fields) {
    if (Object.hasOwn(changes, field.name) && !removed.includes(field.name)) {
      (field.secret ? result.secrets : result.values)[field.name] = changes[field.name];
    }
  }
  return result;
}

export function ApplicationEnvironmentEditor({ onSaved }: { onSaved: () => Promise<void> }) {
  const [snapshot, setSnapshot] = useState<ApplicationEnvironmentSnapshot | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(async (signal?: AbortSignal) => {
    const response = await dexFetch('/api/v2/application-environment', { signal });
    setSnapshot(await readResponseJSON<ApplicationEnvironmentSnapshot>(response));
    setError('');
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal).catch(() => { if (!controller.signal.aborted) setError('Application environment is unavailable. Reload to try again.'); });
    return () => controller.abort();
  }, [load]);
  return <div className="v2-case sc">
    {error && <p className="v2-error" role="alert">{error}</p>}
    {snapshot && <ApplicationEnvironmentForm key={`${snapshot.appManifestRevision}:${snapshot.configurationRevision}`} snapshot={snapshot} onSave={async (body) => {
      const response = await dexFetch('/api/v2/application-environment', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'X-Dex-CSRF-Token': snapshot.csrfToken, 'X-Dex-App-Manifest-Revision': String(snapshot.appManifestRevision), 'X-Dex-Configuration-Revision': String(snapshot.configurationRevision) },
        body: JSON.stringify(body),
      });
      setSnapshot(await readResponseJSON<ApplicationEnvironmentSnapshot>(response));
      await onSaved();
    }} />}
    <button className="v2-ghost" type="button" onClick={() => void load().catch(() => setError('Application environment could not be reloaded.'))}>Reload environment</button>
  </div>;
}

export function ApplicationEnvironmentForm({ snapshot, onSave }: { snapshot: ApplicationEnvironmentSnapshot; onSave: (body: EnvironmentWrite) => Promise<void> }) {
  const [changes, setChanges] = useState<Record<string, string>>({});
  const [removed, setRemoved] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('');
    try { await onSave(applicationEnvironmentWrite(snapshot, changes, removed)); }
    catch { setError('Environment was not saved. Check field requirements; reload if configuration changed.'); }
    finally { setBusy(false); }
  };
  const setRemovedField = (name: string, checked: boolean) => setRemoved((current) => checked ? [...current, name] : current.filter((value) => value !== name));
  return <form className="sc-block" aria-label="Application environment" onSubmit={(event) => void submit(event)}>
    <h3 className="sc-blockhead">Application environment</h3>
    <p className="sc-why">Configure the values declared by this application before building or deploying. Saved changes take effect with the next Preview start or Live deployment.</p>
    {snapshot.fields.length === 0 && <p>This application declares no environment settings.</p>}
    {snapshot.fields.map((field) => {
      const cleared = removed.includes(field.name);
      const current = Object.hasOwn(changes, field.name) ? changes[field.name] : field.secret ? '' : field.value ?? '';
      const id = `application-environment-${field.name}`;
      return <div className="connector-form-field" key={field.name}>
        <label htmlFor={id}>{field.name}{field.required ? ' (required)' : ' (optional)'}</label>
        {field.enum.length > 0 ? <select id={id} disabled={busy || cleared} value={current} onChange={(event) => setChanges((values) => ({ ...values, [field.name]: event.target.value }))}>
          {!field.enum.includes(current) && <option value="">Select a value</option>}
          {field.enum.map((value) => <option key={value} value={value}>{value === '' ? '(empty string)' : value}</option>)}
        </select> : <input id={id} disabled={busy || cleared} type={field.secret ? 'password' : 'text'} autoComplete="off" value={current}
          placeholder={field.secret && field.configured ? 'Configured; leave untouched to keep' : undefined}
          onChange={(event) => setChanges((values) => ({ ...values, [field.name]: event.target.value }))} />}
        <p className="sc-why">{field.secret ? 'Private value. The saved value is never returned. Leave untouched to keep it; enter a value to replace it.' : 'Ordinary string saved with this application’s configuration.'} {field.minLength > 0 ? `At least ${field.minLength} characters. ` : ''}{field.required ? 'Required before deployment.' : 'May be omitted.'}</p>
        {field.configured && <label><input type="checkbox" checked={cleared} disabled={busy} onChange={(event) => setRemovedField(field.name, event.target.checked)} /> Remove saved value</label>}
      </div>;
    })}
    {snapshot.obsolete.map((name) => <label key={name}><input type="checkbox" checked={removed.includes(name)} disabled={busy} onChange={(event) => setRemovedField(name, event.target.checked)} /> Remove {name}, which is no longer declared</label>)}
    {error && <p className="v2-error" role="alert">{error}</p>}
    <button className="v2-primary" disabled={busy || (Object.keys(changes).length === 0 && removed.length === 0)} type="submit">{busy ? 'Saving…' : 'Save environment'}</button>
  </form>;
}
