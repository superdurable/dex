import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow, GraphLoopBack, StepCard} from './flowGraphShared';

type PanelCopy = {
  title: string;
  parallel: string;
};

type Copy = {
  label: string;
  goto: PanelCopy;
  loop: PanelCopy;
  parallel: PanelCopy;
  completeFlow: string;
  stepA: string;
  stepB: string;
  process: string;
};

const EN: Copy = {
  label: 'StepDecision movements: GoTo, GoToMulti, and loop',
  goto: {title: 'GoTo', parallel: ''},
  loop: {title: 'Loop', parallel: ''},
  parallel: {title: 'GoToMulti', parallel: 'both active'},
  completeFlow: 'Complete Flow',
  stepA: 'StepA',
  stepB: 'StepB',
  process: 'ProcessStep',
};

const ZH: Copy = {
  label: 'StepDecision movement：GoTo、GoToMulti 与循环',
  goto: {title: 'GoTo', parallel: ''},
  loop: {title: '循环', parallel: ''},
  parallel: {title: 'GoToMulti', parallel: '同时 active'},
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

export default function StepDecisionMovementsDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph-grid flow-graph-grid-movements">
        <MovementPanel title={copy.goto.title}>
          <StepCard name="ExampleStep" executeOnly execute={`GoTo(${copy.completeFlow})`} />
          <GraphArrow />
          <CompactCard kicker="NEXT" title={copy.completeFlow} />
        </MovementPanel>
        <MovementPanel title={copy.parallel.title}>
          <StepCard name="ExampleStep" executeOnly execute={`GoToMulti(${copy.stepA}, ${copy.stepB})`} />
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
