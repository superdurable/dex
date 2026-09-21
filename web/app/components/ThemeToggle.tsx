// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { THEME_CHOICES, type ThemeChoice, useTheme } from '../theme';

const LABEL: Record<ThemeChoice, string> = {
  light: 'Light',
  dark: 'Dark',
  system: 'Auto',
};

export function ThemeToggle() {
  const state = useTheme();
  return (
    <label className="timezone-control">
      <span>Theme</span>
      <select
        aria-label="Colour theme"
        value={state.choice}
        onChange={(event) => state.set(event.target.value as ThemeChoice)}
      >
        {THEME_CHOICES.map((choice) => (
          <option key={choice} value={choice}>
            {LABEL[choice]}
          </option>
        ))}
      </select>
    </label>
  );
}
