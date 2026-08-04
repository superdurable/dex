// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import { useState } from 'react';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  activeStepSearchModeLabel,
  closeDecisionTypeLabel,
  conditionStatusLabel,
  durabilityLabel,
  executeFailurePolicyLabel,
  flowErrorTypeLabel,
  flowStatusLabel,
  waitForFailurePolicyLabel,
  waitingConditionTypeLabel,
} from '@/lib/semantic';

type Data = Record<string, unknown>;
type Field = [label: string, value: unknown];

const eventTitles: Record<FlowHistoryEvent['type'], string> = {
  FlowStartedOrContinued: 'Flow started',
  FlowClosed: 'Flow closed',
  StepWaitForCompleted: 'WaitForCondition started',
  StepWaitForFailed: 'WaitFor failed',
  StepExecuteCompleted: 'Execute completed',
  StepExecuteFailed: 'Execute failed',
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

function displayScalar(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return JSON.stringify(value);
}

function DetailSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="semantic-section">
      <h4>{title}</h4>
      {children}
    </section>
  );
}

function Fields({ values }: { values: Field[] }) {
  const visible = values.filter(([, value]) => isPresent(value));
  if (visible.length === 0) return null;
  return (
    <dl className="semantic-fields">
      {visible.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{displayScalar(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

function ValueBlock({ label, value }: { label: string; value: unknown }) {
  if (!isPresent(value)) return null;
  const decoded = decodedValue(value);
  return (
    <div className="semantic-value">
      <span>{label}</span>
      {decoded && typeof decoded === 'object'
        ? <pre>{JSON.stringify(decoded, null, 2)}</pre>
        : <code>{displayScalar(decoded)}</code>}
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
          <strong>{displayScalar(entry.key)}</strong>
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
          <strong><ChannelIcon />{displayScalar(message.channelName)}</strong>
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

function StepMovements({ values }: { values: unknown }) {
  const movements = asDataArray(values);
  if (movements.length === 0) return null;
  return (
    <div className="semantic-records">
      {movements.map((movement, index) => (
        <div className="semantic-record" key={`${String(movement.stepType)}-${index}`}>
          <Fields values={[
            ['Step', movement.stepType],
            ['From', movement.fromStepExecutionIdInternalOnly],
          ]} />
          <ValueBlock label="Input" value={movement.stepInput} />
          <StepOptionsView value={movement.stepOptions} />
        </div>
      ))}
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
  const condition = asData(value);
  if (!hasData(condition)) return null;
  const channels = asDataArray(condition.channelConditions);
  const timers = asDataArray(condition.timerConditions);
  const combinations = asDataArray(condition.conditionCombinations);
  return (
    <DetailSection title="WaitFor condition">
      <Fields values={[[
        'Completion rule',
        waitingConditionTypeLabel(condition.waitingConditionType),
      ]]} />
      {channels.length > 0 && (
        <div className="semantic-records">
          {channels.map((channel, index) => (
            <div className="semantic-record channel-record" key={`${String(channel.channelName)}-${index}`}>
              <strong><ChannelIcon />{displayScalar(channel.channelName)}</strong>
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
                ['Fires at', unixTime(timer.firingUnixTimestampSeconds)],
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
    </DetailSection>
  );
}

function unixTime(value: unknown): string | undefined {
  if (!isPresent(value)) return undefined;
  const timestamp = Number(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return undefined;
  return new Date(timestamp * 1000).toLocaleString();
}

function ConditionResultsView({ value }: { value: unknown }) {
  const results = asData(value);
  if (!hasData(results)) return null;
  const channels = asDataArray(results.channelResults);
  const timers = asDataArray(results.timerResults);
  return (
    <DetailSection title="Condition results">
      {results.waitForFailed === true && <p className="semantic-alert">WaitFor failed</p>}
      <div className="semantic-records">
        {channels.map((channel, index) => (
          <div className="semantic-record channel-record" key={`${String(channel.channelName)}-${index}`}>
            <strong><ChannelIcon />{displayScalar(channel.channelName)}</strong>
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
    </DetailSection>
  );
}

function StepDecisionView({ value }: { value: unknown }) {
  const decision = asData(value);
  if (!hasData(decision)) return null;
  const close = asData(decision.closeDecision);
  return (
    <DetailSection title="Step decision">
      <StepMovements values={decision.nextSteps} />
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
    </DetailSection>
  );
}

function FailureView({ value }: { value: unknown }) {
  const failure = asData(value);
  if (!hasData(failure)) return null;
  const errorType = typeof failure.errorType === 'string' && failure.errorType.startsWith('FLOW_ERROR_TYPE_')
    ? flowErrorTypeLabel(failure.errorType)
    : failure.errorType;
  return (
    <DetailSection title="Failure">
      <div className="semantic-alert">
        {isPresent(failure.message) && <strong>{displayScalar(failure.message)}</strong>}
        <Fields values={[
          ['Type', errorType],
          ['Retry state', failure.retryState],
        ]} />
        {isPresent(failure.stackTrace) && <pre>{String(failure.stackTrace)}</pre>}
      </div>
    </DetailSection>
  );
}

function EffectsView({ value }: { value: Data }) {
  const attributes = value.upsertAttributes;
  const events = value.recordEvents;
  const channels = value.publishToChannel;
  const locals = value.upsertStepExeLocals;
  if (![attributes, events, channels, locals].some((entry) => Array.isArray(entry) && entry.length > 0)) return null;
  return (
    <DetailSection title="Side effects">
      {Array.isArray(attributes) && attributes.length > 0 && (
        <div className="semantic-subsection"><h5>Attribute writes</h5><KeyValues values={attributes} /></div>
      )}
      {Array.isArray(events) && events.length > 0 && (
        <div className="semantic-subsection"><h5>Recorded events</h5><KeyValues values={events} /></div>
      )}
      {Array.isArray(locals) && locals.length > 0 && (
        <div className="semantic-subsection"><h5>Step locals</h5><KeyValues values={locals} /></div>
      )}
      {Array.isArray(channels) && channels.length > 0 && (
        <div className="semantic-subsection"><h5>Published channels</h5><ChannelMessages values={channels} /></div>
      )}
    </DetailSection>
  );
}

function StepMethodDetails({ event }: { event: FlowHistoryEvent }) {
  const payload = event.payload;
  const execution = asData(payload.execution);
  const request = asData(payload.request);
  const context = asData(request.context);
  const response = asData(payload.response);
  const previousFailures = asDataArray(execution.previousAttemptFailures);
  const isWaitFor = event.type.startsWith('StepWaitFor');
  return (
    <>
      <DetailSection title="Attempt info">
        <Fields values={[
          ['Durability', durabilityLabel(execution.durability)],
          ['Attempt', context.attempt],
          ['Final attempt', execution.finalAttempt],
          ['First attempt', context.firstAttemptTimestamp],
          ['First started', execution.firstStartedTime],
          ['Duration', execution.duration],
        ]} />
        {previousFailures.map((attempt, index) => (
          <div className="semantic-record" key={index}>
            <Fields values={[
              ['Previous attempt', attempt.attempt],
              ['Failed at', attempt.failedTime],
            ]} />
            <FailureView value={attempt.failure} />
          </div>
        ))}
      </DetailSection>
      <DetailSection title="Method execution context">
        <Fields values={[
          ['Execution ID', execution.stepExecutionId ?? context.stepExecutionId],
          ['Scheduled from', execution.fromStepExecutionId ?? context.fromStepExecutionId],
        ]} />
      </DetailSection>
      {(isPresent(request.stepInput) || Array.isArray(request.attributes) || Array.isArray(request.stepExeLocals)) && (
        <DetailSection title="Method input">
          <ValueBlock label="Step input" value={request.stepInput} />
          {Array.isArray(request.attributes) && request.attributes.length > 0 && (
            <div className="semantic-subsection"><h5>Attributes</h5><KeyValues values={request.attributes} /></div>
          )}
          {Array.isArray(request.stepExeLocals) && request.stepExeLocals.length > 0 && (
            <div className="semantic-subsection"><h5>Step locals</h5><KeyValues values={request.stepExeLocals} /></div>
          )}
        </DetailSection>
      )}
      {isWaitFor
        ? <WaitingConditionView value={response.waitingCondition} />
        : <ConditionResultsView value={request.conditionResults} />}
      <StepDecisionView value={response.stepDecision} />
      <EffectsView value={response} />
      <FailureView value={payload.failure} />
    </>
  );
}

function InitialStartDetails({ payload }: { payload: Data }) {
  const start = asData(payload.initialStart);
  return (
    <>
      <DetailSection title="Initial start">
        <Fields values={[['Start step', start.startStepType]]} />
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

function ContinuedStartDetails({ payload }: { payload: Data }) {
  const continued = asData(payload.continuedStart);
  const resumes = asDataArray(continued.stepsToResume);
  const pendingChannels = asData(continued.pendingChannelMessages);
  return (
    <>
      <DetailSection title="Continued run">
        <Fields values={[['Previous run ID', continued.previousRunId]]} />
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
            <strong>{displayScalar(payload.errorMessage)}</strong>
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

function SemanticEventDetails({ event }: { event: FlowHistoryEvent }) {
  if (event.type.startsWith('StepWaitFor') || event.type.startsWith('StepExecute')) {
    return <StepMethodDetails event={event} />;
  }
  if (event.type === 'FlowStartedOrContinued') {
    return hasData(asData(event.payload.continuedStart))
      ? <ContinuedStartDetails payload={event.payload} />
      : <InitialStartDetails payload={event.payload} />;
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
}: {
  event: FlowHistoryEvent;
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
          ? <div className="semantic-event"><SemanticEventDetails event={event} /></div>
          : <pre className="raw-event-json">{JSON.stringify(event.payload, null, 2)}</pre>}
      </div>
    </div>
  );
}
