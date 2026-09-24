// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { readResponseJSON } from '@/lib/http';
import { dexFetch } from '@/lib/webConfig';
import './connections.css';

type ConnectionStatus = 'Missing' | 'Ready' | 'Expired' | 'Conflict' | 'Unsupported';

interface ConnectionUse {
  flowName: string;
  stepId: string;
  stepName: string;
  operationId: string;
  operationKind: string;
}

interface ConnectionView {
  connectorId: string;
  connectionName: string;
  modulePath?: string;
  moduleVersion?: string;
  provider?: string;
  status: ConnectionStatus;
  configuration?: Record<string, unknown>;
  credentialExpiresAt?: string;
  uses: ConnectionUse[];
}

interface ConnectionsResponse {
  enabled: boolean;
  directory: string;
  filePath: string;
  definitionRevision: string;
  csrfToken: string;
  launchCommand: string;
  connections: ConnectionView[];
}

interface ManifestField {
  name: string;
  type: string;
  description: string;
  required: boolean;
  default?: unknown;
  enum?: string[];
}

interface ReleaseManifest {
  metadata: { displayName: string; description: string };
  spec: {
    provider: string;
    configuration: { fields: ManifestField[] };
    auth: {
      type: string;
      fields: ManifestField[];
      oauth2?: { scopes: string[] };
    };
    studio?: { setup: { backendCapabilities: string[] } };
  };
}

interface UISessionResponse {
  connectorId: string;
  connectionName: string;
  sessionNonce?: string;
  entrypointUrl?: string;
  manifest: ReleaseManifest;
}

export function ConnectionsPage() {
  const [catalog, setCatalog] = useState<ConnectionsResponse | null>(null);
  const [selected, setSelected] = useState<ConnectionView | null>(null);
  const [session, setSession] = useState<UISessionResponse | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    const response = await dexFetch('/api/v2/connector-connections', { signal });
    const next = await readResponseJSON<ConnectionsResponse>(response);
    setCatalog(next);
    setSelected((current) => current
      ? next.connections.find((connection) => connectionKey(connection) === connectionKey(current)) ?? null
      : next.connections[0] ?? null);
    setError('');
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal).catch((loadError: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(loadError));
    });
    return () => controller.abort();
  }, [load]);

  useEffect(() => {
    setSession(null);
    if (!catalog || !selected || selected.status === 'Unsupported' || selected.status === 'Conflict') return;
    const controller = new AbortController();
    setBusy(true);
    void dexFetch('/api/v2/connector-ui-sessions', {
      method: 'POST',
      signal: controller.signal,
      headers: connectorWriteHeaders(catalog),
      body: JSON.stringify({ connectorId: selected.connectorId, connectionName: selected.connectionName }),
    }).then((response) => readResponseJSON<UISessionResponse>(response))
      .then((value) => setSession(value))
      .catch((sessionError: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(sessionError));
      }).finally(() => {
        if (!controller.signal.aborted) setBusy(false);
      });
    return () => controller.abort();
  }, [catalog, selected?.connectorId, selected?.connectionName, selected?.status]);

  const deleteCredentials = async () => {
    if (!catalog || !selected) return;
    setBusy(true);
    try {
      const response = await dexFetch(connectionURL(selected), {
        method: 'DELETE', headers: connectorWriteHeaders(catalog),
      });
      await readResponseJSON<{ deleted: boolean }>(response);
      await load();
    } catch (deleteError) {
      setError(errorMessage(deleteError));
    } finally {
      setBusy(false);
    }
  };

  if (error && !catalog) return <div className="connections-page"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Connections…</div>;
  return (
    <div className="connections-page">
      <header className="connections-hero">
        <div><p className="connections-kicker">Local development</p><h1>Connections</h1></div>
        <div className="connections-paths">
          <span>Store directory</span><code>{catalog.directory}</code>
          <span>Connection file</span><CopyValue value={catalog.filePath} />
          <span>Start your app</span><CopyValue value={catalog.launchCommand} />
        </div>
      </header>
      {error && <div className="error-banner">{error}</div>}
      <div className="connections-layout">
        <aside className="connections-list" aria-label="Named connections">
          {catalog.connections.length === 0 && <p className="connections-empty">No configurable Connector Steps were found.</p>}
          {catalog.connections.map((connection) => (
            <button
              className="connection-row"
              data-selected={selected ? connectionKey(connection) === connectionKey(selected) : false}
              key={connectionKey(connection)}
              onClick={() => setSelected(connection)}
              type="button"
            >
              <span><b>{connection.connectorId}</b><small>{connection.connectionName || 'Unnamed connection'}</small></span>
              <Status status={connection.status} />
            </button>
          ))}
        </aside>
        <section className="connection-detail">
          {!selected && <p>Select a named connection.</p>}
          {selected && (
            <>
              <div className="connection-title">
                <div><h2>{selected.connectorId} / {selected.connectionName || 'unnamed'}</h2><code>{selected.moduleVersion || 'No exact release'}</code></div>
                <Status status={selected.status} />
              </div>
              <div className="connection-uses">
                {selected.uses.map((use) => <div key={`${use.flowName}:${use.stepId}`}>
                  <b>{use.flowName}</b><span>{use.stepName}</span><code>{use.operationId} · {use.operationKind}</code>
                </div>)}
              </div>
              {selected.status === 'Conflict' && <p className="connection-warning">The same connector and connection name use different module versions. Align the Flow dependencies before configuring.</p>}
              {selected.status === 'Unsupported' && <p className="connection-warning">Automatic setup requires an exact official release and a static ConnectionName.</p>}
              {selected.credentialExpiresAt && <p>Token expires: <time>{selected.credentialExpiresAt}</time></p>}
              {session?.entrypointUrl && <StudioFrame connection={selected} session={session} />}
              {session && <ConnectorForm
                catalog={catalog}
                connection={selected}
                manifest={session.manifest}
                onConfigured={load}
                onError={setError}
              />}
              {busy && <p role="status">Loading Connector release…</p>}
              {selected.status !== 'Missing' && selected.status !== 'Unsupported' && selected.status !== 'Conflict' && (
                <button className="connection-delete" disabled={busy} onClick={() => void deleteCredentials()} type="button">
                  Delete local credentials
                </button>
              )}
              <p className="connections-note">Deleting local credentials does not revoke the provider grant. Configuration changes require an app restart; credential changes apply on the next Connector call.</p>
            </>
          )}
        </section>
      </div>
    </div>
  );
}

