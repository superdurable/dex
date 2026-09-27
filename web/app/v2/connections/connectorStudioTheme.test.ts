// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import connectorStudioStylesheetSource from './connectorStudio.css?raw';
import {
  buildConnectorStudioThemeTokens,
  connectorStudioStylesheet,
  connectorStudioThemeTokenSources,
  isConnectorStudioThemeTokenValue,
  type ConnectorStudioThemeTokenKind,
} from './connectorStudioTheme';

const lightDexWebTokens: Record<string, string> = {
  '--surface-page': '#f2f8f4',
  '--surface-card': '#ffffff',
  '--surface-raised': '#f7fcf9',
  '--surface-hover': '#e9efeb',
  '--ink-max': '#181a1c',
  '--ink-strong': '#434547',
  '--ink-mid': '#6e7073',
  '--ink-soft': '#949699',
  '--line-soft': '#e4e6e9',
  '--line-mid': '#d3d5d8',
  '--line-strong': '#abacaf',
  '--color-cta': '#008650',
  '--color-cta-hover': '#007546',
  '--color-cta-on': '#ffffff',
  '--color-accent': '#34659f',
  '--accent-wash': 'rgba(52, 101, 159, 0.08)',
  '--focus-ring': '0 0 0 3px rgba(52, 101, 159, 0.32)',
  '--hue-success-ink': '#1c754a',
  '--hue-success-fill': '#ddf6e6',
  '--hue-danger-ink': '#9a4548',
  '--hue-danger-fill': '#ffe8e8',
  '--hue-attention-ink': '#845b00',
  '--hue-attention-fill': '#fbecd6',
  '--font-sans': "-apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, 'Helvetica Neue', Arial, sans-serif",
  '--font-mono': "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace",
  '--r-sm': '4px',
  '--r': '6px',
  '--r-lg': '10px',
  '--dur': '160ms',
  '--color-canvas': '#eef3f0',
  '--shadow-pop': '0 0 0 1px #d3d5d8, 0 10px 28px -12px rgba(15, 23, 36, 0.22)',
};

const tokenReader = (tokens: Record<string, string>) => (dexWebToken: string) => tokens[dexWebToken] ?? '';

const fontFamilies = (count: number) => Array.from({ length: count }, (_, index) => `Font${index}`).join(', ');

// Keep identical to the list in dex-connectors-library sdk/react/test/studio-theme.test.ts.
const sharedThemeTokenGrammarCases: Record<ConnectorStudioThemeTokenKind, { accepted: string[]; rejected: string[] }> = {
  color: {
    accepted: [
      '#abc', '#abcd', '#a1b2c3', '#A1B2C3D4', 'rgb(0, 134, 80)', 'rgb(0,134,80)', 'rgb(255, 255, 255)',
      'rgba(52, 101, 159, 0.08)', 'rgba(137, 190, 255, .1)', 'rgba(0, 0, 0, 0)', 'rgba(0, 0, 0, 1)',
      'rgba(0, 0, 0, 1.0)', 'rgba(0,0,0,1.00)',
    ],
    rejected: [
      '#abcde', '#ggg', 'red', 'transparent', 'currentColor', 'rgb(0 0 0)', 'rgb(0 0 0 / 50%)', 'rgb(256, 0, 0)',
      'rgb(999,0,0)', 'rgb(-1, 0, 0)', 'rgb(10%,0%,0%)', 'rgb(01, 0, 0)', 'rgba(0,0,0,50%)', 'rgba(0,0,0)',
      'rgb(0,0,0,0.5)', 'rgba(0, 0, 0, 1.5)', 'rgba(0, 0, 0, 0.12345)', 'RGB(0, 0, 0)', 'hsl(0, 0%, 0%)',
      'color-mix(in srgb, red, blue)', '#fff /* */', '#fff;', '',
    ],
  },
  focusRing: {
    accepted: ['0 0 0 3px rgba(137, 190, 255, 0.3)', '0 0 0 3px rgba(137, 190, 255, .3)', '0 0 0 2px #34659f', 'inset 0 0 0 1px rgb(0, 0, 0)', '0 -1px 2px #000'],
    rejected: [
      'none', '0 0 0 3px', '3px #000', '0 0 0 3em #000', '0 0 0 3px #000, 0 0 0 1px #fff', '0 0 0 3px #000 inset',
      '0 0 0 0 0 #000', '0 0 0 3px transparent', '0 0 0 3.1234px #000',
    ],
  },
  fontStack: {
    accepted: [
      'Inter', 'ui-monospace, monospace', "'SF Mono', Menlo", '"Helvetica Neue", Arial, sans-serif', 'Helvetica Neue, sans-serif',
      '-apple-system, BlinkMacSystemFont', "'Font 2', serif", 'Nonesuch, serif', "'inherit', serif", fontFamilies(16),
    ],
    rejected: [
      'Font 2', "'My_Font'", "'My.Font'", "'unterminated, Arial", 'Inter, url(x)', 'Inter\\', 'Inter;', 'Inter, }', "Arial, 'x' y",
      'Inter,,Arial', 'inherit', 'initial', 'unset', 'revert', 'revert-layer', 'default', 'none', 'INHERIT', 'inherit, Arial',
      'Arial, initial', fontFamilies(17), 'a'.repeat(257),
    ],
  },
  length: {
    accepted: ['0', '4px', '6.5px', '4.125px', '999px'],
    rejected: ['6', '-4px', '4rem', '4.1234px', '1000px', '4px 4px', '4px;', 'calc(1px + 2px)'],
  },
  duration: {
    accepted: ['160ms', '0ms', '9999ms', '.16s', '0.16s', '1s', '12.5s'],
    rejected: ['160', '0', '-160ms', '10000ms', '160 ms', '1.5ms', '100s', '.1234s', 'calc(1s)'],
  },
};

