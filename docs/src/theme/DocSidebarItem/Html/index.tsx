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
import DocSidebarItemHtml from '@theme-original/DocSidebarItem/Html';
import ReferenceVersionSelector from '@site/src/components/ReferenceVersionSelector';
import type {Props} from '@theme/DocSidebarItem/Html';

export default function ReferenceSidebarHtml(props: Props): ReactNode {
  if (props.item.className === 'reference-version-selector-item') {
    return <li className={props.item.className}><ReferenceVersionSelector /></li>;
  }
  return <DocSidebarItemHtml {...props} />;
}
