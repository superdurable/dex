// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {Pencil, Plus} from 'lucide-react';

import styles from './ComparedToDatabase.module.css';
import {countNewParts, orderProcessCopy, type RequirementChangeSide} from './orderProcessParts';

function ChangeCell({label, side, isDex}: {label: string; side: RequirementChangeSide; isDex: boolean}): ReactNode {
  return (
    <div className={`${styles.cell}${isDex ? ` ${styles.cellDex}` : ''}`}>
      <span className={styles.cellLabel}>{label}</span>
      {side.newParts.length > 0 ? (
        <ul className={styles.chips}>
          {side.newParts.map(({label: partLabel, icon: Icon}) => (
            <li key={partLabel} className={`${styles.chip}${isDex ? ` ${styles.chipDex}` : ''}`}>
              <Plus className={`${styles.icon} ${styles.newMark}`} aria-hidden="true" />
              <Icon className={styles.icon} aria-hidden="true" />
              {partLabel}
            </li>
          ))}
        </ul>
      ) : null}
      {side.changes.map((change) => (
        <p key={change} className={styles.changeNote}>
          <Pencil className={styles.icon} aria-hidden="true" />
          <span>{change}</span>
        </p>
      ))}
    </div>
  );
}

/**
 * Rows that show what each design adds for each new requirement of the order process.
 * The Markdown export uses markdownDescription in place of the rows.
 */
export default function RequirementChangeComparison(_props: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = orderProcessCopy(i18n.currentLocale);
  const changes = copy.requirementChanges;
  return (
    <section className={`${styles.palette} ${styles.changes}`} aria-label={copy.requirementsAriaLabel}>
      <div className={styles.changeGrid}>
        <div className={styles.changeHeader} aria-hidden="true">
          <span>{copy.requirementColumn}</span>
          <span>{copy.queuesColumn}</span>
          <span>{copy.dexColumn}</span>
        </div>
        {changes.map((change) => {
          const Icon = change.icon;
          return (
            <div key={change.title} className={styles.changeRow}>
              <div className={`${styles.cell} ${styles.cellRequirement}`}>
                <span className={styles.requirementIcon}>
                  <Icon className={styles.icon} aria-hidden="true" />
                </span>
                <span>{change.title}</span>
              </div>
              <ChangeCell label={copy.queuesColumn} side={change.queues} isDex={false} />
              <ChangeCell label={copy.dexColumn} side={change.dex} isDex />
            </div>
          );
        })}
        <div className={styles.changeTotals}>
          <span aria-hidden="true" />
          <span className={styles.totalQueues}>
            <span className={styles.totalNumber}>{countNewParts(changes, 'queues')}</span>
            {copy.newPartsTotal}
            <span className={styles.totalDesign}> ({copy.queues.name})</span>
          </span>
          <span className={styles.totalDex}>
            <span className={styles.totalNumber}>{countNewParts(changes, 'dex')}</span>
            {copy.newPartsTotal}
            <span className={styles.totalDesign}> ({copy.dex.name})</span>
          </span>
        </div>
      </div>
    </section>
  );
}