function ConnectorForm({ catalog, connection, manifest, onConfigured, onError }: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  manifest: ReleaseManifest;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const oauth = manifest.spec.auth.type === 'oauth2';
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    try {
      const configuration = fieldValues(manifest.spec.configuration.fields, values, 'configuration');
      if (oauth) {
        const credentialValues = fieldValues(
          manifest.spec.auth.fields.filter((field) => field.type !== 'secretString'), values, 'credential',
        );
        const credentialSecrets = Object.fromEntries(manifest.spec.auth.fields
          .filter((field) => field.type === 'secretString' && field.name !== 'access_token')
          .map((field) => [field.name, values[`credential:${field.name}`] ?? '']));
        const response = await dexFetch(`${connectionURL(connection)}/oauth/start`, {
          method: 'POST', headers: connectorWriteHeaders(catalog), body: JSON.stringify({
            clientId: values.clientId ?? '', clientSecret: values.clientSecret ?? '',
            configuration, credentialValues, credentialSecrets,
          }),
        });
        const result = await readResponseJSON<{ authorizationUrl: string }>(response);
        window.location.assign(result.authorizationUrl);
        return;
      }
      const credentials = fieldValues(manifest.spec.auth.fields, values, 'credential');
      const response = await dexFetch(connectionURL(connection), {
        method: 'PUT', headers: connectorWriteHeaders(catalog), body: JSON.stringify({
          modulePath: connection.modulePath,
          moduleVersion: connection.moduleVersion,
          provider: manifest.spec.provider,
          configuration,
          credentials,
          credentialExpiresAt: null,
        }),
      });
      await readResponseJSON(response);
      setValues({});
      await onConfigured();
    } catch (submitError) {
      onError(errorMessage(submitError));
    } finally {
      setSubmitting(false);
    }
  };
  return <form className="connector-form" id="connector-host-form" onSubmit={(event) => void submit(event)}>
    <h3>{manifest.metadata.displayName || connection.connectorId} setup</h3>
    <p>{manifest.metadata.description}</p>
    {oauth && <>
      <FormField inputId="connector-oauth-client-id" label="OAuth client ID" name="clientId" required secret={false} values={values} setValues={setValues} />
      <FormField label="OAuth client secret" name="clientSecret" required secret values={values} setValues={setValues} />
      <p className="connections-note">Client credentials remain in memory for this ten-minute OAuth session and are never written to the connection file.</p>
    </>}
    {manifest.spec.configuration.fields.map((field) => <ManifestFormField key={`configuration:${field.name}`} field={field} prefix="configuration" values={values} setValues={setValues} />)}
    {manifest.spec.auth.fields.filter((field) => !oauth || field.name !== 'access_token').map((field) => <ManifestFormField key={`credential:${field.name}`} field={field} prefix="credential" values={values} setValues={setValues} />)}
    {oauth && <p className="connections-scopes">Requested scopes: {manifest.spec.auth.oauth2?.scopes.join(', ')}</p>}
    <button className="v2-primary" disabled={submitting} type="submit">{oauth ? 'Authorize' : 'Save local credentials'}</button>
  </form>;
}

function ManifestFormField({ field, prefix, values, setValues }: {
  field: ManifestField;
  prefix: string;
  values: Record<string, string>;
  setValues: (next: Record<string, string>) => void;
}) {
  return <FormField
    label={field.name}
    name={`${prefix}:${field.name}`}
    required={field.required && field.default === undefined}
    secret={field.type === 'secretString'}
    description={field.description}
    values={values}
    setValues={setValues}
  />;
}

