// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId } from 'react';
import { BookOpen, Settings } from 'lucide-react';
import { usePreferences } from '../../providers';
import { THEME_CHOICES, useTheme, type ThemeChoice } from '../../theme';
import { usePopover } from './usePopover';

/** The Dex documentation site, opened in a new tab. */
export const DEX_DOCS_URL = 'https://docs.superdurable.io/';

const THEME_LABEL: Record<ThemeChoice, string> = { light: 'Light', dark: 'Dark', system: 'Auto' };

/** The viewer's own settings: theme, timezone and a link to the docs. */
export function SettingsMenu() {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const { timezone, setTimezone } = usePreferences();
  const theme = useTheme();
  const menuId = useId();
  return (
    <div className="app-bar-settings" ref={rootRef}>
      <button
        aria-controls={menuId}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
        aria-label="Settings"
        className="app-bar-icon-button"
        onClick={() => setIsOpen((open) => !open)}
        title="Settings"
        type="button"
      >
        <Settings aria-hidden="true" size={16} strokeWidth={2} />
      </button>
      {isOpen && (
        <div aria-label="Settings" className="app-bar-popover app-bar-settings-popover" id={menuId} role="dialog">
          <div className="app-bar-setting" role="group" aria-label="Theme">
            <span>Theme</span>
            <div className="app-bar-segmented">
              {THEME_CHOICES.map((choice) => (
                <button
                  aria-pressed={theme.choice === choice}
                  key={choice}
                  onClick={() => theme.set(choice)}
                  type="button"
                >
                  {THEME_LABEL[choice]}
                </button>
              ))}
            </div>
          </div>
          <div className="app-bar-setting" role="group" aria-label="Timezone">
            <span>Timezone</span>
            <div className="app-bar-segmented">
              {(['local', 'UTC'] as const).map((choice) => (
                <button
                  aria-pressed={timezone === choice}
                  key={choice}
                  onClick={() => setTimezone(choice)}
                  type="button"
                >
                  {choice === 'local' ? 'Local' : 'UTC'}
                </button>
              ))}
            </div>
          </div>
          <a
            aria-label="Dex documentation"
            className="app-bar-menu-item app-bar-docs-link"
            href={DEX_DOCS_URL}
            rel="noopener noreferrer"
            target="_blank"
          >
            <span className="app-bar-menu-icon"><BookOpen aria-hidden="true" size={16} strokeWidth={2} /></span>
            <span className="app-bar-menu-words"><b>Docs</b></span>
          </a>
        </div>
      )}
    </div>
  );
}
