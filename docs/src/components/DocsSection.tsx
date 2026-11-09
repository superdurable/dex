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
import {ArrowRight} from 'lucide-react';
import {docsSectionIcons, type DocsSectionKey} from './docsSections';

export type DocsSectionProps = {
  section: DocsSectionKey;
  to: string;
  title: string;
  children: ReactNode;
};

/** One entry of the docs home page: a section icon, its name, and what readers find there. */
export default function DocsSection({section, to, title, children}: DocsSectionProps): ReactNode {
  const Icon = docsSectionIcons[section];
  return (
    <Link className="docs-section-entry" data-docs-section={section} to={to}>
      <span className="docs-section-entry__icon" aria-hidden="true">
        <Icon size={22} strokeWidth={2} />
      </span>
      <span className="docs-section-entry__text">
        <strong>
          {title}
          <ArrowRight className="docs-section-entry__arrow" size={16} aria-hidden="true" />
        </strong>
        <span>{children}</span>
      </span>
    </Link>
  );
}