describe('Connector Studio theme tokens', () => {
  it('maps exactly the allowlisted Dex Web v2 tokens to --studio-* names', () => {
    const requestedTokens: string[] = [];
    const themeTokens = buildConnectorStudioThemeTokens((dexWebToken) => {
      requestedTokens.push(dexWebToken);
      return lightDexWebTokens[dexWebToken] ?? '';
    });
    expect(Object.keys(themeTokens)).toEqual(connectorStudioThemeTokenSources.map((source) => source.studioToken));
    expect(Object.keys(themeTokens)).toHaveLength(29);
    expect(requestedTokens).not.toContain('--color-canvas');
    expect(requestedTokens).not.toContain('--shadow-pop');
    expect(themeTokens).toMatchObject({
      '--studio-surface-page': '#f2f8f4',
      '--studio-cta': '#008650',
      '--studio-cta-on': '#ffffff',
      '--studio-accent-wash': 'rgba(52, 101, 159, 0.08)',
      '--studio-focus-ring': '0 0 0 3px rgba(52, 101, 159, 0.32)',
      '--studio-font-sans': lightDexWebTokens['--font-sans'],
      '--studio-font-mono': lightDexWebTokens['--font-mono'],
      '--studio-radius': '6px',
      '--studio-radius-lg': '10px',
      '--studio-duration': '160ms',
    });
  });

  it('trims computed values and omits tokens the document does not define', () => {
    const themeTokens = buildConnectorStudioThemeTokens(tokenReader({ '--color-cta': ' #70eea9 ', '--r': '\t6px\n' }));
    expect(themeTokens).toEqual({ '--studio-cta': '#70eea9', '--studio-radius': '6px' });
  });

  it('keeps the minified values the production stylesheet computes', () => {
    const themeTokens = buildConnectorStudioThemeTokens(tokenReader({
      '--accent-wash': 'rgba(137, 190, 255, .1)',
      '--focus-ring': '0 0 0 3px rgba(137, 190, 255, .3)',
      '--font-mono': 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
      '--dur': '.16s',
    }));
    expect(themeTokens).toEqual({
      '--studio-accent-wash': 'rgba(137, 190, 255, .1)',
      '--studio-focus-ring': '0 0 0 3px rgba(137, 190, 255, .3)',
      '--studio-font-mono': 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
      '--studio-duration': '.16s',
    });
  });

  it('drops every value that could escape the declaration it is assigned to', () => {
    const hostileTokens: Record<string, string> = {
      '--surface-page': 'red; background:url(x)',
      '--surface-card': 'expression(alert(1))',
      '--surface-raised': '</style><script>alert(1)</script>',
      '--surface-hover': 'url(https://attacker.example/pixel.png)',
      '--ink-max': '#fff;',
      '--ink-strong': 'rgb(0, 0, 0) url(x)',
      '--ink-mid': 'var(--surface-page)',
      '--ink-soft': 'rgba(0, 0, 0, 0.5) !important',
      '--line-soft': 'red',
      '--line-mid': 'rgb(256, 0, 0)',
      '--line-strong': 'rgba(0, 0, 0, 1.5)',
      '--color-cta': '#ggg',
      '--color-cta-hover': 'rgb(0,0,0)}body{background:red',
      '--color-cta-on': 'color-mix(in srgb, red, blue)',
      '--focus-ring': '0 0 0 3px rgba(0, 0, 0, 0.3), 0 0 0 1px url(x)',
      '--font-sans': "Arial; color: red",
      '--font-mono': "'Segoe UI\\', monospace",
      '--r-sm': '4em',
      '--r': '-6px',
      '--r-lg': 'calc(10px + 1px)',
      '--dur': '160ms; transition: none',
    };
    expect(buildConnectorStudioThemeTokens(tokenReader(hostileTokens))).toEqual({});
  });

  it('applies the grammar shared with sdk/react to every token kind', () => {
    for (const [kind, { accepted, rejected }] of Object.entries(sharedThemeTokenGrammarCases) as [ConnectorStudioThemeTokenKind, { accepted: string[]; rejected: string[] }][]) {
      for (const value of accepted) expect(isConnectorStudioThemeTokenValue(kind, value), `${kind}: ${value}`).toBe(true);
      for (const value of rejected) expect(isConnectorStudioThemeTokenValue(kind, value), `${kind}: ${value}`).toBe(false);
    }
  });
});

