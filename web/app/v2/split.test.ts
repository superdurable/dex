// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  CASE_HEIGHT_FRAC,
  CASE_HEIGHT_MIN,
  LIST_REMAIN_MIN,
  LIST_WIDTH_DEFAULT,
  LIST_WIDTH_MIN,
  clampCaseHeight,
  clampListWidth,
} from './V2SplitHandle';

describe('v2 split clamps', () => {
  it('keeps the listing readable and the canvas usable', () => {
    expect(clampListWidth(LIST_WIDTH_DEFAULT, 1400)).toBe(LIST_WIDTH_DEFAULT);
    expect(clampListWidth(80, 1400)).toBe(LIST_WIDTH_MIN);
    expect(clampListWidth(1200, 1400)).toBe(Math.min(1400 * 0.6, 1400 - 280));
  });

  it('yields to the canvas when the window cannot seat both floors', () => {
    expect(clampListWidth(512, 500)).toBe(Math.max(160, 500 - 280));
  });

  it('keeps Display from swallowing the run list', () => {
    expect(clampCaseHeight(Number.NaN, 1000)).toBe(1000 * CASE_HEIGHT_FRAC);
    expect(clampCaseHeight(40, 1000)).toBe(CASE_HEIGHT_MIN);
    expect(clampCaseHeight(900, 1000)).toBe(1000 - LIST_REMAIN_MIN);
  });
});
