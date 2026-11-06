/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useId} from 'react';
import {useHistory} from '@docusaurus/router';
import {translate} from '@docusaurus/Translate';
import {
  useActiveDocContext,
  useActivePlugin,
  useVersions,
} from '@docusaurus/plugin-content-docs/client';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import styles from './ReferenceVersionSelector.module.css';

export default function ReferenceVersionSelector(): ReactNode {
  const {pluginId} = useActivePlugin({failfast: true})!;
  const versions = useVersions(pluginId);
  const {activeVersion, alternateDocVersions} = useActiveDocContext(pluginId);
  const history = useHistory();
  const mobileSidebar = useNavbarMobileSidebar();
  const selectId = useId();
  return (
    <div className={styles.selector} data-reference-version-selector={pluginId}>
      <label htmlFor={selectId}>{translate({
        id: 'reference.version.label', message: 'Version',
        description: 'Label of the Developer reference sidebar version selector',
      })}</label>
      <select id={selectId} value={activeVersion?.name} onChange={(event) => {
        const targetVersion = versions.find((version) => version.name === event.target.value)!;
        const targetDoc = alternateDocVersions[targetVersion.name] ??
          targetVersion.docs.find((doc) => doc.id === targetVersion.mainDocId)!;
        history.push(`${targetDoc.path.replace(/\/+$/, '')}/`);
        if (mobileSidebar.shown) {
          mobileSidebar.toggle();
        }
      }}>
        {versions.map((version) => <option key={version.name} value={version.name}>
          {version.isLast ? translate({
            id: 'reference.version.latest', message: '{version} (latest)',
            description: 'Label of the latest Developer reference version',
          }, {version: version.label.replace(/ \(latest\)$/, '')}) : version.label}
        </option>)}
      </select>
    </div>
  );
}
