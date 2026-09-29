// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The probe resolves tokens from the v2 stylesheet, shared with the other v2 pages.
import '../css/v2.css';
import type { Theme } from '../../theme';
import connectorStudioStylesheetSource from './connectorStudio.css?raw';

/** Everything a connector.host.ready message sends to control how a Connector Studio frame looks. */
export interface ConnectorStudioFrameAppearance {
  theme: Theme;
  themeTokens: Record<string, string>;
  stylesheet: string;
}

/** The canonical Connector Studio stylesheet from connectorStudio.css, without comments or blank lines. */
export const connectorStudioStylesheet = connectorStudioStylesheetSource
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .split('\n')
  .filter((line) => line.trim() !== '')
  .join('\n');

export type ConnectorStudioThemeTokenKind = 'color' | 'focusRing' | 'fontStack' | 'length' | 'duration';

export interface ConnectorStudioThemeTokenSource {
  studioToken: `--studio-${string}`;
  dexWebToken: `--${string}`;
  kind: ConnectorStudioThemeTokenKind;
}

/** Every --studio-* property a Connector Studio frame may receive, with the Dex Web v2 token it copies. */
export const connectorStudioThemeTokenSources: readonly ConnectorStudioThemeTokenSource[] = [
  { studioToken: '--studio-surface-page', dexWebToken: '--surface-page', kind: 'color' },
  { studioToken: '--studio-surface-card', dexWebToken: '--surface-card', kind: 'color' },
  { studioToken: '--studio-surface-raised', dexWebToken: '--surface-raised', kind: 'color' },
  { studioToken: '--studio-surface-hover', dexWebToken: '--surface-hover', kind: 'color' },
  { studioToken: '--studio-ink-max', dexWebToken: '--ink-max', kind: 'color' },
  { studioToken: '--studio-ink-strong', dexWebToken: '--ink-strong', kind: 'color' },
  { studioToken: '--studio-ink-mid', dexWebToken: '--ink-mid', kind: 'color' },
  { studioToken: '--studio-ink-soft', dexWebToken: '--ink-soft', kind: 'color' },
  { studioToken: '--studio-line-soft', dexWebToken: '--line-soft', kind: 'color' },
  { studioToken: '--studio-line-mid', dexWebToken: '--line-mid', kind: 'color' },
  { studioToken: '--studio-line-strong', dexWebToken: '--line-strong', kind: 'color' },
  { studioToken: '--studio-cta', dexWebToken: '--color-cta', kind: 'color' },
  { studioToken: '--studio-cta-hover', dexWebToken: '--color-cta-hover', kind: 'color' },
  { studioToken: '--studio-cta-on', dexWebToken: '--color-cta-on', kind: 'color' },
  { studioToken: '--studio-accent', dexWebToken: '--color-accent', kind: 'color' },
  { studioToken: '--studio-accent-wash', dexWebToken: '--accent-wash', kind: 'color' },
  { studioToken: '--studio-focus-ring', dexWebToken: '--focus-ring', kind: 'focusRing' },
  { studioToken: '--studio-success-ink', dexWebToken: '--hue-success-ink', kind: 'color' },
  { studioToken: '--studio-success-fill', dexWebToken: '--hue-success-fill', kind: 'color' },
  { studioToken: '--studio-danger-ink', dexWebToken: '--hue-danger-ink', kind: 'color' },
  { studioToken: '--studio-danger-fill', dexWebToken: '--hue-danger-fill', kind: 'color' },
  { studioToken: '--studio-attention-ink', dexWebToken: '--hue-attention-ink', kind: 'color' },
  { studioToken: '--studio-attention-fill', dexWebToken: '--hue-attention-fill', kind: 'color' },
  { studioToken: '--studio-font-sans', dexWebToken: '--font-sans', kind: 'fontStack' },
  { studioToken: '--studio-font-mono', dexWebToken: '--font-mono', kind: 'fontStack' },
  { studioToken: '--studio-radius-sm', dexWebToken: '--r-sm', kind: 'length' },
  { studioToken: '--studio-radius', dexWebToken: '--r', kind: 'length' },
  { studioToken: '--studio-radius-lg', dexWebToken: '--r-lg', kind: 'length' },
  { studioToken: '--studio-duration', dexWebToken: '--dur', kind: 'duration' },
];

// The grammar is identical in dex-connectors-library sdk/react/src/studio-theme.ts; change both together.
const maximumThemeTokenValueLength = 256;

