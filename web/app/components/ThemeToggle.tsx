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

const TITLE: Record<ThemeChoice, string> = {
  light: 'Always use the light theme.',
  dark: 'Always use the dark theme.',
  system: 'Follow this device, and keep following it when the device changes.',
};

export function ThemeToggle() {
  const state = useTheme();
  return (
    <div className="app-themebar" role="radiogroup" aria-label="Colour theme">
      {THEME_CHOICES.map((choice) => (
        <button
          key={choice}
          type="button"
          role="radio"
          aria-checked={state.choice === choice}
          className="app-themebtn"
          data-on={state.choice === choice ? 'true' : undefined}
          title={TITLE[choice]}
          onClick={() => state.set(choice)}
        >
          {LABEL[choice]}
          {choice === 'system' && state.choice === 'system' ? (
            <span className="app-themebtn-echo">{state.theme}</span>
          ) : null}
        </button>
      ))}
    </div>
  );
}
