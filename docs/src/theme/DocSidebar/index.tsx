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
import type {Props} from '@theme/DocSidebar';

export default function ReferenceDocSidebar(props: Props): ReactNode {
  return <DocSidebar {...props} />;
}
