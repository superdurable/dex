// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Trash2 } from 'lucide-react';
import { storedValueJSONReplacer } from '@/lib/blobs';
import { groupPersistenceEntries } from '@/lib/persistenceGroups';
import { CopyButton } from '../../components/CopyButton';
import { InlineValue } from '../../components/StructuredValue';

/** Switches a whole Overview section between its compact rows and the raw server JSON. */
export function SectionViewToggle({
  view,
  onChange,
  label,
}: {
  view: 'details' | 'raw';
  onChange: (view: 'details' | 'raw') => void;
  label: string;
}) {
  return (
    <div aria-label={`${label} view`} className="event-detail-tabs section-view-toggle" role="tablist">
      <button aria-selected={view === 'details'} className={view === 'details' ? 'active' : ''} onClick={() => onChange('details')} role="tab" type="button">
        Details
      </button>
      <button aria-selected={view === 'raw'} className={view === 'raw' ? 'active' : ''} onClick={() => onChange('raw')} role="tab" type="button">
        Raw JSON
      </button>
    </div>
  );
}

export function RawStateJSON({ value }: { value: unknown }) {
  return <pre className="json-view-raw section-raw-json">{JSON.stringify(value, storedValueJSONReplacer, 2)}</pre>;
}

interface AttributeEntry {
  key: string;
  value: unknown;
}

/** Lists Attributes as one compact row each; AttributeMap instances sit under their map name. */
export function AttributeRows({ attributes, expandValues }: { attributes: unknown[]; expandValues: boolean }) {
  const entries = attributes.flatMap((attribute): AttributeEntry[] => {
    if (typeof attribute !== 'object' || attribute === null) return [];
    const data = attribute as { key?: unknown; value?: unknown };
    return [{ key: typeof data.key === 'string' ? data.key : String(data.key ?? ''), value: data.value }];
  });
  const groups = groupPersistenceEntries(entries, (entry) => entry.key);
  const plainEntries = groups.flatMap((group) => group.kind === 'value' ? [group] : []);
  const mapGroups = groups.flatMap((group) => group.kind === 'map' ? [group] : []);
  return (
    <div className="compact-groups">
      {plainEntries.length > 0 && (
        <ul className="compact-rows">
          {plainEntries.map((group) => (
            <li className="compact-row" key={`${group.entry.key}-${group.index}`}>
              <strong className="compact-row-label">{group.entry.key}</strong>
              <div className="compact-row-value"><InlineValue value={group.entry.value} expandValues={expandValues} /></div>
            </li>
          ))}
        </ul>
      )}
      {mapGroups.map((group) => (
        <section aria-label={`AttributeMap ${group.name}`} className="compact-group" key={group.name}>
          <header className="compact-group-head">
            <strong>{group.name}</strong>
            <span>{group.entries.length} {group.entries.length === 1 ? 'instance' : 'instances'}</span>
          </header>
          <ul className="compact-rows">
            {group.entries.map(({ entry, instance, index }) => (
              <li className="compact-row" key={`${instance}-${index}`}>
                <strong className="compact-row-label">{instance}</strong>
                <div className="compact-row-value"><InlineValue value={entry.value} expandValues={expandValues} /></div>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

interface PendingMessage {
  messageId: string;
  value: unknown;
}

interface PendingChannel {
  channelName: string;
  label: string;
  messages: PendingMessage[];
}

/** Groups pending messages by Channel; ChannelMap instances sort the way their map orders them. */
export function pendingChannelGroups(channels: Record<string, unknown>): PendingChannel[] {
  const entries = Object.entries(channels).map(([channelName, entry]) => ({
    channelName,
    messages: messagesOf(entry),
  }));
  return groupPersistenceEntries(entries, (entry) => entry.channelName).flatMap((group) => group.kind === 'value'
    ? [{ ...group.entry, label: group.entry.channelName }]
    : group.entries.map(({ entry, instance }) => ({ ...entry, label: `${group.name} / ${instance}` })))
    .filter((channel) => channel.messages.length > 0);
}

/** Groups event messages, each naming its Channel, the same way pending messages group. */
export function messageChannelGroups(messages: unknown): PendingChannel[] {
  const byChannel = new Map<string, PendingMessage[]>();
  for (const message of Array.isArray(messages) ? messages : []) {
    if (typeof message !== 'object' || message === null) continue;
    const envelope = message as { channelName?: unknown; messageId?: unknown; value?: unknown };
    const channelName = typeof envelope.channelName === 'string' ? envelope.channelName : '';
    const list = byChannel.get(channelName) ?? [];
    list.push({ messageId: typeof envelope.messageId === 'string' ? envelope.messageId : '', value: envelope.value });
    byChannel.set(channelName, list);
  }
  return pendingChannelGroups(Object.fromEntries(
    [...byChannel].map(([channelName, channelMessages]) => [channelName, { messages: channelMessages }]),
  ));
}

function messagesOf(entry: unknown): PendingMessage[] {
  if (typeof entry !== 'object' || entry === null) return [];
  const messages = (entry as { messages?: unknown }).messages;
  if (!Array.isArray(messages)) return [];
  return messages.flatMap((message) => {
    if (typeof message !== 'object' || message === null) return [];
    const envelope = message as { messageId?: unknown; value?: unknown };
    return [{ messageId: typeof envelope.messageId === 'string' ? envelope.messageId : '', value: envelope.value }];
  });
}

/** Shortens a long message ID to its start and end; the full ID stays in the title and the copy button. */
function shortMessageId(messageId: string): string {
  return messageId.length > 16 ? `${messageId.slice(0, 8)}…${messageId.slice(-4)}` : messageId;
}

export function ChannelRows({
  channels,
  expandValues,
  deletingChannelMessage,
  onDeleteChannelMessage,
}: {
  channels: PendingChannel[];
  expandValues: boolean;
  deletingChannelMessage?: string;
  onDeleteChannelMessage?: (channelName: string, messageId: string) => Promise<void>;
}) {
  return (
    <div className="compact-groups">
      {channels.map((channel) => (
        <section aria-label={`Channel ${channel.label}`} className="compact-group" key={channel.channelName}>
          <header className="compact-group-head">
            <strong>{channel.label}</strong>
            <span>{channel.messages.length} {channel.messages.length === 1 ? 'message' : 'messages'}</span>
          </header>
          <ul className="compact-rows">
            {channel.messages.map((message, index) => (
              <li className="compact-row compact-row--message" key={message.messageId || `${channel.channelName}-${index}`}>
                <div className="compact-row-value"><InlineValue value={message.value} expandValues={expandValues} /></div>
                {message.messageId && (
                  <span className="compact-row-meta">
                    <code title={message.messageId}>{shortMessageId(message.messageId)}</code>
                    <CopyButton label={`Copy message ID ${message.messageId}`} value={message.messageId} />
                  </span>
                )}
                {onDeleteChannelMessage && message.messageId && (
                  <button
                    aria-label={`Delete message ${shortMessageId(message.messageId)} from ${channel.label}`}
                    className="icon-action danger"
                    disabled={deletingChannelMessage === message.messageId}
                    onClick={() => void onDeleteChannelMessage(channel.channelName, message.messageId)}
                    title={deletingChannelMessage === message.messageId ? 'Deleting…' : 'Delete message'}
                    type="button"
                  >
                    <Trash2 aria-hidden="true" size={15} strokeWidth={2} />
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
