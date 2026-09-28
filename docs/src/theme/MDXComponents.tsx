// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React from 'react';
import MDXComponents from '@theme-original/MDXComponents';
import DexSkillHostTabs from '@site/src/components/DexSkillHostTabs';
import SdkTabs, {SdkSnippet} from '@site/src/components/SdkTabs';
import ScreenshotPlaceholder from '@site/src/components/ScreenshotPlaceholder';
import DocsFlowDefinitionGraph from '@site/src/components/DocsFlowDefinitionGraph';

export default {
  ...MDXComponents,
  DexSkillHostTabs,
  SdkTabs,
  SdkSnippet,
  ScreenshotPlaceholder,
  DocsFlowDefinitionGraph,
};
