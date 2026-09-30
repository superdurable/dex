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
  localNote: 'local',
  projectNote: 'project',
  loading: 'Loading Connectors…',
  loadingRelease: 'Loading Connector release…',
  empty: 'No configurable connections were found.',
  listLabel: 'Connections',
  selectPrompt: 'Select a connection to set it up.',
  localStoreHeading: 'Local store',
  projectStoreHeading: 'Project configuration',
  expandList: 'Show the connector list',
  resizeList: 'Resize the connector list',
  conflict: 'The same connector and connection name use different module versions. Align the Flow dependencies before configuring.',
  unsupported: 'Automatic setup requires an exact official release and a static ConnectionName.',
  connectionLabel(connectionName: string): string {
    return connectionName ? `Connection ${connectionName}` : 'Unnamed connection';
  },
} as const;