const hexColorPattern = '#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})';
const colorChannelPattern = '(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])';
const colorAlphaPattern = '(?:0(?:\\.[0-9]{1,4})?|1(?:\\.0{1,4})?|\\.[0-9]{1,4})';
const rgbColorPattern = `rgb\\( *${colorChannelPattern} *, *${colorChannelPattern} *, *${colorChannelPattern} *\\)`;
const rgbaColorPattern = `rgba\\( *${colorChannelPattern} *, *${colorChannelPattern} *, *${colorChannelPattern} *, *${colorAlphaPattern} *\\)`;
const colorPattern = `(?:${hexColorPattern}|${rgbColorPattern}|${rgbaColorPattern})`;
const pixelLengthPattern = '(?:0|[0-9]{1,3}(?:\\.[0-9]{1,3})?px)';
const durationPattern = '(?:[0-9]{1,4}ms|(?:[0-9]{1,2}(?:\\.[0-9]{1,3})?|\\.[0-9]{1,3})s)';
// CSS requires these words to be quoted in a family name.
const reservedFontFamilyWordPattern = '(?:inherit|initial|unset|revert|revert-layer|default|none)(?![A-Za-z0-9-])';
const unquotedFontFamilyWordPattern = `(?!${reservedFontFamilyWordPattern})-?[A-Za-z][A-Za-z0-9-]*`;
const fontFamilyPattern = `(?:${unquotedFontFamilyWordPattern}(?: ${unquotedFontFamilyWordPattern})*|'[A-Za-z0-9 -]+'|"[A-Za-z0-9 -]+")`;

const themeTokenValuePatterns: Record<ConnectorStudioThemeTokenKind, RegExp> = {
  color: new RegExp(`^${colorPattern}$`),
  focusRing: new RegExp(`^(?:inset +)?-?${pixelLengthPattern}(?: +-?${pixelLengthPattern}){1,3} +${colorPattern}$`),
  fontStack: new RegExp(`^${fontFamilyPattern}(?: *, *${fontFamilyPattern}){0,15}$`, 'i'),
  length: new RegExp(`^${pixelLengthPattern}$`),
  duration: new RegExp(`^${durationPattern}$`),
};

/** Reports whether value matches the grammar for kind, which admits no semicolon, brace, angle bracket, backslash, url(), or var(). */
export function isConnectorStudioThemeTokenValue(kind: ConnectorStudioThemeTokenKind, value: unknown): value is string {
  return typeof value === 'string' && value.length <= maximumThemeTokenValueLength && themeTokenValuePatterns[kind].test(value);
}

/**
 * Returns the allowlisted --studio-* properties whose Dex Web token value passes its grammar.
 * Undefined and invalid tokens are omitted, so the frame keeps its built-in value for them.
 */
export function buildConnectorStudioThemeTokens(readDexWebToken: (dexWebToken: string) => string): Record<string, string> {
  const themeTokens: Record<string, string> = {};
  for (const source of connectorStudioThemeTokenSources) {
    const value = readDexWebToken(source.dexWebToken).trim();
    if (isConnectorStudioThemeTokenValue(source.kind, value)) themeTokens[source.studioToken] = value;
  }
  return themeTokens;
}

/** Returns the appearance a connector.host.ready message sends for a frame painted in theme. */
export function connectorStudioFrameAppearance(theme: Theme): ConnectorStudioFrameAppearance {
  return { theme, themeTokens: readConnectorStudioThemeTokens(theme), stylesheet: connectorStudioStylesheet };
}

/**
 * Returns the Studio theme tokens resolved from the Dex Web v2 ramps of theme.
 * The document must paint the same theme, because the light ramps match any light-themed ancestor.
 */
export function readConnectorStudioThemeTokens(theme: Theme): Record<string, string> {
  // A hidden probe themed like the frame resolves the v2 ramps of that theme, wherever the frame renders.
  const probeTheme = document.createElement('div');
  probeTheme.dataset.theme = theme;
  probeTheme.hidden = true;
  const probe = document.createElement('div');
  probe.className = 'v2-shell';
  probeTheme.append(probe);
  document.body.append(probeTheme);
  try {
    const computedStyle = window.getComputedStyle(probe);
    return buildConnectorStudioThemeTokens((dexWebToken) => computedStyle.getPropertyValue(dexWebToken));
  } finally {
    probeTheme.remove();
  }
}
