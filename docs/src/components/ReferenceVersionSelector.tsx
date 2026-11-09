/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useId} from 'react';
import {useHistory, useLocation} from '@docusaurus/router';
import {translate} from '@docusaurus/Translate';
import {
  useActiveDocContext,
  useVersions,
} from '@docusaurus/plugin-content-docs/client';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import styles from './ReferenceVersionSelector.module.css';

const referenceVersionSwitchState = 'referenceVersionSwitch';

/** Chooses a version of one reference. Outside that reference it shows the latest and opens a version's index. */
export default function ReferenceVersionSelector({pluginId}: {pluginId: string}): ReactNode {
  const versions = useVersions(pluginId);
  const {activeVersion, alternateDocVersions} = useActiveDocContext(pluginId);
  const history = useHistory();
  const {hash} = useLocation();
  const mobileSidebar = useNavbarMobileSidebar();
  const selectId = useId();
  const activeVersionName = activeVersion?.name ?? versions[0]!.name;
  const openVersion = (versionName: string) => {
    if (versionName === activeVersionName) return;
    const targetVersion = versions.find((version) => version.name === versionName)!;
    const sameDoc = alternateDocVersions[targetVersion.name];
    const targetDoc = sameDoc ?? targetVersion.docs.find((doc) => doc.id === targetVersion.mainDocId)!;
    history.push({
      pathname: `${targetDoc.path.replace(/\/+$/, '')}/`,
      hash: sameDoc ? hash : '',
      state: {[referenceVersionSwitchState]: true},
    });
    if (mobileSidebar.shown) mobileSidebar.toggle();
  };
  return (
    <div className={styles.selector} data-reference-version-selector={pluginId}>
      <label htmlFor={selectId}>{translate({
        id: 'reference.version.label', message: 'Version',
        description: 'Label of the Developer reference version selector',
      })}</label>
      <select id={selectId} value={activeVersionName}
        onChange={(event) => openVersion(event.target.value)}>
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

/** Drops a carried-over anchor that the page opened by the version selector does not have. */
export function useDropMissingReferenceVersionAnchor(): void {
  const history = useHistory();
  const location = useLocation<Record<string, unknown> | undefined>();
  useEffect(() => {
    if (!location.state?.[referenceVersionSwitchState] || !location.hash) {
      return;
    }
    if (!document.getElementById(decodeURIComponent(location.hash.slice(1)))) {
      history.replace({pathname: location.pathname, search: location.search});
    }
  }, [history, location]);
}
