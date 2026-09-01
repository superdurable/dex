// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import {
  isBlobReferenceValue,
  isStoredValueUnavailable,
  storedValueJSONReplacer,
} from '@/lib/blobs';
import { formatDate, type TimezonePreference } from '@/lib/format';
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
          <dd>{displayValue(value)}</dd>
        </div>
      ))}
    </dl>
  );
}

function ValueChip({ value }: { value: unknown }) {
  const decoded = decodedValue(value);
  if (decoded && typeof decoded === 'object') {
    return <pre>{JSON.stringify(decoded, storedValueJSONReplacer, 2)}</pre>;
  }
  return <code>{displayValue(decoded)}</code>;
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
          href={`/flows/${encodeURIComponent(generatedSubFlowID(parentFlowId, stepExecutionId, index))}`}
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

function mapEntry(key: string): { name: string; instance: string } | null {
  const separator = key.indexOf('/');
  if (separator <= 0 || separator === key.length - 1) return null;
  const name = key.slice(0, separator);
  const encodedInstance = key.slice(separator + 1);
  try {
    return { name, instance: decodeURIComponent(encodedInstance) };
  } catch {
    return { name, instance: encodedInstance };
  }
}

type PersistenceGroup =
  | { kind: 'value'; entry: Data; index: number }
  | { kind: 'map'; name: string; entries: Array<{ entry: Data; instance: string; index: number }> };

function persistenceGroups(entries: Data[]): PersistenceGroup[] {
  const groups: PersistenceGroup[] = [];
  const mapsByName = new Map<string, Extract<PersistenceGroup, { kind: 'map' }>>();
  for (const [index, entry] of entries.entries()) {
    const key = typeof entry.key === 'string' ? entry.key : displayValue(entry.key);
    const mapped = mapEntry(key);
    if (!mapped) {
      groups.push({ kind: 'value', entry, index });
      continue;
    }
    let group = mapsByName.get(mapped.name);
    if (!group) {
      group = { kind: 'map', name: mapped.name, entries: [] };
      mapsByName.set(mapped.name, group);
      groups.push(group);
    }
    group.entries.push({ entry, instance: mapped.instance, index });
  }
  return groups;
}

function KeyValueListStructured({ value }: { value: unknown }) {
  const groups = persistenceGroups(asDataArray(value));
  return (
    <div className="structured-value semantic-records">
      {groups.map((group) => group.kind === 'value' ? (
        <div className="semantic-record" key={`${String(group.entry.key)}-${group.index}`}>
          <strong>{displayValue(group.entry.key)}</strong>
          <div className="semantic-value"><ValueChip value={group.entry.value} /></div>
        </div>
      ) : (
        <div className="semantic-record semantic-map-record" key={group.name}>
          <strong>{group.name}</strong>
          <div className="semantic-map-list">
            {group.entries.map(({ entry, instance, index }) => (
              <details className="semantic-record semantic-map-entry" key={`${instance}-${index}`}>
                <summary><strong>{instance}</strong></summary>
                <div className="semantic-record-content">
                  <div className="semantic-value"><ValueChip value={entry.value} /></div>
                </div>
              </details>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function ChannelValues({ value }: { value: unknown }) {
  const values = asDataArray(asData(value).values);
  if (values.length === 0) return <ValueChip value={value} />;
  return (
    <div className="semantic-records semantic-channel-values">
      {values.map((message, index) => (
        <div className="semantic-record" key={index}>
          <strong>Message {index + 1}</strong>
          <div className="semantic-value"><ValueChip value={message} /></div>
        </div>
      ))}
    </div>
  );
}

function PendingChannelsStructured({ value }: { value: Data }) {
  const groups = persistenceGroups(Object.entries(value).map(([key, entry]) => ({ key, value: entry })));
  return (
    <div className="structured-value semantic-records">
      {groups.map((group) => group.kind === 'value' ? (
        <div className="semantic-record channel-record" key={`${String(group.entry.key)}-${group.index}`}>
          <strong>{displayValue(group.entry.key)}</strong>
          <ChannelValues value={group.entry.value} />
        </div>
      ) : (
        <div className="semantic-record channel-record semantic-map-record" key={group.name}>
          <strong>{group.name}</strong>
          <div className="semantic-map-list">
            {group.entries.map(({ entry, instance, index }) => (
              <details className="semantic-record semantic-map-entry" key={`${instance}-${index}`}>
                <summary><strong>{instance}</strong></summary>
                <div className="semantic-record-content"><ChannelValues value={entry.value} /></div>
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

function ObjectStructured({ value }: { value: Data }) {
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
                    ? <pre>{JSON.stringify(decoded, storedValueJSONReplacer, 2)}</pre>
                    : displayValue(decoded)}
              </dd>
            </div>
          );
        })}
      </dl>
    </div>
  );
}

function ArrayStructured({ value }: { value: unknown[] }) {
  if (value.length === 0) return <p className="muted">Empty list</p>;
  return (
    <div className="structured-value semantic-records">
      {value.map((entry, index) => (
        <div className="semantic-record" key={index}>
          <strong>Item {index + 1}</strong>
          {entry && typeof entry === 'object'
            ? <ObjectStructured value={asData(entry)} />
            : <code>{displayValue(entry)}</code>}
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
}: {
  value: unknown;
  parentFlowId?: string;
  stepExecutionId?: string;
  persistenceKind?: 'attributes' | 'channels';
}) {
  if (value === undefined || value === null) {
    return <p className="muted">No value</p>;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return <p className="muted">Empty list</p>;
    if (persistenceKind === 'attributes' && isKeyValueList(value)) return <KeyValueListStructured value={value} />;
    if (isTimerList(value)) return <TimerListStructured value={value} />;
    return <ArrayStructured value={value} />;
  }
  if (typeof value === 'object') {
    if (Object.keys(value).length === 0) return <p className="muted">Empty object</p>;
    if (persistenceKind === 'channels') return <PendingChannelsStructured value={asData(value)} />;
    if (isWaitingCondition(value)) {
      return <WaitingConditionStructured
        value={value}
        parentFlowId={parentFlowId}
        stepExecutionId={stepExecutionId}
      />;
    }
    return <ObjectStructured value={asData(value)} />;
  }
  return <code>{displayValue(value)}</code>;
}
