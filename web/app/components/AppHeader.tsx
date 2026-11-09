// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { Inbox, PencilRuler, Plug } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import { usePreferences } from '../providers';
import type { FlowDefinitionCatalog } from '@/lib/types';
import { isWorkQueueApplicable } from '../catalog/applicability';
import { useWebCatalog } from '../catalog/WebCatalogProvider';
import { ExperimentalMenu, type ExperimentalPage } from './ExperimentalMenu';
import { CONNECTORS_PATH, DESIGNS_PATH, FLOWS_PATH, WORK_QUEUE_PATH } from '../routes';
import { DexMark } from './DexMark';
import { ThemeToggle } from './ThemeToggle';

/** The company site that the header logo opens in the same tab. */
export const COMPANY_HOME_URL = 'https://superdurable.io';

/** Designs has something to show only when the definitions directory holds at least one JSON file. */
export function hasLoadedFlowDefinitions(definitions: FlowDefinitionCatalog | null): boolean {
  return definitions?.configured === true && definitions.definitions.length > 0;
}

/**
 * Reports whether the Designs page applies.
 * It rereads the catalog on every route change, so a failed read or a changed directory recovers on the next navigation.
 */
function useHasLoadedFlowDefinitions(pathname: string): boolean {
  const [hasDefinitions, setHasDefinitions] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    fetch('/api/flow-definitions', { signal: controller.signal })
      .then((response) => readResponseJSON<FlowDefinitionCatalog>(response))
      .then((catalog) => setHasDefinitions(hasLoadedFlowDefinitions(catalog)))
      // A failed read keeps the last known answer; the next navigation retries.
      .catch(() => undefined);
    return () => controller.abort();
  }, [pathname]);
  return hasDefinitions;
}

/** The part of the local Connector setup listing that says whether the page has anything to show. */
interface ConnectorSetupListing {
  enabled: boolean;
  connections: unknown[];
}

/** Connector setup is applicable to a local environment whose loaded Flows use at least one Connector. */
export function isConnectorSetupApplicable(listing: ConnectorSetupListing | null): boolean {
  return listing?.enabled === true && listing.connections.length > 0;
}

/**
 * Reports whether the Connectors page applies.
 * The listing exists only where Dex Web runs local Connector setup, so other servers never show it.
 */
function useHasConnectorSetup(canUseOperatorAPI: boolean): boolean {
  const [hasConnectors, setHasConnectors] = useState(false);
  useEffect(() => {
    if (!canUseOperatorAPI) {
      setHasConnectors(false);
      return undefined;
    }
    const controller = new AbortController();
    fetch('/api/v2/connector-connections', { signal: controller.signal })
      .then((response) => readResponseJSON<ConnectorSetupListing>(response))
      .then((listing) => setHasConnectors(isConnectorSetupApplicable(listing)))
      // Without local Connector setup the route does not exist, so the link stays hidden.
      .catch(() => {
        if (!controller.signal.aborted) setHasConnectors(false);
      });
    return () => controller.abort();
  }, [canUseOperatorAPI]);
  return hasConnectors;
}

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  const { canUseV2, catalog } = useWebCatalog();
  const location = useLocation();
  const hasFlowDefinitions = useHasLoadedFlowDefinitions(location.pathname);
  const hasWorkQueue = isWorkQueueApplicable(catalog, canUseV2);
  const hasConnectors = useHasConnectorSetup(canUseV2);
  const experimentalPages: ExperimentalPage[] = [
    ...(hasFlowDefinitions ? [{
      path: DESIGNS_PATH, label: 'Designs', Icon: PencilRuler,
      description: 'Every loaded Flow design, drawn as a graph',
    }] : []),
    ...(hasWorkQueue ? [{
      path: WORK_QUEUE_PATH, label: 'Work Queue', Icon: Inbox,
      description: 'Runs and agent sessions that wait for a person',
    }] : []),
    ...(hasConnectors ? [{
      path: CONNECTORS_PATH, label: 'Connectors', Icon: Plug,
      description: 'Connections the loaded Flows use',
    }] : []),
  ];
  return (
    <header className="app-header">
      <a className="header-brand" href={COMPANY_HOME_URL} aria-label="Super Durable home">
          <span className="brand-mark">
            <DexMark size={28} />
          </span>
          {/* Dex leads and Super Durable is the maker credit. The product being
              branded is Dex; the old single line put the company first and the
              product last, behind an interpunct. */}
          <span className="brand-name">
            <b>Dex</b>
            <span>Super Durable</span>
          </span>
      </a>
      <nav className="header-nav" aria-label="Primary navigation">
        <NavLink className="header-core-link" to={FLOWS_PATH}>Flows</NavLink>
        <span className="header-nav-spacer" />
        <span className="connection-pill">
            <span className="connection-dot" />
            Dex server
        </span>
        <label className="timezone-control">
          <span>Timezone</span>
          <select
            value={timezone}
            onChange={(event) => setTimezone(event.target.value as 'local' | 'UTC')}
          >
            <option value="local">Local</option>
            <option value="UTC">UTC</option>
          </select>
        </label>
        <ExperimentalMenu pages={experimentalPages} />
        <ThemeToggle />
      </nav>
    </header>
  );
}
