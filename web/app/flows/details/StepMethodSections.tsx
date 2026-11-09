// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import type { ReactNode } from 'react';
import { InlineValue, isShortFlatValue, LongValue } from '@/app/components/StructuredValue';
import { AttributeRows, ChannelRows, messageChannelGroups } from './CompactStateLists';
import {
  isBlobReferenceValue,
  isStoredValueUnavailable,
} from '@/lib/blobs';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  ASYNC_STEP_INPUT_SNAPSHOT_NOT_RECORDED,
  STEP_INPUT_SNAPSHOT_UNAVAILABLE,
  VALUE_BLOB_UNAVAILABLE,
} from '@/lib/unavailable';
import {
  activeStepSearchModeLabel,
  closeDecisionTypeLabel,
  conditionStatusLabel,
  durabilityLabel,
  executeFailurePolicyLabel,
  flowErrorTypeLabel,
  flowStatusLabel,
  flowTimeoutPolicyLabel,
  grpcStatusLabel,
  subFlowReusePolicyLabel,
  waitForFailurePolicyLabel,
  waitingConditionTypeLabel,
} from '@/lib/semantic';
import { formatDate, type TimezonePreference } from '@/lib/format';
import { formatElapsedDuration } from '@/lib/timeline';
import { findSourceStepOptions } from '@/lib/stepOptions';
import { generatedSubFlowID } from '@/lib/subflows';
import { usePreferences } from '@/app/providers';

export type Data = Record<string, unknown>;

/** Long Attribute values in event details preview about one line before Show all. */
const eventValuePreviewCharacters = 64;
export type Field = [label: string, value: unknown, wide?: boolean];

