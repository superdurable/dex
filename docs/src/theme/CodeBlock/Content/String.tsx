/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useMemo} from 'react';
import CodeBlockString from '@theme-original/CodeBlock/Content/String';
import type {Props} from '@theme/CodeBlock/Content/String';
import {IdeCodeBlockContext, ideCodeMode} from '@site/src/components/ideCode';

// Keeps the fence metastring, which Docusaurus drops from its code block metadata.
export default function IdeCodeBlockString(props: Props): ReactNode {
  const language = props.language ?? props.className?.match(/language-([\w-]+)/)?.[1];
  const info = useMemo(
    () => ({mode: ideCodeMode(language), metastring: props.metastring}),
    [language, props.metastring],
  );
  return (
    <IdeCodeBlockContext.Provider value={info}>
      <CodeBlockString {...props} showLineNumbers={false} />
    </IdeCodeBlockContext.Provider>
  );
}
