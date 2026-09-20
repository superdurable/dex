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

/** What the canvas is currently showing, so the drawer can say so without owning selection. */
export interface StepBand {
  stepType: string;
  /** One-sentence purpose from dex:explanation, when the Flow declares it. */
  explanation: string | null;
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
  reloadKey,
  onStranded,
  onStopped,
  onClose,
}: {
  flowType: string;
  flowId: string;
  definition: FlowV2Definition;
  summary: FlowSummary | null;
  flowStatusCode?: number;
  band: StepBand | null;
  reloadKey: number;
  onStranded?: (flowID: string) => void;
  onStopped: () => void;
  onClose: () => void;
}) {
  return (
    <section className="rdw" aria-label={flowId}>
      <RunHeader
        flowId={flowId}
        flowType={flowType}
        summary={summary}
        onClose={onClose}
        onStopped={onStopped}
      />
      {band && (
        <div className="rdw-band" data-tone={band.tone ?? undefined}>
          <span className="rdw-bandlabel">
            {band.isBlocking ? RUN_COPY.waitingAt : RUN_COPY.showingStep}
          </span>
          <span className="rdw-bandstep t-mono">{band.stepType}</span>
          {band.explanation && <span className="rdw-bandwhat">{band.explanation}</span>}
          {band.reason && <span className="rdw-bandwhy">{band.reason}</span>}
        </div>
      )}
      <SelectedRunPanel
        definition={definition}
        flowId={flowId}
        flowStatusCode={flowStatusCode}
        flowType={flowType}
        order="actions-first"
        showHeading={false}
        reloadKey={reloadKey}
        onActed={onClose}
        onStranded={onStranded}
      />
    </section>
  );
}
