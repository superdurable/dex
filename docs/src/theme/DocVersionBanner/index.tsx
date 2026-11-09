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
import DocVersionBanner from '@theme-original/DocVersionBanner';
import Link from '@docusaurus/Link';
import Translate from '@docusaurus/Translate';
import {
  useActivePlugin,
  useActiveDocContext,
  useVersions,
} from '@docusaurus/plugin-content-docs/client';
import {useDropMissingReferenceVersionAnchor} from '@site/src/components/ReferenceVersionSelector';
import type {Props} from '@theme/DocVersionBanner';

function ReferenceVersionBanner({pluginId, className}: Props & {pluginId: string}): ReactNode {
  const versions = useVersions(pluginId);
  const {activeVersion, alternateDocVersions} = useActiveDocContext(pluginId);
  const latestVersion = versions.find((version) => version.isLast)!;
  useDropMissingReferenceVersionAnchor();
  if (!activeVersion) return null;
  const latestDoc = alternateDocVersions[latestVersion.name] ??
    latestVersion.docs.find((doc) => doc.id === latestVersion.mainDocId)!;
  return <>
    {!activeVersion.isLast && <div className={`alert alert--warning margin-bottom--md ${className ?? ''}`}
      data-reference-version-banner role="note">
      <Translate id="reference.version.banner"
        description="Notice on an older Developer reference version"
        values={{version: activeVersion.label, latest: <Link to={latestDoc.path}>
          {latestVersion.label.replace(/ \(latest\)$/, '')}
        </Link>}}>
        {'You are viewing {version}. The latest is {latest}.'}
      </Translate>
    </div>}
  </>;
}

export default function ReferenceDocVersionBanner(props: Props): ReactNode {
  const activePlugin = useActivePlugin();
  return activePlugin?.pluginId.startsWith('reference-')
    ? <ReferenceVersionBanner {...props} pluginId={activePlugin.pluginId} />
    : <DocVersionBanner {...props} />;
}
