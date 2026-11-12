// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import {ThemeClassNames} from '@docusaurus/theme-common';
import {useActivePlugin, useDoc, useSidebarBreadcrumbs} from '@docusaurus/plugin-content-docs/client';
import Heading from '@theme/Heading';
import MDXContent from '@theme/MDXContent';
import type {Props} from '@theme/DocItem/Content';
import DocMarkdownLink, {DocMarkdownLinkScope} from '@site/src/components/DocMarkdownLink';
import {useCurrentDocsSection} from '@site/src/components/docsSections';
import styles from './styles.module.css';

const referenceComponentLabels: Record<string, string> = {
  server: 'Server reference',
  cli: 'CLI reference',
  web: 'Dex Web reference',
  go: 'Go SDK reference',
  python: 'Python SDK reference',
  java: 'Java SDK reference',
  typescript: 'TypeScript SDK reference',
  rust: 'Rust SDK reference',
  agents: 'Agents reference',
};

/**
 * The section path of the page, such as "Go SDK reference › Primitives". The local search indexer reads the first
 * navbar__link--active element as a page's breadcrumb, so search results can tell same-titled pages apart.
 */
function useSearchPath(title: string): string {
  const activePlugin = useActivePlugin();
  const breadcrumbs = useSidebarBreadcrumbs() ?? [];
  const pluginId = activePlugin?.pluginId ?? '';
  const parts = breadcrumbs.map((item) => item.label).filter((label) => label !== title && label !== 'Overview');
  if (pluginId.startsWith('reference-')) {
    const component = pluginId.slice('reference-'.length);
    parts.unshift(referenceComponentLabels[component] ?? 'Developer Reference');
  }
  return parts.join(' › ');
}

// Mirrors the original DocItem/Content and places the Markdown link right after the page h1.
export default function DocItemContent({children}: Props): ReactNode {
  const {metadata, frontMatter, contentTitle} = useDoc();
  const hasContentTitle = typeof contentTitle !== 'undefined';
  const syntheticTitle = !frontMatter.hide_title && !hasContentTitle ? metadata.title : null;
  const section = useCurrentDocsSection();
  const searchPath = useSearchPath(metadata.title);
  return (
    <div className={styles.content} data-docs-section={section}>
      {searchPath ? (
        <span className="navbar__link--active" hidden aria-hidden="true" data-search-path="">
          {searchPath}
        </span>
      ) : null}
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
