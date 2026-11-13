// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronLeft } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import { scopedStorageKey, webURL } from '@/lib/basePath';
import { readHostLink } from '@/lib/hostLink';
import type { FlowDefinitionTypeList } from '@/lib/types';
import { isWorkQueueApplicable } from '../../catalog/applicability';
import { useWebCatalog } from '../../catalog/WebCatalogProvider';
import { FLOWS_PATH } from '../../routes';
import { DexMark } from '../DexMark';
import { AppBarSlotTarget } from './AppBarSlots';
import {
  CONNECTORS_SECTION, DESIGNS_SECTION, FLOWS_SECTION, SectionMenu, WORK_QUEUE_SECTION, type AppSection,
} from './SectionMenu';
import { SettingsMenu } from './SettingsMenu';
import { JumpTo } from './JumpTo';
import { useServerReachability } from './useServerReachability';
import './frame.css';

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

const LAST_FLOWS_SEARCH_KEY = 'dex-web.last-flows-search';

function readLastFlowsSearch(): string {
  try {
    return window.sessionStorage.getItem(scopedStorageKey(LAST_FLOWS_SEARCH_KEY)) ?? '';
  } catch {
    return '';
  }
}

/** Remembers the Flows search, so the trail's Flows link returns to it. */
function useFlowsHref(pathname: string, search: string): string {
  const [lastSearch, setLastSearch] = useState(readLastFlowsSearch);
  useEffect(() => {
    if (pathname !== FLOWS_PATH) return;
    setLastSearch(search);
    try {
      window.sessionStorage.setItem(scopedStorageKey(LAST_FLOWS_SEARCH_KEY), search);
    } catch {
      // Without storage the link still opens the Flows page.
    }
  }, [pathname, search]);
  return `${FLOWS_PATH}${lastSearch}`;
}

/**
 * The one bar of every page: the trail from home, then what the page puts in
 * its page, views and actions slots, then the viewer's settings.
 */
export function AppBar() {
  const { canUseV2, hasConnectorSetup } = useWebCatalog();
  const { pathname, search } = useLocation();
  const flowTypes = useFlowDefinitionTypes(pathname);
  const flowsHref = useFlowsHref(pathname, search);
  const hostLink = useMemo(readHostLink, []);
  const reachability = useServerReachability(pathname);
  const hasFlowDefinitions = hasLoadedFlowDefinitions(flowTypes);
  const hasWorkQueue = isWorkQueueApplicable(flowTypes, canUseV2);
  const sections: AppSection[] = [
    FLOWS_SECTION,
    ...(hasWorkQueue ? [WORK_QUEUE_SECTION] : []),
    ...(hasFlowDefinitions ? [DESIGNS_SECTION] : []),
    ...(hasConnectorSetup ? [CONNECTORS_SECTION] : []),
  ];
  return (
    <header className="app-bar v1-page">
      <nav aria-label="Trail" className="app-bar-trail">
        {hostLink ? (
          <a className="app-bar-root app-bar-root--host" href={hostLink.url}>
            <ChevronLeft aria-hidden="true" size={16} strokeWidth={2.25} />
            <span>{hostLink.name}</span>
          </a>
        ) : (
          <Link aria-label="Dex home" className="app-bar-root" to={FLOWS_PATH}>
            <DexMark size={22} />
          </Link>
        )}
        {reachability !== 'checking' && (
          <span
            aria-label={reachability === 'reachable' ? 'Dex Server reachable' : 'Dex Web cannot reach the Dex Server'}
            className={`app-bar-reach-dot is-${reachability}`}
            role="img"
            title={reachability === 'reachable' ? 'Dex Server reachable' : 'Dex Web cannot reach the Dex Server'}
          />
        )}
        <span aria-hidden="true" className="app-bar-separator">/</span>
        <SectionMenu flowsHref={flowsHref} sections={sections} />
        <AppBarSlotTarget className="app-bar-page" name="page" />
      </nav>
      <AppBarSlotTarget className="app-bar-views" name="views" />
      <AppBarSlotTarget className="app-bar-actions" name="actions" />
      <JumpTo sections={sections} />
      <SettingsMenu />
    </header>
  );
}
