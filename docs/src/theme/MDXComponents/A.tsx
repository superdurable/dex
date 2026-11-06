/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ComponentProps, type ReactNode} from 'react';
import Link from '@docusaurus/Link';
import MDXA from '@theme-original/MDXComponents/A';

const apiReferenceLinkPrefix = 'pathname:///api/';

// API HTML lives at the site root: skip the locale base URL and stay in the same tab.
export default function MDXAWrapper(props: ComponentProps<'a'>): ReactNode {
  if (props.href?.startsWith(apiReferenceLinkPrefix)) {
    return <Link {...props} autoAddBaseUrl={false} target="_self" />;
  }
  return <MDXA {...props} />;
}
