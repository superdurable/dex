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
import type { Theme } from '../../theme';
import {
  connectorStudioStylesheet,
  readConnectorStudioLightThemeTokens,
  type ConnectorStudioFrameAppearance,
} from './connectorStudioTheme';
import './connections.css';

// Connections has no dark theme yet, so every Studio frame paints light to match the page.
const connectorStudioFrameTheme: Theme = 'light';

type ConnectionStatus = 'Missing' | 'Ready' | 'Expired' | 'Conflict' | 'Unsupported' | 'Reauthorization required';

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
  authMethodId?: string;
  authMethodIds?: string[];
  connectionName: string;
  modulePath?: string;
  moduleVersion?: string;
  localOverride?: boolean;
  provider?: string;
  status: ConnectionStatus;
  configuration?: Record<string, unknown>;
  storedCredentialFields?: string[];
  credentialExpiresAt?: string;
  credentialStatus?: string;
  uses: ConnectionUse[];
  triggerUses?: TriggerUse[];
}

interface ConnectionsResponse {
  enabled: boolean;
  mode: 'local' | 'hosted';
  directory?: string;
  filePath?: string;
  useConfigurationsFilePath?: string;
  configurationRevision?: string;
  configurationState?: 'Draft' | 'Valid' | 'Ready to deploy' | 'Reauthorization required';
  applicationRevision?: string;
  definitionRevision: string;
  csrfToken: string;
  launchCommand?: string;
  connections: ConnectionView[];
}

interface ManifestField {
  name: string;
  type: string;
  description: string;
  required: boolean;
  default?: unknown;
  enum?: string[];
  studioUnit?: { unit: string; port: string };
}

interface ManifestAuthMethod {
  id: string;
  displayName: string;
  description: string;
  recommended?: boolean;
  type: string;
  fields: ManifestField[];
  configuration?: { fields: ManifestField[] };
  guide?: { startURL: string; steps: string[] };
  oauth2?: {
    scopes: string[];
    clientIDCredential?: string;
    clientSecretCredential?: string;
    userScopes?: string[];
    credentialMappings?: { credential: string; source: string }[];
    credentialDerivations?: { credential: string; endpoint: string; source: string; verifiedBy?: string }[];
  };
}

interface ReleaseManifest {
  metadata: { displayName: string; description: string };
  spec: {
    provider: string;
    configuration: { fields: ManifestField[] };
    auth: {
      type?: string;
      fields: ManifestField[];
      guide?: { startURL: string; steps: string[] };
      oauth2?: {
        scopes: string[];
        clientIDCredential?: string;
        clientSecretCredential?: string;
        userScopes?: string[];
        credentialMappings?: { credential: string; source: string }[];
        credentialDerivations?: { credential: string; endpoint: string; source: string; verifiedBy?: string }[];
      };
      defaultMethod?: string;
      selection?: 'single' | 'multiple';
      methodLabel?: string;
      methods?: ManifestAuthMethod[];
    };
    studio?: {
      setup: { backendCapabilities: string[] };
      commands?: {id: string; capability: string}[];
      units?: { id: string; description: string; backendCapabilities?: string[]; inputs?: {name: string; type: string}[]; outputs: {name: string; type: string}[] }[];
    };
  };
}

interface UISessionResponse {
  connectorId: string;
  connectionName: string;
  sessionNonce?: string;
  entrypointUrl?: string;
  oauthRedirectUri?: string;
  manifest: ReleaseManifest;
}

type StudioTarget = {
  kind: 'connection';
  unitId?: string;
  bindings?: ConnectorUIBinding[];
  value?: Record<string, unknown>;
} | {
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
        <div><p className="connections-kicker">{catalog.mode === 'hosted' ? 'Hosted environment' : 'Local development'}</p><h1>Connections</h1></div>
        {catalog.mode === 'hosted'
          ? <div className="connections-paths">
            <span>Configuration status</span><code>{catalog.configurationState || 'Draft'}</code>
            <span>Draft revision</span><code>{catalog.configurationRevision || 'Not created'}</code>
            <span>Application revision</span><code>{catalog.applicationRevision || 'Not deployed'}</code>
          </div>
          : <div className="connections-paths">
            <span>Store directory</span><code>{catalog.directory}</code>
            <span>Connection file</span>{catalog.filePath && <CopyValue value={catalog.filePath} />}
            <span>Flow configuration file</span>{catalog.useConfigurationsFilePath && <CopyValue value={catalog.useConfigurationsFilePath} />}
            <span>Start your app</span>{catalog.launchCommand && <CopyValue value={catalog.launchCommand} />}
          </div>}
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
                  {connectionDeleteLabel(catalog.mode)}
                </button>
              )}
              <p className="connections-note">{connectorConfigurationEffectText(catalog.mode)}</p>
            </>
          )}
        </section>
      </div>
    </div>
  );
}

export function connectionDeleteLabel(mode: ConnectionsResponse['mode']) {
  return mode === 'hosted' ? 'Delete connection' : 'Delete local credentials';
}

