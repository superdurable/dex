// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import {ThemeClassNames} from '@docusaurus/theme-common';
import {useDoc} from '@docusaurus/plugin-content-docs/client';
import Heading from '@theme/Heading';
import MDXContent from '@theme/MDXContent';
import type {Props} from '@theme/DocItem/Content';
import DocMarkdownLink, {DocMarkdownLinkScope} from '@site/src/components/DocMarkdownLink';
import styles from './styles.module.css';

// Mirrors the original DocItem/Content and places the Markdown link right after the page h1.
export default function DocItemContent({children}: Props): ReactNode {
  const {metadata, frontMatter, contentTitle} = useDoc();
  const hasContentTitle = typeof contentTitle !== 'undefined';
  const syntheticTitle = !frontMatter.hide_title && !hasContentTitle ? metadata.title : null;
  return (
    <div className={styles.content}>
      <div className={`${ThemeClassNames.docs.docMarkdown} markdown`}>
        {syntheticTitle ? (
          <header>
            <Heading as="h1">{syntheticTitle}</Heading>
            <DocMarkdownLink />
          </header>
        ) : null}
        {!syntheticTitle && !hasContentTitle ? <DocMarkdownLink /> : null}
        <DocMarkdownLinkScope.Provider value={hasContentTitle}>
          <MDXContent>{children}</MDXContent>
        </DocMarkdownLinkScope.Provider>
      </div>
    </div>
  );
}
