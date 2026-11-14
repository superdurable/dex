// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Every string the Connectors page says outside a Connector's own manifest. */
export const CONNECTORS_COPY = {
  heading: 'Connectors',
  localNote: 'Local connections',
  loading: 'Loading Connectors…',
  loadingRelease: 'Loading Connector release…',
  setupSaved: 'Restart the application to use new settings. A new key applies on the next Connector call.',
  empty: 'No connections yet. Connections that loaded Flows use appear here. Add a connection for an agent tool or another application.',
  listLabel: 'Connections',
  selectPrompt: 'Select a connection to set it up, or add one.',
  addConnection: 'Add connection',
  addConnectionPurpose: 'Add a named connection that no loaded Flow uses, such as one an agent tool opens by name. Dex Web saves it to the same local file as every other connection.',
  addConnectionNameHelp: 'The name your application opens, such as support-slack. It must be unique for this Connector.',
  loadingCatalog: 'Loading the Connector catalog…',
  publicCatalogUnavailable: 'The public Connector catalog is unavailable. Only local release overrides are listed.',
  standaloneNote: 'No loaded Flow uses this connection. An application, such as an agent with Connector tools, opens it by its Connector and connection name.',
  standaloneReady: 'This connection is ready. Applications open it by its connection name.',
  removePendingConnection: 'Remove connection',
  deleteStandaloneConnection: 'Delete connection',
  localStoreHeading: 'Local store',
  expandList: 'Show the connector list',
  resizeList: 'Resize the connector list',
  conflict: 'The same connector and connection name use different module versions. Align the Flow dependencies before configuring.',
  unsupported: 'Automatic setup requires an exact official release and a static ConnectionName.',
  conflictTitle: 'Module versions conflict',
  unsupportedTitle: 'Automatic setup is unavailable',
  expiredTitle: 'Authorization expired',
  missingTitle: 'Not set up yet',
  reauthorizeAction: 'Reauthorize',
  setUpAction: 'Set up',
  expiredNextStep(connectorName: string): string {
    return `${connectorName} calls fail until you authorize this connection again.`;
  },
  missingNextStep(connectorName: string): string {
    return `Complete the setup below to start using ${connectorName}.`;
  },
  connectionCount(count: number): string {
    return count === 1 ? '1 connection' : `${count} connections`;
  },
  connectionRenderFailed(detail: string): string {
    return `This connection could not be shown: ${detail}`;
  },
  connectionLabel(connectionName: string): string {
    return connectionName ? `Connection ${connectionName}` : 'Unnamed connection';
  },
} as const;
