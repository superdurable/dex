/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useState} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import {useLocation} from '@docusaurus/router';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import SearchPage from '@theme-original/SearchPage';
// @ts-expect-error The search plugin generates this module at build time.
import {searchContextByPaths} from '@generated/@easyops-cn/docusaurus-search-local/default/generated.js';
import {readSdkPreference} from '@site/src/components/sdkPreference';
import styles from './styles.module.css';

type SearchContext = {path: string; label: string | Record<string, string>};

// Guides are the index without a context. The reader's SDK reference comes right after them.
function orderedContexts(preferred: string): SearchContext[] {
  const contexts = (searchContextByPaths ?? []) as SearchContext[];
  const preferredPath = `reference/${preferred}`;
  return [...contexts.filter((item) => item.path === preferredPath), ...contexts.filter((item) => item.path !== preferredPath)];
}

function contextLabel(context: SearchContext, locale: string): string {
  return typeof context.label === 'string' ? context.label : context.label[locale] ?? context.label.en ?? context.path;
}

export default function SearchPageWithScopes(props: Record<string, unknown>): ReactNode {
  const location = useLocation();
  const {i18n} = useDocusaurusContext();
  const [preferred, setPreferred] = useState('python');
  useEffect(() => setPreferred(readSdkPreference()), []);
  const params = new URLSearchParams(location.search);
  const current = params.get('ctx') ?? '';
  const linkFor = (path: string) => {
    const next = new URLSearchParams(location.search);
    if (path) next.set('ctx', path);
    else next.delete('ctx');
    return `${location.pathname}?${next.toString()}`;
  };
  const isChinese = i18n.currentLocale === 'zh-Hans';
  const scopes = [{path: '', label: isChinese ? '指南' : 'Guides'},
    ...orderedContexts(preferred).map((item) => ({path: item.path, label: contextLabel(item, i18n.currentLocale)}))];
  return (
    <div className={styles.scoped}>
      <nav className={clsx('container', styles.scopes)} aria-label={isChinese ? '搜索范围' : 'Search in'}>
        <span className={styles.scopesLabel}>{isChinese ? '搜索范围' : 'Search in'}</span>
        {scopes.map((scope) => (
          <Link
            key={scope.path || 'guides'}
            to={linkFor(scope.path)}
            className={clsx(styles.scope, scope.path === current && styles.scopeActive)}
            aria-current={scope.path === current ? 'page' : undefined}>
            {scope.label}
          </Link>
        ))}
      </nav>
      <SearchPage {...props} />
    </div>
  );
}
