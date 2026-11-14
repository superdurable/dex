// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const designCss = readFileSync('app/v1-design.css', 'utf8');
const frameCss = readFileSync('app/components/frame/frame.css', 'utf8');

describe('Execution graph event panel layout', () => {
  it('measures the graph panel independently of the viewport, Overview, and Timeline', () => {
    const containerRules = [...designCss.matchAll(/([^{}]+)\{\s*container: graph-event-panel \/ inline-size;\s*\}/g)];
    expect(containerRules).toHaveLength(1);
    expect(containerRules[0][1].trim()).toBe(
      '.v1-page .run-content:not(.run-content--overview):not(.run-content--timeline) .sidebar-stack',
    );
  });

  it('stacks Attribute and Context rows only in narrow graph panels, preserving message controls', () => {
    const narrowPanelRules = designCss.match(
      /@container graph-event-panel \(max-width: 480px\) \{((?:[^{}]|\{[^{}]*\})*)\}/,
    )?.[1];
    expect(narrowPanelRules).toBeDefined();
    const stackedSelectors = [...narrowPanelRules!.matchAll(/([^{}]+)\{\s*grid-template-columns: minmax\(0, 1fr\);\s*\}/g)]
      .map((match) => match[1].trim());
    expect(stackedSelectors).toEqual([
      '.v1-page .run-sidebar .compact-row:not(.compact-row--message)',
      '.v1-page .run-sidebar .semantic-fields-compact',
    ]);
    expect(designCss).toMatch(/\.compact-row \{[^}]*grid-template-columns: minmax\(96px, 200px\) minmax\(0, 1fr\);/);
  });
});

describe('motion', () => {
  const reducedMotionBlocks = [...frameCss.matchAll(/@media \(prefers-reduced-motion: reduce\) \{((?:[^{}]|\{[^{}]*\})*)\}/g)]
    .map((match) => match[1]);

  it('turns off the switch indicator, the picture fade and control transitions under reduced motion', () => {
    const reduced = reducedMotionBlocks.join('\n');
    expect(frameCss).toMatch(/\.events-view-switch-indicator \{[^}]*transition: transform 180ms/);
    expect(frameCss).toMatch(/\.events-picture-view \{[^}]*animation: events-picture-in 160ms/);
    expect(reduced).toMatch(/\.events-view-switch-indicator,/);
    expect(reduced).toMatch(/\.events-picture-view,/);
    expect(reduced).toMatch(/\.pager-button,/);
    expect(reduced).toMatch(/\.run-io-button,[^{}]*\{\s*transition: none !important;\s*\}/);
    expect(reduced).toMatch(/\{\s*animation: none;\s*\}/);
  });

  it('never eases outlines, so focus rings stay instant', () => {
    expect(frameCss).not.toMatch(/transition:[^;]*\b(all|outline)\b/);
  });
});
