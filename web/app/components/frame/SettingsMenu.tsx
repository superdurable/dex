// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useState } from 'react';
import { BookOpen, Settings } from 'lucide-react';
import {
  fetchServerInfoDisplay,
  serverInfoSummaryOf,
  serverProtocolRangeOf,
  type ServerInfoDisplay,
} from '@/lib/serverInfo';
import { usePreferences } from '../../providers';
import { THEME_CHOICES, useTheme, type ThemeChoice } from '../../theme';
import { usePopover } from './usePopover';

/** The Dex documentation site, opened in a new tab. */
export const DEX_DOCS_URL = 'https://docs.superdurable.io/';

const THEME_LABEL: Record<ThemeChoice, string> = { light: 'Light', dark: 'Dark', system: 'Auto' };

/** Reads the server info each time the menu opens; undefined while the read runs. */
function useServerInfoWhileOpen(isOpen: boolean): ServerInfoDisplay | undefined {
  const [info, setInfo] = useState<ServerInfoDisplay>();
  useEffect(() => {
    if (!isOpen) return undefined;
    const controller = new AbortController();
    void fetchServerInfoDisplay(controller.signal).then((next) => {
      if (!controller.signal.aborted) setInfo(next);
    });
    return () => controller.abort();
  }, [isOpen]);
  return info;
}

/** What Dex Web is connected to: a one-line summary, then the engine, versions and protocol range. */
function ConnectedServerInfo({ info }: { info: ServerInfoDisplay | undefined }) {
  const summary = info ? serverInfoSummaryOf(info) ?? 'unknown' : 'Checking…';
  const protocolRange = info && serverProtocolRangeOf(info);
  const details: [string, string | undefined][] = info ? [
    ['Engine', info.engineLabel],
    ['Version', info.serverVersion],
    ['Protocol', protocolRange],
    ['Dex Web', info.webVersion],
  ] : [];
  const knownDetails = details.filter((detail): detail is [string, string] => detail[1] !== undefined);
  return (
    <div aria-label="Dex Server" className="app-bar-server-info" role="group">
      <div className="app-bar-server-info-head">
        <span>Server</span>
        <span className="app-bar-server-info-summary">{summary}</span>
      </div>
      {knownDetails.length > 0 && (
        <dl className="app-bar-server-info-details">
          {knownDetails.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

/** The viewer's own settings: theme, timezone, the connected server and a link to the docs. */
export function SettingsMenu() {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const { timezone, setTimezone } = usePreferences();
  const theme = useTheme();
  const menuId = useId();
  const serverInfo = useServerInfoWhileOpen(isOpen);
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
          <ConnectedServerInfo info={serverInfo} />
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
