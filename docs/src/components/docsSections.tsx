/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import type {LucideIcon} from 'lucide-react';
import {
  BookMarked,
  Boxes,
  Code2,
  Layers,
  Puzzle,
  Rocket,
  Shapes,
  Sparkles,
  Workflow,
} from 'lucide-react';
import {useActivePlugin, useSidebarBreadcrumbs} from '@docusaurus/plugin-content-docs/client';

/** A top-level docs section. Each has its own icon and accent color, defined in refresh.css. */
export type DocsSectionKey =
  | 'intro'
  | 'concepts'
  | 'components'
  | 'agent-frameworks'
  | 'agent-skills'
  | 'design-patterns'
  | 'product-examples'
  | 'reference'
  | 'glossary';

export const docsSectionIcons: Record<DocsSectionKey, LucideIcon> = {
  intro: Rocket,
  concepts: Shapes,
  components: Boxes,
  'agent-frameworks': Layers,
  'agent-skills': Sparkles,
  'design-patterns': Puzzle,
  'product-examples': Workflow,
  reference: Code2,
  glossary: BookMarked,
};

export function isDocsSectionKey(value: unknown): value is DocsSectionKey {
  return typeof value === 'string' && Object.hasOwn(docsSectionIcons, value);
}

/** Returns the section of the current docs page, or undefined outside a section. */
export function useCurrentDocsSection(): DocsSectionKey | undefined {
  const activePlugin = useActivePlugin();
  const breadcrumbs = useSidebarBreadcrumbs();
  if (activePlugin?.pluginId.startsWith('reference-')) {
    return 'reference';
  }
  const section = breadcrumbs?.[0]?.customProps?.section;
  return isDocsSectionKey(section) ? section : undefined;
}
