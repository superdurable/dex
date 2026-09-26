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
  configurationUI: ConnectorConfigurationUI;
  configuration: Record<string, unknown>;
  configured: boolean;
}

interface ConnectorUIBinding { port: string; jsonPointer: string; }
interface ConnectorUIUnit { id: string; unitId: string; label: string; description?: string; required: boolean; bindings: ConnectorUIBinding[]; }
interface ConnectorConfigurationUI { units: ConnectorUIUnit[]; }
interface TriggerUse { flowName: string; triggerName: string; bindingName: string; configurationUI: ConnectorConfigurationUI; configuration: Record<string, unknown>; configured: boolean; }

interface ConnectionView {
  connectorId: string;
  connectionName: string;
  modulePath?: string;
  moduleVersion?: string;
  localOverride?: boolean;
  provider?: string;
  status: ConnectionStatus;
  configuration?: Record<string, unknown>;
  credentialExpiresAt?: string;
  uses: ConnectionUse[];
  triggerUses?: TriggerUse[];
}

interface ConnectionsResponse {
  enabled: boolean;
  directory: string;
  filePath: string;
  useConfigurationsFilePath: string;
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
      oauth2?: {
        scopes: string[];
        userScopes?: string[];
        credentialMappings?: { credential: string; source: string }[];
      };
    };
    studio?: { setup: { backendCapabilities: string[] }; units?: { id: string; description: string; backendCapabilities?: string[]; inputs?: {name: string; type: string}[]; outputs: {name: string; type: string}[] }[] };
  };
}

interface UISessionResponse {
  connectorId: string;
  connectionName: string;
  sessionNonce?: string;
  entrypointUrl?: string;
  manifest: ReleaseManifest;
}

type StudioTarget = {kind: 'connection'} | {
  kind: 'configurationUnit';
  scope: {kind: 'operation'; operationId: string; flowType: string; stepType: string} | {kind: 'trigger'; triggerName: string; bindingName: string; flowType: string};
  instanceId: string;
  unitId: string;
  label: string;
  description?: string;
  required: boolean;
  bindings: ConnectorUIBinding[];
  value: Record<string, unknown>;
};

type ConnectorSetupTab =
  | {key: 'authorize'; kind: 'authorize'; label: string; detail: string; configured: boolean}
  | {key: string; kind: 'operation'; label: string; detail: string; configured: boolean; use: ConnectionUse}
  | {key: string; kind: 'trigger'; label: string; detail: string; configured: boolean; use: TriggerUse};

