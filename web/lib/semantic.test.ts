// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { flowErrorTypeLabel } from './semantic';

describe('flowErrorTypeLabel', () => {
  it('labels an abandoned SubFlow by number and by name', () => {
    expect(flowErrorTypeLabel(7)).toBe('SubFlow abandoned by a time travel');
    expect(flowErrorTypeLabel('FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED')).toBe('SubFlow abandoned by a time travel');
  });
});
