// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow, GraphLoopBack, StepCard} from './flowGraphShared';

type PanelCopy = {
  title: string;
  parallel: string;
};

type Copy = {
  goto: PanelCopy;
  loop: PanelCopy;
  parallel: PanelCopy;
  completeFlow: string;
  stepA: string;
  stepB: string;
  process: string;
};

const EN: Copy = {
  goto: {title: 'GoTo', parallel: ''},
  loop: {title: 'Loop', parallel: ''},
  parallel: {title: 'GoToMany', parallel: 'both active'},
  completeFlow: 'Complete Flow',
  stepA: 'StepA',
  stepB: 'StepB',
  process: 'ProcessStep',
};

const ZH: Copy = {
  goto: {title: 'GoTo', parallel: ''},
  loop: {title: '循环', parallel: ''},
  parallel: {title: 'GoToMany', parallel: '同时 active'},
  completeFlow: 'Complete Flow',
  stepA: 'StepA',
  stepB: 'StepB',
  process: 'ProcessStep',
};

function MovementPanel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}): ReactNode {
  return (
    <div className="flow-graph-panel">
      <div className="flow-graph-panel-title">{title}</div>
      <div className="flow-graph flow-graph-panel-body">{children}</div>
    </div>
  );
}

export default function StepDecisionMovementsDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={markdownDescription}>
      <div className="flow-graph-grid flow-graph-grid-movements">
        <MovementPanel title={copy.goto.title}>
          <StepCard name="ExampleStep" executeOnly execute={`GoTo(${copy.completeFlow})`} />
          <GraphArrow />
          <CompactCard kicker="NEXT" title={copy.completeFlow} />
        </MovementPanel>
        <MovementPanel title={copy.parallel.title}>
          <StepCard name="ExampleStep" executeOnly execute={`GoToMany(${copy.stepA}, ${copy.stepB})`} />
          <div className="flow-graph-split flow-graph-split-compact">
            <div className="flow-graph-split-stem" aria-hidden="true" />
            <div className="flow-graph-split-bar" aria-hidden="true" />
            <div className="flow-graph-split-cards">
              <div className="flow-graph-split-branch">
                <GraphArrow />
                <CompactCard kicker="PARALLEL" title={copy.stepA} detail={copy.parallel.parallel} />
              </div>
              <div className="flow-graph-split-branch">
                <GraphArrow />
                <CompactCard kicker="PARALLEL" title={copy.stepB} detail={copy.parallel.parallel} />
              </div>
            </div>
          </div>
        </MovementPanel>
        <MovementPanel title={copy.loop.title}>
          <div className="flow-graph-loop-def">
            <div className="flow-graph-loop-def-steps">
              <StepCard name="ExampleStep" executeOnly execute={`GoTo(${copy.process})`} />
              <GraphArrow />
              <StepCard name={copy.process} executeOnly execute="GoTo(ExampleStep)" />
            </div>
            <GraphLoopBack />
          </div>
        </MovementPanel>
      </div>
    </div>
  );
}
