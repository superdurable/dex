// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { BookOpen, Inbox, PencilRuler, Plug } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import { usePreferences } from '../providers';
import type { FlowDefinitionTypeList } from '@/lib/types';
import { isWorkQueueApplicable } from '../catalog/applicability';
import { useWebCatalog } from '../catalog/WebCatalogProvider';
import { ExperimentalMenu, type ExperimentalPage } from './ExperimentalMenu';
import { CONNECTORS_PATH, DESIGNS_PATH, WORK_QUEUE_PATH } from '../routes';
import { DexMark } from './DexMark';
import { ThemeToggle } from './ThemeToggle';
import { webURL } from '@/lib/basePath';

/** The company site that the header logo opens in the same tab. */
export const COMPANY_HOME_URL = 'https://superdurable.io';

/** The Dex documentation site that the header Docs link opens in a new tab. */
export const DEX_DOCS_URL = 'https://docs.superdurable.io/';

/** Designs has something to show only when a directory file or an upload declares at least one Flow type. */
export function hasLoadedFlowDefinitions(flowTypes: FlowDefinitionTypeList | null): boolean {
  return flowTypes?.configured === true && flowTypes.flowTypes.length > 0;
}

/**
 * The Flow type list, which reads no definition.
 * It rereads the list on every route change, so a failed read or a new definition recovers on the next navigation.
 */
function useFlowDefinitionTypes(pathname: string): FlowDefinitionTypeList | null {
  const [flowTypes, setFlowTypes] = useState<FlowDefinitionTypeList | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetch(webURL('/api/flow-definition-types'), { signal: controller.signal })
      .then((response) => readResponseJSON<FlowDefinitionTypeList>(response))
      .then(setFlowTypes)
      // A failed read keeps the last known answer; the next navigation retries.
      .catch(() => undefined);
    return () => controller.abort();
  }, [pathname]);
  return flowTypes;
}

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  const { canUseV2, hasConnectorSetup: hasConnectors } = useWebCatalog();
  const location = useLocation();
  const flowTypes = useFlowDefinitionTypes(location.pathname);
  const hasFlowDefinitions = hasLoadedFlowDefinitions(flowTypes);
  const hasWorkQueue = isWorkQueueApplicable(flowTypes, canUseV2);
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
      description: 'Connections for Flows and agent tools',
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
        <span className="header-nav-spacer" />
        <span className="connection-pill" title="Dex server">
            <span className="connection-dot" />
            <span className="connection-label">Dex server</span>
        </span>
        <a
          className="header-docs-link"
          href={DEX_DOCS_URL}
          target="_blank"
          rel="noopener noreferrer"
          aria-label="Dex documentation"
        >
          <BookOpen aria-hidden="true" size={15} strokeWidth={2} />
          <span className="header-docs-label">Docs</span>
        </a>
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