export function ConnectionsPage() {
  const [catalog, setCatalog] = useState<ConnectionsResponse | null>(null);
  const [selected, setSelected] = useState<ConnectionView | null>(null);
  const [session, setSession] = useState<UISessionResponse | null>(null);
  const [selectedSetupTabKey, setSelectedSetupTabKey] = useState('authorize');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const initializedSetupConnection = useRef('');

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

  useEffect(() => {
    if (!selected || !session) return;
    const selectedConnectionKey = connectionKey(selected);
    const tabs = connectorSetupTabs(selected, Boolean(session.entrypointUrl));
    const initialTabKey = initialConnectorSetupTabKey(selected, Boolean(session.entrypointUrl));
    setSelectedSetupTabKey((current) => {
      if (initializedSetupConnection.current !== selectedConnectionKey) {
        initializedSetupConnection.current = selectedConnectionKey;
        return initialTabKey;
      }
      const currentTab = tabs.find((tab) => tab.key === current);
      if (!currentTab || (currentTab.kind !== 'authorize' && selected.status !== 'Ready')) return initialTabKey;
      return current;
    });
  }, [selected?.connectorId, selected?.connectionName, selected?.status, session?.entrypointUrl]);

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
  const setupTabs = selected && session ? connectorSetupTabs(selected, Boolean(session.entrypointUrl)) : [];
  const requestedSetupTab = setupTabs.find((tab) => tab.key === selectedSetupTabKey);
  const activeSetupTab = selected && setupTabs.length > 0
    ? requestedSetupTab && (requestedSetupTab.kind === 'authorize' || selected.status === 'Ready')
      ? requestedSetupTab
      : setupTabs.find((tab) => tab.key === initialConnectorSetupTabKey(selected, Boolean(session?.entrypointUrl))) ?? setupTabs[0]
    : null;
  return (
    <div className="connections-page">
      <header className="connections-hero">
        <div><p className="connections-kicker">Local development</p><h1>Connections</h1></div>
        <div className="connections-paths">
          <span>Store directory</span><code>{catalog.directory}</code>
          <span>Connection file</span><CopyValue value={catalog.filePath} />
          <span>Flow configuration file</span><CopyValue value={catalog.useConfigurationsFilePath} />
          <span>Start your app</span><CopyValue value={catalog.launchCommand} />
        </div>
      </header>
      {error && <div className="error-banner">{error}</div>}
      <div className="connections-layout">
        <aside className="connections-list" aria-label="Named connections">
          {catalog.connections.length === 0 && <p className="connections-empty">No configurable Connector Steps or Triggers were found.</p>}
          {catalog.connections.map((connection) => {
            const isSelected = selected ? connectionKey(connection) === connectionKey(selected) : false;
            return <div className="connection-list-item" data-selected={isSelected} key={connectionKey(connection)}>
              <button
                className="connection-row"
                data-selected={isSelected}
                onClick={() => {
                  setSelected(connection);
                  setSelectedSetupTabKey('authorize');
                }}
                type="button"
              >
                <span><b>{connection.connectorId}</b><small>{connection.connectionName || 'Unnamed connection'}</small></span>
                <Status status={connection.status} />
              </button>
              {isSelected && activeSetupTab && <ConnectorSetupNavigation
                activeTab={activeSetupTab}
                connection={connection}
                onSelect={setSelectedSetupTabKey}
                tabs={setupTabs}
              />}
            </div>;
          })}
        </aside>
        <section className="connection-detail">
          {!selected && <p>Select a named connection.</p>}
          {selected && (
            <>
              <div className="connection-title">
                <div><h2>{selected.connectorId} / {selected.connectionName || 'unnamed'}</h2><code>{selected.localOverride ? `Local override · ${selected.moduleVersion}` : selected.moduleVersion || 'No exact release'}</code></div>
                <Status status={selected.status} />
              </div>
              {selected.status === 'Conflict' && <p className="connection-warning">The same connector and connection name use different module versions. Align the Flow dependencies before configuring.</p>}
              {selected.status === 'Unsupported' && <p className="connection-warning">Automatic setup requires an exact official release and a static ConnectionName.</p>}
              {selected.credentialExpiresAt && <p>Token expires: <time>{selected.credentialExpiresAt}</time></p>}
              {busy && <p role="status">Loading Connector release…</p>}
              {session && activeSetupTab && <ConnectorSetupPanel
                activeTab={activeSetupTab} catalog={catalog} connection={selected} session={session}
                onConfigured={load} onError={setError} onSelect={setSelectedSetupTabKey} setupTabs={setupTabs}
              />}
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

function ConnectorSetupNavigation({activeTab, connection, tabs, onSelect}: {
  activeTab: ConnectorSetupTab;
  connection: ConnectionView;
  tabs: ConnectorSetupTab[];
  onSelect: (key: string) => void;
}) {
  return <div className="connector-setup-tabs" role="tablist" aria-label={`${connection.connectorId} setup`}>
    {tabs.map((tab, index) => {
      const disabled = tab.kind !== 'authorize' && connection.status !== 'Ready';
      return <button
        aria-controls="connector-setup-panel"
        aria-selected={activeTab.key === tab.key}
        className="connector-setup-tab"
        data-configured={tab.configured}
        disabled={disabled}
        id={`connector-setup-tab-${tab.key}`}
        key={tab.key}
        onClick={() => onSelect(tab.key)}
        role="tab"
        type="button"
      >
        <span className="connector-setup-tab-order">{index + 1}</span>
        <span className="connector-setup-tab-copy"><b>{tab.label}</b><small>{tab.detail}</small></span>
        <span aria-label={tab.configured ? 'Configured' : 'Not configured'} className="connector-setup-tab-status">{tab.configured ? '✓' : ''}</span>
      </button>;
    })}
  </div>;
}

function ConnectorSetupPanel({activeTab, catalog, connection, session, setupTabs, onConfigured, onError, onSelect}: {
  activeTab: ConnectorSetupTab;
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  session: UISessionResponse;
  setupTabs: ConnectorSetupTab[];
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
  onSelect: (key: string) => void;
}) {
  const completeAuthorization = async () => {
    await onConfigured();
    const firstConfiguration = setupTabs.find((tab) => tab.kind !== 'authorize');
    if (firstConfiguration) onSelect(firstConfiguration.key);
  };
  return <div aria-labelledby={`connector-setup-tab-${activeTab.key}`} className="connector-setup-panel" id="connector-setup-panel" role="tabpanel">
    {activeTab.kind === 'authorize'
      ? <AuthorizationPanel
        catalog={catalog} connection={connection} manifest={session.manifest}
        onConfigured={completeAuthorization} onError={onError}
      />
      : <ConnectorUsePanel
        catalog={catalog} connection={connection} onConfigured={onConfigured} onError={onError}
        session={session} tab={activeTab}
      />}
  </div>;
}

function AuthorizationPanel({catalog, connection, manifest, onConfigured, onError}: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  manifest: ReleaseManifest;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const [reauthorizing, setReauthorizing] = useState(false);
  if (connection.status === 'Ready' && !reauthorizing) {
    return <div className="connector-authorization-complete">
      <span aria-hidden="true" className="connector-authorization-check">✓</span>
      <div><h3>Authorization complete</h3><p>This connection is ready. Continue with each Flow operation and trigger.</p></div>
      <button className="connector-secondary-action" onClick={() => setReauthorizing(true)} type="button">Reauthorize</button>
    </div>;
  }
  return <ConnectorForm
    catalog={catalog} connection={connection} manifest={manifest}
    onConfigured={onConfigured} onError={onError}
  />;
}

function ConnectorUsePanel({catalog, connection, session, tab, onConfigured, onError}: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  session: UISessionResponse;
  tab: Extract<ConnectorSetupTab, {kind: 'operation' | 'trigger'}>;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  return <section className="connector-use">
    <header>
      <div><span className="connector-use-kind">{tab.kind}</span><h3>{tab.label}</h3></div>
      <p><b>{tab.use.flowName}</b> · {tab.kind === 'operation' ? tab.use.stepName : tab.use.bindingName}</p>
    </header>
    <div className="connector-use-units">
      {tab.kind === 'operation'
        ? tab.use.configurationUI.units.map((unit) => <StudioFrame
          catalog={catalog} connection={connection} configuration={tab.use.configuration} key={`${tab.key}:${unit.id}`}
          onConfigured={onConfigured} onError={onError} session={session}
          target={configurationUnitTarget(unit, {
            kind: 'operation', operationId: tab.use.operationId, flowType: tab.use.flowName, stepType: tab.use.stepName,
          }, tab.use.configuration)}
        />)
        : tab.use.configurationUI.units.map((unit) => <StudioFrame
          catalog={catalog} connection={connection} configuration={tab.use.configuration} key={`${tab.key}:${unit.id}`}
          onConfigured={onConfigured} onError={onError} session={session}
          target={configurationUnitTarget(unit, {
            kind: 'trigger', triggerName: tab.use.triggerName, bindingName: tab.use.bindingName, flowType: tab.use.flowName,
          }, tab.use.configuration)}
        />)}
    </div>
  </section>;
}

export function connectorSetupTabs(connection: ConnectionView, hasStudio = true): ConnectorSetupTab[] {
  const tabs: ConnectorSetupTab[] = [{
    key: 'authorize', kind: 'authorize', label: 'Authorize', detail: connection.connectionName || 'Connection',
    configured: connection.status === 'Ready',
  }];
  if (!hasStudio) return tabs;
  for (const use of connection.uses.filter((candidate) => candidate.configurationUI.units.length > 0)) {
    tabs.push({
      key: `operation:${use.flowName}:${use.stepId}`, kind: 'operation', label: use.operationId,
      detail: `${use.stepName} · ${use.operationKind}`, configured: use.configured, use,
    });
  }
  for (const use of connection.triggerUses?.filter((candidate) => candidate.configurationUI.units.length > 0) ?? []) {
    tabs.push({
      key: `trigger:${use.flowName}:${use.bindingName}`, kind: 'trigger', label: use.triggerName,
      detail: `${use.bindingName} · trigger`, configured: use.configured, use,
    });
  }
  return tabs;
}

export function initialConnectorSetupTabKey(connection: ConnectionView, hasStudio = true) {
  if (connection.status !== 'Ready') return 'authorize';
  const configurationTabs = connectorSetupTabs(connection, hasStudio).filter((tab) => tab.kind !== 'authorize');
  return configurationTabs.find((tab) => !tab.configured)?.key ?? configurationTabs[0]?.key ?? 'authorize';
}

function configurationUnitTarget(
  unit: ConnectorUIUnit,
  scope: Extract<StudioTarget, {kind: 'configurationUnit'}>['scope'],
  configuration: Record<string, unknown>,
): StudioTarget {
  return {
    kind: 'configurationUnit', scope, instanceId: unit.id, unitId: unit.unitId, label: unit.label,
    description: unit.description, required: unit.required,
    bindings: unit.bindings,
    value: Object.fromEntries(unit.bindings.map((binding) => [binding.port, jsonPointerValue(configuration, binding.jsonPointer)])),
  };
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
        const mappedCredentials = new Set(manifest.spec.auth.oauth2?.credentialMappings?.map((mapping) => mapping.credential) ?? ['access_token']);
        const credentialValues = fieldValues(
          manifest.spec.auth.fields.filter((field) => field.type !== 'secretString'), values, 'credential',
        );
        const credentialSecrets = Object.fromEntries(manifest.spec.auth.fields
          .filter((field) => field.type === 'secretString' && !mappedCredentials.has(field.name))
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
    {manifest.spec.auth.fields.filter((field) => !oauth || !(manifest.spec.auth.oauth2?.credentialMappings?.map((mapping) => mapping.credential) ?? ['access_token']).includes(field.name)).map((field) => <ManifestFormField key={`credential:${field.name}`} field={field} prefix="credential" values={values} setValues={setValues} />)}
    {oauth && <p className="connections-scopes">Requested bot scopes: {manifest.spec.auth.oauth2?.scopes.join(', ')}{manifest.spec.auth.oauth2?.userScopes?.length ? `; user scopes: ${manifest.spec.auth.oauth2.userScopes.join(', ')}` : ''}</p>}
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

function StudioFrame({ catalog, connection, configuration, session, target, onConfigured, onError }: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  configuration: Record<string, unknown>;
  session: UISessionResponse;
  target: StudioTarget;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [frameHeight, setFrameHeight] = useState<number>();
  useEffect(() => {
    const receive = (event: MessageEvent<unknown>) => {
      if (event.source !== frame.current?.contentWindow || event.origin !== 'null') return;
      if (isStudioFrameResize(event.data, session)) {
        setFrameHeight(event.data.height);
        return;
      }
      if (!isStudioCommand(event.data, session)) return;
      const commandMessage = event.data;
      const capability = studioCommandCapability(commandMessage.command);
      const supported = capability !== null && studioHostCapabilities(session).includes(capability);
      if (supported && (commandMessage.command === 'oauth.connect' || commandMessage.command === 'oauth.reconnect')) {
        const form = document.getElementById('connector-host-form');
        if (form instanceof HTMLFormElement) form.requestSubmit();
        frame.current?.contentWindow?.postMessage({
          type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
          connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: true,
        }, '*');
        return;
      }
      if (supported) {
        void executeStudioCommand(catalog, connection, configuration, target, commandMessage.command, commandMessage.input).then(async (value) => {
          frame.current?.contentWindow?.postMessage({
            type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
            connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: true, value,
          }, '*');
          if (commandMessage.command === 'use.configuration.save') await onConfigured();
        }).catch((commandError: unknown) => {
          onError(errorMessage(commandError));
          frame.current?.contentWindow?.postMessage({
            type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
            connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: false,
            error: { code: 'COMMAND_FAILED', message: errorMessage(commandError) },
          }, '*');
        });
        return;
      }
      frame.current?.contentWindow?.postMessage({
        type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
        connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: false,
        error: { code: 'COMMAND_UNSUPPORTED', message: 'Connector command is not supported by this host.' },
      }, '*');
    };
    window.addEventListener('message', receive);
    return () => window.removeEventListener('message', receive);
  }, [catalog, configuration, connection, onConfigured, onError, session, target]);
  const ready = () => frame.current?.contentWindow?.postMessage({
    type: 'connector.host.ready', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
    connectorId: connection.connectorId,
    capabilities: studioHostCapabilities(session),
    connection: {
      state: studioState(connection.status), grantedScopes: [],
      detail: connection.status,
    },
    target,
  }, '*');
  const sendReadyAfterStudioMount = () => window.setTimeout(ready, 100);
  return <div
    aria-label={expanded ? `${connection.connectorId} Connector ${target.kind === 'connection' ? 'setup' : target.label}` : undefined}
    aria-modal={expanded || undefined}
    className="connector-studio-shell"
    data-expanded={expanded}
    data-surface={target.kind}
    role={expanded ? 'dialog' : undefined}
  >
    <div className="connector-studio-toolbar">
      <button className="connector-studio-expand" onClick={() => setExpanded((current) => !current)} type="button">
        {expanded ? 'Close expanded setup' : 'Expand setup'}
      </button>
    </div>
    <iframe
      className="connector-studio"
      onLoad={sendReadyAfterStudioMount}
      ref={frame}
      sandbox="allow-scripts"
      src={session.entrypointUrl}
      style={expanded || frameHeight === undefined ? undefined : {height: `${frameHeight}px`}}
      title={`${connection.connectorId} Connector ${target.kind === 'connection' ? 'setup' : target.label}`}
    />
  </div>;
}

type StudioCommand = 'oauth.connect' | 'oauth.reconnect' | 'google.picker.open-spreadsheet' | 'google.sheets.list-tabs' | 'slack.channels.list' | 'slack.users.list' | 'use.configuration.save';

export function isStudioCommand(value: unknown, session: UISessionResponse): value is { requestId: string; command: StudioCommand; input?: Record<string, unknown> } {
  if (typeof value !== 'object' || value === null) return false;
  const message = value as Record<string, unknown>;
  return message.type === 'connector.command' && message.protocolVersion === '0.2.0'
    && message.sessionNonce === session.sessionNonce && message.connectorId === session.connectorId
    && typeof message.requestId === 'string'
    && (message.input === undefined || isRecord(message.input))
    && (message.command === 'oauth.connect' || message.command === 'oauth.reconnect'
      || message.command === 'google.picker.open-spreadsheet' || message.command === 'google.sheets.list-tabs'
      || message.command === 'slack.channels.list' || message.command === 'slack.users.list' || message.command === 'use.configuration.save');
}

export function isStudioFrameResize(value: unknown, session: UISessionResponse): value is {height: number} {
  if (typeof value !== 'object' || value === null) return false;
  const message = value as Record<string, unknown>;
  return message.type === 'connector.frame.resize' && message.protocolVersion === '0.2.0'
    && message.sessionNonce === session.sessionNonce && message.connectorId === session.connectorId
    && typeof message.height === 'number' && Number.isInteger(message.height)
    && message.height >= 80 && message.height <= 4096;
}

function studioCommandCapability(command: StudioCommand) {
  if (command === 'oauth.connect' || command === 'oauth.reconnect') return 'oauth.connection.manage';
  if (command === 'use.configuration.save') return 'use.configuration.write';
  if (command === 'google.picker.open-spreadsheet') return 'google.picker.spreadsheets';
  if (command === 'google.sheets.list-tabs') return 'google.sheets.tabs-list';
  if (command === 'slack.channels.list') return 'slack.channels-list';
  if (command === 'slack.users.list') return 'slack.users-list';
  return null;
}

export function studioHostCapabilities(session: UISessionResponse): string[] {
  const declared = session.manifest.spec.studio?.setup.backendCapabilities ?? [];
  const supported = new Set(['oauth.connection.manage', 'use.configuration.write', 'slack.channels-list', 'slack.users-list']);
  return declared.filter((capability) => supported.has(capability));
}

async function executeStudioCommand(
  catalog: ConnectionsResponse,
  connection: ConnectionView,
  configuration: Record<string, unknown>,
  targetScope: StudioTarget,
  command: StudioCommand,
  input?: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  let target = connectionURL(connection);
  let method = 'GET';
  let body: string | undefined;
  if (command === 'slack.channels.list') target += '/slack/channels';
  else if (command === 'slack.users.list') target += '/slack/users';
  else if (command === 'use.configuration.save' && targetScope.kind === 'configurationUnit') {
    const value = recordInput(input, 'value');
    const unit = targetScope;
    const nextConfiguration = mergeUnitValue(configuration, unit, value);
    if (unit.scope.kind === 'trigger') {
      target = `/api/v2/connector-trigger-bindings/${encodeURIComponent(connection.connectorId)}/${encodeURIComponent(connection.connectionName)}/${encodeURIComponent(unit.scope.triggerName)}/${encodeURIComponent(unit.scope.bindingName)}`;
    } else {
      target = `/api/v2/connector-use-configurations/${encodeURIComponent(connection.connectorId)}/${encodeURIComponent(connection.connectionName)}/${encodeURIComponent(unit.scope.operationId)}/${encodeURIComponent(unit.scope.flowType)}/${encodeURIComponent(unit.scope.stepType)}`;
    }
    method = 'PUT';
    body = JSON.stringify({ configuration: nextConfiguration });
  } else {
    throw new Error('Connector command is not implemented');
  }
  const response = await dexFetch(target, { method, headers: connectorWriteHeaders(catalog), body });
  return readResponseJSON<Record<string, unknown>>(response);
}

function recordInput(input: Record<string, unknown> | undefined, name: string): Record<string, unknown> {
  const value = input?.[name];
  if (!isRecord(value)) throw new Error(`${name} is required`);
  return value;
}

function mergeUnitValue(
  configuration: Record<string, unknown>,
  target: Extract<StudioTarget, {kind: 'configurationUnit'}>,
  value: Record<string, unknown>,
): Record<string, unknown> {
  const next = JSON.parse(JSON.stringify(configuration)) as Record<string, unknown>;
  const allowedPorts = new Set(target.bindings.map((binding) => binding.port));
  for (const port of Object.keys(value)) {
    if (!allowedPorts.has(port)) throw new Error(`Unit returned undeclared port ${port}`);
  }
  for (const binding of target.bindings) {
    if (Object.hasOwn(value, binding.port)) setJSONPointerValue(next, binding.jsonPointer, value[binding.port]);
  }
  return next;
}

function jsonPointerValue(configuration: Record<string, unknown>, pointer: string): unknown {
  let current: unknown = configuration;
  for (const segment of jsonPointerSegments(pointer)) {
    if (!isRecord(current)) return undefined;
    current = current[segment];
  }
  return current;
}

function setJSONPointerValue(configuration: Record<string, unknown>, pointer: string, value: unknown) {
  const segments = jsonPointerSegments(pointer);
  if (segments.length === 0) throw new Error('Connector UI binding cannot replace the configuration root');
  let current = configuration;
  for (const segment of segments.slice(0, -1)) {
    if (!isRecord(current[segment])) current[segment] = {};
    current = current[segment] as Record<string, unknown>;
  }
  current[segments[segments.length - 1]] = value;
}

function jsonPointerSegments(pointer: string): string[] {
  if (!pointer.startsWith('/')) throw new Error('Connector UI binding has an invalid JSON Pointer');
  return pointer.slice(1).split('/').map((segment) => {
    const decoded = segment.replace(/~1/g, '/').replace(/~0/g, '~');
    if (decoded === '__proto__' || decoded === 'prototype' || decoded === 'constructor') throw new Error('Connector UI binding path is unsafe');
    return decoded;
  });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
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
