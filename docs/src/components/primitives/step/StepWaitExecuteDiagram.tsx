import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow, StepCard} from './flowGraphShared';

type Copy = {
  label: string;
  returnsWait: string;
  conditionWaiting: string;
  conditionDetail: string;
  satisfied: string;
  executeMove: string;
  decision: string;
  decisionDetail: string;
};

const EN: Copy = {
  label: 'Step lifecycle: WaitFor, condition waiting, Execute, StepDecision',
  returnsWait: 'returns Wait',
  conditionWaiting: 'Condition waiting',
  conditionDetail: 'Dex evaluates timer or channel',
  satisfied: 'condition satisfied',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
  decisionDetail: 'GoTo · GracefulComplete · Fail',
};

const ZH: Copy = {
  label: 'Step 生命周期：WaitFor、条件等待、Execute、StepDecision',
  returnsWait: '返回 Wait',
  conditionWaiting: 'Condition 等待',
  conditionDetail: 'Dex 评估 timer 或 channel',
  satisfied: '条件满足',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
  decisionDetail: 'GoTo · GracefulComplete · Fail',
};

export default function StepWaitExecuteDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph">
        <StepCard
          name="ExampleStep"
          waitForOnly
          waitFor={{channel: 'approval.for_one()'}}
          tone="waiting"
        />
        <GraphArrow label={copy.returnsWait} />
        <CompactCard
          kicker="DEX"
          title={copy.conditionWaiting}
          detail={copy.conditionDetail}
          tone="waiting"
        />
        <GraphArrow label={copy.satisfied} />
        <StepCard name="ExampleStep" executeOnly execute={copy.executeMove} />
        <GraphArrow />
        <CompactCard
          kicker="DECISION"
          title={copy.decision}
          detail={copy.decisionDetail}
          tone="decision"
        />
      </div>
    </div>
  );
}
