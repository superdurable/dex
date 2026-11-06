/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useId, useState} from 'react';
import {useHistory, useLocation} from '@docusaurus/router';
import {translate} from '@docusaurus/Translate';
import {
  useActiveDocContext,
  useActivePlugin,
  useVersions,
} from '@docusaurus/plugin-content-docs/client';
import {useNavbarMobileSidebar} from '@docusaurus/theme-common/internal';
import styles from './ReferenceVersionSelector.module.css';

const referenceVersionSwitchState = 'referenceVersionSwitch';

// Keyboard changes on a closed native select fire change events, so only the form submit navigates.
export default function ReferenceVersionSelector(): ReactNode {
  const {pluginId} = useActivePlugin({failfast: true})!;
  const versions = useVersions(pluginId);
  const {activeVersion, alternateDocVersions} = useActiveDocContext(pluginId);
  const history = useHistory();
  const {hash} = useLocation();
  const mobileSidebar = useNavbarMobileSidebar();
  const selectId = useId();
  const activeVersionName = activeVersion?.name ?? versions[0]!.name;
  const [selectedVersionName, setSelectedVersionName] = useState(activeVersionName);
  useEffect(() => setSelectedVersionName(activeVersionName), [activeVersionName]);
  const isActiveVersionSelected = selectedVersionName === activeVersionName;
  const openSelectedVersion = () => {
    if (isActiveVersionSelected) {
      return;
    }
    const targetVersion = versions.find((version) => version.name === selectedVersionName)!;
    const sameDoc = alternateDocVersions[targetVersion.name];
    const targetDoc = sameDoc ?? targetVersion.docs.find((doc) => doc.id === targetVersion.mainDocId)!;
    history.push({
      pathname: `${targetDoc.path.replace(/\/+$/, '')}/`,
      hash: sameDoc ? hash : '',
      state: {[referenceVersionSwitchState]: true},
    });
    if (mobileSidebar.shown) {
      mobileSidebar.toggle();
    }
  };
  return (
    <form className={styles.selector} data-reference-version-selector={pluginId}
      onSubmit={(event) => {
        event.preventDefault();
        openSelectedVersion();
      }}>
      <label htmlFor={selectId}>{translate({
        id: 'reference.version.label', message: 'Version',
        description: 'Label of the Developer reference sidebar version selector',
      })}</label>
      <div className={styles.controls}>
        <select id={selectId} value={selectedVersionName}
          onChange={(event) => setSelectedVersionName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              openSelectedVersion();
            }
          }}>
          {versions.map((version) => <option key={version.name} value={version.name}>
            {version.isLast ? translate({
              id: 'reference.version.latest', message: '{version} (latest)',
              description: 'Label of the latest Developer reference version',
            }, {version: version.label.replace(/ \(latest\)$/, '')}) : version.label}
          </option>)}
        </select>
        <button type="submit" className="button button--primary" disabled={isActiveVersionSelected}>
          {translate({
            id: 'reference.version.open', message: 'Go',
            description: 'Button that opens the Developer reference version chosen in the sidebar selector',
          })}
        </button>
      </div>
    </form>
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
