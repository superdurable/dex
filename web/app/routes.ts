// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The Flows page, which is also where `/` lands. */
export const FLOWS_PATH = '/v1/flows';

/** The run page's query parameter that opens one of its tabs directly. */
export const RUN_VIEW_PARAM = 'view';

/** The run page view that opens a Dex Agents session on its Agent session tab. */
export const RUN_SESSION_VIEW = 'session';

/** The Work Queue view that lists Dex Agents sessions that wait for a person. */
export const AGENT_INBOX_VIEW = 'agents';

export const WORK_QUEUE_PATH = '/v1/work-queue';

export const CONNECTORS_PATH = '/v1/connectors';

/** Where Connectors lived before it was renamed; its links still resolve. */
export const LEGACY_CONNECTORS_PATH = '/v2/connections';

/** A run page by Flow ID, which resolves the current run, or by Flow ID and run ID. */
export function runPath(flowId: string, runId?: string) {
  const flowPath = `${FLOWS_PATH}/${encodeURIComponent(flowId)}`;
  return runId === undefined ? flowPath : `${flowPath}/${encodeURIComponent(runId)}`;
}

/** The Flows page searching one Flow type. */
export function flowsOfTypePath(flowType: string) {
  return `${FLOWS_PATH}?${new URLSearchParams({ q: `FlowType = ${JSON.stringify(flowType)}` })}`;
}

/** Opens a Dex Agents session on its Agent session tab. Other runs open their usual tab. */
export function sessionRunPath(flowId: string) {
  return `${runPath(flowId)}?${RUN_VIEW_PARAM}=${RUN_SESSION_VIEW}`;
}

export function workQueuePath(flowType?: string, flowId?: string) {
  if (flowType === undefined) return WORK_QUEUE_PATH;
  const typePath = `${WORK_QUEUE_PATH}/${encodeURIComponent(flowType)}`;
  return flowId === undefined ? typePath : `${typePath}/${encodeURIComponent(flowId)}`;
}

export function agentInboxPath(flowType?: string, flowId?: string) {
  return `${workQueuePath(flowType, flowId)}?${RUN_VIEW_PARAM}=${AGENT_INBOX_VIEW}`;
}

export function isPathOrChild(pathname: string, basePath: string) {
  return pathname === basePath || pathname.startsWith(`${basePath}/`);
}

interface RouterLocation {
  pathname: string;
  search: string;
  hash: string;
}

/**
 * Maps a former Dex Web v2 URL to the page that now does its job, keeping the query and hash.
 *
 * Run pages lose their Flow type, because a v1 run page is keyed by Flow ID alone.
 * The former Deep Dive was the v1 run page already, so it lands there too.
 */
export function legacyV2RedirectPath(location: RouterLocation) {
  const segments = location.pathname.split('/').filter(Boolean).slice(1).map(decodeURIComponent);
  const [mode, ...rest] = segments;
  const keep = `${location.search}${location.hash}`;
  if (mode === 'run') {
    const [flowType, flowId, debug, runId] = rest;
    if (flowType === undefined) return FLOWS_PATH;
    if (flowId === undefined) return flowsOfTypePath(flowType);
    return `${debug === 'debug' && runId !== undefined ? runPath(flowId, runId) : runPath(flowId)}${keep}`;
  }
  if (mode === 'work-queue') {
    const [flowType, flowId] = rest;
    return `${workQueuePath(flowType, flowId)}${keep}`;
  }
  if (mode === 'connectors' || mode === 'connections') {
    const subPath = rest.map(encodeURIComponent).join('/');
    return `${CONNECTORS_PATH}${subPath ? `/${subPath}` : ''}${keep}`;
  }
  return FLOWS_PATH;
}
