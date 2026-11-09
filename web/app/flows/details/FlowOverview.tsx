// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type ReactNode } from 'react';
import { ChevronRight, CircleAlert, Hourglass, Play, Plus, Send, Trash2 } from 'lucide-react';
import type { ActiveStepExecution, FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { FlowRouting, PullRouting, flowRoutingData } from './PullRouting';
import { StepMethodInvocation } from './StepMethodInvocation';
import { HistorySettingsCard, type HistorySettingsChange } from './HistorySettingsCard';
import { JsonView } from '../../components/JsonView';
import { FailureContent, SemanticEventDetails } from './EventDetails';
import { Fields } from './StepMethodSections';
import type { StateWriteKind } from './StateWriteDialog';

const sectionExpandByKey = new Map<string, boolean>();

/** Deprecated Flow configuration fields that Dex stores but ignores. */
const ignoredFlowConfigKeys = new Set(['continueAsNewThreshold', 'continueAsNewPageSizeInBytes']);

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
    <label className="section-expand-toggle">
      <input
        type="checkbox"
        checked={expanded}
        onChange={(event) => onChange(event.target.checked)}
      />
      {expanded ? 'Collapse all' : 'Expand all'}
      <span className="sr-only">{label}</span>
    </label>
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
  onHistorySettingsSaved,
}: {
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  state: FlowState | null;
  deletingChannelMessage?: string;
  onDeleteChannelMessage?: (channelName: string, messageId: string) => Promise<void>;
  onOpenStateWrite?: (kind: StateWriteKind) => void;
  savedHistorySettings?: HistorySettingsChange;
  onHistorySettingsSaved?: (saved: HistorySettingsChange, savedRun: FlowSummary) => Promise<void>;
}) {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const closed = summary.flowStatus === 'Running'
    ? undefined
    : events.findLast((event) => event.type === 'FlowClosed');
  const isContinuedStart = Boolean(started?.payload.continuedStart
    && typeof started.payload.continuedStart === 'object'
    && Object.keys(started.payload.continuedStart).length > 0);
  const [collapsedActiveStepIds, setCollapsedActiveStepIds] = useState<Set<string>>(
    () => new Set(),
  );
  const attributesExpand = useSectionExpand('overview:expand:attributes');
  const channelsExpand = useSectionExpand('overview:expand:channels');
  const routing = flowRoutingData(state, events);
  const activeStepIds = state?.activeStepExecutions.map((step) => step.stepExecutionId) ?? [];
  const allActiveStepsExpanded = activeStepIds.length > 0
    && activeStepIds.every((stepExecutionId) => !collapsedActiveStepIds.has(stepExecutionId));
  const pendingMessages = pendingChannelEntries(state?.pendingChannelMessages ?? {});
  const flowConfig = state?.flowConfig ? withoutIgnoredFlowConfig(state.flowConfig) : undefined;

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
            actions={state.activeStepExecutions.length > 0 && (
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
            {closed && typeof closed.payload.errorMessage === 'string' && closed.payload.errorMessage && (
              <p className="overview-outcome-error">
                <CircleAlert aria-hidden="true" size={16} strokeWidth={2.25} />
                {closed.payload.errorMessage}
              </p>
            )}
            {!closed && <p className="overview-quiet">Live state is queried only while this run is active.</p>}
            {closed && (
              <JsonView
                value={closed.payload.results ?? []}
                label="Close result"
                persistKey="overview:close-result"
              />
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
                <SectionExpandToggle
                  expanded={attributesExpand.expanded}
                  onChange={attributesExpand.setExpanded}
                  label="attributes"
                />
              )}
            </>}
          >
            {state.attributes.length > 0 ? (
              <JsonView
                value={state.attributes}
                label="Attributes"
                variant="inline"
                persistKey="overview:attributes"
                persistenceKind="attributes"
                expandValues={attributesExpand.expanded}
              />
            ) : (
              <p className="overview-quiet">No attributes are set.</p>
            )}
          </OverviewSection>
        )}

        {state && (
          <OverviewSection
            title="Channels"
            count={pendingMessages.length}
            actions={<>
              {onOpenStateWrite && (
                <button className="button quiet" onClick={() => onOpenStateWrite('channel')} type="button">
                  <Send aria-hidden="true" size={15} strokeWidth={2} />
                  Publish message
                </button>
              )}
              {pendingMessages.length > 0 && (
                <SectionExpandToggle
                  expanded={channelsExpand.expanded}
                  onChange={channelsExpand.setExpanded}
                  label="channel messages"
                />
              )}
            </>}
          >
            {pendingMessages.length > 0 ? (
              <>
                <div className="channel-message-list">
                  {pendingMessages.map((message) => (
                    <div className="channel-message-row" key={`${message.channelName}:${message.messageId}`}>
                      <div className="channel-message-main">
                        <div className="channel-message-meta">
                          <b>{message.channelName}</b>
                          <code>{message.messageId}</code>
                        </div>
                        <JsonView
                          value={message.value}
                          label="Value"
                          variant="inline"
                          persistKey={`overview:channel-message:${message.messageId}`}
                          expandValues={channelsExpand.expanded}
                        />
                      </div>
                      {onDeleteChannelMessage && (
                        <button
                          className="button quiet danger"
                          disabled={deletingChannelMessage === message.messageId}
                          onClick={() => void onDeleteChannelMessage(
                            message.channelName,
                            message.messageId,
                          )}
                          type="button"
                        >
                          <Trash2 aria-hidden="true" size={15} strokeWidth={2} />
                          {deletingChannelMessage === message.messageId ? 'Deleting…' : 'Delete'}
                        </button>
                      )}
                    </div>
                  ))}
                </div>
                <p className="overview-note">
                  Temporal deletes are atomic. Cadence deletion is best effort and may race consumption.
                </p>
                <JsonView
                  value={state.pendingChannelMessages}
                  label="All pending messages by Channel"
                  persistKey="overview:pending-channels"
                  persistenceKind="channels"
                  forceOpen={channelsExpand.expanded || undefined}
                  collapseNonce={channelsExpand.collapseNonce}
                  expandValues={channelsExpand.expanded}
                />
              </>
            ) : (
              <p className="overview-quiet">No pending channel messages.</p>
            )}
          </OverviewSection>
        )}

        {state && state.queuedSteps.length > 0 && (
          <OverviewSection title="Queued steps" count={state.queuedSteps.length}>
            <JsonView value={state.queuedSteps} label="Queued steps" variant="inline" persistKey="overview:queued-steps" />
          </OverviewSection>
        )}
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
        {state && state.queuedSteps.length === 0 && state.completedSteps.length === 0 && (
          <p className="overview-quiet overview-quiet-line">No queued steps · No completed outputs</p>
        )}

        <OverviewSection title={isContinuedStart ? 'Continued start input' : 'Start input'}>
          {started ? (
            <div className="semantic-event">
              <SemanticEventDetails
                event={started}
                parentFlowId={summary.flowId}
                showStartHeading={false}
              />
            </div>
          ) : (
            <p className="overview-quiet">The start event is not in the loaded history page.</p>
          )}
        </OverviewSection>
      </div>

      <div className="run-overview-side">
        <OverviewSection title="Details">
          <FlowRouting routing={routing} flowType={summary.flowType} showFlowType />
          <Fields compact values={[
            ['Request ID', summary.requestId || undefined],
            ['First run ID', summary.firstRunId && summary.firstRunId !== summary.runId ? summary.firstRunId : undefined],
          ]} />
          {flowConfig && (
            <JsonView value={flowConfig} label="Flow config" persistKey="overview:flow-config" />
          )}
        </OverviewSection>

        <HistorySettingsCard
          summary={summary}
          flowConfig={state ? { ...state.flowConfig, ...savedHistorySettings } : undefined}
          onSaved={onHistorySettingsSaved}
        />
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
          ['From', step.fromStepExecutionId || '—'],
        ]} />
        <StepMethodInvocation value={step.waitForInvocation} label="WaitFor invocation" />
        <StepMethodInvocation value={step.executeInvocation} label="Execute invocation" />
        {isPullMode && <>
          <PullRouting value={step.waitForPullRouting} label="WaitFor routing" />
          <PullRouting value={step.executePullRouting} label="Execute routing" />
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

function withoutIgnoredFlowConfig(flowConfig: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(flowConfig).filter(([key]) => !ignoredFlowConfigKeys.has(key)));
}

function statusTone(status: string): string {
  return status.toLowerCase().replaceAll(' ', '-');
}

interface PendingChannelEntry {
  channelName: string;
  messageId: string;
  value: unknown;
}

function pendingChannelEntries(channels: Record<string, unknown>): PendingChannelEntry[] {
  return Object.entries(channels).flatMap(([channelName, entry]) => {
    if (typeof entry !== 'object' || entry === null) return [];
    const messages = 'messages' in entry ? (entry as { messages?: unknown }).messages : undefined;
    if (!Array.isArray(messages)) return [];
    return messages.flatMap((message) => {
      if (typeof message !== 'object' || message === null) return [];
      const envelope = message as { messageId?: unknown; value?: unknown };
      if (typeof envelope.messageId !== 'string' || envelope.messageId.length === 0) return [];
      return [{ channelName, messageId: envelope.messageId, value: envelope.value }];
    });
  });
}