// Keep identical to connectorStudioClassNames in dex-connectors-library sdk/react/src/studio-theme.ts.
// Released bundles ignore stylesheets missing any; never rename or drop one.
const sharedConnectorStudioClassNames = [
  'studio-surface', 'studio-header', 'studio-muted', 'studio-field', 'studio-actions', 'studio-button', 'studio-button-primary',
  'studio-notice', 'studio-notice-info', 'studio-notice-success', 'studio-notice-error', 'studio-notice-attention',
  'studio-options', 'studio-option', 'studio-option-label', 'studio-option-id', 'studio-option-detail',
  'studio-badges', 'studio-badge', 'studio-checkbox',
];

// sdk/react connectorStudioStylesheetMaxLength; bundles ignore a longer ready.stylesheet.
const maximumConnectorStudioStylesheetLength = 64 * 1024;

interface StudioStylesheetRule {
  selectors: string[];
  declarations: { property: string; value: string }[];
}

const tokenBlockSelectors = new Set([':root', ":root[data-theme='dark']"]);
const documentSelectors = new Set(['html', 'body']);
const attributeSelectorPattern = String.raw`\[[a-z-]+(?:='[A-Za-z0-9 _-]*')?\]`;
const statePseudoClassPattern = ':(?:hover|focus-visible|focus-within|disabled|checked)';
const pseudoClassPattern = `(?:${statePseudoClassPattern}|:not\\((?:${attributeSelectorPattern}|${statePseudoClassPattern})\\))`;
const studioClassCompoundPattern = `(?:\\.studio-[a-z0-9-]+)+(?:${attributeSelectorPattern}|${pseudoClassPattern})*`;
const elementCompoundPattern = `[a-z][a-z0-9]*(?:${attributeSelectorPattern}|${pseudoClassPattern})*`;
// A selector starts at a studio-* class; plain elements are allowed only inside one.
const studioSelectorPattern = new RegExp(
  `^${studioClassCompoundPattern}(?: (?:> )?(?:${studioClassCompoundPattern}|${elementCompoundPattern}))*$`,
);
const studioTokenReferencePattern = /^var\((--studio-[a-z-]+)\)$/;
const valueTermPattern = /^(?:-?(?:[0-9]+|[0-9]*\.[0-9]+)(?:px|fr|%|ms|s)?|var\(--studio-[a-z-]+\))$/;
// The only keywords non-token rules may use. transparent is the only color among them.
const studioStylesheetKeywords = new Set([
  'absolute', 'anywhere', 'auto', 'background', 'baseline', 'block', 'bold', 'border-box', 'border-color', 'bottom', 'box-shadow',
  'break-word', 'center', 'clip', 'color', 'column', 'content-box', 'contents', 'default', 'ellipsis', 'end', 'flex', 'grid', 'hidden',
  'inherit', 'inline', 'inline-block', 'inline-flex', 'inline-grid', 'left', 'middle', 'none', 'normal', 'not-allowed', 'nowrap',
  'opacity', 'pointer', 'relative', 'right', 'row', 'scroll', 'solid', 'space-between', 'start', 'sticky', 'stretch', 'top',
  'transparent', 'underline', 'visible', 'wrap',
]);
const themeTokenKinds = new Map<string, ConnectorStudioThemeTokenKind>(
  connectorStudioThemeTokenSources.map((source) => [source.studioToken, source.kind]),
);