function FormField({ inputId, label, name, required, secret, description, values, setValues }: {
  inputId?: string;
  label: string;
  name: string;
  required: boolean;
  secret: boolean;
  description?: string;
  values: Record<string, string>;
  setValues: (next: Record<string, string>) => void;
}) {
  return <label><span>{label}{required ? ' *' : ''}</span>
    <input
      autoComplete="off"
      id={inputId}
      required={required}
      type={secret ? 'password' : 'text'}
      value={values[name] ?? ''}
      onChange={(event) => setValues({ ...values, [name]: event.target.value })}
    />
    {description && <small>{description}</small>}
  </label>;
}

function StudioFrame({ connection, session }: { connection: ConnectionView; session: UISessionResponse }) {
  const frame = useRef<HTMLIFrameElement>(null);
  useEffect(() => {
    const receive = (event: MessageEvent<unknown>) => {
      if (event.source !== frame.current?.contentWindow || event.origin !== 'null' || !isStudioCommand(event.data, session)) return;
      const capability = studioCommandCapability(event.data.command);
      const supported = capability !== null && studioHostCapabilities(session).includes(capability);
      if (supported && (event.data.command === 'oauth.connect' || event.data.command === 'oauth.reconnect')) {
        const form = document.getElementById('connector-host-form');
        if (form instanceof HTMLFormElement) form.requestSubmit();
      }
      frame.current?.contentWindow?.postMessage({
        type: 'connector.command.result', protocolVersion: '0.1.0', sessionNonce: session.sessionNonce,
        connectorId: connection.connectorId, requestId: event.data.requestId, ok: supported,
        ...(!supported ? { error: { code: 'COMMAND_UNSUPPORTED', message: 'Connector command is not supported by this host.' } } : {}),
      }, '*');
    };
    window.addEventListener('message', receive);
    return () => window.removeEventListener('message', receive);
  }, [connection.connectorId, session]);
  const ready = () => frame.current?.contentWindow?.postMessage({
    type: 'connector.host.ready', protocolVersion: '0.1.0', sessionNonce: session.sessionNonce,
    connectorId: connection.connectorId,
    capabilities: studioHostCapabilities(session),
    connection: {
      state: studioState(connection.status), grantedScopes: [],
      detail: connection.status,
    },
    configuration: connection.configuration ?? {},
  }, '*');
  return <iframe
    className="connector-studio"
    onLoad={ready}
    ref={frame}
    sandbox="allow-scripts"
    src={session.entrypointUrl}
    title={`${connection.connectorId} Connector setup`}
  />;
}

type StudioCommand = 'oauth.connect' | 'oauth.reconnect' | 'configuration.save';

export function isStudioCommand(value: unknown, session: UISessionResponse): value is { requestId: string; command: StudioCommand } {
  if (typeof value !== 'object' || value === null) return false;
  const message = value as Record<string, unknown>;
  return message.type === 'connector.command' && message.protocolVersion === '0.1.0'
    && message.sessionNonce === session.sessionNonce && message.connectorId === session.connectorId
    && typeof message.requestId === 'string'
    && (message.command === 'oauth.connect' || message.command === 'oauth.reconnect' || message.command === 'configuration.save');
}

function studioCommandCapability(command: StudioCommand) {
  if (command === 'oauth.connect' || command === 'oauth.reconnect') return 'oauth.connection.manage';
  if (command === 'configuration.save') return 'configuration.write';
  return null;
}

export function studioHostCapabilities(session: UISessionResponse): string[] {
  const declared = session.manifest.spec.studio?.setup.backendCapabilities ?? [];
  return declared.filter((capability) => capability === 'oauth.connection.manage');
}

function fieldValues(fields: ManifestField[], values: Record<string, string>, prefix: string) {
  return Object.fromEntries(fields
    .map((field) => [field.name, values[`${prefix}:${field.name}`] ?? ''] as const)
    .filter(([, value]) => value !== ''));
}

function Status({ status }: { status: ConnectionStatus }) {
  return <span className="connection-status" data-status={status.toLowerCase()}>{status}</span>;
}

function CopyValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return <span className="copy-value"><code>{value}</code><button type="button" onClick={() => {
    void navigator.clipboard.writeText(value).then(() => setCopied(true));
  }}>{copied ? 'Copied' : 'Copy'}</button></span>;
}

export function studioState(status: ConnectionStatus) {
  if (status === 'Ready') return 'connected';
  if (status === 'Expired') return 'expired';
  if (status === 'Missing') return 'not_configured';
  return 'error';
}

export function connectorWriteHeaders(catalog: ConnectionsResponse) {
  return {
    'Content-Type': 'application/json',
    'X-Dex-CSRF-Token': catalog.csrfToken,
    'X-Dex-Flow-Definition-Revision': catalog.definitionRevision,
  };
}

function connectionURL(connection: ConnectionView) {
  return `/api/v2/connector-connections/${encodeURIComponent(connection.connectorId)}/${encodeURIComponent(connection.connectionName)}`;
}

export function connectionKey(connection: ConnectionView) {
  return `${connection.connectorId}\u0000${connection.connectionName}`;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'Connector request failed';
}
