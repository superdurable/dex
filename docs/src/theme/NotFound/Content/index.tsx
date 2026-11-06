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
import {retiredApiReferenceRedirectTarget} from '@site/src/apiReferenceRedirect.mjs';

type Props = WrapperProps<typeof NotFoundContentType>;

export default function NotFoundContentWrapper(props: Props): ReactNode {
  useEffect(() => {
    void redirectRetiredApiReferencePage(window.location);
  }, []);
  return <NotFoundContent {...props} />;
}

async function redirectRetiredApiReferencePage(location: Location): Promise<void> {
  const target = await retiredApiReferenceRedirectTarget(location, apiReferenceVersions, isPagePublished);
  if (target !== undefined) {
    location.replace(target);
  }
}

async function isPagePublished(pathname: string): Promise<boolean> {
  try {
    return (await fetch(pathname, {method: 'HEAD'})).ok;
  } catch {
    return false;
  }
}
