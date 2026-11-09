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
