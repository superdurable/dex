/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode} from 'react';
import Link from '@docusaurus/Link';
import type {LucideIcon} from 'lucide-react';
import {ArrowRight, Bot, Cloud, Code2, Gauge, GitCompare, Layers, Monitor, Plug, RefreshCw, Rocket, Scale, Server, TriangleAlert, Wrench} from 'lucide-react';
import {docsSectionIcons, type DocsSectionKey} from './docsSections';
import {frameworkLogoMarkup, type FrameworkLogoKey} from './frameworkLogos';

// Icons a card can use instead of its section's icon; the colour still comes from the section.
const cardIcons = {
  bot: Bot,
  cloud: Cloud,
  code: Code2,
  compare: GitCompare,
  gauge: Gauge,
  layers: Layers,
  monitor: Monitor,
  plug: Plug,
  refresh: RefreshCw,
  rocket: Rocket,
  scale: Scale,
  server: Server,
  warning: TriangleAlert,
  wrench: Wrench,
} satisfies Record<string, LucideIcon>;

export type DocsSectionProps = {
  section: DocsSectionKey;
  icon?: keyof typeof cardIcons;
  /** An agent framework's logo, shown instead of an icon. */
  logo?: FrameworkLogoKey;
  /** A short note under the title, such as how long the path takes. */
  meta?: string;
  to: string;
  title: string;
  children: ReactNode;
};

/** One entry of the docs home page: a section icon, its name, and what readers find there. */
export default function DocsSection({section, icon, logo, meta, to, title, children}: DocsSectionProps): ReactNode {
  const Icon = icon ? cardIcons[icon] : docsSectionIcons[section];
  return (
    <Link className="docs-section-entry" data-docs-section={section} to={to}>
      <span className={logo ? 'docs-section-entry__icon docs-section-entry__icon--logo' : 'docs-section-entry__icon'} aria-hidden="true">
        {logo ? (
          <svg viewBox="0 0 24 24" width={22} height={22} fill="currentColor" fillRule="evenodd"
            dangerouslySetInnerHTML={{__html: frameworkLogoMarkup[logo]}} />
        ) : (
          <Icon size={22} strokeWidth={2} />
        )}
      </span>
      <span className="docs-section-entry__text">
        <strong>
          {title}
          <ArrowRight className="docs-section-entry__arrow" size={16} aria-hidden="true" />
        </strong>
        {meta ? <span className="docs-section-entry__meta">{meta}</span> : null}
        <span>{children}</span>
      </span>
    </Link>
  );
}
