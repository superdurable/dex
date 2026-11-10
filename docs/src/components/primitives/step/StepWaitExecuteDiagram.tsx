// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type CSSProperties, type ReactNode} from 'react';
import clsx from 'clsx';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import type {LucideIcon} from 'lucide-react';
import {
  ArrowRight,
  CircleCheck,
  CircleX,
  Footprints,
  Hourglass,
  MessagesSquare,
  Play,
  Signpost,
  Timer,
  Workflow,
} from 'lucide-react';

import {
  Arrow,
  Chip,
  ChipRow,
  Code,
  ConceptDiagramFrame,
  Detail,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
  type DiagramTone,
} from '../../conceptDiagram/ConceptDiagram';
import styles from './StepWaitExecuteDiagram.module.css';

type Copy = {
  returnsWait: string;
  conditionWaiting: string;
  /** Uses {timer}, {channel}, and {subflow} placeholders, which render as icon chips. */
  conditionDetail: string;
  satisfied: string;
  executeMove: string;
  decision: string;
};

const EN: Copy = {
  returnsWait: 'returns Wait',
  conditionWaiting: 'Condition waiting',
  conditionDetail: 'Dex evaluates {timer}, {channel}, or {subflow}',
  satisfied: 'condition satisfied',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
};

const ZH: Copy = {
  returnsWait: '返回 Wait',
  conditionWaiting: 'Condition 等待',
  conditionDetail: 'Dex 评估 {timer}、{channel} 或 {subflow}',
  satisfied: '条件满足',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
};

const conditionKinds: Record<string, {icon: LucideIcon; name: string}> = {
  timer: {icon: Timer, name: 'Timer'},
  channel: {icon: MessagesSquare, name: 'Channel'},
  subflow: {icon: Workflow, name: 'SubFlow'},
};

function ConditionDetail({template}: {template: string}): ReactNode {
  const parts = template.split(/\{(timer|channel|subflow)\}/);
  return (
    <Detail>
      {parts.map((part, index) => {
        const kind = index % 2 === 1 ? conditionKinds[part] : undefined;
        if (!kind) return <React.Fragment key={index}>{part}</React.Fragment>;
        return (
          <span key={index} className={styles.inlineChip}>
            <Chip tone="wait" icon={kind.icon}>
              {kind.name}
            </Chip>
          </span>
        );
      })}
    </Detail>
  );
}

function Stage({
  index,
  icon,
  tone,
  isSolid,
  tailTone,
  children,
}: {
  index: number;
  icon: LucideIcon;
  tone: DiagramTone;
  isSolid?: boolean;
  tailTone?: DiagramTone;
  children: ReactNode;
}): ReactNode {
  const position = {'--col': index, '--row': index * 2 - 1} as CSSProperties;
  return (
    <div className={styles.stage} style={position}>
      <div className={styles.rail}>
        <IconBadge icon={icon} tone={tone} isSolid={isSolid} />
        {tailTone ? <span className={clsx(styles.tail, tailTone === 'wait' ? styles.toneWait : styles.toneAccent)} /> : null}
      </div>
      <div className={styles.body}>{children}</div>
    </div>
  );
}

function StageLink({index, tone, label}: {index: number; tone: DiagramTone; label?: string}): ReactNode {
  const position = {'--col': index, '--row': index * 2} as CSSProperties;
  const isFlowing = tone === 'accent';
  return (
    <div className={styles.link} style={position}>
      <Arrow direction="down" tone={tone} isFlowing={isFlowing} className={styles.linkNarrow} />
      {label ? (
        <span className={clsx(styles.linkNarrowLabel, tone === 'wait' ? styles.toneWait : styles.toneAccent)}>
          {label}
        </span>
      ) : null}
      <Arrow direction="right" tone={tone} isFlowing={isFlowing} label={label} className={styles.linkWide} />
    </div>
  );
}

export default function StepWaitExecuteDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.stepper}>
        <Stage index={1} icon={Footprints} tone="wait" tailTone="wait">
          <Tile>
            <Eyebrow name="ExampleStep">STEP</Eyebrow>
            <TileTitle tone="wait">WaitFor</TileTitle>
            <Code tone="wait" icon={MessagesSquare}>
              approval.for_one()
            </Code>
          </Tile>
        </Stage>
        <StageLink index={1} tone="wait" label={copy.returnsWait} />
        <Stage index={2} icon={Hourglass} tone="wait" isSolid tailTone="accent">
          <Tile tone="wait">
            <Eyebrow>DEX</Eyebrow>
            <TileTitle>{copy.conditionWaiting}</TileTitle>
            <ConditionDetail template={copy.conditionDetail} />
          </Tile>
        </Stage>
        <StageLink index={2} tone="accent" label={copy.satisfied} />
        <Stage index={3} icon={Play} tone="accent" tailTone="accent">
          <Tile>
            <Eyebrow name="ExampleStep">STEP</Eyebrow>
            <TileTitle tone="accent">Execute</TileTitle>
            <Code tone="accent">{copy.executeMove}</Code>
          </Tile>
        </Stage>
        <StageLink index={3} tone="accent" />
        <Stage index={4} icon={Signpost} tone="accent" isSolid>
          <Tile tone="accent">
            <Eyebrow>DECISION</Eyebrow>
            <TileTitle>{copy.decision}</TileTitle>
            <ChipRow>
              <Chip tone="accent" isSolid icon={ArrowRight}>
                GoTo
              </Chip>
              <Chip icon={CircleCheck}>GracefulComplete</Chip>
              <Chip icon={CircleX}>ForceFail</Chip>
            </ChipRow>
          </Tile>
        </Stage>
      </div>
    </ConceptDiagramFrame>
  );
}
