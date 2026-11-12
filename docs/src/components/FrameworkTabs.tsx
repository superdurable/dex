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
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';
import {agentFrameworks} from './agentFrameworks';

/** Agent framework tabs. Every group on a page shares one choice, kept in ?framework= and in the browser. */
export default function FrameworkTabs({children}: {children: ReactNode}): ReactNode {
  return (
    <Tabs groupId="agent-framework" queryString="framework" className="framework-tabs">
      {React.Children.toArray(children)
        .filter(React.isValidElement)
        .map((child) => {
          const framework = (child.props as {framework: string}).framework;
          const label = agentFrameworks.find((entry) => entry.id === framework)?.label ?? framework;
          return (
            <TabItem key={framework} value={framework} label={label}>
              {(child.props as {children: ReactNode}).children}
            </TabItem>
          );
        })}
    </Tabs>
  );
}

/** One framework's content inside FrameworkTabs. FrameworkTabs renders it. */
export function FrameworkTab(_props: {framework: string; children: ReactNode}): ReactNode {
  return null;
}