export function asData(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

export function asDataArray(value: unknown): Data[] {
  return Array.isArray(value) ? value.map(asData) : [];
}

export function hasData(value: Data): boolean {
  return Object.keys(value).length > 0;
}

export function isPresent(value: unknown): boolean {
  return value !== undefined && value !== null && value !== '';
}

function decodedValue(value: unknown): unknown {
  if (isBlobReferenceValue(value)) return 'Loading stored value…';
  if (isStoredValueUnavailable(value)) return VALUE_BLOB_UNAVAILABLE;
  const message = asData(value);
  if ('stringValue' in message) return message.stringValue;
  if ('intValue' in message) return message.intValue;
  if ('doubleValue' in message) return message.doubleValue;
  if ('boolValue' in message) return message.boolValue;
  if ('nullValue' in message) return null;
  const object = asData(message.objValue);
  if (typeof object.payload !== 'string') return value;
  try {
    const bytes = Uint8Array.from(atob(object.payload), (character) => character.charCodeAt(0));
    const decoded = new TextDecoder().decode(bytes);
    if (object.encoding === 'json') {
      try {
        return JSON.parse(decoded) as unknown;
      } catch {
        return decoded;
      }
    }
    return decoded;
  } catch {
    return value;
  }
}

export function displayValue(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return JSON.stringify(value);
}

export function DetailSection({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="semantic-section">
      {title && <h4>{title}</h4>}
      {children}
    </section>
  );
}

export function Fields({
  values,
  compact = false,
  stacked = false,
  inline = false,
}: {
  values: Field[];
  compact?: boolean;
  stacked?: boolean;
  /** Puts each label beside its value, for short scalar lists. */
  inline?: boolean;
}) {
  const visible = values.filter(([, value]) => isPresent(value));
  if (visible.length === 0) return null;
  const classes = [
    'semantic-fields',
    compact ? 'semantic-fields-compact' : '',
    stacked ? 'semantic-fields-stacked' : '',
    inline ? 'semantic-fields-inline' : '',
  ].filter(Boolean).join(' ');
  return (
    <dl className={classes}>
      {visible.map(([label, value, wide]) => (
        <div className={wide ? 'semantic-field-wide' : undefined} key={label}>
          <dt>{label}</dt>
          <dd title={displayValue(value) === '—' ? undefined : displayValue(value)}>{displayValue(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

export function ValueBlock({
  label,
  value,
  showEmpty = false,
}: {
  label: string;
  value: unknown;
  showEmpty?: boolean;
}) {
  if (!isPresent(value) && !showEmpty) return null;
  const decoded = decodedValue(value);
  if (decoded && typeof decoded === 'object' && !isShortFlatValue(decoded)) {
    return (
      <div className="semantic-value">
        <span>{label}</span>
        <LongValue text={JSON.stringify(decoded, null, 2)} isBlock />
      </div>
    );
  }
  return (
    <div className="semantic-value semantic-value-inline">
      <span>{label}</span>
      <span className="semantic-value-content">
        {decoded && typeof decoded === 'object'
          ? <code>{JSON.stringify(decoded)}</code>
          : <LongValue text={displayValue(decoded)} isBlock={false} />}
      </span>
    </div>
  );
}

export function KeyValues({ values, emptyLabel }: { values: unknown; emptyLabel?: string }) {
  const entries = asDataArray(values);
  if (entries.length === 0) return emptyLabel ? <p className="muted">{emptyLabel}</p> : null;
  return <AttributeRows attributes={entries} characterLimit={eventValuePreviewCharacters} expandValues={false} />;
}

/** Lists event Channel messages grouped by Channel, each with its decoded value and short message ID. */
export function ChannelMessages({ values }: { values: unknown }) {
  const channels = messageChannelGroups(values);
  if (channels.length === 0) return null;
  return (
    <ChannelRows channels={channels} expandValues={false} />
  );
}

function ChannelIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
}

export function StepOutputs({ values }: { values: unknown }) {
  const outputs = asDataArray(values);
  if (outputs.length === 0) return null;
  return (
    <div className="semantic-records">
      {outputs.map((output, index) => (
        <div className="semantic-record" key={`${String(output.completedStepExecutionId)}-${index}`}>
          <Fields values={[
            ['Step', output.completedStepType],
            ['Execution ID', output.completedStepExecutionId],
          ]} />
          <ValueBlock label="Output" value={output.completedStepOutput} />
        </div>
      ))}
    </div>
  );
}

export function StepOptionsView({ value }: { value: unknown }) {
  const options = asData(value);
  if (!hasData(options)) return null;
  return (
    <div className="semantic-subsection">
      <h5>Options</h5>
      <Fields inline values={[
        ['WaitFor timeout', seconds(options.waitForTimeoutSeconds)],
        ['Execute timeout', seconds(options.executeTimeoutSeconds)],
        ['Skip WaitFor', options.skipWaitFor],
        ['WaitFor durability', isPresent(options.waitForDurabilityOverride) ? durabilityLabel(options.waitForDurabilityOverride) : undefined],
        ['Execute durability', isPresent(options.executeDurabilityOverride) ? durabilityLabel(options.executeDurabilityOverride) : undefined],
        ['WaitFor failure', isPresent(options.waitForFailurePolicy) ? waitForFailurePolicyLabel(options.waitForFailurePolicy) : undefined],
        ['Execute failure', isPresent(options.executeFailurePolicy) ? executeFailurePolicyLabel(options.executeFailurePolicy) : undefined],
        ['Failure proceeds to', options.executeFailureProceedStepType],
        ['WaitFor locks', listText(options.waitForLockAttributeKeys)],
        ['Execute locks', listText(options.executeLockAttributeKeys)],
        ['WaitFor AttributeMap loads', listText(options.waitForLoadAttributeMapInstances)],
        ['WaitFor Channel loads', listText(options.waitForLoadChannelNames)],
        ['WaitFor ChannelMap loads', listText(options.waitForLoadChannelMapInstances)],
        ['Execute AttributeMap loads', listText(options.executeLoadAttributeMapInstances)],
        ['Execute Channel loads', listText(options.executeLoadChannelNames)],
        ['Execute ChannelMap loads', listText(options.executeLoadChannelMapInstances)],
      ]} />
    </div>
  );
}

function seconds(value: unknown): string | undefined {
  return isPresent(value) ? `${String(value)}s` : undefined;
}

function listText(value: unknown): string | undefined {
  return Array.isArray(value) && value.length > 0 ? value.map(String).join(', ') : undefined;
}

export function StepMovements({ values, showFrom = true }: { values: unknown; showFrom?: boolean }) {
  const movements = asDataArray(values);
  if (movements.length === 0) return null;
  return (
    <div className="semantic-records">
      {movements.map((movement, index) => {
        const fields: Field[] = [['Step type', movement.stepType]];
        if (showFrom) fields.push(['From', movement.fromStepExecutionIdInternalOnly]);
        return (
          <div className="semantic-record" key={`${String(movement.stepType)}-${index}`}>
            <Fields compact values={fields} />
            <ValueBlock label="Input" value={movement.stepInput} />
            <StepOptionsView value={movement.stepOptions} />
          </div>
        );
      })}
    </div>
  );
}

export function FlowConfigView({ value }: { value: unknown }) {
  const config = asData(value);
  if (!hasData(config)) return null;
  return (
    <DetailSection title="Flow configuration">
      <Fields values={[
        ['Step durability', isPresent(config.stepDurability) ? durabilityLabel(config.stepDurability) : undefined],
        ['Active step search', isPresent(config.activeStepSearchMode) ? activeStepSearchModeLabel(config.activeStepSearchMode) : undefined],
      ]} />
    </DetailSection>
  );
}

export function FlowTimeoutHandlerOptionsView({ value }: { value: unknown }) {
  const options = asData(value);
  if (!hasData(options)) return null;
  const retry = asData(options.retryPolicy);
  return (
    <DetailSection title="Timeout handler options">
      <Fields values={[
        ['Method timeout', seconds(options.methodTimeoutSeconds)],
        ['Heartbeat timeout', seconds(options.heartbeatTimeoutSeconds)],
        ['Retry initial interval', seconds(retry.initialIntervalSeconds)],
        ['Retry backoff coefficient', retry.backoffCoefficient],
        ['Retry maximum interval', seconds(retry.maximumIntervalSeconds)],
        ['Retry maximum attempts', retry.maximumAttempts],
        ['Retry total duration', seconds(retry.totalDurationSeconds)],
        ['Failure policy', isPresent(options.failurePolicy) ? executeFailurePolicyLabel(options.failurePolicy) : undefined],
        ['Failure proceeds to', options.failureProceedStepType],
        ['Durability', isPresent(options.durabilityOverride) ? durabilityLabel(options.durabilityOverride) : undefined],
        ['Locking attributes', listText(options.lockAttributeKeys)],
        ['AttributeMap loads', listText(options.loadAttributeMapInstances)],
        ['Channel loads', listText(options.loadChannelNames)],
        ['ChannelMap loads', listText(options.loadChannelMapInstances)],
      ]} />
      <StepOptionsView value={options.failureProceedStepOptions} />
    </DetailSection>
  );
}

export function WaitingConditionView({
  value,
  parentFlowId,
  stepExecutionId,
}: {
  value: unknown;
  parentFlowId: string;
  stepExecutionId: string;
}) {
  return (
    <DetailSection title="WaitFor condition">
      <WaitingConditionContent
        value={value}
        parentFlowId={parentFlowId}
        stepExecutionId={stepExecutionId}
      />
    </DetailSection>
  );
}

function WaitingConditionContent({
  value,
  parentFlowId = '',
  stepExecutionId = '',
  showEmpty = false,
}: {
  value: unknown;
  parentFlowId?: string;
  stepExecutionId?: string;
  showEmpty?: boolean;
}) {
  const { timezone } = usePreferences();
  const condition = asData(value);
  if (!hasData(condition) && !showEmpty) return null;
  const channels = asDataArray(condition.channelConditions);
  const timers = asDataArray(condition.timerConditions);
  const subFlows = asDataArray(condition.subFlowConditions);
  const combinations = asDataArray(condition.conditionCombinations);
  return (
    <>
      <Fields values={[[
        'Completion rule',
        waitingConditionCompletionRule(condition, channels.length + timers.length + subFlows.length),
      ]]} />
      {channels.length > 0 && (
        <div className="semantic-records">
          {channels.map((channel, index) => (
            <div className="semantic-record channel-record" key={`${String(channel.channelName)}-${index}`}>
              <strong><ChannelIcon />{displayValue(channel.channelName)}</strong>
              <Fields values={[
                ['Condition ID', channel.conditionId],
                ['At least', channel.atLeast],
                ['At most', channel.atMost],
              ]} />
            </div>
          ))}
        </div>
      )}
      {timers.length > 0 && (
        <div className="semantic-records">
          {timers.map((timer, index) => (
            <div className="semantic-record" key={`${String(timer.conditionId)}-${index}`}>
              <strong>Timer {index + 1}</strong>
              <Fields values={[
                ['Condition ID', timer.conditionId],
                ['Delay', seconds(timer.durationSeconds)],
                ['Fires at', unixTime(timer.firingUnixTimestampSeconds, timezone)],
              ]} />
            </div>
          ))}
        </div>
      )}
      {subFlows.length > 0 && (
        <div className="semantic-records sub-flow-records">
          {subFlows.map((subFlow, index) => (
            <SubFlowRecord
              value={subFlow}
              index={index}
              parentFlowId={parentFlowId}
              stepExecutionId={stepExecutionId}
              key={`${parentFlowId}-${stepExecutionId}-${index}`}
            />
          ))}
        </div>
      )}
      {combinations.length > 0 && (
        <div className="semantic-subsection">
          <h5>Condition combinations</h5>
          {combinations.map((combination, index) => (
            <p key={index}>{listText(combination.conditionIds) || '—'}</p>
          ))}
        </div>
      )}
    </>
  );
}

function waitingConditionCompletionRule(condition: Data, conditionCount: number): string {
  if (conditionCount === 0) return 'Empty condition — skips WaitFor immediately';
  if (conditionCount === 1) return 'Single condition';
  return waitingConditionTypeLabel(condition.waitingConditionType);
}

function unixTime(value: unknown, timezone: TimezonePreference): string | undefined {
  if (!isPresent(value)) return undefined;
  const timestamp = Number(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return undefined;
  return formatDate(new Date(timestamp * 1000).toISOString(), timezone);
}

function ConditionResultsContent({
  value,
  subFlowConditions = [],
  parentFlowId = '',
  stepExecutionId = '',
  showEmpty = false,
}: {
  value: unknown;
  subFlowConditions?: Data[];
  parentFlowId?: string;
  stepExecutionId?: string;
  showEmpty?: boolean;
}) {
  const results = asData(value);
  if (!hasData(results)) {
    return showEmpty ? <p className="muted">No condition results</p> : null;
  }
  const channels = asDataArray(results.channelResults);
  const timers = asDataArray(results.timerResults);
  const subFlows = asDataArray(results.subFlowResults);
  return (
    <>
      {results.waitForFailed === true && <p className="semantic-alert">WaitFor failed</p>}
      <div className="semantic-records">
        {channels.map((channel, index) => (
          <div className="semantic-record channel-record" key={`${String(channel.channelName)}-${index}`}>
            <strong><ChannelIcon />{displayValue(channel.channelName)}</strong>
            <Fields values={[
              ['Condition ID', channel.conditionId],
              ['Status', conditionStatusLabel(channel.conditionStatus)],
            ]} />
            {Array.isArray(channel.values) && channel.values.map((entry, valueIndex) => (
              <ValueBlock label={`Value ${valueIndex + 1}`} value={entry} key={valueIndex} />
            ))}
          </div>
        ))}
        {timers.map((timer, index) => (
          <div className="semantic-record" key={`${String(timer.conditionId)}-${index}`}>
            <strong>Timer {index + 1}</strong>
            <Fields values={[
              ['Condition ID', timer.conditionId],
              ['Status', conditionStatusLabel(timer.conditionStatus)],
            ]} />
          </div>
        ))}
        {subFlows.map((subFlow, index) => (
          <SubFlowResultRecord
            value={subFlow}
            condition={subFlowConditions[index]}
            parentFlowId={parentFlowId}
            stepExecutionId={stepExecutionId}
            index={index}
            key={index}
          />
        ))}
      </div>
    </>
  );
}

function SubFlowRecord({
  value,
  index,
  parentFlowId,
  stepExecutionId,
}: {
  value: Data;
  index: number;
  parentFlowId: string;
  stepExecutionId: string;
}) {
  const options = asData(value.options);
  const retry = asData(options.retryPolicy);
  const flowId = generatedSubFlowID(parentFlowId, stepExecutionId, index);
  return (
    <a
      className="semantic-record sub-flow-record"
      href={flowId ? `/v1/flows/${encodeURIComponent(flowId)}` : '#'}
      aria-label={`Open SubFlow ${flowId || index + 1}`}
    >
      <strong><SubFlowIcon /><code>{flowId}</code></strong>
      <Fields values={[
        ['Condition ID', value.conditionId],
        ['Reuse policy', subFlowReusePolicyLabel(options.reusePolicy)],
        ['Timeout', seconds(options.flowTimeoutSeconds)],
        ['Timeout policy', Number(options.flowTimeoutSeconds) > 0
          ? specifiedTimeoutPolicy(options.flowTimeoutPolicy)
          : undefined],
        ['Start delay', seconds(options.flowStartDelaySeconds)],
        ['Retry initial interval', seconds(retry.initialIntervalSeconds)],
        ['Retry backoff coefficient', retry.backoffCoefficient],
        ['Retry maximum interval', seconds(retry.maximumIntervalSeconds)],
        ['Retry maximum attempts', retry.maximumAttempts],
      ]} />
      <ValueBlock label="Input" value={value.stepInput} />
      <ValueBlock label="Starting Step options" value={value.stepOptions} />
      <ValueBlock label="Flow configuration override" value={options.flowConfigOverride} />
      {asDataArray(options.attributes).length > 0 && (
        <div className="semantic-subsection">
          <h5>Initial Attributes</h5>
          <KeyValues values={options.attributes} />
        </div>
      )}
    </a>
  );
}

function SubFlowResultRecord({
  value,
  condition = {},
  parentFlowId,
  stepExecutionId,
  index,
}: {
  value: Data;
  condition?: Data;
  parentFlowId: string;
  stepExecutionId: string;
  index: number;
}) {
  const flowId = generatedSubFlowID(parentFlowId, stepExecutionId, index);
  return (
    <a
      className="semantic-record sub-flow-record"
      href={flowId ? `/v1/flows/${encodeURIComponent(flowId)}` : '#'}
      aria-label={`Open SubFlow result ${flowId || index + 1}`}
    >
      <strong><SubFlowIcon /><code>{flowId}</code></strong>
      <Fields values={[
        ['Condition ID', condition.conditionId],
        ['Status', flowStatusLabel(value.flowStatus)],
        ['Failure type', isPresent(value.errorType) ? flowErrorTypeLabel(value.errorType) : undefined],
        ['Failure', value.errorMessage],
      ]} />
      <StepOutputs values={value.results} />
    </a>
  );
}

function SubFlowIcon() {
  return (
    <svg aria-hidden="true" className="sub-flow-icon" viewBox="0 0 16 16">
      <rect x="2" y="3" width="5" height="4" rx="1" />
      <rect x="9" y="9" width="5" height="4" rx="1" />
      <path d="M7 5h2.5a2 2 0 0 1 2 2v2" />
    </svg>
  );
}

export function StepDecisionView({ value }: { value: unknown }) {
  return (
    <DetailSection title="Step decision">
      <StepDecisionSummary value={value} />
    </DetailSection>
  );
}

/** Reads a Step decision as one row per target, with its full detail collapsed below. */
function StepDecisionSummary({ value, effects }: { value: unknown; effects?: Data }) {
  const decision = asData(value);
  if (!hasData(decision)) return null;
  const rows = decisionSummaryRows(decision);
  const effectNames = effectSummary(effects ?? {});
  return (
    <div className="decision-summary">
      <ul className="decision-summary-rows">
        {rows.map((row, index) => (
          <li key={`${row.target}-${index}`}>
            <strong>{row.target}</strong>
            {row.input !== undefined && (
              <span className="decision-summary-part">input <InlineValue value={row.input} /></span>
            )}
            {row.flags.map((flag) => <span className="decision-summary-part" key={flag}>{flag}</span>)}
            {index === rows.length - 1 && effectNames.map((effect) => (
              <span className="decision-summary-part" key={effect}>{effect}</span>
            ))}
          </li>
        ))}
      </ul>
      {hasDecisionDetail(decision) && (
        <details className="decision-summary-details">
          <summary>Decision details</summary>
          <StepDecisionContent value={decision} />
        </details>
      )}
    </div>
  );
}

interface DecisionSummaryRow {
  target: string;
  input?: unknown;
  flags: string[];
}

function decisionSummaryRows(decision: Data): DecisionSummaryRow[] {
  const rows: DecisionSummaryRow[] = asDataArray(decision.nextSteps).map((movement) => ({
    target: `→ ${displayValue(movement.stepType)}`,
    input: isPresent(movement.stepInput) ? movement.stepInput : undefined,
    flags: stepOptionFlags(asData(movement.stepOptions)),
  }));
  const flowCancels = listText(decision.cancelStepTypes);
  const siblingCancels = listText(decision.cancelSiblingStepTypes);
  if (flowCancels) rows.push({ target: `Cancel ${flowCancels}`, flags: ['entire Flow'] });
  if (siblingCancels) rows.push({ target: `Cancel ${siblingCancels}`, flags: ['same parent execution'] });
  const close = asData(decision.closeDecision);
  if (hasData(close)) {
    rows.push({
      target: closeDecisionTypeLabel(close.closeDecisionType),
      input: isPresent(close.closeInput) ? close.closeInput : undefined,
      flags: listText(close.conditionalChannelNames) ? [`channels ${listText(close.conditionalChannelNames)}`] : [],
    });
  }
  return rows;
}

/** Names the options a movement sets, such as Skip WaitFor or an Execute timeout. */
function stepOptionFlags(options: Data): string[] {
  const flags: string[] = [];
  if (options.skipWaitFor === true) flags.push('Skip WaitFor');
  if (isPresent(options.waitForTimeoutSeconds)) flags.push(`WaitFor timeout ${seconds(options.waitForTimeoutSeconds)}`);
  if (isPresent(options.executeTimeoutSeconds)) flags.push(`Execute timeout ${seconds(options.executeTimeoutSeconds)}`);
  if (isPresent(options.waitForDurabilityOverride)) flags.push(`WaitFor ${durabilityLabel(options.waitForDurabilityOverride)}`);
  if (isPresent(options.executeDurabilityOverride)) flags.push(`Execute ${durabilityLabel(options.executeDurabilityOverride)}`);
  if (isPresent(options.executeFailureProceedStepType)) flags.push(`on failure → ${displayValue(options.executeFailureProceedStepType)}`);
  return flags;
}

function effectSummary(effects: Data): string[] {
  const names = (values: unknown, field: string) => [...new Set(asDataArray(values)
    .map((entry) => entry[field])
    .filter((name): name is string => typeof name === 'string' && name !== ''))];
  const parts: string[] = [];
  const upserts = names(effects.upsertAttributes, 'key');
  const publishes = names(effects.publishToChannel, 'channelName');
  if (upserts.length > 0) parts.push(`upserts ${upserts.join(', ')}`);
  if (publishes.length > 0) parts.push(`publishes to ${publishes.join(', ')}`);
  return parts;
}

/** Whether the collapsed detail adds anything the summary rows do not show. */
function hasDecisionDetail(decision: Data): boolean {
  return asDataArray(decision.nextSteps).some((movement) => {
    const input = movement.stepInput;
    const decoded = decodedValue(input);
    const options = asData(movement.stepOptions);
    const setOptionCount = Object.values(options).filter((option) => isPresent(option) && option !== false).length;
    return setOptionCount > stepOptionFlags(options).length
      || (isPresent(input) && decoded !== null && typeof decoded === 'object' && !isShortFlatValue(decoded));
  });
}

function StepDecisionContent({ value }: { value: unknown }) {
  const decision = asData(value);
  if (!hasData(decision)) return null;
  const close = asData(decision.closeDecision);
  return (
    <div className="step-decision-content">
      <StepMovements values={decision.nextSteps} showFrom={false} />
      <CancellationSelectors decision={decision} />
      {hasData(close) && (
        <div className="semantic-record decision-record">
          <strong>{closeDecisionTypeLabel(close.closeDecisionType)}</strong>
          <Fields values={[[
            'Conditional channels',
            listText(close.conditionalChannelNames),
          ]]} />
          <ValueBlock label="Close input" value={close.closeInput} />
        </div>
      )}
    </div>
  );
}

function CancellationSelectors({ decision }: { decision: Data }) {
  const flowStepTypes = listText(decision.cancelStepTypes);
  const siblingStepTypes = listText(decision.cancelSiblingStepTypes);
  if (!flowStepTypes && !siblingStepTypes) return null;
  return (
    <div className="semantic-records">
      {flowStepTypes && (
        <div className="semantic-record decision-record">
          <strong>Cancel Step executions</strong>
          <Fields values={[
            ['Scope', 'Entire Flow'],
            ['Step types', flowStepTypes],
          ]} />
        </div>
      )}
      {siblingStepTypes && (
        <div className="semantic-record decision-record">
          <strong>Cancel sibling Step executions</strong>
          <Fields values={[
            ['Scope', 'Same parent execution'],
            ['Step types', siblingStepTypes],
          ]} />
        </div>
      )}
    </div>
  );
}

export function FailureContent({
  value,
  stackInitiallyExpanded = false,
  showAttempt = true,
  hiddenPullRouting,
}: {
  value: unknown;
  stackInitiallyExpanded?: boolean;
  showAttempt?: boolean;
  /** Routing already shown nearby; a failure with the same routing omits it. */
  hiddenPullRouting?: unknown;
}) {
  const failure = asData(value);
  if (!hasData(failure)) return null;
  const details = asData(failure.details);
  const backendError = typeof failure.backendError === 'string'
    && failure.backendError.startsWith('FLOW_ERROR_TYPE_')
    ? flowErrorTypeLabel(failure.backendError)
    : failure.backendError;
  const workerStatus = isPresent(details.originalWorkerErrorStatus)
    ? grpcStatusLabel(details.originalWorkerErrorStatus)
    : undefined;
  const workerDetail = isPresent(details.originalWorkerErrorDetail)
    ? details.originalWorkerErrorDetail
    : undefined;
  const serverDetail = workerDetail === undefined ? details.detail : undefined;
  return (
    <div className="semantic-alert failure-alert">
      <Fields compact stacked values={[
        ['Attempt', showAttempt ? failure.attempt : undefined],
        ['Error type', backendError],
        ['Detail', serverDetail],
        ['Dispatch failure', pullDispatchFailureLabel(details.dispatchTaskErrorReason, serverDetail)],
        ['Worker error type', details.originalWorkerErrorType],
        ['Worker error detail', workerDetail],
        ['Worker gRPC status', workerStatus],
      ]} />
      {Boolean(failure.pullRouting)
        && JSON.stringify(failure.pullRouting) !== JSON.stringify(hiddenPullRouting)
        && <PullRouting value={failure.pullRouting} />}
      {isPresent(details.originalWorkerErrorStackTrace) && (
        <details className="failure-stack" open={stackInitiallyExpanded}>
          <summary>Stack trace</summary>
          <pre>{String(details.originalWorkerErrorStackTrace)}</pre>
        </details>
      )}
    </div>
  );
}

export function EffectsView({ value }: { value: Data }) {
  return (
    <DetailSection title="Side effects">
      <EffectsContent value={value} />
    </DetailSection>
  );
}

export function EffectsContent({ value }: { value: Data }) {
  const attributes = value.upsertAttributes;
  const events = value.recordEvents;
  const channels = value.publishToChannel;
  const deletions = asDataArray(value.deleteFromChannel);
  const locals = value.upsertStepExecutionLocals;
  if (
    deletions.length === 0 &&
    ![attributes, events, channels, locals].some((entry) => Array.isArray(entry) && entry.length > 0)
  ) return null;
  return (
    <>
      {Array.isArray(attributes) && attributes.length > 0 && (
        <div className="semantic-subsection"><h5>Attributes upserted</h5><KeyValues values={attributes} /></div>
      )}
      {Array.isArray(events) && events.length > 0 && (
        <div className="semantic-subsection"><h5>Recorded events</h5><KeyValues values={events} /></div>
      )}
      {Array.isArray(locals) && locals.length > 0 && (
        <div className="semantic-subsection"><h5>Step locals</h5><KeyValues values={locals} /></div>
      )}
      {Array.isArray(channels) && channels.length > 0 && (
        <div className="semantic-subsection"><h5>Channel publishes</h5><ChannelMessages values={channels} /></div>
      )}
      {deletions.length > 0 && (
        <div className="semantic-subsection">
          <h5>Channel deletions</h5>
          <div className="semantic-records">
            {deletions.map((deletion, index) => (
              <div className="semantic-record" key={`${String(deletion.messageId)}-${index}`}>
                <Fields values={[
                  ['Channel', deletion.channelName],
                  ['Message ID', deletion.messageId],
                ]} />
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

/** The method's timeout, retry, failure, and lock options as Context fields. */
function stepMethodOptionFields(value: unknown, stepOptions: unknown, isWaitFor: boolean): Field[] {
  const options = asData(value);
  const movementOptions = asData(stepOptions);
  if (!hasData(options) && !hasData(movementOptions)) return [];
  const policy = asData(options.retryPolicy);
  const failurePolicy = isWaitFor
    ? movementOptions.waitForFailurePolicy
    : movementOptions.executeFailurePolicy;
  const lockingAttributes = isWaitFor
    ? movementOptions.waitForLockAttributeKeys
    : movementOptions.executeLockAttributeKeys;
  return [
    ['Timeout', seconds(options.timeoutSeconds)],
    ['Retry initial interval', seconds(policy.initialIntervalSeconds)],
    ['Retry backoff coefficient', policy.backoffCoefficient],
    ['Retry maximum interval', seconds(policy.maximumIntervalSeconds)],
    ['Retry maximum attempts', policy.maximumAttempts === 0 ? 'Unlimited' : policy.maximumAttempts],
    ['Retry total duration', policy.totalDurationSeconds === 0 ? 'Unlimited' : seconds(policy.totalDurationSeconds)],
    ['Failure policy', isPresent(failurePolicy)
      ? isWaitFor
        ? waitForFailurePolicyLabel(failurePolicy)
        : executeFailurePolicyLabel(failurePolicy)
      : undefined],
    ['Failure proceeds to', isWaitFor ? undefined : movementOptions.executeFailureProceedStepType],
    ['Locking attributes', listText(lockingAttributes), true],
  ];
}

export type StepMethodSectionProps = {
  event: FlowHistoryEvent;
  history: FlowHistoryEvent[];
  parentFlowId: string;
  /** When false, omit the DetailSection wrapper (v2 panel supplies its own chrome). */
  wrapSection?: boolean;
};

function stepMethodFlags(event: FlowHistoryEvent) {
  const payload = event.payload;
  const input = asData(payload.input);
  const output = asData(payload.output);
  const context = asData(payload.context);
  const isWaitFor = event.type.startsWith('StepWaitFor');
  const isPending = event.type.endsWith('Pending');
  const isAsyncTerminalFailure = event.type.endsWith('Failed')
    && durabilityLabel(context.durability) === 'async';
  const hasInput = payload.input !== undefined && input.unavailable !== true;
  const stepExecutionId = typeof context.stepExecutionId === 'string' ? context.stepExecutionId : '';
  return {
    payload,
    input,
    output,
    context,
    lastFailure: asData(context.lastFailureInfo),
    isWaitFor,
    isPending,
    isAsyncTerminalFailure,
    hasInput,
    stepExecutionId,
  };
}

function SectionShell({
  title,
  wrapSection,
  children,
}: {
  title: string;
  wrapSection: boolean;
  children: ReactNode;
}) {
  if (!wrapSection) return <>{children}</>;
  return <DetailSection title={title}>{children}</DetailSection>;
}

export function StepMethodInputSection({
  event,
  history,
  parentFlowId,
  wrapSection = true,
}: StepMethodSectionProps) {
  const {
    input,
    isWaitFor,
    isAsyncTerminalFailure,
    hasInput,
    stepExecutionId,
  } = stepMethodFlags(event);
  const subFlowConditions = findSubFlowConditions(history, stepExecutionId);
  return (
    <SectionShell title="Input" wrapSection={wrapSection}>
      {input.unavailable === true ? (
        <div className="semantic-notice">
          <strong>Step method input snapshot unavailable</strong>
          <p>
            {isAsyncTerminalFailure
              ? ASYNC_STEP_INPUT_SNAPSHOT_NOT_RECORDED
              : STEP_INPUT_SNAPSHOT_UNAVAILABLE}
          </p>
          {isAsyncTerminalFailure && (
            <p>
              Step input, attributes, condition results, and step locals are absent for this failure.
              Follow its source arrow in Timeline to inspect the event that scheduled the Step.
            </p>
          )}
        </div>
      ) : hasInput ? (
        <>
          <ValueBlock label="Step input" value={input.stepInput} />
          {!isWaitFor && (hasData(asData(input.conditionResults)) || subFlowConditions.length > 0) && (
            <div className="semantic-subsection">
              <h5>Condition results</h5>
              <ConditionResultsContent
                value={input.conditionResults}
                subFlowConditions={subFlowConditions}
                parentFlowId={parentFlowId}
                stepExecutionId={stepExecutionId}
                showEmpty
              />
            </div>
          )}
          {Array.isArray(input.attributes) && input.attributes.length > 0 && (
            <div className="semantic-subsection">
              <h5>Attributes read</h5>
              <KeyValues values={input.attributes} />
            </div>
          )}
          {!isWaitFor && Array.isArray(input.stepExecutionLocals) && input.stepExecutionLocals.length > 0 && (
            <div className="semantic-subsection">
              <h5>Step locals</h5>
              <KeyValues values={input.stepExecutionLocals} />
            </div>
          )}
        </>
      ) : <p className="muted">No input</p>}
    </SectionShell>
  );
}

export function StepMethodOutputSection({
  event,
  parentFlowId,
  wrapSection = true,
}: StepMethodSectionProps) {
  const { payload, output, context, isWaitFor, isPending } = stepMethodFlags(event);
  // The Context section already shows this method's routing.
  return (
    <SectionShell title={isPending ? 'Status' : 'Output'} wrapSection={wrapSection}>
      {isPending && <Fields values={[['Activity phase', pendingPhaseLabel(payload.phase)]]} />}
      {event.type === 'StepWaitForCompleted' ? (
        <div className="semantic-subsection">
          <h5>WaitFor condition</h5>
          <WaitingConditionContent
            value={output.waitForCondition}
            parentFlowId={parentFlowId}
            stepExecutionId={typeof context.stepExecutionId === 'string' ? context.stepExecutionId : ''}
            showEmpty
          />
        </div>
      ) : !isWaitFor && hasData(asData(output.stepDecision)) ? (
        <div className="semantic-subsection">
          <h5>Step decision</h5>
          <StepDecisionSummary value={output.stepDecision} effects={output} />
        </div>
      ) : null}
      <EffectsContent value={output} />
      {hasData(asData(output.failure)) && (
        <div className="semantic-subsection">
          <h5>Failure</h5>
          <FailureContent hiddenPullRouting={context.pullRouting} value={output.failure} />
        </div>
      )}
    </SectionShell>
  );
}

export function StepMethodContextSection({
  event,
  history,
  parentFlowId: _parentFlowId,
  wrapSection = true,
}: StepMethodSectionProps) {
  const { timezone } = usePreferences();
  const { context, lastFailure, isWaitFor } = stepMethodFlags(event);
  return (
    <SectionShell title="Context" wrapSection={wrapSection}>
      <Fields compact values={[
        ['Execution ID', context.stepExecutionId],
        ['From', context.fromStepExecutionId],
        ['Durability', durabilityLabel(context.durability)],
        ['Final attempt', context.finalAttempt],
        ['Scheduled', isPresent(context.scheduledTime) ? formatDate(String(context.scheduledTime), timezone) : undefined],
        ['Started', isPresent(context.startedTime) ? formatDate(String(context.startedTime), timezone) : undefined],
        ['Duration', protobufDuration(context.duration)],
        ...pullRoutingFields(context.pullRouting, true),
        ...stepMethodOptionFields(context.methodOptions, findSourceStepOptions(event, history), isWaitFor),
      ]} />
      {hasData(lastFailure) && (
        <div className="semantic-subsection">
          <h5>Last failure</h5>
          <FailureContent value={lastFailure} />
        </div>
      )}
    </SectionShell>
  );
}

export function StepMethodDetails({
  event,
  history,
  parentFlowId,
}: {
  event: FlowHistoryEvent;
  history: FlowHistoryEvent[];
  parentFlowId: string;
}) {
  return (
    <>
      <StepMethodInputSection event={event} history={history} parentFlowId={parentFlowId} />
      <StepMethodOutputSection event={event} history={history} parentFlowId={parentFlowId} />
      <StepMethodContextSection event={event} history={history} parentFlowId={parentFlowId} />
    </>
  );
}

function findSubFlowConditions(history: FlowHistoryEvent[], stepExecutionId: string): Data[] {
  if (!stepExecutionId) return [];
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const candidate = history[index];
    if (candidate.type !== 'StepWaitForCompleted') continue;
    const candidateContext = asData(candidate.payload.context);
    if (candidateContext.stepExecutionId !== stepExecutionId) continue;
    const output = asData(candidate.payload.output);
    return asDataArray(asData(output.waitForCondition).subFlowConditions);
  }
  return [];
}

function protobufDuration(value: unknown): string | undefined {
  if (!isPresent(value)) return undefined;
  if (typeof value === 'string') {
    const match = /^(-?\d+(?:\.\d+)?)s$/.exec(value);
    if (match) return formatElapsedDuration(Number(match[1]) * 1000);
  }
  const duration = asData(value);
  const seconds = Number(duration.seconds);
  const nanos = Number(duration.nanos);
  if (Number.isFinite(seconds) || Number.isFinite(nanos)) {
    return formatElapsedDuration(
      (Number.isFinite(seconds) ? seconds * 1000 : 0) +
      (Number.isFinite(nanos) ? nanos / 1_000_000 : 0),
    );
  }
  return displayValue(value);
}

export function flowTimeoutFields(payload: Data): Field[] {
  const duration = protobufDuration(payload.flowTimeout);
  if (!duration) {
    return [['Flow timeout', 'No timeout']];
  }
  return [
    ['Flow timeout', duration],
    ['Timeout policy', specifiedTimeoutPolicy(payload.flowTimeoutPolicy)],
  ];
}

function specifiedTimeoutPolicy(value: unknown): string | undefined {
  const label = flowTimeoutPolicyLabel(value);
  return label === 'Unspecified' ? undefined : label;
}

export function pendingPhaseLabel(value: unknown): string {
  if (value === 1) return 'Scheduled';
  if (value === 2) return 'Started';
  return 'Unspecified';
}

function pullDispatchFailureLabel(reason: unknown, value: unknown): string | undefined {
  if (reason === 1) return 'No Worker available';
  if (reason === 2) return 'Worker disconnected';
  if (reason === 3) return 'Unknown Step type: dispatch timed out';
  if (typeof value !== 'string') return undefined;
  if (value.includes('no Worker available for queue')) return 'No Worker available';
  if (value.includes('Worker disconnected during pull dispatch')) return 'Worker disconnected';
  if (value.includes('no pull Worker registered Step')) return 'Unknown Step type: dispatch timed out';
  if (value.includes('no pull Worker registered RPC')) return 'Unknown RPC name: dispatch timed out';
  return undefined;
}

/** Where a method was routed; hideEmpty leaves unknown fields empty instead of a dash. */
function pullRoutingFields(value: unknown, hideEmpty: boolean): Field[] {
  const routing = asData(value);
  const blank = hideEmpty ? undefined : '—';
  const queueSource = routing.source === 1 ? 'Sticky queue' : routing.source === 2 ? 'Task queue' : blank;
  return [
    ['Serving Worker ID', routing.servedByAppWorkerId || blank],
    ['Queue source', queueSource],
    ['Dispatch sticky Worker ID', routing.stickyAppWorkerId || blank],
  ];
}

/** Shows where a method was routed; hideEmpty omits unknown fields and an empty routing. */
export function PullRouting({ value, label = 'Pull routing', hideEmpty = false }: {
  value: unknown;
  label?: string;
  hideEmpty?: boolean;
}) {
  const values = pullRoutingFields(value, hideEmpty);
  if (hideEmpty && values.every(([, fieldValue]) => !isPresent(fieldValue))) return null;
  return <div className="semantic-subsection">
    <h5>{label}</h5>
    <Fields compact values={values} />
  </div>;
}
