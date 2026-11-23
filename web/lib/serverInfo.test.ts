// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { supportsUnlimitedHistoryWindow } from './serverInfo';

describe('unlimited history window support', () => {
  it('holds for server-zd 0.1.38 and later releases', () => {
    for (const serverVersion of ['0.1.38', '0.1.40', '0.2.0', '1.0.0']) {
      expect(supportsUnlimitedHistoryWindow({ engineLabel: 'server-zd', serverVersion }), serverVersion).toBe(true);
    }
  });

  it('does not hold for released server-zd versions before 0.1.38 or for other engines', () => {
    for (const serverVersion of ['0.1.37', '0.1.21', '0.0.9']) {
      expect(supportsUnlimitedHistoryWindow({ engineLabel: 'server-zd', serverVersion }), serverVersion).toBe(false);
    }
    expect(supportsUnlimitedHistoryWindow({ engineLabel: 'Temporal', serverVersion: '1.25.0' })).toBe(false);
  });

  it('holds for unreleased builds and unknown servers', () => {
    for (const serverVersion of ['0.1.0', '0.1.0-dev.gabc1234', '', undefined]) {
      expect(supportsUnlimitedHistoryWindow({ engineLabel: 'server-zd', serverVersion }), `${serverVersion}`).toBe(true);
    }
    expect(supportsUnlimitedHistoryWindow({})).toBe(true);
  });
});
