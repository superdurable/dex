/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

// Sections of the old Dex vs. Temporal page that moved to other pages. The page redirect keeps the hash,
// so an old link such as /intro/compared-to-durable-execution/#run-the-server arrives here.
const movedSections: Record<string, {page: string; anchor?: string}> = {
  'run-the-server': {page: 'compared-to-temporal-deep-dive', anchor: 'run-the-server'},
  'concurrency-defaults': {page: 'compared-to-temporal-deep-dive', anchor: 'concurrency-defaults'},
  'the-same-process-in-both-models': {page: 'compared-to-temporal-deep-dive', anchor: 'complete-code'},
  'other-engines': {page: 'compared-to-other-engines'},
  'replay-based-workflow-engines': {page: 'compared-to-other-engines', anchor: 'replay-based-workflow-engines'},
  'journal-based-durable-function-runtimes': {page: 'compared-to-other-engines', anchor: 'journal-based-durable-function-runtimes'},
  'database-backed-workflow-libraries': {page: 'compared-to-other-engines', anchor: 'database-backed-workflow-libraries'},
  'when-restate-or-dbos-fits': {page: 'compared-to-other-engines', anchor: 'when-restate-or-dbos-fits'},
  'step-function-services': {page: 'compared-to-other-engines', anchor: 'step-function-services'},
  'agent-framework-checkpoints': {page: 'compared-to-other-engines', anchor: 'agent-framework-checkpoints'},
  'agent-frameworks-on-a-durable-execution-engine': {
    page: 'compared-to-other-engines',
    anchor: 'agent-frameworks-on-a-durable-execution-engine',
  },
};

const overviewPath = /^(.*\/intro\/)compared-to-temporal\/?$/;

export function onRouteDidUpdate({location}: {location: {pathname: string; hash: string}}): void {
  const match = overviewPath.exec(location.pathname);
  const id = decodeURIComponent(location.hash.slice(1));
  const moved = match ? movedSections[id] : undefined;
  // A section that still exists on this page keeps its own anchor.
  if (!match || !moved || document.getElementById(id)) return;
  window.location.replace(`${match[1]}${moved.page}/${moved.anchor ? `#${moved.anchor}` : ''}`);
}
