// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type ReactNode } from 'react';
import { ChevronRight, CircleAlert, Hourglass, Play, Plus, Send } from 'lucide-react';
import { flowErrorTypeLabel } from '@/lib/semantic';
import type { ActiveStepExecution, FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { FlowRouting, PullRouting, flowRoutingData } from './PullRouting';
import { StepMethodInvocation } from './StepMethodInvocation';
import { FlowConfigCard, type FlowConfigChange } from './FlowConfigCard';
import { JsonView } from '../../components/JsonView';
import { FailureContent, SemanticEventDetails } from './EventDetails';
import { Fields } from './StepMethodSections';
import { AttributeRows, ChannelRows, pendingChannelGroups, RawStateJSON, SectionViewToggle } from './CompactStateLists';
import type { StateWriteKind } from './StateWriteDialog';

const sectionExpandByKey = new Map<string, boolean>();

function useSectionExpand(persistKey: string) {
  const [expanded, setExpanded] = useState(() => sectionExpandByKey.get(persistKey) ?? false);
  const [collapseNonce, setCollapseNonce] = useState(0);
  return {
    expanded,
    collapseNonce,
    setExpanded: (next: boolean) => {
      sectionExpandByKey.set(persistKey, next);
      setExpanded(next);
      if (!next) setCollapseNonce((current) => current + 1);
    },
  };
}

function SectionExpandToggle({
  expanded,
  onChange,
  label,
}: {
  expanded: boolean;
  onChange: (next: boolean) => void;
  label: string;
}) {
  return (
    <button aria-pressed={expanded} className="button quiet section-expand-toggle" onClick={() => onChange(!expanded)} type="button">
      {expanded ? 'Collapse all' : 'Expand all'}
      <span className="sr-only"> {label}</span>
    </button>
  );
}

/** One Overview section: a single heading, an optional count, actions, and content. */
function OverviewSection({
  title,
  count,
  actions,
  className = '',
  children,
}: {
  title: string;
  count?: number;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={`overview-section ${className}`.trim()}>
      <header className="overview-section-head">
        <h2>
          {title}
          {count !== undefined && <span className={`overview-count${count === 0 ? ' is-zero' : ''}`}>{count}</span>}
        </h2>
        {actions && <div className="overview-section-actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

export function FlowOverview({
  summary,
  events,
  state,
  deletingChannelMessage,
  onDeleteChannelMessage,
  onOpenStateWrite,
  savedHistorySettings,
  closedFlowConfig,
  onHistorySettingsSaved,
}: {
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  state: FlowState | null;
  deletingChannelMessage?: string;
  onDeleteChannelMessage?: (channelName: string, messageId: string) => Promise<void>;
  onOpenStateWrite?: (kind: StateWriteKind) => void;
  savedHistorySettings?: FlowConfigChange;
  /** A closed run's Flow config, read once since live state is not loaded for it. */
  closedFlowConfig?: Record<string, unknown>;
  onHistorySettingsSaved?: (saved: FlowConfigChange, savedRun: FlowSummary) => Promise<void>;
}) {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const closed = summary.flowStatus === 'Running'
    ? undefined
    : events.findLast((event) => event.type === 'FlowClosed');
  const [collapsedActiveStepIds, setCollapsedActiveStepIds] = useState<Set<string>>(
    () => new Set(),
  );
  const attributesExpand = useSectionExpand('overview:expand:attributes');
  const channelsExpand = useSectionExpand('overview:expand:channels');
  const routing = flowRoutingData(state, events);
  const activeStepIds = state?.activeStepExecutions.map((step) => step.stepExecutionId) ?? [];
  const allActiveStepsExpanded = activeStepIds.length > 0
    && activeStepIds.every((stepExecutionId) => !collapsedActiveStepIds.has(stepExecutionId));
  const channelGroups = pendingChannelGroups(state?.pendingChannelMessages ?? {});
  const pendingMessageCount = channelGroups.reduce((count, channel) => count + channel.messages.length, 0);
  const [attributesView, setAttributesView] = useState<'details' | 'raw'>('details');
  const [channelsView, setChannelsView] = useState<'details' | 'raw'>('details');
  const failure = closed ? closeFailureOf(closed) : undefined;

  const setAllActiveStepsExpanded = (expanded: boolean) => {
    setCollapsedActiveStepIds((current) => {
      const next = new Set(current);
      for (const stepExecutionId of activeStepIds) {
        if (expanded) next.delete(stepExecutionId);
        else next.add(stepExecutionId);
      }
      return next;
    });
  };

  const toggleActiveStep = (stepExecutionId: string) => {
    setCollapsedActiveStepIds((current) => {
      const next = new Set(current);
      if (next.has(stepExecutionId)) next.delete(stepExecutionId);
      else next.add(stepExecutionId);
      return next;
    });
  };

  return (
    <div className="run-overview">
      <div className="run-overview-main">
        {state ? (
          <OverviewSection
            title="Active steps"
            count={state.activeStepExecutions.length}
            className="overview-focus"
            actions={state.activeStepExecutions.length > 1 && (
              <SectionExpandToggle
                expanded={allActiveStepsExpanded}
                onChange={setAllActiveStepsExpanded}
                label="active steps"
              />
            )}
          >
            {state.activeStepExecutions.map((step) => (
              <ActiveStep
                key={step.stepExecutionId}
                step={step}
                isOpen={!collapsedActiveStepIds.has(step.stepExecutionId)}
                isPullMode={routing.workerTarget.isPullMode === true}
                parentFlowId={summary.flowId}
                onToggle={() => toggleActiveStep(step.stepExecutionId)}
              />
            ))}
            {state.activeStepExecutions.length === 0 && (
              <p className="overview-quiet">No active steps right now.</p>
            )}
          </OverviewSection>
        ) : (
          <OverviewSection title="Outcome" className={`overview-focus overview-outcome tone-${statusTone(summary.flowStatus)}`}>
            {failure && (
              <div className="overview-outcome-error" role="note">
                <CircleAlert aria-hidden="true" size={16} strokeWidth={2.25} />
                <div>
                  <strong>{failure.message || failure.type}</strong>
                  {failure.message && failure.type && <span className="overview-outcome-error-type">{failure.type}</span>}
                </div>
              </div>
            )}
            {!closed && <p className="overview-quiet">Live state is queried only while this run is active.</p>}
            {closed && !hasCloseResults(closed) && !failure && (
              <p className="overview-quiet">The run closed without results.</p>
            )}
            {closed && hasCloseResults(closed) && (
              <>
                <h3 className="overview-subheading">Close result</h3>
                <JsonView
                  value={closed.payload.results ?? []}
                  label="Close result"
                  variant="inline"
                  persistKey="overview:close-result"
                />
              </>
            )}
          </OverviewSection>
        )}

        {state && (
          <OverviewSection
            title="Attributes"
            count={state.attributes.length}
            actions={<>
              {onOpenStateWrite && (
                <button className="button quiet" onClick={() => onOpenStateWrite('attribute')} type="button">
                  <Plus aria-hidden="true" size={15} strokeWidth={2} />
                  Set attribute
                </button>
              )}
              {state.attributes.length > 0 && (
                <>
                  <SectionExpandToggle
                    expanded={attributesExpand.expanded}
                    onChange={attributesExpand.setExpanded}
                    label="attributes"
                  />
                  <SectionViewToggle label="Attributes" onChange={setAttributesView} view={attributesView} />
                </>
              )}
            </>}
          >
            {state.attributes.length > 0 ? (
              attributesView === 'raw'
                ? <RawStateJSON value={state.attributes} />
                : <AttributeRows attributes={state.attributes} expandValues={attributesExpand.expanded} />
            ) : (
              <p className="overview-quiet">No attributes are set.</p>
            )}
          </OverviewSection>
        )}

        {state && (
          <OverviewSection
            title="Channels"
            count={pendingMessageCount}
            actions={<>
              {onOpenStateWrite && (
                <button className="button quiet" onClick={() => onOpenStateWrite('channel')} type="button">
                  <Send aria-hidden="true" size={15} strokeWidth={2} />
                  Publish message
                </button>
              )}
              {pendingMessageCount > 0 && (
                <>
                  <SectionExpandToggle
                    expanded={channelsExpand.expanded}
                    onChange={channelsExpand.setExpanded}
                    label="channel messages"
                  />
                  <SectionViewToggle label="Channels" onChange={setChannelsView} view={channelsView} />
                </>
              )}
            </>}
          >
            {pendingMessageCount > 0 ? (
              channelsView === 'raw'
                ? <RawStateJSON value={state.pendingChannelMessages} />
                : (
                  <ChannelRows
                    channels={channelGroups}
                    deletingChannelMessage={deletingChannelMessage}
                    expandValues={channelsExpand.expanded}
                    onDeleteChannelMessage={onDeleteChannelMessage}
                  />
                )
            ) : (
              <p className="overview-quiet">No pending channel messages.</p>
            )}
          </OverviewSection>
        )}
      </div>

      {/* The side column holds the run's identity, config and records, so both columns carry weight. */}
      <div className="run-overview-side">
        <FlowConfigCard
          summary={summary}
          flowConfig={state ? { ...(state.flowConfig ?? {}), ...savedHistorySettings } : closedFlowConfig}
          onSaved={onHistorySettingsSaved}
        />

        <OverviewSection title="Identity">
          <FlowRouting routing={routing} flowType={summary.flowType} showFlowType />
          <Fields compact values={[
            ['Request ID', summary.requestId || undefined],
            ['First run ID', summary.firstRunId && summary.firstRunId !== summary.runId ? summary.firstRunId : undefined],
          ]} />
        </OverviewSection>

        <OverviewSection title="Start input">
          {started ? (
            <div className="semantic-event">
              <SemanticEventDetails
                event={started}
                parentFlowId={summary.flowId}
                showStartHeading={false}
                showStartFlowConfig={false}
              />
            </div>
          ) : (
            <p className="overview-quiet">The start event is not in the loaded history page.</p>
          )}
        </OverviewSection>

        {state && state.completedSteps.length > 0 && (
          <OverviewSection title="Completed outputs" count={state.completedSteps.length}>
            <JsonView
              value={state.completedSteps}
              label="Completed outputs"
              variant="inline"
              persistKey="overview:completed-outputs"
            />
          </OverviewSection>
        )}
        {state && state.queuedSteps.length > 0 && (
          <OverviewSection title="Queued steps" count={state.queuedSteps.length}>
            <JsonView value={state.queuedSteps} label="Queued steps" variant="inline" persistKey="overview:queued-steps" />
          </OverviewSection>
        )}
        {state && state.queuedSteps.length === 0 && state.completedSteps.length === 0 && (
          <p className="overview-quiet overview-quiet-line">No queued steps · No completed outputs</p>
        )}
      </div>
    </div>
  );
}

function ActiveStep({
  step,
  isOpen,
  isPullMode,
  parentFlowId,
  onToggle,
}: {
  step: ActiveStepExecution;
  isOpen: boolean;
  isPullMode: boolean;
  parentFlowId: string;
  onToggle: () => void;
}) {
  const waitingSummary = waitingConditionSummary(step.waitingCondition);
  const PhaseIcon = step.phase === 'Waiting' ? Hourglass : Play;
  return (
    <details className="active-step-card" open={isOpen}>
      <summary onClick={(event) => {
        event.preventDefault();
        onToggle();
      }}>
        <ChevronRight aria-hidden="true" className="active-step-disclosure" size={16} strokeWidth={2} />
        <span className={`phase phase-${step.phase.toLowerCase()}`}>
          <PhaseIcon aria-hidden="true" size={13} strokeWidth={2.25} />
          {step.phase}
        </span>
        <span className="active-step-heading">
          <b>{step.stepType}</b>
          {waitingSummary && <span className="active-step-waits">{waitingSummary}</span>}
        </span>
      </summary>
      <div className="active-step-content">
        <Fields compact values={[
          ['Step execution ID', step.stepExecutionId],
          ['From', step.fromStepExecutionId || undefined],
        ]} />
        <StepMethodInvocation value={step.waitForInvocation} label="WaitFor invocation" />
        <StepMethodInvocation value={step.executeInvocation} label="Execute invocation" />
        {isPullMode && <>
          <PullRouting value={step.waitForPullRouting} label="WaitFor routing" hideEmpty />
          <PullRouting value={step.executePullRouting} label="Execute routing" hideEmpty />
        </>}
        {step.waitingCondition && Object.keys(step.waitingCondition).length > 0 && (
          <JsonView
            value={step.waitingCondition}
            label="Waiting condition"
            persistKey={`overview:active-step:${step.stepType}:waiting-condition`}
            parentFlowId={parentFlowId}
            stepExecutionId={step.stepExecutionId}
          />
        )}
        {step.timers.length > 0 && (
          <JsonView
            value={step.timers}
            label={`${step.timers.length} timers`}
            persistKey={`overview:active-step:${step.stepType}:timers`}
          />
        )}
        {step.lastFailureInfo && Object.keys(step.lastFailureInfo).length > 0 && (
          <div className="semantic-subsection">
            <h5>Last failure</h5>
            {/* Pending backend attempts exclude local fallback attempts, so hide them until cumulative values are available. */}
            <FailureContent value={step.lastFailureInfo} stackInitiallyExpanded showAttempt={false} />
          </div>
        )}
      </div>
    </details>
  );
}

/** Names what a waiting Step waits for, such as "Waits for Channel approvals". */
function waitingConditionSummary(condition: Record<string, unknown> | undefined): string {
  if (!condition) return '';
  const names = [
    ...listOf(condition.channelConditions).map((channel) => `Channel ${String(channel.channelName ?? '')}`.trim()),
    ...listOf(condition.timerConditions).map(() => 'a timer'),
    ...listOf(condition.subFlowConditions).map(() => 'a SubFlow'),
  ];
  if (names.length === 0) return '';
  const shown = names.slice(0, 2).join(', ');
  return names.length > 2 ? `Waits for ${shown} and ${names.length - 2} more` : `Waits for ${shown}`;
}

function listOf(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value)
    ? value.filter((entry): entry is Record<string, unknown> => typeof entry === 'object' && entry !== null)
    : [];
}

/** The close failure's message and semantic type; either may be empty, but not both. */
function closeFailureOf(closed: FlowHistoryEvent): { message: string; type: string } | undefined {
  const message = typeof closed.payload.errorMessage === 'string' ? closed.payload.errorMessage : '';
  const errorType = closed.payload.errorType;
  const typeLabel = errorType === undefined || errorType === null || errorType === '' ? '' : flowErrorTypeLabel(errorType);
  const type = typeLabel === 'Unspecified' ? '' : typeLabel;
  return message || type ? { message, type } : undefined;
}

function hasCloseResults(closed: FlowHistoryEvent): boolean {
  const results = closed.payload.results;
  return Array.isArray(results) ? results.length > 0 : results !== undefined && results !== null;
}

function statusTone(status: string): string {
  return status.toLowerCase().replaceAll(' ', '-');
}
