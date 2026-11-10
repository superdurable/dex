// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import type {LucideIcon} from 'lucide-react';
import {ArrowRight, Repeat, Split} from 'lucide-react';

import {
  Arrow,
  Code,
  ConceptDiagramFrame,
  Detail,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../../conceptDiagram/ConceptDiagram';
import styles from './StepDecisionMovementsDiagram.module.css';

type Copy = {
  goto: string;
  loop: string;
  parallel: string;
  bothActive: string;
  completeFlow: string;
  stepA: string;
  stepB: string;
  process: string;
};

const EN: Copy = {
  goto: 'GoTo',
  loop: 'Loop',
  parallel: 'GoToMany',
  bothActive: 'both active',
  completeFlow: 'Complete Flow',
  stepA: 'StepA',
  stepB: 'StepB',
  process: 'ProcessStep',
};

const ZH: Copy = {
  goto: 'GoTo',
  loop: '循环',
  parallel: 'GoToMany',
  bothActive: '同时 active',
  completeFlow: 'Complete Flow',
  stepA: 'StepA',
  stepB: 'StepB',
  process: 'ProcessStep',
};

function MovementPanel({title, icon, children}: {title: string; icon: LucideIcon; children: ReactNode}): ReactNode {
  return (
    <section className={styles.panel}>
      <header className={styles.panelHeader}>
        <IconBadge icon={icon} tone="accent" isSmall />
        <b>{title}</b>
      </header>
      <div className={styles.panelBody}>{children}</div>
    </section>
  );
}

function SourceStep({name, move}: {name: string; move: string}): ReactNode {
  return (
    <Tile>
      <Eyebrow name={name}>STEP</Eyebrow>
      <span className={styles.method}>Execute</span>
      <Code tone="accent">{move}</Code>
    </Tile>
  );
}

function TargetStep({kicker, name, detail}: {kicker: string; name: string; detail?: string}): ReactNode {
  return (
    <Tile tone="accent">
      <Eyebrow>{kicker}</Eyebrow>
      <TileTitle>{name}</TileTitle>
      {detail ? <Detail>{detail}</Detail> : null}
    </Tile>
  );
}

export default function StepDecisionMovementsDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.grid}>
        <MovementPanel title={copy.goto} icon={ArrowRight}>
          <SourceStep name="ExampleStep" move={`GoTo(${copy.completeFlow})`} />
          <Arrow direction="down" tone="accent" isFlowing />
          <TargetStep kicker="NEXT" name={copy.completeFlow} />
        </MovementPanel>
        <MovementPanel title={copy.parallel} icon={Split}>
          <SourceStep name="ExampleStep" move={`GoToMany(${copy.stepA}, ${copy.stepB})`} />
          <div className={styles.fork}>
            <span className={styles.forkStem} />
            <div className={styles.forkBranches}>
              <div className={styles.branch}>
                <Arrow direction="down" tone="accent" isFlowing />
                <TargetStep kicker="PARALLEL" name={copy.stepA} detail={copy.bothActive} />
              </div>
              <div className={styles.branch}>
                <Arrow direction="down" tone="accent" isFlowing />
                <TargetStep kicker="PARALLEL" name={copy.stepB} detail={copy.bothActive} />
              </div>
            </div>
          </div>
        </MovementPanel>
        <MovementPanel title={copy.loop} icon={Repeat}>
          <div className={styles.loop}>
            <div className={styles.loopSteps}>
              <SourceStep name="ExampleStep" move={`GoTo(${copy.process})`} />
              <Arrow direction="down" tone="accent" isFlowing />
              <SourceStep name={copy.process} move="GoTo(ExampleStep)" />
            </div>
            <span className={styles.loopBack} />
          </div>
        </MovementPanel>
      </div>
    </ConceptDiagramFrame>
  );
}
