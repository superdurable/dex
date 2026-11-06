// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {createContext, type ReactNode} from 'react';
import {translate} from '@docusaurus/Translate';
import {useLocation} from '@docusaurus/router';
import useBaseUrl from '@docusaurus/useBaseUrl';
import {markdownPath} from '@site/src/markdownPath.mjs';
import styles from './DocMarkdownLink.module.css';

// True inside docs page content, where the title h1 gets the Markdown link.
export const DocMarkdownLinkScope = createContext(false);

export default function DocMarkdownLink(): ReactNode {
  const {pathname} = useLocation();
  const baseUrl = useBaseUrl('/');
  const label = translate({
    id: 'docs.markdown.view',
    message: 'View as Markdown',
    description: 'The visible label of the link that opens the Markdown export of a docs page',
  });
  const accessibleLabel = translate({
    id: 'docs.markdown.view.newTab',
    message: 'View as Markdown (opens in a new tab)',
    description: 'The accessible name of the link that opens the Markdown export of a docs page in a new tab',
  });
  return (
    <a className={styles.link} href={markdownPath(pathname, baseUrl)} target="_blank" rel="noopener" aria-label={accessibleLabel}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
        <path d="M12 3v3M9 3h6M5 11H3v6h2m14-6h2v6h-2" />
        <rect x="5" y="6" width="14" height="15" rx="3" />
        <circle cx="9" cy="12" r="1" /><circle cx="15" cy="12" r="1" />
        <path d="M9 17h6" />
      </svg>
      <span className={styles.label}>{label}</span>
      <span className={styles.tooltip} aria-hidden="true">{label}</span>
    </a>
  );
}