export function connectorConfigurationEffectText(mode: ConnectionsResponse['mode']) {
  return mode === 'hosted'
    ? 'Configuration changes create a new revision and require redeployment. Credential refresh and rotation apply on the next Connector call.'
    : 'Deleting local credentials does not revoke the provider grant. Configuration changes require an app restart; credential changes apply on the next Connector call.';
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
        catalog={catalog} connection={connection} key={connectionKey(connection)} session={session}
        onConfigured={completeAuthorization} onError={onError} onReload={onConfigured}
      />
      : <ConnectorUsePanel
        catalog={catalog} connection={connection} onConfigured={onConfigured} onError={onError}
        session={session} tab={activeTab}
      />}
  </div>;
}

function AuthorizationPanel({catalog, connection, session, onConfigured, onError, onReload}: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  session: UISessionResponse;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
  onReload: () => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const manifest = session.manifest;
  const isOAuthConnection = !isMultipleAuthMethodSelection(manifest)
    && selectedManifestAuth(manifest.spec.auth, connection.authMethodId ?? '').type === 'oauth2';
  if (connection.status === 'Ready' && !editing) {
    const studioFields = manifest.spec.configuration.fields.filter((field) => isStudioConnectionField(field, session));
    return <div className="connector-authorization-summary">
      <div className="connector-authorization-complete">
        <span aria-hidden="true" className="connector-authorization-check">✓</span>
        <div><h3>Authorization complete</h3><p>This connection is ready. Continue with each Flow operation and trigger.</p></div>
        <button className="connector-secondary-action" onClick={() => setEditing(true)} type="button">
          {isOAuthConnection ? 'Reauthorize' : 'Edit connection'}
        </button>
      </div>
      {studioFields.length > 0 && <section aria-label="Connection settings" className="connector-connection-settings">
        <h4>Connection settings</h4>
        {studioFields.map((field) => <ConnectionStudioField
          catalog={catalog} connection={connection} field={field} key={field.name}
          onConfigured={onReload} onError={onError} session={session}
        />)}
      </section>}
    </div>;
  }
  return <ConnectorForm
    catalog={catalog} connection={connection} session={session}
    onCancel={connection.status === 'Ready' ? () => setEditing(false) : undefined}
    onConfigured={async () => {
      await onConfigured();
      setEditing(false);
    }}
    onError={onError} onReload={onReload}
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

type ManifestFormFieldEntry = {field: ManifestField; prefix: 'configuration' | 'credential'};

export function ConnectorForm({ catalog, connection, session, onConfigured, onError, onReload = onConfigured, onCancel }: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  session: UISessionResponse;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
  onReload?: () => Promise<void>;
  onCancel?: () => void;
}) {
  const manifest = session.manifest;
  const isMultiple = isMultipleAuthMethodSelection(manifest);
  const [values, setValues] = useState<Record<string, string>>(() => connectionFormInitialValues(manifest, connection));
  const [submitting, setSubmitting] = useState(false);
  const [authMethodId, setAuthMethodId] = useState(
    connection.authMethodId || manifest.spec.auth.defaultMethod || manifest.spec.auth.methods?.[0]?.id || '',
  );
  const [authMethodIds, setAuthMethodIds] = useState(() => initialConnectionAuthMethodIds(manifest, connection));
  const auth = selectedManifestAuth(manifest.spec.auth, authMethodId);
  const oauth = !isMultiple && auth.type === 'oauth2';
  const storedCredentialFields = new Set(oauth ? [] : connection.storedCredentialFields ?? []);
  const storedValueFields = connectionStoredValueFieldNames(manifest, connection, session);
  const mappedCredentialNames = new Set([
    ...(auth.oauth2?.clientIDCredential ? [auth.oauth2.clientIDCredential] : []),
    ...(auth.oauth2?.clientSecretCredential ? [auth.oauth2.clientSecretCredential] : []),
    ...(auth.oauth2?.credentialMappings?.map((mapping) => mapping.credential) ?? ['access_token']),
    ...(auth.oauth2?.credentialDerivations?.map((derivation) => derivation.credential) ?? []),
  ]);
  const visibleManifestFormFields: ManifestFormFieldEntry[] = [
    ...manifest.spec.configuration.fields.map((field) => ({field, prefix: 'configuration'} as const)),
    ...(isMultiple ? [] : [
      ...methodConfigurationFields(auth).map((field) => ({field, prefix: 'configuration'} as const)),
      ...auth.fields
        .filter((field) => !oauth || !mappedCredentialNames.has(field.name))
        .map((field) => ({field, prefix: 'credential'} as const)),
    ]),
  ];
  const requiredManifestFormFields = visibleManifestFormFields.filter(({field}) => isManifestFieldInputRequired(field));
  const optionalManifestFormFields = visibleManifestFormFields.filter(({field}) => !isManifestFieldInputRequired(field));
  const renderFormField = ({field, prefix}: ManifestFormFieldEntry) => prefix === 'configuration'
    ? <ConnectionConfigurationField
      catalog={catalog} connection={connection} field={field} key={`${prefix}:${field.name}`}
      onConfigured={onReload} onError={onError} session={session} setValues={setValues} values={values}
    />
    : <ManifestFormField
      field={field} key={`${prefix}:${field.name}`} prefix={prefix}
      stored={storedCredentialFields.has(field.name)} setValues={setValues} values={values}
    />;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    try {
      if (oauth) {
        const configuration = connectionConfigurationValues(
          manifest, [auth], values, connection.configuration ?? {}, storedValueFields,
        );
        const credentialValues = fieldValues(
          auth.fields.filter((field) => field.type !== 'secretString'), values, 'credential',
        );
        const credentialSecrets = Object.fromEntries(auth.fields
          .filter((field) => field.type === 'secretString' && !mappedCredentialNames.has(field.name))
          .map((field) => [field.name, values[`credential:${field.name}`] ?? '']));
        const response = await dexFetch(`${connectionURL(connection)}/oauth/start`, {
          method: 'POST', headers: connectorWriteHeaders(catalog), body: JSON.stringify({
            authMethodId, clientId: values.clientId ?? '', clientSecret: values.clientSecret ?? '',
            configuration, credentialValues, credentialSecrets,
          }),
        });
        const result = await readResponseJSON<{ authorizationUrl: string }>(response);
        window.location.assign(result.authorizationUrl);
        return;
      }
      const response = await dexFetch(connectionURL(connection), {
        method: 'PUT', headers: connectorWriteHeaders(catalog),
        body: JSON.stringify(connectionWriteRequestBody(
          connection, manifest, isMultiple ? authMethodIds : [authMethodId], values, storedValueFields,
        )),
      });
      await readResponseJSON(response);
      setValues(configurationFormValues);
      await onConfigured();
    } catch (submitError) {
      onError(errorMessage(submitError));
    } finally {
      setSubmitting(false);
    }
  };
  const authMethodLabels = connectorAuthMethodLabels(manifest.spec.auth.methodLabel);
  return <form className="connector-form" id="connector-host-form" onSubmit={(event) => void submit(event)}>
    <h3>{manifest.metadata.displayName || connection.connectorId} setup</h3>
    <p>{manifest.metadata.description}</p>
    {!isMultiple && (manifest.spec.auth.methods?.length ?? 0) > 1 && <fieldset className="connector-auth-methods">
      <legend>Authentication method</legend>
      {manifest.spec.auth.methods?.map((method) => <label key={method.id}>
        <input
          checked={authMethodId === method.id}
          name="connector-auth-method"
          onChange={() => {
            setAuthMethodId(method.id);
            setValues(configurationFormValues);
          }}
          type="radio"
          value={method.id}
        />
        <span><b>{method.displayName}</b>{method.recommended && <em>Recommended</em>}<small>{method.description}</small></span>
      </label>)}
    </fieldset>}
    {!isMultiple && auth.guide && <AuthorizationGuide guide={auth.guide} redirectURI={oauth ? session.oauthRedirectUri : undefined} />}
    {isMultiple && <section aria-label={authMethodLabels.plural} className="connector-auth-method-cards">
      <div className="connector-auth-method-cards-header">
        <h4>{authMethodLabels.plural}</h4>
        <AddAuthMethodMenu
          label={authMethodLabels.add}
          methods={addableAuthMethods(manifest, authMethodIds)}
          onAdd={(methodId) => setAuthMethodIds((current) => addConnectionAuthMethod(current, methodId))}
        />
      </div>
      {authMethodIds.length === 0 && <p className="connections-note">{authMethodLabels.empty}</p>}
      {selectedManifestAuthMethods(manifest, authMethodIds).map((method) => <article className="connector-auth-method-card" key={method.id}>
        <header>
          <span><b>{method.displayName}</b><small>{method.description}</small></span>
          <button
            aria-label={`Remove ${method.displayName}`}
            className="connector-secondary-action"
            onClick={() => {
              setAuthMethodIds((current) => removeConnectionAuthMethod(current, method.id));
              setValues((current) => withoutCredentialFormValues(current, method.fields));
            }}
            type="button"
          >Remove</button>
        </header>
        {method.guide && <AuthorizationGuide guide={method.guide} />}
        {method.fields.map((field) => renderFormField({field, prefix: 'credential'}))}
        {methodConfigurationFields(method).map((field) => renderFormField({field, prefix: 'configuration'}))}
      </article>)}
    </section>}
    {(oauth || requiredManifestFormFields.length > 0) && <fieldset className="connector-field-group connector-field-group-required">
      <legend>Required</legend>
      {oauth && <>
        <FormField inputId="connector-oauth-client-id" label="OAuth client ID" name="clientId" required secret={false} description="Copy the client ID from the provider application created with the guide above. This identifier is not secret." values={values} setValues={setValues} />
        <FormField label="OAuth client secret" name="clientSecret" required secret description="Copy the matching client secret from the provider application. This write-only value is stored with the connection so access tokens can refresh." values={values} setValues={setValues} />
        <p className="connections-note">Client credentials are stored as write-only credential material so access tokens can refresh. They are never returned to this page.</p>
      </>}
      {requiredManifestFormFields.map(renderFormField)}
    </fieldset>}
    {optionalManifestFormFields.length > 0 && <fieldset className="connector-field-group connector-field-group-optional">
      <legend>Optional settings ({optionalManifestFormFields.length})</legend>
      {optionalManifestFormFields.map(renderFormField)}
    </fieldset>}
    {oauth && <p className="connections-scopes">Requested bot scopes: {auth.oauth2?.scopes.join(', ')}{auth.oauth2?.userScopes?.length ? `; user scopes: ${auth.oauth2.userScopes.join(', ')}` : ''}</p>}
    <div className="connector-form-actions">
      <button className="v2-primary" disabled={submitting || (isMultiple && authMethodIds.length === 0)} type="submit">{oauth ? 'Authorize' : catalog.mode === 'hosted' ? 'Save credentials' : 'Save local credentials'}</button>
      {onCancel && <button className="connector-secondary-action" onClick={onCancel} type="button">Cancel</button>}
    </div>
  </form>;
}

