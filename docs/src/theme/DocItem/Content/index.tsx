// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import {translate} from '@docusaurus/Translate';
import {useLocation} from '@docusaurus/router';
import useBaseUrl from '@docusaurus/useBaseUrl';
import Content from '@theme-original/DocItem/Content';
import type {Props} from '@theme/DocItem/Content';
import styles from './styles.module.css';

export default function DocItemContent(props: Props): ReactNode {
  const {pathname} = useLocation();
  const baseUrl = useBaseUrl('/');
  const route = pathname.replace(/\/+$/, '');
  const markdownUrl = route === baseUrl.replace(/\/+$/, '') ? `${baseUrl}index.md` : `${route}.md`;
  const label = translate({id: 'docs.markdown.view', message: 'View as Markdown'});
  return (
    <div className={styles.content}>
      <a className={styles.link} href={markdownUrl} target="_blank" rel="noopener" aria-label={label} title={label}>
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
          <path d="M12 3v3M9 3h6M5 11H3v6h2m14-6h2v6h-2" />
          <rect x="5" y="6" width="14" height="15" rx="3" />
          <circle cx="9" cy="12" r="1" /><circle cx="15" cy="12" r="1" />
          <path d="M9 17h6" />
        </svg>
        <span className={styles.label}>{label}</span>
        <span className={styles.tooltip} role="tooltip">{label}</span>
      </a>
      <Content {...props} />
    </div>
  );
}
