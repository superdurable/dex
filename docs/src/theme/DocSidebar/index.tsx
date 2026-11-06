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
import DocSidebar from '@theme-original/DocSidebar';
import {useActivePlugin} from '@docusaurus/plugin-content-docs/client';
import type {Props} from '@theme/DocSidebar';

export default function ReferenceDocSidebar(props: Props): ReactNode {
  const activePlugin = useActivePlugin();
  if (!activePlugin?.pluginId.startsWith('reference-')) {
    return <DocSidebar {...props} />;
  }
  return <DocSidebar {...props} sidebar={[
    {type: 'html', value: '', defaultStyle: false, className: 'reference-version-selector-item'},
    ...props.sidebar,
  ]} />;
}