function AuthorizationGuide({guide, redirectURI}: {guide: {startURL: string; steps: string[]}; redirectURI?: string}) {
  return <section className="connector-authorization-guide">
    <h4>Authorization guide</h4>
    <p>Start at <a href={guide.startURL} rel="noreferrer" target="_blank">{guide.startURL}</a></p>
    <ol>{guide.steps.map((step, index) => <li key={`${index}:${step}`}>{linkifiedDescription(step)}</li>)}</ol>
    {redirectURI && <div className="connector-redirect-uri"><span>Redirect URI</span><CopyValue value={redirectURI} /></div>}
  </section>;
}

function AddAuthMethodMenu({label, methods, onAdd}: {
  label: string;
  methods: ManifestAuthMethod[];
  onAdd: (methodId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  if (methods.length === 0) return null;
  return <div className="connector-add-auth-method">
    <button aria-expanded={open} aria-haspopup="menu" className="connector-secondary-action" onClick={() => setOpen((current) => !current)} type="button">
      {label}
    </button>
    {open && <div aria-label={label} className="connector-add-auth-method-menu" role="menu">
      {methods.map((method) => <button
        key={method.id}
        onClick={() => {
          onAdd(method.id);
          setOpen(false);
        }}
        role="menuitem"
        type="button"
      >
        <b>{method.displayName}</b><small>{method.description}</small>
      </button>)}
    </div>}
  </div>;
}

function ConnectionConfigurationField({catalog, connection, field, session, values, setValues, onConfigured, onError}: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  field: ManifestField;
  session: UISessionResponse;
  values: Record<string, string>;
  setValues: (next: Record<string, string>) => void;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const presentation = connectionFieldPresentation(field, connection, session);
  if (presentation === 'studioFrame') {
    return <ConnectionStudioField
      catalog={catalog} connection={connection} field={field}
      onConfigured={onConfigured} onError={onError} session={session}
    />;
  }
  if (presentation === 'studioNote') {
    return <div className="connector-studio-field">
      <span>{field.name}</span>
      <p className="connector-studio-field-note">Save the connection to choose <code>{field.name}</code>.</p>
      {field.description && <small>{linkifiedDescription(field.description)}</small>}
    </div>;
  }
  return <ManifestFormField field={field} prefix="configuration" setValues={setValues} values={values} />;
}

function ConnectionStudioField({catalog, connection, field, session, onConfigured, onError}: {
  catalog: ConnectionsResponse;
  connection: ConnectionView;
  field: ManifestField;
  session: UISessionResponse;
  onConfigured: () => Promise<void>;
  onError: (message: string) => void;
}) {
  return <div className="connector-studio-field">
    <span>{field.name}{isManifestFieldInputRequired(field) ? ' *' : ''}</span>
    {field.description && <small>{linkifiedDescription(field.description)}</small>}
    <StudioFrame
      catalog={catalog} connection={connection} configuration={connection.configuration ?? {}}
      onConfigured={onConfigured} onError={onError} session={session}
      target={connectionFieldStudioTarget(field, connection)}
    />
  </div>;
}

export function isMultipleAuthMethodSelection(manifest: ReleaseManifest): boolean {
  return manifest.spec.auth.selection === 'multiple';
}

export function isStudioConnectionField(field: ManifestField, session: UISessionResponse): boolean {
  const studioUnit = field.studioUnit;
  return studioUnit !== undefined && Boolean(session.entrypointUrl)
    && (session.manifest.spec.studio?.units ?? []).some((unit) => unit.id === studioUnit.unit);
}

// A required field without a default stays a plain input so the first save can succeed.
export function connectionFieldPresentation(
  field: ManifestField,
  connection: ConnectionView,
  session: UISessionResponse,
): 'studioFrame' | 'studioNote' | 'input' {
  if (!isStudioConnectionField(field, session)) return 'input';
  if (connection.status === 'Ready') return 'studioFrame';
  return isManifestFieldInputRequired(field) ? 'input' : 'studioNote';
}

function connectionStoredValueFieldNames(
  manifest: ReleaseManifest,
  connection: ConnectionView,
  session: UISessionResponse,
): ReadonlySet<string> {
  return new Set(manifest.spec.configuration.fields
    .filter((field) => connectionFieldPresentation(field, connection, session) !== 'input')
    .map((field) => field.name));
}

export function connectionFieldStudioTarget(field: ManifestField, connection: ConnectionView): StudioTarget {
  const storedValue = connection.configuration?.[field.name];
  return {
    kind: 'connection',
    unitId: field.studioUnit?.unit,
    bindings: [{port: field.studioUnit?.port ?? field.name, jsonPointer: `/${jsonPointerSegment(field.name)}`}],
    value: storedValue === undefined ? {} : {[field.name]: storedValue},
  };
}

export function connectorAuthMethodLabels(methodLabel?: string) {
  const label = methodLabel?.trim() || 'Authentication method';
  const inlineLabel = label.length > 1 && label[1] === label[1].toUpperCase() && label[1] !== label[1].toLowerCase()
    ? label
    : label[0].toLowerCase() + label.slice(1);
  return {plural: `${label}s`, add: `Add ${inlineLabel}`, empty: `Add at least one ${inlineLabel} to save this connection.`};
}

export function initialConnectionAuthMethodIds(manifest: ReleaseManifest, connection: ConnectionView): string[] {
  const declared = new Set((manifest.spec.auth.methods ?? []).map((method) => method.id));
  const stored = (connection.authMethodIds ?? []).filter((methodId) => declared.has(methodId));
  if (stored.length > 0) return stored;
  const defaultMethod = manifest.spec.auth.defaultMethod;
  return defaultMethod && declared.has(defaultMethod) ? [defaultMethod] : [];
}

export function addConnectionAuthMethod(authMethodIds: string[], methodId: string): string[] {
  return authMethodIds.includes(methodId) ? authMethodIds : [...authMethodIds, methodId];
}

export function removeConnectionAuthMethod(authMethodIds: string[], methodId: string): string[] {
  return authMethodIds.filter((candidate) => candidate !== methodId);
}

export function addableAuthMethods(manifest: ReleaseManifest, authMethodIds: string[]): ManifestAuthMethod[] {
  return (manifest.spec.auth.methods ?? []).filter((method) => !authMethodIds.includes(method.id));
}

function selectedManifestAuthMethods(manifest: ReleaseManifest, authMethodIds: string[]): ManifestAuthMethod[] {
  return authMethodIds.flatMap((methodId) => manifest.spec.auth.methods?.find((method) => method.id === methodId) ?? []);
}

function methodConfigurationFields(method: ManifestAuthMethod): ManifestField[] {
  return method.configuration?.fields ?? [];
}

function configurationFormValues(values: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(values).filter(([name]) => name.startsWith('configuration:')));
}

function withoutCredentialFormValues(values: Record<string, string>, fields: ManifestField[]): Record<string, string> {
  const removed = new Set(fields.map((field) => `credential:${field.name}`));
  return Object.fromEntries(Object.entries(values).filter(([name]) => !removed.has(name)));
}

export function connectionFormInitialValues(manifest: ReleaseManifest, connection: ConnectionView): Record<string, string> {
  const configuration = connection.configuration ?? {};
  const fields = [
    ...manifest.spec.configuration.fields,
    ...(manifest.spec.auth.methods ?? []).flatMap(methodConfigurationFields),
  ];
  return Object.fromEntries(fields
    .filter((field) => configuration[field.name] !== undefined)
    .map((field) => [`configuration:${field.name}`, formFieldText(configuration[field.name])]));
}

// The server stamps the selected methods into the stored credentials; blank stored credentials are kept.
export function connectionWriteRequestBody(
  connection: ConnectionView,
  manifest: ReleaseManifest,
  authMethodIds: string[],
  values: Record<string, string>,
  storedValueFieldNames: ReadonlySet<string> = new Set(),
) {
  const isMultiple = isMultipleAuthMethodSelection(manifest);
  const methods = isMultiple
    ? selectedManifestAuthMethods(manifest, authMethodIds)
    : [selectedManifestAuth(manifest.spec.auth, authMethodIds[0] ?? '')];
  const storedCredentialFields = new Set(connection.storedCredentialFields ?? []);
  const credentials: Record<string, string> = {};
  const keepCredentialFields: string[] = [];
  for (const name of new Set(methods.flatMap((method) => method.fields.map((field) => field.name)))) {
    const value = values[`credential:${name}`] ?? '';
    if (value !== '') credentials[name] = value;
    else if (storedCredentialFields.has(name)) keepCredentialFields.push(name);
  }
  return {
    modulePath: connection.modulePath,
    moduleVersion: connection.moduleVersion,
    provider: manifest.spec.provider,
    ...(isMultiple ? {authMethodIds: methods.map((method) => method.id)} : {authMethodId: authMethodIds[0] ?? ''}),
    configuration: connectionConfigurationValues(manifest, methods, values, connection.configuration ?? {}, storedValueFieldNames),
    credentials,
    keepCredentialFields,
    credentialExpiresAt: null,
  };
}

// A connection-field Studio unit saves one configuration field and keeps every stored credential.
export function connectionConfigurationSaveRequestBody(
  connection: ConnectionView,
  manifest: ReleaseManifest,
  configuration: Record<string, unknown>,
) {
  const isMultiple = isMultipleAuthMethodSelection(manifest);
  const methods = isMultiple
    ? selectedManifestAuthMethods(manifest, connection.authMethodIds ?? [])
    : [selectedManifestAuth(manifest.spec.auth, connection.authMethodId ?? '')];
  const declaredCredentialFields = new Set(methods.flatMap((method) => method.fields.map((field) => field.name)));
  return {
    modulePath: connection.modulePath,
    moduleVersion: connection.moduleVersion,
    provider: manifest.spec.provider,
    ...(isMultiple ? {authMethodIds: methods.map((method) => method.id)} : {authMethodId: connection.authMethodId ?? ''}),
    configuration,
    credentials: {},
    keepCredentialFields: (connection.storedCredentialFields ?? []).filter((name) => declaredCredentialFields.has(name)),
    credentialExpiresAt: connection.credentialExpiresAt ?? null,
  };
}

// Unchanged prefilled text sends the stored JSON value, so non-string values keep their type.
function connectionConfigurationValues(
  manifest: ReleaseManifest,
  methods: ManifestAuthMethod[],
  values: Record<string, string>,
  storedConfiguration: Record<string, unknown>,
  storedValueFieldNames: ReadonlySet<string>,
): Record<string, unknown> {
  const configuration: Record<string, unknown> = {};
  for (const field of [...manifest.spec.configuration.fields, ...methods.flatMap(methodConfigurationFields)]) {
    const storedValue = storedConfiguration[field.name];
    if (storedValueFieldNames.has(field.name)) {
      if (storedValue !== undefined) configuration[field.name] = storedValue;
      continue;
    }
    const text = values[`configuration:${field.name}`] ?? '';
    if (text === '') continue;
    configuration[field.name] = storedValue !== undefined && text === formFieldText(storedValue) ? storedValue : text;
  }
  return configuration;
}

function formFieldText(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value) ?? '';
}

