// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { useState } from 'react';
import {
  isBlobReferenceValue,
  isStoredValueUnavailable,
  storedValueJSONReplacer,
} from '@/lib/blobs';
import { formatDate, type TimezonePreference } from '@/lib/format';
import { groupPersistenceEntries } from '@/lib/persistenceGroups';
import {
  conditionStatusLabel,
  durabilityLabel,
  waitingConditionTypeLabel,
} from '@/lib/semantic';
import { generatedSubFlowID } from '@/lib/subflows';
import { VALUE_BLOB_UNAVAILABLE } from '@/lib/unavailable';
import { usePreferences } from '@/app/providers';

type Data = Record<string, unknown>;

function asData(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

function asDataArray(value: unknown): Data[] {
  return Array.isArray(value) ? value.map(asData) : [];
}

function isPresent(value: unknown): boolean {
  return value !== undefined && value !== null && value !== '';
}

function displayValue(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return JSON.stringify(value);
}

const dexValueKeys = new Set(['stringValue', 'intValue', 'doubleValue', 'boolValue', 'nullValue', 'objValue']);

/** Whether a value is one Dex Value envelope, such as a Channel message or a stored blob reference. */
function isDexValue(value: unknown): boolean {
  if (isBlobReferenceValue(value) || isStoredValueUnavailable(value)) return true;
  const keys = Object.keys(asData(value));
  return keys.length === 1 && dexValueKeys.has(keys[0]);
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

function Fields({ values }: { values: Array<[string, unknown]> }) {
  const visible = values.filter(([, value]) => isPresent(value));
  if (visible.length === 0) return null;
  return (
    <dl className="semantic-fields semantic-fields-compact">
      {visible.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd title={displayValue(value) === '—' ? undefined : displayValue(value)}>{displayValue(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

const longValueCharacters = 320;
const longValueLines = 8;

function isLongValue(text: string, characterLimit: number): boolean {
  return text.length > characterLimit || text.split('\n').length > longValueLines;
}

function truncatedValue(text: string, characterLimit: number): string {
  const firstLines = text.split('\n').slice(0, longValueLines).join('\n');
  return `${firstLines.slice(0, characterLimit)}…`;
}

function valueSizeLabel(text: string): string {
  const bytes = new TextEncoder().encode(text).length;
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KiB`;
}

/** Shows a value, truncating a long one behind a Show all control. */
export function LongValue({
  text,
  isBlock,
  isForcedOpen = false,
  characterLimit = longValueCharacters,
}: {
  text: string;
  isBlock: boolean;
  isForcedOpen?: boolean;
  /** How many characters show before Show all; compact rows use a shorter limit. */
  characterLimit?: number;
}) {
  const [isExpanded, setIsExpanded] = useState(false);
  const isLong = isLongValue(text, characterLimit);
  const isShowingAll = !isLong || isExpanded || isForcedOpen;
  const shown = isShowingAll ? text : truncatedValue(text, characterLimit);
  return (
    <>
      {isBlock ? <pre>{shown}</pre> : <code>{shown}</code>}
      {isLong && !isForcedOpen && (
        <button
          aria-expanded={isExpanded}
          className="value-expand-button"
          onClick={() => setIsExpanded(!isExpanded)}
          type="button"
        >
          {isExpanded ? 'Show less' : `Show all · ${valueSizeLabel(text)}`}
        </button>
      )}
    </>
  );
}

const inlinePreviewCharacters = 96;
const inlineValueCharacters = 160;

/**
 * Shows one decoded value on a single line: a primitive with Show all when long, or an object or list
 * as a one-line preview that expands into its structured view.
 */
export function InlineValue({ value, expandValues = false }: { value: unknown; expandValues?: boolean }) {
  const decoded = isDexValue(value) ? decodedValue(value) : value;
  if (decoded && typeof decoded === 'object') {
    const preview = JSON.stringify(decoded, storedValueJSONReplacer) ?? '';
    return (
      <details className="inline-value-object" open={expandValues || undefined}>
        <summary>
          <code>{preview.length > inlinePreviewCharacters ? `${preview.slice(0, inlinePreviewCharacters)}…` : preview}</code>
        </summary>
        <StructuredValue value={decoded} expandValues={expandValues} />
      </details>
    );
  }
  return (
    <span className="inline-value">
      <LongValue characterLimit={inlineValueCharacters} text={displayValue(decoded)} isBlock={false} isForcedOpen={expandValues} />
    </span>
  );
}

function ValueChip({ value, expandValues = false }: { value: unknown; expandValues?: boolean }) {
  const decoded = decodedValue(value);
  if (decoded && typeof decoded === 'object') {
    return <LongValue text={JSON.stringify(decoded, storedValueJSONReplacer, 2)} isBlock isForcedOpen={expandValues} />;
  }
  return <LongValue text={displayValue(decoded)} isBlock={false} isForcedOpen={expandValues} />;
}

function unixTime(value: unknown, timezone: TimezonePreference): string | undefined {
  if (!isPresent(value)) return undefined;
  const timestamp = Number(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return undefined;
  return formatDate(new Date(timestamp * 1000).toISOString(), timezone);
}

function isWaitingCondition(value: unknown): boolean {
  const data = asData(value);
  return 'waitingConditionType' in data
    || 'channelConditions' in data
    || 'timerConditions' in data
    || 'subFlowConditions' in data;
}

function isKeyValueList(value: unknown): boolean {
  const entries = asDataArray(value);
  return entries.length > 0 && entries.every((entry) => 'key' in entry);
}

function isTimerList(value: unknown): boolean {
  const entries = asDataArray(value);
  return entries.length > 0 && entries.every((entry) => (
    'firingUnixTimestampSeconds' in entry || 'durationSeconds' in entry
  ));
}

function WaitingConditionStructured({
  value,
  parentFlowId,
  stepExecutionId,
}: {
  value: unknown;
  parentFlowId: string;
  stepExecutionId: string;
}) {
  const { timezone } = usePreferences();
  const condition = asData(value);
  const channels = asDataArray(condition.channelConditions);
  const timers = asDataArray(condition.timerConditions);
  const subFlows = asDataArray(condition.subFlowConditions);
  return (
    <div className="structured-value">
      <Fields values={[[
        'Completion rule',
        waitingConditionTypeLabel(condition.waitingConditionType),
      ]]} />
      {channels.map((channel, index) => (
        <div className="semantic-record channel-record" key={`${String(channel.channelName)}-${index}`}>
          <strong>{displayValue(channel.channelName)}</strong>
          <Fields values={[
            ['Condition ID', channel.conditionId],
            ['At least', channel.atLeast],
            ['At most', channel.atMost],
          ]} />
        </div>
      ))}
      {timers.map((timer, index) => (
        <div className="semantic-record" key={`${String(timer.conditionId)}-${index}`}>
          <strong>Timer {index + 1}</strong>
          <Fields values={[
            ['Condition ID', timer.conditionId],
            ['Fires at', unixTime(timer.firingUnixTimestampSeconds, timezone)],
            ['Delay', isPresent(timer.durationSeconds) ? `${timer.durationSeconds}s` : undefined],
          ]} />
        </div>
      ))}
      {subFlows.map((subFlow, index) => (
        <a
          className="semantic-record sub-flow-record"
          href={`/v1/flows/${encodeURIComponent(generatedSubFlowID(parentFlowId, stepExecutionId, index))}`}
          aria-label={`Open SubFlow ${generatedSubFlowID(parentFlowId, stepExecutionId, index)}`}
          key={`${parentFlowId}-${stepExecutionId}-${index}`}
        >
          <strong><code>{generatedSubFlowID(parentFlowId, stepExecutionId, index)}</code></strong>
          <Fields values={[
            ['Condition ID', subFlow.conditionId],
            ['Reuse policy', asData(subFlow.options).reusePolicy],
          ]} />
        </a>
      ))}
    </div>
  );
}

function KeyValueListStructured({ value, expandValues }: { value: unknown; expandValues: boolean }) {
  const groups = groupPersistenceEntries(
    asDataArray(value),
    (entry) => typeof entry.key === 'string' ? entry.key : displayValue(entry.key),
  );
  return (
    <div className="structured-value semantic-records">
      {groups.map((group) => group.kind === 'value' ? (
        <div className="semantic-record" key={`${String(group.entry.key)}-${group.index}`}>
          <strong>{displayValue(group.entry.key)}</strong>
          <div className="semantic-value"><ValueChip value={group.entry.value} expandValues={expandValues} /></div>
        </div>
      ) : (
        <div className="semantic-record semantic-map-record" key={group.name}>
          <strong>{group.name}</strong>
          <div className="semantic-map-list">
            {group.entries.map(({ entry, instance, index }) => (
              <details className="semantic-record semantic-map-entry" key={`${instance}-${index}`} open={expandValues || undefined}>
                <summary><strong>{instance}</strong></summary>
                <div className="semantic-record-content">
                  <div className="semantic-value"><ValueChip value={entry.value} expandValues={expandValues} /></div>
                </div>
              </details>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function ChannelValues({ value, expandValues }: { value: unknown; expandValues: boolean }) {
  const messages = asDataArray(asData(value).messages);
  if (messages.length === 0) return <ValueChip value={value} expandValues={expandValues} />;
  return (
    <div className="semantic-records semantic-channel-values">
      {messages.map((message, index) => (
        <div className="semantic-record" key={index}>
          <strong>Message {index + 1}</strong>
          <div className="semantic-value"><ValueChip value={message.value} expandValues={expandValues} /></div>
        </div>
      ))}
    </div>
  );
}

function PendingChannelsStructured({ value, expandValues }: { value: Data; expandValues: boolean }) {
  const groups = groupPersistenceEntries(
    Object.entries(value).map(([key, entry]) => ({ key, value: entry })),
    (entry) => entry.key,
  );
  return (
    <div className="structured-value semantic-records">
      {groups.map((group) => group.kind === 'value' ? (
        <div className="semantic-record channel-record" key={`${String(group.entry.key)}-${group.index}`}>
          <strong>{displayValue(group.entry.key)}</strong>
          <ChannelValues value={group.entry.value} expandValues={expandValues} />
        </div>
      ) : (
        <div className="semantic-record channel-record semantic-map-record" key={group.name}>
          <strong>{group.name}</strong>
          <div className="semantic-map-list">
            {group.entries.map(({ entry, instance, index }) => (
              <details className="semantic-record semantic-map-entry" key={`${instance}-${index}`} open={expandValues || undefined}>
                <summary><strong>{instance}</strong></summary>
                <div className="semantic-record-content"><ChannelValues value={entry.value} expandValues={expandValues} /></div>
              </details>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function TimerListStructured({ value }: { value: unknown }) {
  const { timezone } = usePreferences();
  return (
    <div className="structured-value semantic-records">
      {asDataArray(value).map((timer, index) => (
        <div className="semantic-record" key={`${String(timer.conditionId)}-${index}`}>
          <strong>Timer {index + 1}</strong>
          <Fields values={[
            ['Condition ID', timer.conditionId],
            ['Status', conditionStatusLabel(timer.status)],
            ['Fires at', unixTime(timer.firingUnixTimestampSeconds, timezone)],
            ['Delay', isPresent(timer.durationSeconds) ? `${timer.durationSeconds}s` : undefined],
          ]} />
        </div>
      ))}
    </div>
  );
}

function ObjectStructured({ value, expandValues = false }: { value: Data; expandValues?: boolean }) {
  const entries = Object.entries(value);
  if (entries.length === 0) return <p className="muted">Empty object</p>;
  return (
    <div className="structured-value">
      <dl className="semantic-fields semantic-fields-compact">
        {entries.map(([key, entry]) => {
          const decoded = decodedValue(entry);
          const labeled = key === 'stepDurability' || key === 'waitForDurability'
            ? durabilityLabel(entry)
            : null;
          return (
            <div className="semantic-field-wide" key={key}>
              <dt>{key}</dt>
              <dd>
                {labeled !== null
                  ? labeled
                  : decoded && typeof decoded === 'object'
                    ? <LongValue text={JSON.stringify(decoded, storedValueJSONReplacer, 2)} isBlock isForcedOpen={expandValues} />
                    : <LongValue text={displayValue(decoded)} isBlock={false} isForcedOpen={expandValues} />}
              </dd>
            </div>
          );
        })}
      </dl>
    </div>
  );
}

function ArrayStructured({ value, expandValues }: { value: unknown[]; expandValues: boolean }) {
  if (value.length === 0) return <p className="muted">Empty list</p>;
  return (
    <div className="structured-value semantic-records">
      {value.map((entry, index) => (
        <div className="semantic-record" key={index}>
          <strong>Item {index + 1}</strong>
          {Array.isArray(entry)
            ? <StructuredValue value={entry} expandValues={expandValues} />
            : entry && typeof entry === 'object' && !isDexValue(entry)
              ? <ObjectStructured value={asData(entry)} expandValues={expandValues} />
              : <ValueChip value={entry} expandValues={expandValues} />}
        </div>
      ))}
    </div>
  );
}

export function StructuredValue({
  value,
  parentFlowId = '',
  stepExecutionId = '',
  persistenceKind,
  expandValues = false,
}: {
  value: unknown;
  parentFlowId?: string;
  stepExecutionId?: string;
  persistenceKind?: 'attributes' | 'channels';
  /** Shows long values in full and opens map instances. */
  expandValues?: boolean;
}) {
  if (value === undefined || value === null) {
    return <p className="muted">No value</p>;
  }
  if (isDexValue(value)) {
    const decoded = decodedValue(value);
    // A decoded object renders structurally; a primitive renders as one truncatable value.
    if (decoded && typeof decoded === 'object' && !isDexValue(decoded)) {
      return (
        <StructuredValue
          value={decoded}
          parentFlowId={parentFlowId}
          stepExecutionId={stepExecutionId}
          expandValues={expandValues}
        />
      );
    }
    return <div className="structured-value semantic-value"><ValueChip value={value} expandValues={expandValues} /></div>;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return <p className="muted">Empty list</p>;
    if (persistenceKind === 'attributes' && isKeyValueList(value)) {
      return <KeyValueListStructured value={value} expandValues={expandValues} />;
    }
    if (isTimerList(value)) return <TimerListStructured value={value} />;
    return <ArrayStructured value={value} expandValues={expandValues} />;
  }
  if (typeof value === 'object') {
    if (Object.keys(value).length === 0) return <p className="muted">Empty object</p>;
    if (persistenceKind === 'channels') {
      return <PendingChannelsStructured value={asData(value)} expandValues={expandValues} />;
    }
    if (isWaitingCondition(value)) {
      return <WaitingConditionStructured
        value={value}
        parentFlowId={parentFlowId}
        stepExecutionId={stepExecutionId}
      />;
    }
    return <ObjectStructured value={asData(value)} expandValues={expandValues} />;
  }
  return <LongValue text={displayValue(value)} isBlock={false} isForcedOpen={expandValues} />;
}
