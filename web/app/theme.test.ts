// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { isThemeChoice, parseThemeChoice, resolveTheme } from './theme';

describe('Dex Web theme', () => {
  it('defaults to light when nothing is stored', () => {
    expect(parseThemeChoice(null)).toBe('light');
    expect(parseThemeChoice('')).toBe('light');
    expect(parseThemeChoice('nope')).toBe('light');
  });

  it('accepts the three stored choices', () => {
    expect(isThemeChoice('light')).toBe(true);
    expect(isThemeChoice('dark')).toBe(true);
    expect(isThemeChoice('system')).toBe(true);
    expect(parseThemeChoice('dark')).toBe('dark');
    expect(parseThemeChoice('system')).toBe('system');
  });

  it('resolves Auto from the OS while Light and Dark stay fixed', () => {
    expect(resolveTheme('light', false)).toBe('light');
    expect(resolveTheme('dark', true)).toBe('dark');
    expect(resolveTheme('system', true)).toBe('light');
    expect(resolveTheme('system', false)).toBe('dark');
  });
});
