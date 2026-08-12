// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import { useState } from 'react';
import {
  isBlobReferenceValue,
  isStoredValueUnavailable,
  storedValueJSONReplacer,
} from '@/lib/blobs';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  STEP_EVENT_INPUT_UNAVAILABLE,
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
  grpcStatusLabel,
  waitForFailurePolicyLabel,
  waitingConditionTypeLabel,
} from '@/lib/semantic';
import { formatDate, type TimezonePreference } from '@/lib/format';
import { formatElapsedDuration } from '@/lib/timeline';
import { findSourceStepOptions } from '@/lib/stepOptions';
import { usePreferences } from '@/app/providers';

type Data = Record<string, unknown>;
type Field = [label: string, value: unknown, wide?: boolean];

const eventTitles: Record<FlowHistoryEvent['type'], string> = {
  FlowStartedOrContinued: 'Flow started',
  FlowClosed: 'Flow closed',
  StepWaitForCompleted: 'WaitForCondition started',
  StepWaitForFailed: 'WaitFor failed',
  StepWaitForPending: 'WaitFor pending',
  StepExecuteCompleted: 'Execute completed',
  StepExecuteFailed: 'Execute failed',
  StepExecutePending: 'Execute pending',
  RpcExecutionCompleted: 'RPC completed',
  ChannelExternalPublish: 'Channel published',
};

export function eventTitle(event: FlowHistoryEvent): string {
  if (event.type === 'FlowStartedOrContinued' && hasData(asData(event.payload.continuedStart))) {
    return 'Flow continued';
  }
  return eventTitles[event.type];
}

