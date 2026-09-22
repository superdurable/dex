// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { DexAPIError } from '@/lib/http';
import {
  RUNNING_FLOW_STATUS_CODE,
  absorb,
  classifyReadFailure,
  isOpenFlowStatusCode,
  isStrandedRunFailure,
  nothingHeld,
  openFlowStatusLabel,
  type Held,
} from './liveness';

const COMPLETED_FLOW_STATUS_CODE = 2;

// Measured against a live server: a dead worker and a closed run share this code and status.
const workerGone = () => new DexAPIError(
  'connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:8873: connect: connection refused"',
  409,
  9,
);
const inactiveFlow = () => new DexAPIError('Flow is not active', 409, 9);

describe('absorb', () => {
  it('starts as loading, which is not the same as an empty answer', () => {
    expect(nothingHeld<string[]>()).toEqual({ value: null, liveness: 'loading', reason: null });
  });

  it('keeps the previous answer and marks it stale when a refresh fails', () => {
    const ok = absorb(nothingHeld<string[]>(), { state: 'ok', value: ['a'] });
    const failed = absorb(ok, { state: 'unreachable', reason: 'timeout' });
    expect(failed.value).toEqual(['a']);
    expect(failed.liveness).toBe('stale');
    expect(failed.reason).toBe('timeout');
  });

  it('reports unreachable with no value when nothing ever arrived', () => {
    const failed = absorb(nothingHeld<string[]>(), { state: 'unreachable', reason: 'refused' });
    expect(failed.value).toBeNull();
    expect(failed.liveness).toBe('unreachable');
  });

  it('distinguishes an empty success from a failure', () => {
    const empty = absorb(nothingHeld<string[]>(), { state: 'ok', value: [] });
    expect(empty.liveness).toBe('ok');
    expect(empty.value).toEqual([]);
    expect(empty.reason).toBeNull();
  });

  it('does not call a stranded run stale, because nothing is coming back', () => {
    const ok = absorb(nothingHeld<string[]>(), { state: 'ok', value: ['a'] });
    const stranded = absorb(ok, { state: 'stranded', reason: 'worker gone' });
    expect(stranded.liveness).toBe('stranded');
    expect(stranded.value).toEqual(['a']);
  });

  it('clears a stale reason once a read succeeds again', () => {
    const stale = absorb(
      { value: ['a'], liveness: 'stale', reason: 'timeout' },
      { state: 'ok', value: ['b'] },
    );
    expect(stale).toEqual({ value: ['b'], liveness: 'ok', reason: null });
  });

  it('is pure: it neither mutates the prior nor varies between identical calls', () => {
    const prior: Held<string[]> = { value: ['a'], liveness: 'ok', reason: null };
    const frozen = { ...prior, value: [...prior.value as string[]] };
    const first = absorb(prior, { state: 'unreachable', reason: 'timeout' });
    const second = absorb(prior, { state: 'unreachable', reason: 'timeout' });
    expect(first).toEqual(second);
    expect(prior).toEqual(frozen);
  });
});

describe('isStrandedRunFailure', () => {
  it('calls a running run whose worker exited stranded', () => {
    expect(isStrandedRunFailure(workerGone(), RUNNING_FLOW_STATUS_CODE)).toBe(true);
  });

  it('does not call a closed run stranded, even though its Display is equally unreadable', () => {
    expect(isStrandedRunFailure(workerGone(), COMPLETED_FLOW_STATUS_CODE)).toBe(false);
  });

  it('does not treat "Flow is not active" as stranded, though it shares the gRPC code', () => {
    expect(isStrandedRunFailure(inactiveFlow(), RUNNING_FLOW_STATUS_CODE)).toBe(false);
  });

  it('ignores other gRPC codes and plain errors', () => {
    const notFound = new DexAPIError('workflow not found for ID: nope', 404, 5);
    expect(isStrandedRunFailure(notFound, RUNNING_FLOW_STATUS_CODE)).toBe(false);
    expect(isStrandedRunFailure(new Error('network down'), RUNNING_FLOW_STATUS_CODE)).toBe(false);
  });

  it('is not stranded when the run status is unknown', () => {
    expect(isStrandedRunFailure(workerGone(), undefined)).toBe(false);
  });
});

describe('classifyReadFailure', () => {
  it('routes a stranded run and a general failure to different outcomes', () => {
    expect(classifyReadFailure(workerGone(), RUNNING_FLOW_STATUS_CODE).state).toBe('stranded');
    expect(classifyReadFailure(workerGone(), COMPLETED_FLOW_STATUS_CODE).state).toBe('unreachable');
    const failure = classifyReadFailure(new Error('boom'), RUNNING_FLOW_STATUS_CODE);
    expect(failure.state === 'ok' ? '' : failure.reason).toBe('boom');
  });
});

describe('open flow status', () => {
  it('treats only Running as open work', () => {
    expect(isOpenFlowStatusCode(RUNNING_FLOW_STATUS_CODE)).toBe(true);
    for (const closed of [0, 2, 3, 4, 5, 6, 7]) {
      expect(isOpenFlowStatusCode(closed)).toBe(false);
    }
    expect(isOpenFlowStatusCode(undefined)).toBe(false);
  });

  it('names the status with the label the search filter accepts', () => {
    expect(openFlowStatusLabel()).toBe('Running');
  });
});
