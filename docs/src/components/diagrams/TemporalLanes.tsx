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
import type {LucideIcon} from 'lucide-react';

import {Arrow, DiagramIcon, Eyebrow} from '../conceptDiagram/ConceptDiagram';
import styles from './TemporalComparison.module.css';

export type LaneTone = 'external' | 'accent';

/** One side of a Dex vs. Temporal figure: blue for Temporal, green for Dex. */
export function TemporalLane({
  name,
  icon,
  tone,
  children,
}: {
  name: string;
  icon: LucideIcon;
  tone: LaneTone;
  children: ReactNode;
}): ReactNode {
  return (
    <div className={`${styles.lane} ${tone === 'external' ? styles.external : styles.dex}`}>
      <Eyebrow icon={icon}>{name}</Eyebrow>
      {children}
    </div>
  );
}

export function TemporalLaneBox({icon, title, detail}: {icon: LucideIcon; title: string; detail?: string}): ReactNode {
  return (
    <div className={styles.box}>
      <DiagramIcon icon={icon} size={15} />
      <span>
        <b>{title}</b>
        {detail ? <small>{detail}</small> : null}
      </span>
    </div>
  );
}

export function TemporalLaneRow({children}: {children: ReactNode}): ReactNode {
  return <div className={styles.row}>{children}</div>;
}

export function TemporalLaneArrow({tone, label}: {tone: LaneTone; label?: string}): ReactNode {
  return <Arrow direction="down" tone={tone} isFlowing label={label} />;
}

export function TemporalLaneNote({icon, children}: {icon: LucideIcon; children: ReactNode}): ReactNode {
  return (
    <span className={styles.note}>
      <DiagramIcon icon={icon} size={13} />
      {children}
    </span>
  );
}

export function TemporalLaneLayout({children}: {children: ReactNode}): ReactNode {
  return <div className={styles.layout}>{children}</div>;
}
