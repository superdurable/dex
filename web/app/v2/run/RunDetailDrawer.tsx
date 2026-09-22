// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { FlowSummary } from '@/lib/types';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { RUN_COPY } from './copy';
import { RunHeader } from './RunHeader';
import { hasMultipleActions, type ActionableStep } from './actionableSteps';
import type { StepContextView } from './stepContext';

/** What the canvas is currently showing, so the drawer can say so without owning selection. */
export interface StepBand {
  stepType: string;
  /** The templated why-line the canvas already renders, or null for a healthy Step. */
  reason: string | null;
  tone: 'blocked' | 'failed' | 'terminal' | null;
  /** True when this is the Step the run is actually waiting on. */
  isBlocking: boolean;
}

/**
 * Run facts and Actions stay put; only the top band follows the canvas. Actions are never
 * hidden behind a tab or a scroll, because they are the reason an Admin opened the run.
 */
export function RunDetailDrawer({
  flowType,
  flowId,
  definition,
  summary,
  flowStatusCode,
  band,
  actionable,
  stepContext,
  reloadKey,
  onStranded,
  onStopped,
  onClose,
  workQueuePermissions,
}: {
  flowType: string;
  /** Empty when nothing is selected: the drawer then explains the Step alone. */
  flowId: string;
  definition: FlowV2Definition;
  summary: FlowSummary | null;
  flowStatusCode?: number;
  band: StepBand | null;
  actionable: readonly ActionableStep[];
  stepContext: StepContextView | null;
  reloadKey: number;
  onStranded?: (flowID: string) => void;
  onStopped: () => void;
  onClose: () => void;
  workQueuePermissions?: readonly string[];
}) {
  const hasRun = flowId !== '';
  return (
    <section
      aria-label={hasRun ? flowId : stepContext?.stepType ?? RUN_COPY.thisStep}
      className="rdw"
      data-step-only={hasRun ? undefined : 'true'}
    >
      {hasRun ? (
        <RunHeader
          flowId={flowId}
          flowType={flowType}
          summary={summary}
          onStopped={onStopped}
        />
      ) : (
        <div className="rdw-steponly">
          <span className="rdw-steponlylabel">{RUN_COPY.stepOnly}</span>
          <button aria-label={RUN_COPY.close} className="rhd-close" onClick={onClose} type="button">
            ✕
          </button>
        </div>
      )}
      {hasRun && hasMultipleActions(actionable) && <ActionProgress steps={actionable} />}
      {hasRun && band && (
        <div className="rdw-band" data-tone={band.tone ?? undefined}>
          <span className="rdw-bandlabel">
            {band.isBlocking ? RUN_COPY.waitingAt : RUN_COPY.showingStep}
          </span>
          <span className="rdw-bandstep t-mono">{band.stepType}</span>
          {band.reason && <span className="rdw-bandwhy">{band.reason}</span>}
        </div>
      )}
      {hasRun && (
        <SelectedRunPanel
          definition={definition}
          flowId={flowId}
          flowStatusCode={flowStatusCode}
          flowType={flowType}
          order="actions-first"
          showHeading={false}
          reloadKey={reloadKey}
          onActed={() => {
          // Closing was right when one Action ended the run. With another gate pending,
          // advancing is the point: the reader should not have to find it again.
          if (actionable.filter((step) => step.state !== 'done').length <= 1) onClose();
        }}
          onStranded={onStranded}
          workQueuePermissions={workQueuePermissions}
        />
      )}
      {stepContext !== null && <StepContextBlock view={stepContext} />}
    </section>
  );
}

/** What the selected Step is for, and where it sits. Flow-level: no run values here. */
function StepContextBlock({ view }: { view: StepContextView }) {
  return (
    <div className="sc-block scx">
      <div className="sc-blockhead">{RUN_COPY.thisStep}</div>
      <p className="scx-step t-mono">{view.stepType}</p>
      <p className="scx-purpose">{view.explanation ?? RUN_COPY.noPurpose}</p>
      <dl className="scx-facts">
        {view.facts.map((fact) => (
          <div className="scx-fact" key={fact.label}>
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

/**
 * Progress through a Flow's human gates. Only one is ever open, so this reads as a sequence
 * rather than a list of things that could be done now.
 */
function ActionProgress({ steps }: { steps: readonly ActionableStep[] }) {
  const current = steps.findIndex((step) => step.state === 'current');
  const remaining = steps.filter((step) => step.state !== 'done').length;
  return (
    <div className="apg">
      <div className="apg-head">
        <span className="apg-label">{RUN_COPY.actionProgress}</span>
        <span className="apg-count">
          {current < 0 ? RUN_COPY.allActionsDone : RUN_COPY.actionsLeft(remaining)}
        </span>
      </div>
      <ol className="apg-list">
        {steps.map((step, index) => (
          <li className="apg-step" data-state={step.state} key={step.stepId}>
            <span className="apg-mark" aria-hidden="true">{step.state === 'done' ? '✓' : index + 1}</span>
            <span className="apg-name t-mono">{step.stepType}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}
