// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import {
  isGracefulCompletionConditionCompleted,
  supportsGracefulCompletionCondition,
  type Attribute,
  type AttributeMap,
  type Channel,
  type ChannelMap,
  type ChannelMessage,
  type Codec,
  type Context,
  type RecoveryErrorInfo,
  type Stream,
} from "../src/index.js";

// Implements only the Context members published before the graceful completion condition.
// It must keep compiling, so the graceful completion helpers stay out of the Context type.
class ApplicationFakeContext implements Context {
  public readonly flowId = "fake-flow";
  public readonly runId = "fake-run";
  public readonly flowStartedAt = new Date(0);
  public readonly stepExecutionId = "fake-step";
  public readonly fromStepExecutionId = "";
  public readonly recoveryError: RecoveryErrorInfo | undefined = undefined;
  public readonly firstAttemptAt = new Date(0);
  public readonly deadline: Date | undefined = undefined;
  public readonly attempt = 1;
  public readonly cancellationSignal = new AbortController().signal;

  public hasLastHeartbeatValue(): boolean {
    return false;
  }

  public getLastHeartbeatValue<T = unknown>(_codec?: Codec<T>): T | undefined {
    return undefined;
  }

  public hasTimerFired(_index?: number): boolean {
    return false;
  }

  public waitForMethodFailed(): boolean {
    return false;
  }

  public setStepExecutionLocal<T>(_key: string, _value: T, _codec: Codec<T>): void {}

  public getStepExecutionLocal<T>(_key: string, _codec: Codec<T>): T | undefined {
    return undefined;
  }

  public recordEvent<T>(_name: string, _value: T, _codec: Codec<T>): void {}

  public getAttribute<T>(_attribute: Attribute<T> | AttributeMap<T>, _instance?: string): T {
    throw new Error("not used");
  }

  public setAttribute<T>(_attribute: Attribute<T> | AttributeMap<T>, _value: T, _instance?: string): void {}

  public deleteAttribute(_attribute: Attribute<unknown> | AttributeMap<unknown>, _instance?: string): void {}

  public attributeMapKeys(_attribute: AttributeMap<unknown>): readonly string[] {
    return [];
  }

  public publish<T>(_channel: Channel<T> | ChannelMap<T>, _value: T, _instance?: string): void {}

  public deleteChannelMessage(
    _channel: Channel<unknown> | ChannelMap<unknown>,
    _messageId: string,
    _instance?: string,
  ): void {}

  public channelSize(_channel: Channel<unknown> | ChannelMap<unknown>, _instance?: string): number {
    return 0;
  }

  public pendingChannelMessages<T>(
    _channel: Channel<T> | ChannelMap<T>,
    _instance?: string,
  ): readonly ChannelMessage<T>[] {
    return [];
  }

  public channelResults<T>(_channel: Channel<T> | ChannelMap<T>, _instance?: string): readonly T[] {
    return [];
  }

  public channelMapKeys(_channel: ChannelMap<unknown>): readonly string[] {
    return [];
  }

  public writeStream<T>(_stream: Stream<T>, _value: T): void {}
}

test("graceful completion helpers return false for a Context that Dex did not create", () => {
  const context: Context = new ApplicationFakeContext();

  assert.equal(isGracefulCompletionConditionCompleted(context), false);
  assert.equal(supportsGracefulCompletionCondition(context), false);
});
