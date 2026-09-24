// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactElement } from 'react';

import { THEME_CHOICES, type ThemeChoice, useTheme } from '../theme';

const LABEL: Record<ThemeChoice, string> = {
  light: 'Light',
  dark: 'Dark',
  system: 'Auto',
};

/**
 * Sun, moon, half-moon. The three most recognisable glyphs for this control, and
 * the reason it is a button rather than the labelled <select> it replaces: a
 * theme switch is the one header control nobody needs a word for, and a dropdown
 * spent a label plus a chevron plus a popup on a choice between three states.
 *
 * Drawn rather than typed. `☀` and `☾` fall back to a colour emoji on some
 * platforms and to tofu on others, and the half-moon has no dependable glyph at
 * all — so all three are paths, sharing one 16px box and `currentColor`.
 */
const ICON: Record<ThemeChoice, ReactElement> = {
  light: (
    <>
      <circle cx="8" cy="8" r="3.25" />
      <path
        d="M8 1v1.6M8 13.4V15M1 8h1.6M13.4 8H15M3.05 3.05l1.13 1.13M11.82 11.82l1.13 1.13M12.95 3.05l-1.13 1.13M4.18 11.82l-1.13 1.13"
        strokeLinecap="round"
      />
    </>
  ),
  dark: (
    // One path, not a circle minus a circle: a crescent cut by a second disc
    // needs a mask, and a mask is one more thing that can fail to print.
    <path d="M13.2 10.1A5.6 5.6 0 0 1 5.9 2.8 5.7 5.7 0 1 0 13.2 10.1Z" />
  ),
  system: (
    // Half filled. The conventional "follow the system" mark, and it reads as
    // "some of each" without needing a monitor glyph nobody scans at 16px.
    <>
      <circle cx="8" cy="8" r="5.6" />
      <path d="M8 2.4a5.6 5.6 0 0 1 0 11.2Z" fill="currentColor" stroke="none" />
    </>
  ),
};

export function ThemeToggle() {
  const state = useTheme();
  const index = THEME_CHOICES.indexOf(state.choice);
  const next = THEME_CHOICES[(index + 1) % THEME_CHOICES.length];
  return (
    <button
      // The label names the state AND the next one, because an icon-only control
      // gives a screen reader nothing and gives a first-time user no hint that
      // there is a third stop.
      aria-label={`Colour theme: ${LABEL[state.choice]}. Activate for ${LABEL[next]}.`}
      className="theme-toggle"
      onClick={() => state.set(next)}
      title={`Theme: ${LABEL[state.choice]} — switch to ${LABEL[next]}`}
      type="button"
    >
      <svg
        aria-hidden="true"
        fill="none"
        focusable="false"
        height="16"
        stroke="currentColor"
        strokeWidth="1.5"
        viewBox="0 0 16 16"
        width="16"
        xmlns="http://www.w3.org/2000/svg"
      >
        {ICON[state.choice]}
      </svg>
    </button>
  );
}
