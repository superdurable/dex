// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow, StepCard} from './flowGraphShared';

type Copy = {
  returnsWait: string;
  conditionWaiting: string;
  conditionDetail: string;
  satisfied: string;
  executeMove: string;
  decision: string;
  decisionDetail: string;
};

const EN: Copy = {
  returnsWait: 'returns Wait',
  conditionWaiting: 'Condition waiting',
  conditionDetail: 'Dex evaluates Timer, Channel, or SubFlow',
  satisfied: 'condition satisfied',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
  decisionDetail: 'GoTo · GracefulComplete · ForceFail',
};

const ZH: Copy = {
  returnsWait: '返回 Wait',
  conditionWaiting: 'Condition 等待',
  conditionDetail: 'Dex 评估 Timer、Channel 或 SubFlow',
  satisfied: '条件满足',
  executeMove: 'GoTo(Complete Flow, input + 1)',
  decision: 'StepDecision',
  decisionDetail: 'GoTo · GracefulComplete · ForceFail',
};

export default function StepWaitExecuteDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={markdownDescription}>
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
