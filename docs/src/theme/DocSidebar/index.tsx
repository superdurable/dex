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
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import englishSidebar from '@site/src/data/docs-sidebar-en.json';
import chineseSidebar from '@site/src/data/docs-sidebar-zh-Hans.json';
import {useActivePlugin} from '@docusaurus/plugin-content-docs/client';
import type {Props} from '@theme/DocSidebar';

export default function ReferenceDocSidebar(props: Props): ReactNode {
  const activePlugin = useActivePlugin();
  const {i18n} = useDocusaurusContext();
  if (!activePlugin?.pluginId.startsWith('reference-')) {
    return <DocSidebar {...props} />;
  }
  const sidebar = i18n.currentLocale === 'zh-Hans' ? chineseSidebar : englishSidebar;
  return <DocSidebar {...props} sidebar={sidebar as Props['sidebar']} />;
}
