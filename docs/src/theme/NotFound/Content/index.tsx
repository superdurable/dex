/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect} from 'react';
import NotFoundContent from '@theme-original/NotFound/Content';
import type NotFoundContentType from '@theme/NotFound/Content';
import type {WrapperProps} from '@docusaurus/types';
import apiReferenceVersions from '@site/api-reference-versions.json';

type Props = WrapperProps<typeof NotFoundContentType>;

const apiReferencePagePattern = /^\/api\/([^/]+)\/(\d+\.\d+)\/(.*)$/;
const releasesByLanguage: Record<string, string[]> = apiReferenceVersions;

export default function NotFoundContentWrapper(props: Props): ReactNode {
  useEffect(() => {
    void redirectRetiredApiReferencePage(window.location);
  }, []);
  return <NotFoundContent {...props} />;
}

// A retired version's pages are deleted on deploy, so they reach this page and move to the latest release.
async function redirectRetiredApiReferencePage(location: Location): Promise<void> {
  const match = apiReferencePagePattern.exec(location.pathname);
  const releases = match ? releasesByLanguage[match[1]] : undefined;
  if (!match || !releases) {
    return;
  }
  const [, language, minor, pagePath] = match;
  if (releases.some((release) => release.startsWith(`${minor}.`))) {
    return;
  }
  const latestPagePath = `/api/${language}/latest/${pagePath}`;
  location.replace(
    (await isPagePublished(latestPagePath))
      ? `${latestPagePath}${location.search}${location.hash}`
      : `/reference/${language}/`,
  );
}

async function isPagePublished(pathname: string): Promise<boolean> {
  try {
    return (await fetch(pathname, {method: 'HEAD'})).ok;
  } catch {
    return false;
  }
}
