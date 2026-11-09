/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useContext} from 'react';
import clsx from 'clsx';
import {useCodeBlockContext} from '@docusaurus/theme-common/internal';
import OriginalLayout from '@theme-original/CodeBlock/Layout';
import Container from '@theme/CodeBlock/Container';
import Content from '@theme/CodeBlock/Content';
import CopyButton from '@theme/CodeBlock/Buttons/CopyButton';
import type {Props} from '@theme/CodeBlock/Layout';
import {IdeExampleFileContext, ideLanguageLabel, useIdeCodeBlockInfo} from '@site/src/components/ideCode';

/** Frames code like an editor window and commands like a terminal, as on the superdurable.io home page. */
export default function IdeCodeBlockLayout(props: Props): ReactNode {
  const {metadata} = useCodeBlockContext();
  const {mode} = useIdeCodeBlockInfo();
  const exampleFile = useContext(IdeExampleFileContext);
  if (mode === 'plain') {
    return <OriginalLayout {...props} />;
  }
  if (mode === 'terminal') {
    return (
      <Container as="div" className={clsx(props.className, metadata.className, 'ide-code', 'ide-code--terminal')}>
        <Content />
        <CopyButton className="ide-code__copy ide-code__copy--floating" />
      </Container>
    );
  }
  const tabName = metadata.title || exampleFile?.split('/').pop();
  return (
    <Container as="div" className={clsx(props.className, metadata.className, 'ide-code', 'ide-code--editor')}>
      <div className="ide-code__bar">
        <span className="ide-code__dots" aria-hidden="true"><i /><i /><i /></span>
        {tabName ? <span className="ide-code__tab">{tabName}</span> : null}
        <span className="ide-code__language">{ideLanguageLabel(metadata.language)}</span>
        <CopyButton className="ide-code__copy" />
      </div>
      <Content />
    </Container>
  );
}
