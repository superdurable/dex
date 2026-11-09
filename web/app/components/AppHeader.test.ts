// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { v2ModePath } from '../v2/contract';
import { V2_MODES, hasLoadedFlowDefinitions } from './AppHeader';

describe('Dex Web v2 header', () => {
  it('names the third mode Connectors and opens it at /v2/connectors', () => {
    expect(V2_MODES.map(({ label }) => label)).toEqual(['Run', 'Work Queue', 'Connectors']);
    expect(V2_MODES.map(({ mode }) => v2ModePath(mode))).toEqual(['/v2/run', '/v2/work-queue', '/v2/connectors']);
  });
});

describe('Dex Web v1 header', () => {
  it('shows Flow Rendering only when Flow definition JSON files are loaded', () => {
    const catalog = (configured: boolean, count: number) => ({
      configured,
      directory: '/defs',
      definitionRevision: 'r1',
      definitions: Array.from({ length: count }, () => ({})),
    }) as unknown as Parameters<typeof hasLoadedFlowDefinitions>[0];
    expect(hasLoadedFlowDefinitions(null)).toBe(false);
    expect(hasLoadedFlowDefinitions(catalog(false, 0))).toBe(false);
    expect(hasLoadedFlowDefinitions(catalog(true, 0))).toBe(false);
    expect(hasLoadedFlowDefinitions(catalog(true, 1))).toBe(true);
  });
});