export function selectedManifestAuth(auth: ReleaseManifest['spec']['auth'], methodId: string): ManifestAuthMethod {
  if (!auth.methods?.length) {
    return {
      id: '', displayName: '', description: '', type: auth.type ?? 'none', fields: auth.fields,
      guide: auth.guide, oauth2: auth.oauth2,
    };
  }
  return auth.methods.find((method) => method.id === methodId)
    ?? auth.methods.find((method) => method.id === auth.defaultMethod)
    ?? auth.methods[0];
}

export const storedCredentialPlaceholder = 'Stored - leave blank to keep';

function ManifestFormField({ field, prefix, stored = false, values, setValues }: {
  field: ManifestField;
  prefix: string;
  stored?: boolean;
  values: Record<string, string>;
  setValues: (next: Record<string, string>) => void;
}) {
  return <FormField
    label={field.name}
    name={`${prefix}:${field.name}`}
    placeholder={stored ? storedCredentialPlaceholder : undefined}
    required={!stored && isManifestFieldInputRequired(field)}
    secret={field.type === 'secretString'}
    description={field.description}
    defaultText={manifestFieldDefaultText(field)}
    values={values}
    setValues={setValues}
  />;
}

function isManifestFieldInputRequired(field: ManifestField): boolean {
  return field.required && field.default === undefined;
}

