// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowHistoryEvent } from '@/lib/types';
import {
  Flag,
  FlagOff,
  GitFork,
  Hourglass,
  Play,
  Send,
  Tag,
  Trash2,
  Zap,
  type LucideIcon,
} from 'lucide-react';

export type EventKind = 'waitfor' | 'execute' | 'channel-publish' | 'channel-delete' | 'flow-start' | 'flow-close'
  | 'rpc' | 'attributes' | 'fork';

export type KindFamily = 'step' | 'input' | 'lifecycle';

/** Each event kind's icon, legend name, and family; the family sets a quiet icon tint. */
export const eventKinds: Record<EventKind, { icon: LucideIcon; name: string; family: KindFamily }> = {
  waitfor: { icon: Hourglass, name: 'WaitFor', family: 'step' },
  execute: { icon: Play, name: 'Execute', family: 'step' },
  'channel-publish': { icon: Send, name: 'Channel published', family: 'input' },
  'channel-delete': { icon: Trash2, name: 'Channel message deleted', family: 'input' },
  rpc: { icon: Zap, name: 'RPC', family: 'input' },
  attributes: { icon: Tag, name: 'Attributes updated', family: 'input' },
  'flow-start': { icon: Flag, name: 'Flow started', family: 'lifecycle' },
  'flow-close': { icon: FlagOff, name: 'Flow closed', family: 'lifecycle' },
  fork: { icon: GitFork, name: 'Time Travel fork', family: 'lifecycle' },
};

/** The Legend and the filter list event kinds in this order. */
export const timelineEventKindOrder: readonly EventKind[] = [
  'waitfor',
  'execute',
  'channel-publish',
  'channel-delete',
  'rpc',
  'attributes',
  'flow-start',
  'flow-close',
  'fork',
];

export function eventKindOf(event: FlowHistoryEvent): EventKind {
  if (event.type.startsWith('StepWaitFor')) return 'waitfor';
  if (event.type.startsWith('StepExecute')) return 'execute';
  if (event.type === 'ChannelExternalPublish') return 'channel-publish';
  if (event.type === 'ChannelExternalDelete') return 'channel-delete';
  if (event.type === 'FlowStartedOrContinued') return 'flow-start';
  if (event.type === 'FlowClosed') return 'flow-close';
  if (event.type === 'TimeTravelFork') return 'fork';
  return event.payload.isSetAttributeApi === true ? 'attributes' : 'rpc';
}

export function namesIn(values: unknown, field: string): string[] {
  const list = Array.isArray(values) ? values : [];
  return [...new Set(list.flatMap((value) => {
    const name = value && typeof value === 'object' ? (value as Record<string, unknown>)[field] : undefined;
    return typeof name === 'string' && name ? [name] : [];
  }))];
}

/** The noun for the names inside a kind, such as Step type; kinds without names have none. */
export const eventKindNameNouns: Partial<Record<EventKind, string>> = {
  waitfor: 'Step type',
  execute: 'Step type',
  'channel-publish': 'Channel name',
  'channel-delete': 'Channel name',
  rpc: 'RPC name',
  attributes: 'Attribute name',
};

/**
 * Returns the names an event carries inside its kind: its Step type, RPC name, Channel names,
 * or Attribute names. Returns an empty list for kinds without names or an event without one.
 */
export function eventKindNames(event: FlowHistoryEvent): string[] {
  const kind = eventKindOf(event);
  if (kind === 'waitfor' || kind === 'execute') {
    const context = event.payload.context;
    const stepType = context && typeof context === 'object' ? (context as Record<string, unknown>).stepType : undefined;
    return typeof stepType === 'string' && stepType ? [stepType] : [];
  }
  if (kind === 'channel-publish' || kind === 'channel-delete') return namesIn(event.payload.messages, 'channelName');
  if (kind === 'rpc') return typeof event.payload.rpcName === 'string' && event.payload.rpcName ? [event.payload.rpcName] : [];
  if (kind === 'attributes') return namesIn(event.payload.upsertAttributes, 'key');
  return [];
}
