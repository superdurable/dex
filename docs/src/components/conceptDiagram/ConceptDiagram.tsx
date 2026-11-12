// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import clsx from 'clsx';
import type {LucideIcon} from 'lucide-react';

import styles from './ConceptDiagram.module.css';

/** neutral is muted; accent marks the path a diagram is about; wait marks durable waiting. */
export type DiagramTone = 'neutral' | 'accent' | 'wait' | 'external';

function toneClass(prefix: 'tile' | 'title' | 'code' | 'chip' | 'badge' | 'arrow', tone: DiagramTone): string | undefined {
  if (tone === 'accent') return styles[`${prefix}Accent`];
  if (tone === 'wait') return styles[`${prefix}Wait`];
  if (tone === 'external') return styles[`${prefix}External`];
  return undefined;
}

export function ConceptDiagramFrame({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: ReactNode;
}): ReactNode {
  return (
    <div className="exec-diagram" role="img" aria-label={label}>
      <div className={clsx(styles.root, className)}>{children}</div>
    </div>
  );
}

export function DiagramIcon({icon: Icon, size = 16}: {icon: LucideIcon; size?: number}): ReactNode {
  return <Icon aria-hidden="true" focusable="false" size={size} strokeWidth={2} absoluteStrokeWidth />;
}

export function IconBadge({
  icon,
  tone = 'neutral',
  isSolid,
  isSmall,
  className,
}: {
  icon: LucideIcon;
  tone?: DiagramTone;
  isSolid?: boolean;
  isSmall?: boolean;
  className?: string;
}): ReactNode {
  return (
    <span
      className={clsx(
        styles.badge,
        toneClass('badge', tone),
        isSolid && styles.badgeSolid,
        isSmall && styles.badgeSmall,
        className,
      )}>
      <DiagramIcon icon={icon} size={isSmall ? 14 : 17} />
    </span>
  );
}

export function Tile({
  tone = 'neutral',
  className,
  children,
}: {
  tone?: DiagramTone;
  className?: string;
  children: ReactNode;
}): ReactNode {
  return <div className={clsx(styles.tile, toneClass('tile', tone), className)}>{children}</div>;
}

/** A small uppercase label above a title. name renders as a code identifier. */
export function Eyebrow({
  icon,
  name,
  children,
}: {
  icon?: LucideIcon;
  name?: string;
  children?: ReactNode;
}): ReactNode {
  return (
    <span className={styles.eyebrow}>
      {icon ? <DiagramIcon icon={icon} size={13} /> : null}
      {children}
      {name ? <span className={styles.eyebrowName}>{name}</span> : null}
    </span>
  );
}

export function TileTitle({tone = 'neutral', children}: {tone?: DiagramTone; children: ReactNode}): ReactNode {
  return <b className={clsx(styles.title, toneClass('title', tone))}>{children}</b>;
}

export function Detail({children}: {children: ReactNode}): ReactNode {
  return <p className={styles.detail}>{children}</p>;
}

/** Adds line-break opportunities after punctuation so long calls wrap between arguments. */
function withBreakOpportunities(text: ReactNode): ReactNode {
  if (typeof text !== 'string') return text;
  return text.split(/([(,.])/).map((part, index) => (
    <React.Fragment key={index}>
      {part}
      {index % 2 === 1 ? <wbr /> : null}
    </React.Fragment>
  ));
}

export function Code({
  tone = 'neutral',
  icon,
  children,
}: {
  tone?: DiagramTone;
  icon?: LucideIcon;
  children: ReactNode;
}): ReactNode {
  return (
    <code className={clsx(styles.code, toneClass('code', tone))}>
      {icon ? <DiagramIcon icon={icon} size={13} /> : null}
      <span>{withBreakOpportunities(children)}</span>
    </code>
  );
}

export function Chip({
  tone = 'neutral',
  isSolid,
  icon,
  children,
}: {
  tone?: DiagramTone;
  isSolid?: boolean;
  icon?: LucideIcon;
  children: ReactNode;
}): ReactNode {
  return (
    <span className={clsx(styles.chip, toneClass('chip', tone), isSolid && styles.chipSolid)}>
      {icon ? <DiagramIcon icon={icon} size={13} /> : null}
      {children}
    </span>
  );
}

export function ChipRow({children}: {children: ReactNode}): ReactNode {
  return <span className={styles.chipRow}>{children}</span>;
}

/** A tinted region that groups nodes without drawing a box around them. */
export function Group({className, children}: {className?: string; children: ReactNode}): ReactNode {
  return <div className={clsx(styles.group, className)}>{children}</div>;
}

export type ArrowDirection = 'down' | 'up' | 'right' | 'left';

const arrowDirectionClass: Record<ArrowDirection, string> = {
  down: styles.arrowDown,
  up: styles.arrowUp,
  right: styles.arrowRight,
  left: styles.arrowLeft,
};

/** An arrow with an optional label. isFlowing animates dashes along the path the diagram is about. */
export function Arrow({
  direction,
  tone = 'neutral',
  isFlowing,
  label,
  className,
}: {
  direction: ArrowDirection;
  tone?: DiagramTone;
  isFlowing?: boolean;
  label?: ReactNode;
  className?: string;
}): ReactNode {
  return (
    <div
      className={clsx(
        styles.arrow,
        arrowDirectionClass[direction],
        toneClass('arrow', tone),
        isFlowing && styles.arrowFlowing,
        className,
      )}>
      {label ? <span className={styles.arrowLabel}>{label}</span> : null}
      <span className={styles.arrowLine} />
    </div>
  );
}