function FormField({ inputId, label, name, placeholder, required, secret, description, defaultText, values, setValues }: {
  inputId?: string;
  label: string;
  name: string;
  placeholder?: string;
  required: boolean;
  secret: boolean;
  description?: string;
  defaultText?: string;
  values: Record<string, string>;
  setValues: (next: Record<string, string>) => void;
}) {
  return <label><span>{label}{required ? ' *' : ''}</span>
    <input
      autoComplete="off"
      id={inputId}
      placeholder={placeholder}
      required={required}
      type={secret ? 'password' : 'text'}
      value={values[name] ?? ''}
      onChange={(event) => setValues({ ...values, [name]: event.target.value })}
    />
    {description && <small>{linkifiedDescription(description)}</small>}
    {defaultText && <small className="connector-field-default">{defaultText}</small>}
  </label>;
}

export function manifestFieldDefaultText(field: ManifestField): string | undefined {
  if (field.default === undefined || field.type === 'secretString') return undefined;
  const value = typeof field.default === 'string'
    ? field.default
    : JSON.stringify(field.default);
  return `(Default: ${value})`;
}

function linkifiedDescription(description: string) {
  return descriptionParts(description).map((part, index) => part.url
    ? <a href={part.url} key={`${part.text}:${index}`} rel="noreferrer" target="_blank">{part.text}</a>
    : part.text);
}

