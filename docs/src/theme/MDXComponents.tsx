// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ComponentProps, type ReactNode, useContext} from 'react';
import MDXComponents from '@theme-original/MDXComponents';
import MDXHeading from '@theme/MDXComponents/Heading';
import DocMarkdownLink, {DocMarkdownLinkScope} from '@site/src/components/DocMarkdownLink';
import DexSkillHostTabs from '@site/src/components/DexSkillHostTabs';
import SdkTabs, {SdkSnippet} from '@site/src/components/SdkTabs';
import ScreenshotPlaceholder from '@site/src/components/ScreenshotPlaceholder';
import DocsFlowDefinitionGraph from '@site/src/components/DocsFlowDefinitionGraph';
import DocsSection from '@site/src/components/DocsSection';

function DocTitleHeading(props: ComponentProps<'h1'>): ReactNode {
  const isDocTitle = useContext(DocMarkdownLinkScope);
  return (
    <>
      <MDXHeading as="h1" {...props} />
      {isDocTitle ? <DocMarkdownLink /> : null}
    </>
  );
}

export default {
  ...MDXComponents,
  h1: DocTitleHeading,
  DexSkillHostTabs,
  SdkTabs,
  SdkSnippet,
  ScreenshotPlaceholder,
  DocsFlowDefinitionGraph,
  DocsSection,
};
