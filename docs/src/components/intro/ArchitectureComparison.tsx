// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import styles from './ComparedToDatabase.module.css';
import {
  orderProcessCopy,
  type OrderProcessDesign,
  type OrderProcessPart,
} from './orderProcessParts';

export function PartChips({parts, isDex}: {parts: OrderProcessPart[]; isDex?: boolean}): ReactNode {
  return (
    <ul className={styles.chips}>
      {parts.map(({label, icon: Icon}) => (
        <li key={label} className={`${styles.chip}${isDex ? ` ${styles.chipDex}` : ''}`}>
          <Icon className={styles.icon} aria-hidden="true" />
          {label}
        </li>
      ))}
    </ul>
  );
}

function DesignColumn({design, isDex}: {design: OrderProcessDesign; isDex: boolean}): ReactNode {
  return (
    <div className={`${styles.column}${isDex ? ` ${styles.columnDex}` : ''}`}>
      <div>
        <p className={styles.designName}>{design.name}</p>
        <p className={styles.count}>{design.subtitle}</p>
      </div>
      {design.groups.map((group) => (
        <div key={group.title} className={styles.group}>
          <span className={styles.groupTitle}>{group.title}</span>
          <PartChips parts={group.parts} isDex={isDex} />
        </div>
      ))}
      <div className={styles.platform}>
        <p className={styles.platformTitle}>{design.platformTitle}</p>
        {design.platformParts.length > 0 ? (
          <div className={styles.platformChips}>
            <PartChips parts={design.platformParts} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

/**
 * Two columns that list every part of the full order process in each design.
 * The Markdown export uses markdownDescription in place of the columns.
 */
export default function ArchitectureComparison(_props: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = orderProcessCopy(i18n.currentLocale);
  return (
    <section className={`${styles.palette} ${styles.glance}`} aria-label={copy.ariaLabel}>
      <div className={styles.glanceColumns}>
        <DesignColumn design={copy.queues} isDex={false} />
        <DesignColumn design={copy.dex} isDex />
      </div>
    </section>
  );
}