export function descriptionParts(description: string): {text: string; url?: string}[] {
  const parts: {text: string; url?: string}[] = [];
  const pattern = /https?:\/\/[^\s,;)]+/g;
  let start = 0;
  for (const match of description.matchAll(pattern)) {
    const index = match.index ?? 0;
    if (index > start) parts.push({text: description.slice(start, index)});
    parts.push({text: match[0], url: match[0]});
    start = index + match[0].length;
  }
  if (start < description.length) parts.push({text: description.slice(start)});
  return parts;
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
      const capability = studioCommandCapability(commandMessage.command, commandMessage.input, session);
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
        void executeStudioCommand(catalog, connection, configuration, session, target, commandMessage.command, commandMessage.input).then(async (value) => {
          frame.current?.contentWindow?.postMessage({
            type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
            connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: true, value,
          }, '*');
          if (commandMessage.command === 'use.configuration.save') await onConfigured();
        }).catch((commandError: unknown) => {
          const failure = studioCommandFailureOutcome(connection, session, commandMessage, commandError);
          if (failure.pageBannerMessage !== undefined) onError(failure.pageBannerMessage);
          frame.current?.contentWindow?.postMessage(failure.frameResult, '*');
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
  const sendHostReady = () => frame.current?.contentWindow?.postMessage(connectorHostReadyMessage(session, connection, target, {
    theme: connectorStudioFrameTheme,
    themeTokens: readConnectorStudioLightThemeTokens(),
    stylesheet: connectorStudioStylesheet,
  }), '*');
  const sendReadyAfterStudioMount = () => window.setTimeout(sendHostReady, 100);
  const frameLabel = `${connection.connectorId} Connector ${studioTargetLabel(target)}`;
  return <div
    aria-label={expanded ? frameLabel : undefined}
    aria-modal={expanded || undefined}
    className="connector-studio-shell"
    data-expanded={expanded}
    data-surface={target.kind === 'connection' && target.unitId !== undefined ? 'connectionField' : target.kind}
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
      style={{
        // A frame whose color-scheme differs from its element paints an opaque canvas.
        colorScheme: connectorStudioFrameTheme,
        ...(expanded || frameHeight === undefined ? {} : {height: `${frameHeight}px`}),
      }}
      title={frameLabel}
    />
  </div>;
}

function studioTargetLabel(target: StudioTarget): string {
  if (target.kind === 'configurationUnit') return target.label;
  const boundField = target.bindings?.[0]?.jsonPointer.slice(1);
  return boundField ? `${boundField} setting` : 'setup';
}

export function connectorHostReadyMessage(
  session: UISessionResponse,
  connection: ConnectionView,
  target: StudioTarget,
  appearance: ConnectorStudioFrameAppearance,
) {
  return {
    type: 'connector.host.ready', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
    connectorId: connection.connectorId,
    capabilities: studioHostCapabilities(session),
    connection: {
      state: studioState(connection.status), grantedScopes: [],
      detail: connection.status,
      authMethodIds: connectionAuthMethodIds(connection),
      configuration: connection.configuration ?? {},
    },
    target,
    theme: appearance.theme,
    themeTokens: appearance.themeTokens,
    stylesheet: appearance.stylesheet,
  };
}

export function connectionAuthMethodIds(connection: ConnectionView): string[] {
  if (connection.authMethodIds?.length) return [...connection.authMethodIds];
  return connection.authMethodId ? [connection.authMethodId] : [];
}

type StudioCommand = 'oauth.connect' | 'oauth.reconnect' | 'provider.command.execute' | 'use.configuration.save';

export function isStudioCommand(value: unknown, session: UISessionResponse): value is { requestId: string; command: StudioCommand; input?: Record<string, unknown> } {
  if (typeof value !== 'object' || value === null) return false;
  const message = value as Record<string, unknown>;
  return message.type === 'connector.command' && message.protocolVersion === '0.2.0'
    && message.sessionNonce === session.sessionNonce && message.connectorId === session.connectorId
    && typeof message.requestId === 'string'
    && (message.input === undefined || isRecord(message.input))
    && (message.command === 'oauth.connect' || message.command === 'oauth.reconnect'
      || message.command === 'provider.command.execute' || message.command === 'use.configuration.save');
}

export function isStudioFrameResize(value: unknown, session: UISessionResponse): value is {height: number} {
  if (typeof value !== 'object' || value === null) return false;
  const message = value as Record<string, unknown>;
  return message.type === 'connector.frame.resize' && message.protocolVersion === '0.2.0'
    && message.sessionNonce === session.sessionNonce && message.connectorId === session.connectorId
    && typeof message.height === 'number' && Number.isInteger(message.height)
    && message.height >= 80 && message.height <= 4096;
}

function studioCommandCapability(command: StudioCommand, input: Record<string, unknown> | undefined, session: UISessionResponse) {
  if (command === 'oauth.connect' || command === 'oauth.reconnect') return 'oauth.connection.manage';
  if (command === 'use.configuration.save') return 'use.configuration.write';
  if (command === 'provider.command.execute') {
    const commandId = input?.commandId;
    if (typeof commandId !== 'string') return null;
    return session.manifest.spec.studio?.commands?.find((candidate) => candidate.id === commandId)?.capability ?? null;
  }
  return null;
}

export function studioHostCapabilities(session: UISessionResponse): string[] {
  const declared = session.manifest.spec.studio?.setup.backendCapabilities ?? [];
  const supported = new Set(['oauth.connection.manage', 'use.configuration.write']);
  for (const command of session.manifest.spec.studio?.commands ?? []) supported.add(command.capability);
  return declared.filter((capability) => supported.has(capability));
}

// The Connector UI presents every provider command failure itself, for example as a manual-entry fallback.
export function studioCommandFailureOutcome(
  connection: ConnectionView,
  session: UISessionResponse,
  commandMessage: { requestId: string; command: StudioCommand },
  commandError: unknown,
): { pageBannerMessage?: string; frameResult: Record<string, unknown> } {
  const message = errorMessage(commandError);
  return {
    pageBannerMessage: commandMessage.command === 'provider.command.execute' ? undefined : message,
    frameResult: {
      type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: session.sessionNonce,
      connectorId: connection.connectorId, requestId: commandMessage.requestId, ok: false,
      error: { code: 'COMMAND_FAILED', message },
    },
  };
}

async function executeStudioCommand(
  catalog: ConnectionsResponse,
  connection: ConnectionView,
  configuration: Record<string, unknown>,
  session: UISessionResponse,
  targetScope: StudioTarget,
  command: StudioCommand,
  input?: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  let target = connectionURL(connection);
  let method = 'GET';
  let body: string | undefined;
  if (command === 'provider.command.execute') {
    const commandId = input?.commandId;
    if (typeof commandId !== 'string' || !session.manifest.spec.studio?.commands?.some((candidate) => candidate.id === commandId)) {
      throw new Error('Provider command is not declared');
    }
    target = `/api/v2/connector-ui-sessions/${encodeURIComponent(session.sessionNonce ?? '')}/commands/${encodeURIComponent(commandId)}`;
    method = 'POST';
    body = JSON.stringify({parameters: stringRecordInput(input, 'parameters')});
  } else if (command === 'use.configuration.save' && targetScope.kind === 'configurationUnit') {
    const value = recordInput(input, 'value');
    const unit = targetScope;
    const nextConfiguration = mergeUnitValue(configuration, unit.bindings, value);
    if (unit.scope.kind === 'trigger') {
      target = `/api/v2/connector-trigger-bindings/${encodeURIComponent(connection.connectorId)}/${encodeURIComponent(connection.connectionName)}/${encodeURIComponent(unit.scope.triggerName)}/${encodeURIComponent(unit.scope.bindingName)}`;
    } else {
      target = `/api/v2/connector-use-configurations/${encodeURIComponent(connection.connectorId)}/${encodeURIComponent(connection.connectionName)}/${encodeURIComponent(unit.scope.operationId)}/${encodeURIComponent(unit.scope.flowType)}/${encodeURIComponent(unit.scope.stepType)}`;
    }
    method = 'PUT';
    body = JSON.stringify({ configuration: nextConfiguration });
  } else if (command === 'use.configuration.save' && targetScope.kind === 'connection' && targetScope.unitId !== undefined) {
    const nextConfiguration = mergeUnitValue(connection.configuration ?? {}, targetScope.bindings ?? [], recordInput(input, 'value'));
    method = 'PUT';
    body = JSON.stringify(connectionConfigurationSaveRequestBody(connection, session.manifest, nextConfiguration));
  } else {
    throw new Error('Connector command is not implemented');
  }
  const response = await dexFetch(target, { method, headers: connectorWriteHeaders(catalog), body });
  return readResponseJSON<Record<string, unknown>>(response);
}

function stringRecordInput(input: Record<string, unknown> | undefined, name: string): Record<string, string> {
  const value = input?.[name];
  if (value === undefined) return {};
  if (!isRecord(value) || Object.values(value).some((item) => typeof item !== 'string')) throw new Error(`${name} must contain string values`);
  return value as Record<string, string>;
}

function recordInput(input: Record<string, unknown> | undefined, name: string): Record<string, unknown> {
  const value = input?.[name];
  if (!isRecord(value)) throw new Error(`${name} is required`);
  return value;
}

export function mergeUnitValue(
  configuration: Record<string, unknown>,
  bindings: ConnectorUIBinding[],
  value: Record<string, unknown>,
): Record<string, unknown> {
  const next = JSON.parse(JSON.stringify(configuration)) as Record<string, unknown>;
  const allowedPorts = new Set(bindings.map((binding) => binding.port));
  for (const port of Object.keys(value)) {
    if (!allowedPorts.has(port)) throw new Error(`Unit returned undeclared port ${port}`);
  }
  for (const binding of bindings) {
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

function jsonPointerSegment(name: string): string {
  return name.replace(/~/g, '~0').replace(/\//g, '~1');
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
