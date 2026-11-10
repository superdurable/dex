/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ComponentProps, type ReactNode, useEffect, useState} from 'react';
import Link from '@docusaurus/Link';
import MDXA from '@theme-original/MDXComponents/A';
import {
  DEFAULT_SDK,
  type SdkLanguage,
  readSdkPreference,
  subscribeSdkPreference,
} from '@site/src/components/sdkPreference';

const apiReferenceLinkPrefix = 'pathname:///api/';

// Docusaurus copies files linked from Markdown, such as PDFs, here.
const markdownAssetFilePath = /\/assets\/files\/[^/]+\.[a-z0-9]+$/i;

// Every SDK reference carries these pages with the same anchors; docs link to the default SDK's copy.
const sharedSdkReferencePages = [
  'flow',
  'start-flow-options',
  'step',
  'step-options',
  'step-decisions',
  'waits',
  'attribute',
  'rpc',
  'channel',
  'stream',
  'timer',
  'subflow',
  'client',
  'failure-and-retries',
  'workers',
];

const defaultSdkReferenceLink = new RegExp(
  `^/reference/${DEFAULT_SDK}/(${sharedSdkReferencePages.join('|')})(?=[/#?]|$)`,
);

const defaultSdkLabel = 'Python';

function usePreferredSdk(): SdkLanguage {
  // Server rendering and hydration use the default SDK so the markup matches.
  const [preferredSdk, setPreferredSdk] = useState<SdkLanguage>(DEFAULT_SDK);
  useEffect(() => {
    setPreferredSdk(readSdkPreference());
    return subscribeSdkPreference(setPreferredSdk);
  }, []);
  return preferredSdk;
}

function PreferredSdkReferenceLink(props: ComponentProps<'a'>): ReactNode {
  const preferredSdk = usePreferredSdk();
  const href = props.href!.replace(
    defaultSdkReferenceLink,
    (_match, page: string) => `/reference/${preferredSdk}/${page}`,
  );
  return <MDXA {...props} href={href} />;
}

// API HTML lives at the site root: skip the locale base URL and stay in the same tab.
export default function MDXAWrapper(props: ComponentProps<'a'>): ReactNode {
  if (props.href?.startsWith(apiReferenceLinkPrefix)) {
    return <Link {...props} autoAddBaseUrl={false} target="_self" />;
  }
  // Link would append the trailing slash of page routes and break the file URL.
  if (props.href && markdownAssetFilePath.test(props.href)) {
    return <a {...props} />;
  }
  // A link named after the SDK, such as "Python", promises that SDK and keeps it.
  if (props.href && defaultSdkReferenceLink.test(props.href) && props.children !== defaultSdkLabel) {
    return <PreferredSdkReferenceLink {...props} />;
  }
  return <MDXA {...props} />;
}
