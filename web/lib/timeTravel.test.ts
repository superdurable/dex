// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { historyEventTimeFromLocalInput, positiveRunAttempt, runAttemptSearch } from './timeTravel';

const RFC_3339_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

describe('historyEventTimeFromLocalInput', () => {
  it('reads the input as UTC when the page shows UTC', () => {
    expect(historyEventTimeFromLocalInput('2026-10-24T17:30', 'UTC')).toBe('2026-10-24T17:30:00.000Z');
    expect(historyEventTimeFromLocalInput('2026-10-24T17:30:05', 'UTC')).toBe('2026-10-24T17:30:05.000Z');
    expect(historyEventTimeFromLocalInput('2026-10-24T17:30:05.25', 'UTC')).toBe('2026-10-24T17:30:05.250Z');
  });

  it('reads the input in the browser zone when the page shows local time', () => {
    const converted = historyEventTimeFromLocalInput('2026-10-24T17:30:05', 'local');
    expect(converted).toMatch(RFC_3339_UTC);
    expect(converted).toBe(new Date(2026, 9, 24, 17, 30, 5).toISOString());
  });

  it('never sends a time without a zone', () => {
    for (const timezone of ['UTC', 'local'] as const) {
      expect(historyEventTimeFromLocalInput('2026-01-02T03:04', timezone)).toMatch(RFC_3339_UTC);
    }
  });

  it('rejects empty, malformed, and impossible times', () => {
    expect(historyEventTimeFromLocalInput('', 'UTC')).toBe('');
    expect(historyEventTimeFromLocalInput('yesterday', 'UTC')).toBe('');
    expect(historyEventTimeFromLocalInput('2026-02-30T10:00', 'UTC')).toBe('');
    expect(historyEventTimeFromLocalInput('2026-10-24T17:30:00Z', 'UTC')).toBe('');
  });
});

describe('run attempts', () => {
  it('accepts only positive integers', () => {
    expect(positiveRunAttempt(3)).toBe(3);
    expect(positiveRunAttempt('2')).toBe(2);
    expect(positiveRunAttempt(0)).toBe(0);
    expect(positiveRunAttempt(undefined)).toBe(0);
    expect(positiveRunAttempt('')).toBe(0);
    expect(positiveRunAttempt('1.5')).toBe(0);
    expect(positiveRunAttempt(-1)).toBe(0);
  });

  it('builds the search string that selects an attempt', () => {
    expect(runAttemptSearch(2)).toBe('?runAttempt=2');
  });
});
