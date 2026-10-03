// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowExecution } from '@/lib/types';
import { selectVisibleIndexedAttributeKeys } from './FlowSearchPage';

describe('Flow Search Indexed Attribute columns', () => {
  it('shows application attributes without duplicate or internal columns', () => {
    const flows: FlowExecution[] = [
      {
        flowId: 'registration-1',
        runId: 'run-1',
        flowType: 'RegistrationFlow',
        flowStatus: 'Running',
        flowStatusCode: 1,
        startTime: null,
        closeTime: null,
        indexedAttributes: [
          { key: 'ActiveStepTypes', value: ['CollectPaymentStep'] },
          { key: 'BinaryChecksums', value: ['worker-checksum'] },
          { key: 'BuildIds', value: ['unversioned'] },
          { key: 'CadenceChangeVersion', value: ['change-1'] },
          { key: 'DexParentFlowID', value: 'parent-registration' },
          { key: 'DexWorkQueuePermissions', value: ['registration.resend-ticket'] },
          { key: 'FlowType', value: 'RegistrationFlow' },
          { key: 'TemporalChangeVersion', value: [] },
          { key: 'registration-state', value: 'payment_pending' },
          { key: 'customer-email', value: 'customer@example.com' },
        ],
      },
      {
        flowId: 'registration-2',
        runId: 'run-2',
        flowType: 'RegistrationFlow',
        flowStatus: 'Completed',
        flowStatusCode: 2,
        startTime: null,
        closeTime: null,
        indexedAttributes: [
          { key: 'registration-state', value: 'checked_in' },
        ],
      },
    ];

    expect(selectVisibleIndexedAttributeKeys(flows)).toEqual([
      'customer-email',
      'registration-state',
    ]);
  });
});
