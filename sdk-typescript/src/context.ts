// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Attribute, AttributeMap } from "./persistence.js";
import type { Channel, ChannelMap } from "./wait.js";
import type { Codec } from "./codec.js";

export interface Context {
  readonly flowId: string;
  readonly runId: string;
  readonly stepExecutionId: string;
  readonly fromStepExecutionId: string;
  readonly attempt: number;
  hasTimerFired(index?: number): boolean;
  waitForMethodFailed(): boolean;
  setStepExecutionLocal<T>(key: string, value: T, codec: Codec<T>): void;
  getStepExecutionLocal<T>(key: string, codec: Codec<T>): T | undefined;
  recordEvent<T>(name: string, value: T, codec: Codec<T>): void;
  getAttribute<T>(attribute: Attribute<T> | AttributeMap<T>, instance?: string): T;
  setAttribute<T>(attribute: Attribute<T> | AttributeMap<T>, value: T, instance?: string): void;
  deleteAttribute(attribute: Attribute<unknown> | AttributeMap<unknown>, instance?: string): void;
  publish<T>(channel: Channel<T> | ChannelMap<T>, value: T, instance?: string): void;
  channelSize(channel: Channel<unknown> | ChannelMap<unknown>, instance?: string): number;
  channelResults<T>(channel: Channel<T> | ChannelMap<T>, instance?: string): readonly T[];
}
