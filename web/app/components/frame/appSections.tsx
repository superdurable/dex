// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { Inbox, ListTree, PencilRuler, Plug, type LucideIcon } from 'lucide-react';
import { readResponseJSON } from '@/lib/http';
import { webURL } from '@/lib/basePath';
import type { FlowDefinitionTypeList } from '@/lib/types';
import { isWorkQueueApplicable } from '../../catalog/applicability';
import { useWebCatalog } from '../../catalog/WebCatalogProvider';
import { CONNECTORS_PATH, DESIGNS_PATH, FLOWS_PATH, isPathOrChild, WORK_QUEUE_PATH } from '../../routes';
import { readLastFlowsSearch, rememberFlowsSearch } from './lastFlowsSearch';

/** One top-level page of Dex Web. */
export interface AppSection {
  path: string;
  label: string;
  description: string;
  Icon: LucideIcon;
  isExperimental?: boolean;
}

export const FLOWS_SECTION: AppSection = {
  path: FLOWS_PATH, label: 'Flows', Icon: ListTree, description: 'Search runs and open one',
};
export const DESIGNS_SECTION: AppSection = {
  path: DESIGNS_PATH, label: 'Designs', Icon: PencilRuler, isExperimental: true,
  description: 'Every loaded Flow design, drawn as a graph',
};
export const WORK_QUEUE_SECTION: AppSection = {
  path: WORK_QUEUE_PATH, label: 'Work Queue', Icon: Inbox, isExperimental: true,
  description: 'Runs and agent sessions that wait for a person',
};
export const CONNECTORS_SECTION: AppSection = {
  path: CONNECTORS_PATH, label: 'Connectors', Icon: Plug, isExperimental: true,
  description: 'Connections for Flows and agent tools',
};

/** The section a path belongs to; every page that is not another section belongs to Flows. */
export function sectionOf(pathname: string, sections: readonly AppSection[]): AppSection {
  return sections.find((section) => section !== FLOWS_SECTION && isPathOrChild(pathname, section.path))
    ?? FLOWS_SECTION;
}

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

/** The Flows link returns to the last Flows search, which the Flows page records as it searches. */
function useFlowsHref(pathname: string, search: string): string {
  const [lastSearch, setLastSearch] = useState(readLastFlowsSearch);
  useEffect(() => {
    if (pathname === FLOWS_PATH && search) rememberFlowsSearch(search);
    setLastSearch(readLastFlowsSearch());
  }, [pathname, search]);
  return `${FLOWS_PATH}${lastSearch}`;
}

/** The sections that apply to this Dex Web, and where the Flows link returns to. */
export function useAppSections(pathname: string, search: string): { sections: AppSection[]; flowsHref: string } {
  const { canUseV2, hasConnectorSetup } = useWebCatalog();
  const flowTypes = useFlowDefinitionTypes(pathname);
  const flowsHref = useFlowsHref(pathname, search);
  const sections: AppSection[] = [
    FLOWS_SECTION,
    ...(hasLoadedFlowDefinitions(flowTypes) ? [DESIGNS_SECTION] : []),
    ...(isWorkQueueApplicable(flowTypes, canUseV2) ? [WORK_QUEUE_SECTION] : []),
    ...(hasConnectorSetup ? [CONNECTORS_SECTION] : []),
  ];
  return { sections, flowsHref };
}

/** The word that marks an early feature, the same everywhere it appears. */
export function ExperimentalTag() {
  return <span className="experimental-tag" title="Early feature; it may change">Experimental</span>;
}