function asData(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

function asDataArray(value: unknown): Data[] {
  return Array.isArray(value) ? value.map(asData) : [];
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function hasData(value: Data): boolean {
  return Object.keys(value).length > 0;
}

function isPresent(value: unknown): boolean {
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

function displayValue(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return JSON.stringify(value);
}

function DetailSection({ title, children }: { title?: string; children: React.ReactNode }) {
  return (
    <section className="semantic-section">
      {title && <h4>{title}</h4>}
      {children}
    </section>
  );
}

function Fields({
  values,
  compact = false,
  stacked = false,
}: {
  values: Field[];
  compact?: boolean;
  stacked?: boolean;
}) {
  const visible = values.filter(([, value]) => isPresent(value));
  if (visible.length === 0) return null;
  const classes = [
    'semantic-fields',
    compact ? 'semantic-fields-compact' : '',
    stacked ? 'semantic-fields-stacked' : '',
  ].filter(Boolean).join(' ');
  return (
    <dl className={classes}>
      {visible.map(([label, value, wide]) => (
        <div className={wide ? 'semantic-field-wide' : undefined} key={label}>
          <dt>{label}</dt>
          <dd>{displayValue(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

function ValueBlock({
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
  return (
    <div className="semantic-value">
      <span>{label}</span>
      {decoded && typeof decoded === 'object'
        ? <pre>{JSON.stringify(decoded, null, 2)}</pre>
        : <code>{displayValue(decoded)}</code>}
    </div>
  );
}

function KeyValues({ values, emptyLabel }: { values: unknown; emptyLabel?: string }) {
  const entries = asDataArray(values);
  if (entries.length === 0) return emptyLabel ? <p className="muted">{emptyLabel}</p> : null;
  return (
    <div className="semantic-records">
      {entries.map((entry, index) => (
        <div className="semantic-record" key={`${String(entry.key)}-${index}`}>
          <strong>{displayValue(entry.key)}</strong>
          <ValueBlock label="Value" value={entry.value} />
        </div>
      ))}
    </div>
  );
}

function ChannelMessages({ values }: { values: unknown }) {
  const messages = asDataArray(values);
  if (messages.length === 0) return null;
  return (
    <div className="semantic-records">
      {messages.map((message, index) => (
        <div className="semantic-record channel-record" key={`${String(message.channelName)}-${index}`}>
          <strong><ChannelIcon />{displayValue(message.channelName)}</strong>
          <ValueBlock label="Message" value={message.value} />
        </div>
      ))}
    </div>
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

function StepOutputs({ values }: { values: unknown }) {
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

function StepOptionsView({ value }: { value: unknown }) {
  const options = asData(value);
  if (!hasData(options)) return null;
  return (
    <div className="semantic-subsection">
      <h5>Options</h5>
      <Fields values={[
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

function StepMovements({ values, showFrom = true }: { values: unknown; showFrom?: boolean }) {
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

function FlowConfigView({ value }: { value: unknown }) {
  const config = asData(value);
  if (!hasData(config)) return null;
  return (
    <DetailSection title="Flow configuration">
      <Fields values={[
        ['Step durability', isPresent(config.stepDurability) ? durabilityLabel(config.stepDurability) : undefined],
        ['Active step search', isPresent(config.activeStepSearchMode) ? activeStepSearchModeLabel(config.activeStepSearchMode) : undefined],
        ['Continue-as-new threshold', config.continueAsNewThreshold],
        ['Continue-as-new page size', bytes(config.continueAsNewPageSizeInBytes)],
      ]} />
    </DetailSection>
  );
}

function bytes(value: unknown): string | undefined {
  return isPresent(value) ? `${String(value)} bytes` : undefined;
}

function WaitingConditionView({ value }: { value: unknown }) {
  return (
    <DetailSection title="WaitFor condition">
      <WaitingConditionContent value={value} />
    </DetailSection>
  );
}

function WaitingConditionContent({ value }: { value: unknown }) {
  const { timezone } = usePreferences();
  const condition = asData(value);
  if (!hasData(condition)) return null;
  const channels = asDataArray(condition.channelConditions);
  const timers = asDataArray(condition.timerConditions);
  const combinations = asDataArray(condition.conditionCombinations);
  return (
    <>
      <Fields values={[[
        'Completion rule',
        waitingConditionTypeLabel(condition.waitingConditionType),
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

function unixTime(value: unknown, timezone: TimezonePreference): string | undefined {
  if (!isPresent(value)) return undefined;
  const timestamp = Number(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return undefined;
  return formatDate(new Date(timestamp * 1000).toISOString(), timezone);
}

function ConditionResultsContent({ value, showEmpty = false }: { value: unknown; showEmpty?: boolean }) {
  const results = asData(value);
  if (!hasData(results)) {
    return showEmpty ? <p className="muted">No condition results</p> : null;
  }
  const channels = asDataArray(results.channelResults);
  const timers = asDataArray(results.timerResults);
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
      </div>
    </>
  );
}

function StepDecisionView({ value }: { value: unknown }) {
  return (
    <DetailSection title="Step decision">
      <StepDecisionContent value={value} />
    </DetailSection>
  );
}

function StepDecisionContent({ value }: { value: unknown }) {
  const decision = asData(value);
  if (!hasData(decision)) return null;
  const close = asData(decision.closeDecision);
  return (
    <>
      <StepMovements values={decision.nextSteps} showFrom={false} />
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
    </>
  );
}

export function FailureContent({
  value,
  stackInitiallyExpanded = false,
}: {
  value: unknown;
  stackInitiallyExpanded?: boolean;
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
        ['Attempt', failure.attempt],
        ['Error type', backendError],
        ['Detail', serverDetail],
        ['Worker error type', details.originalWorkerErrorType],
        ['Worker error detail', workerDetail],
        ['Worker gRPC status', workerStatus],
      ]} />
      {isPresent(details.originalWorkerErrorStackTrace) && (
        <details className="failure-stack" open={stackInitiallyExpanded}>
          <summary>Stack trace</summary>
          <pre>{String(details.originalWorkerErrorStackTrace)}</pre>
        </details>
      )}
    </div>
  );
}

function EffectsView({ value }: { value: Data }) {
  return (
    <DetailSection title="Side effects">
      <EffectsContent value={value} />
    </DetailSection>
  );
}

function EffectsContent({ value }: { value: Data }) {
  const attributes = value.upsertAttributes;
  const events = value.recordEvents;
  const channels = value.publishToChannel;
  const locals = value.upsertStepExecutionLocals;
  if (![attributes, events, channels, locals].some((entry) => Array.isArray(entry) && entry.length > 0)) return null;
  return (
    <>
      {Array.isArray(attributes) && attributes.length > 0 && (
        <div className="semantic-subsection"><h5>Upsert attributes</h5><KeyValues values={attributes} /></div>
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
    </>
  );
}

function StepMethodOptionsView({
  value,
  stepOptions,
  isWaitFor,
}: {
  value: unknown;
  stepOptions: unknown;
  isWaitFor: boolean;
}) {
  const options = asData(value);
  const movementOptions = asData(stepOptions);
  if (!hasData(options) && !hasData(movementOptions)) return null;
  const policy = asData(options.retryPolicy);
  const failurePolicy = isWaitFor
    ? movementOptions.waitForFailurePolicy
    : movementOptions.executeFailurePolicy;
  const lockingAttributes = isWaitFor
    ? movementOptions.waitForLockAttributeKeys
    : movementOptions.executeLockAttributeKeys;
  return (
    <div className="semantic-subsection">
      <h5>Step options</h5>
      <Fields compact values={[
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
      ]} />
    </div>
  );
}

function StepMethodDetails({
  event,
  history,
}: {
  event: FlowHistoryEvent;
  history: FlowHistoryEvent[];
}) {
  const { timezone } = usePreferences();
  const payload = event.payload;
  const input = asData(payload.input);
  const output = asData(payload.output);
  const context = asData(payload.context);
  const lastFailure = asData(context.lastFailureInfo);
  const isWaitFor = event.type.startsWith('StepWaitFor');
  const isPending = event.type.endsWith('Pending');
  const hasInput = payload.input !== undefined && input.unavailable !== true;
  return (
    <>
      <DetailSection title="Input">
        {input.unavailable === true ? (
          <p className="muted">{STEP_EVENT_INPUT_UNAVAILABLE}</p>
        ) : hasInput ? (
          <>
            <ValueBlock label="Step input" value={input.stepInput} showEmpty />
            {!isWaitFor && (
              <div className="semantic-subsection">
                <h5>Condition results</h5>
                <ConditionResultsContent value={input.conditionResults} showEmpty />
              </div>
            )}
            <div className="semantic-subsection">
              <h5>Attributes</h5>
              <KeyValues values={input.attributes} emptyLabel="No attributes" />
            </div>
            {!isWaitFor && Array.isArray(input.stepExecutionLocals) && input.stepExecutionLocals.length > 0 && (
              <div className="semantic-subsection">
                <h5>Step locals</h5>
                <KeyValues values={input.stepExecutionLocals} />
              </div>
            )}
          </>
        ) : <p className="muted">No input</p>}
      </DetailSection>
      <DetailSection title={isPending ? 'Status' : 'Output'}>
        {isPending && <Fields values={[['Activity phase', pendingPhaseLabel(payload.phase)]]} />}
        {isWaitFor && hasData(asData(output.waitForCondition)) ? (
          <div className="semantic-subsection">
            <h5>WaitFor condition</h5>
            <WaitingConditionContent value={output.waitForCondition} />
          </div>
        ) : !isWaitFor && hasData(asData(output.stepDecision)) ? (
          <div className="semantic-subsection">
            <h5>Step decision</h5>
            <StepDecisionContent value={output.stepDecision} />
          </div>
        ) : null}
        <EffectsContent value={output} />
        {hasData(asData(output.failure)) && (
          <div className="semantic-subsection">
            <h5>Failure</h5>
            <FailureContent value={output.failure} />
          </div>
        )}
      </DetailSection>
      <DetailSection title="Context">
        <Fields compact values={[
          ['Execution ID', context.stepExecutionId],
          ['From', context.fromStepExecutionId],
          ['Durability', durabilityLabel(context.durability)],
          ['Final attempt', context.finalAttempt],
          ['Started', isPresent(context.startedTime) ? formatDate(String(context.startedTime), timezone) : undefined],
          ['Duration', protobufDuration(context.duration)],
        ]} />
        <StepMethodOptionsView
          value={context.methodOptions}
          stepOptions={findSourceStepOptions(event, history)}
          isWaitFor={isWaitFor}
        />
        {hasData(lastFailure) && (
          <div className="semantic-subsection">
            <h5>Last failure</h5>
            <FailureContent value={lastFailure} />
          </div>
        )}
      </DetailSection>
    </>
  );
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

function InitialStartDetails({ payload, showHeading = true }: { payload: Data; showHeading?: boolean }) {
  const start = asData(payload.initialStart);
  return (
    <>
      <DetailSection title={showHeading ? 'Initial start' : undefined}>
        <Fields values={[
          ['Flow timeout', protobufDuration(payload.flowTimeout) ?? 'No timeout'],
          ['Start step', start.startStepType],
        ]} />
        <ValueBlock label="Step input" value={start.stepInput} />
        <StepOptionsView value={start.stepOptions} />
      </DetailSection>
      {Array.isArray(start.initialAttributes) && start.initialAttributes.length > 0 && (
        <DetailSection title="Initial attributes"><KeyValues values={start.initialAttributes} /></DetailSection>
      )}
      <FlowConfigView value={payload.flowConfig} />
    </>
  );
}

function ContinuedStartDetails({ payload, showHeading = true }: { payload: Data; showHeading?: boolean }) {
  const continued = asData(payload.continuedStart);
  const resumes = asDataArray(continued.stepsToResume);
  const pendingChannels = asData(continued.pendingChannelMessages);
  return (
    <>
      <DetailSection title={showHeading ? 'Continued run' : undefined}>
        <Fields values={[
          ['Previous run ID', continued.previousRunId],
          ['Flow timeout', protobufDuration(payload.flowTimeout) ?? 'No timeout'],
        ]} />
      </DetailSection>
      {Array.isArray(continued.stepsToStart) && continued.stepsToStart.length > 0 && (
        <DetailSection title="Steps to start"><StepMovements values={continued.stepsToStart} /></DetailSection>
      )}
      {resumes.length > 0 && (
        <DetailSection title="Steps to resume">
          <div className="semantic-records">
            {resumes.map((resume, index) => (
              <div className="semantic-record" key={`${String(resume.stepExecutionId)}-${index}`}>
                <Fields values={[['Execution ID', resume.stepExecutionId]]} />
                <StepMovements values={[resume.step]} />
                <WaitingConditionView value={resume.waitingCondition} />
                <KeyValues values={resume.stepExeLocals} />
              </div>
            ))}
          </div>
        </DetailSection>
      )}
      {Object.keys(pendingChannels).length > 0 && (
        <DetailSection title="Pending channels">
          <div className="semantic-records">
            {Object.entries(pendingChannels).map(([name, entry]) => (
              <div className="semantic-record channel-record" key={name}>
                <strong><ChannelIcon />{name}</strong>
                {asArray(asData(entry).values).map((value, index) => (
                  <ValueBlock label={`Message ${index + 1}`} value={value} key={index} />
                ))}
              </div>
            ))}
          </div>
        </DetailSection>
      )}
      {Array.isArray(continued.attributes) && continued.attributes.length > 0 && (
        <DetailSection title="Attributes"><KeyValues values={continued.attributes} /></DetailSection>
      )}
      {Array.isArray(continued.completedSteps) && continued.completedSteps.length > 0 && (
        <DetailSection title="Completed steps"><StepOutputs values={continued.completedSteps} /></DetailSection>
      )}
      <FlowConfigView value={payload.flowConfig} />
    </>
  );
}

function FlowClosedDetails({ payload }: { payload: Data }) {
  const errorType = isPresent(payload.errorType) ? flowErrorTypeLabel(payload.errorType) : undefined;
  return (
    <>
      <DetailSection title="Outcome">
        <Fields values={[
          ['Status', flowStatusLabel(payload.flowStatus)],
          ['Continued to run', payload.continuedToRunId],
        ]} />
      </DetailSection>
      {(isPresent(payload.errorMessage) || (errorType && errorType !== 'Unspecified')) && (
        <DetailSection title="Failure">
          <div className="semantic-alert">
            <strong>{displayValue(payload.errorMessage)}</strong>
            <Fields values={[['Type', errorType]]} />
          </div>
        </DetailSection>
      )}
      {Array.isArray(payload.results) && payload.results.length > 0 && (
        <DetailSection title="Flow results"><StepOutputs values={payload.results} /></DetailSection>
      )}
    </>
  );
}

function pendingPhaseLabel(value: unknown): string {
  if (value === 1) return 'Scheduled';
  if (value === 2) return 'Started';
  return 'Unspecified';
}

function RPCDetails({ payload }: { payload: Data }) {
  return (
    <>
      <DetailSection title="RPC call">
        <Fields values={[['RPC name', payload.rpcName]]} />
        <ValueBlock label="Input" value={payload.input} />
        <ValueBlock label="Output" value={payload.output} />
      </DetailSection>
      <StepDecisionView value={payload.stepDecision} />
      <EffectsView value={payload} />
    </>
  );
}

export function SemanticEventDetails({
  event,
  history = [event],
  showStartHeading = true,
}: {
  event: FlowHistoryEvent;
  history?: FlowHistoryEvent[];
  showStartHeading?: boolean;
}) {
  if (event.type.startsWith('StepWaitFor') || event.type.startsWith('StepExecute')) {
    return <StepMethodDetails event={event} history={history} />;
  }
  if (event.type === 'FlowStartedOrContinued') {
    return hasData(asData(event.payload.continuedStart))
      ? <ContinuedStartDetails payload={event.payload} showHeading={showStartHeading} />
      : <InitialStartDetails payload={event.payload} showHeading={showStartHeading} />;
  }
  if (event.type === 'FlowClosed') return <FlowClosedDetails payload={event.payload} />;
  if (event.type === 'RpcExecutionCompleted') return <RPCDetails payload={event.payload} />;
  return (
    <DetailSection title="Published messages">
      <ChannelMessages values={event.payload.messages} />
    </DetailSection>
  );
}

export function EventDetails({
  event,
  history,
}: {
  event: FlowHistoryEvent;
  history: FlowHistoryEvent[];
}) {
  const [view, setView] = useState<'details' | 'raw'>('details');
  return (
    <div className="event-details">
      <div className="event-details-content">
        <div className="event-detail-tabs" role="tablist" aria-label="Event payload view">
          <button aria-selected={view === 'details'} className={view === 'details' ? 'active' : ''} role="tab" type="button" onClick={() => setView('details')}>Details</button>
          <button aria-selected={view === 'raw'} className={view === 'raw' ? 'active' : ''} role="tab" type="button" onClick={() => setView('raw')}>Raw JSON</button>
        </div>
        {view === 'details'
          ? <div className="semantic-event"><SemanticEventDetails event={event} history={history} /></div>
          : <pre className="raw-event-json">{JSON.stringify(event.payload, storedValueJSONReplacer, 2)}</pre>}
      </div>
    </div>
  );
}