function parseStudioStylesheet(stylesheet: string): { rules: StudioStylesheetRule[]; strayText: string[] } {
  const rules: StudioStylesheetRule[] = [];
  const strayText: string[] = [];
  let consumedLength = 0;
  for (const match of stylesheet.matchAll(/([^{}]*)\{([^{}]*)\}/g)) {
    if (stylesheet.slice(consumedLength, match.index).trim() !== '') strayText.push(stylesheet.slice(consumedLength, match.index).trim());
    const selectorText = match[1].trim();
    if (selectorText === '') strayText.push(match[0]);
    rules.push({
      selectors: selectorText.split(',').map((selector) => selector.trim().replace(/\s+/g, ' ')),
      declarations: match[2].split(';').map((declaration) => declaration.trim()).filter(Boolean).map((declaration) => {
        const separator = declaration.indexOf(':');
        return separator < 0
          ? { property: declaration, value: '' }
          : { property: declaration.slice(0, separator).trim(), value: declaration.slice(separator + 1).trim() };
      }),
    });
    consumedLength = match.index + match[0].length;
  }
  if (stylesheet.slice(consumedLength).trim() !== '') strayText.push(stylesheet.slice(consumedLength).trim());
  return { rules, strayText };
}

function listStudioStylesheetViolations(stylesheet: string): string[] {
  const violations: string[] = [];
  if (stylesheet.length > maximumConnectorStudioStylesheetLength) violations.push(`longer than ${maximumConnectorStudioStylesheetLength} characters`);
  if (!/^[\t\n\x20-\x7e]*$/.test(stylesheet)) violations.push('non-ASCII or control character');
  for (const forbidden of ['</', '<', '\\', '@', '/*', '!', 'url(', 'expression(']) {
    if (stylesheet.toLowerCase().includes(forbidden)) violations.push(`contains ${forbidden}`);
  }
  const { rules, strayText } = parseStudioStylesheet(stylesheet);
  for (const text of strayText) violations.push(`text outside a rule: ${text}`);
  for (const rule of rules) {
    const isTokenBlock = rule.selectors.every((selector) => tokenBlockSelectors.has(selector));
    for (const selector of rule.selectors) {
      if (!tokenBlockSelectors.has(selector) && !documentSelectors.has(selector) && !studioSelectorPattern.test(selector)) {
        violations.push(`selector ${selector}`);
      }
      if (tokenBlockSelectors.has(selector) && !isTokenBlock) violations.push(`token selector ${selector} shares a rule`);
    }
    for (const { property, value } of rule.declarations) {
      if (isTokenBlock) {
        const kind = themeTokenKinds.get(property);
        const isColorScheme = property === 'color-scheme' && (value === 'light' || value === 'dark');
        if (!isColorScheme && (kind === undefined || !isConnectorStudioThemeTokenValue(kind, value))) {
          violations.push(`token declaration ${property}: ${value}`);
        }
        continue;
      }
      if (!/^[a-z][a-z-]*$/.test(property)) violations.push(`property ${property}`);
      const terms = value.split(/\s*,\s*|\s+|\//);
      for (const term of terms) {
        const reference = studioTokenReferencePattern.exec(term);
        const isAllowedTerm = studioStylesheetKeywords.has(term) || valueTermPattern.test(term);
        if (!isAllowedTerm || (reference !== null && !themeTokenKinds.has(reference[1]))) {
          violations.push(`value ${property}: ${value}`);
        }
      }
    }
  }
  return violations;
}

function listStudioClassesStyledDirectly(rules: StudioStylesheetRule[]): Set<string> {
  const styledClasses = new Set<string>();
  for (const rule of rules) {
    if (rule.declarations.length === 0) continue;
    for (const selector of rule.selectors) {
      const subjectCompound = selector.split(/ (?:> )?/).at(-1) ?? '';
      for (const match of subjectCompound.matchAll(/\.(studio-[a-z0-9-]+)/g)) styledClasses.add(match[1]);
    }
  }
  return styledClasses;
}

describe('Connector Studio stylesheet', () => {
  const { rules } = parseStudioStylesheet(connectorStudioStylesheet);

  it('sends connectorStudio.css without its comments', () => {
    expect(connectorStudioStylesheetSource).toContain('Canonical Connector Studio stylesheet');
    expect(connectorStudioStylesheet).not.toContain('/*');
    expect(connectorStudioStylesheet).not.toContain('Copyright');
    expect(connectorStudioStylesheet.split('{')).toHaveLength(connectorStudioStylesheetSource.split('{').length);
    expect(connectorStudioStylesheet.startsWith(':root {')).toBe(true);
  });

  it('stays within the size bound sdk/react accepts', () => {
    expect(connectorStudioStylesheet.length).toBeGreaterThan(0);
    expect(new TextEncoder().encode(connectorStudioStylesheet).length).toBeLessThanOrEqual(maximumConnectorStudioStylesheetLength);
  });

  it('uses only studio-* selectors, validated token blocks, and var(--studio-*) references', () => {
    expect(listStudioStylesheetViolations(connectorStudioStylesheet)).toEqual([]);
  });

  it('rejects constructs that could load resources, escape the style element, or outrank host tokens', () => {
    const hostileStylesheets = [
      "@import url('https://attacker.example/x.css');",
      '@media (min-width: 1px) { .studio-button { padding: 0; } }',
      '.studio-button { background: url(https://attacker.example/pixel.png); }',
      '.studio-button { width: expression(alert(1)); }',
      '.studio-button { color: red; } </style><script>alert(1)</script>',
      '.studio-button { color: \\72 ed; }',
      ':root { --studio-cta: #008650 !important; }',
      ':root { --studio-cta: red; }',
      ':root { --studio-unknown: #008650; }',
      ':root, .studio-button { --studio-cta: #008650; }',
      ":root[data-theme='light'] { --studio-cta: #008650; }",
      '.studio-button { --studio-cta: #008650; }',
      '.studio-button { color: red; }',
      '.studio-button { border-color: currentcolor; }',
      '.studio-notice-error { background: canvastext; }',
      '.studio-button { color: #ff0000; }',
      '.studio-button { color: rgb(255, 0, 0); }',
      '.studio-button { color: var(--surface-page); }',
      '.studio-button { color: var(--studio-cta, red); }',
      '.studio-button::before { content: x; }',
      'button { color: red; }',
      '.other-class .studio-button { color: red; }',
      '* { color: red; }',
      '.studio-button { color: red; } stray',
      '/* comment */ .studio-button { color: red; }',
      `.studio-button { padding: ${'1px '.repeat(maximumConnectorStudioStylesheetLength / 4)}; }`,
    ];
    for (const stylesheet of hostileStylesheets) {
      expect(listStudioStylesheetViolations(stylesheet), stylesheet.slice(0, 80)).not.toEqual([]);
    }
  });

  it('declares every allowlisted token in :root', () => {
    const rootDeclarations = rules.filter((rule) => rule.selectors.includes(':root')).flatMap((rule) => rule.declarations);
    const declaredTokens = rootDeclarations.map((declaration) => declaration.property).filter((property) => property.startsWith('--studio-'));
    expect(declaredTokens).toEqual(connectorStudioThemeTokenSources.map((source) => source.studioToken));
  });

  it('styles exactly the studio-* class contract shared with sdk/react', () => {
    const referencedClasses = new Set(rules.flatMap((rule) => rule.selectors).flatMap((selector) => [...selector.matchAll(/\.(studio-[a-z0-9-]+)/g)].map((match) => match[1])));
    expect([...referencedClasses].sort()).toEqual([...sharedConnectorStudioClassNames].sort());
    expect([...listStudioClassesStyledDirectly(rules)].sort()).toEqual([...sharedConnectorStudioClassNames].sort());
  });
});
