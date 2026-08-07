// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { BlobCache } from "./blob-cache.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import { laterPhase } from "./errors.js";
import type { Flow, Registry } from "./flow.js";
import type {
  ClientOptions,
  FlowConfig,
  FlowInfo,
  ResetFlowOptions,
  StartFlowOptions,
  StepExecutionId,
  StopFlowOptions,
  TimerId,
} from "./options.js";
import type { Attribute, AttributeMap } from "./persistence.js";
import type { RPCResult } from "./rpc.js";
import type { Channel, ChannelMap } from "./wait.js";

export class Client {
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: ClientOptions = {},
  ) {}

  public async startFlow<StartInput>(
    flow: Flow<StartInput>,
    flowId: string,
    input: StartInput,
    options: StartFlowOptions = {},
  ): Promise<string> {
    void flow;
    void flowId;
    void input;
    void options;
    throw laterPhase("Client transport");
  }

  public invokeRPC<Input, Output>(
    rpcMethod: (context: Context, input: Input) => RPCResult<Output>,
    flowId: string,
    input: Input,
    runId?: string,
  ): Promise<Output>;

  public invokeRPC<Output>(
    rpcMethod: (context: Context) => RPCResult<Output>,
    flowId: string,
    runId?: string,
  ): Promise<Output>;

  public invokeRPC<Input>(
    rpcMethod: (context: Context, input: Input) => void,
    flowId: string,
    input: Input,
    runId?: string,
  ): Promise<void>;

  public invokeRPC(
    rpcMethod: (context: Context) => void,
    flowId: string,
    runId?: string,
  ): Promise<void>;

  public async invokeRPC(
    rpcMethod: Function,
    flowId: string,
    inputOrRunId?: unknown,
    runId = "",
  ): Promise<unknown> {
    void rpcMethod;
    void flowId;
    void inputOrRunId;
    void runId;
    throw laterPhase("Client transport");
  }

  public getAttribute<T>(flowId: string, attribute: Attribute<T>, runId?: string): Promise<T>;

  public getAttribute<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    runId?: string,
  ): Promise<T>;

  public async getAttribute(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instanceOrRunId = "",
    runId = "",
  ): Promise<unknown> {
    void flowId;
    void attribute;
    void instanceOrRunId;
    void runId;
    throw laterPhase("Client transport");
  }

  public setAttribute<T>(
    flowId: string,
    attribute: Attribute<T>,
    value: T,
    runId?: string,
  ): Promise<void>;

  public setAttribute<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    value: T,
    runId?: string,
  ): Promise<void>;

  public async setAttribute(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instanceOrValue: unknown,
    valueOrRunId?: unknown,
    runId = "",
  ): Promise<void> {
    void flowId;
    void attribute;
    void instanceOrValue;
    void valueOrRunId;
    void runId;
    throw laterPhase("Client transport");
  }

  public publish<T>(flowId: string, channel: Channel<T>, ...values: readonly T[]): Promise<void>;

  public publish<T>(
    flowId: string,
    channel: ChannelMap<T>,
    instance: string,
    ...values: readonly T[]
  ): Promise<void>;

  public async publish(
    flowId: string,
    channel: Channel<unknown> | ChannelMap<unknown>,
    ...instanceAndValues: readonly unknown[]
  ): Promise<void> {
    void flowId;
    void channel;
    void instanceAndValues;
    throw laterPhase("Client transport");
  }

  public waitForFlow(flowId: string): Promise<void>;

  public waitForFlow<Output>(
    flowId: string,
    outputCodec: Codec<Output>,
    timeoutMs?: number,
  ): Promise<Output>;

  public async waitForFlow(
    flowId: string,
    outputCodec?: Codec<unknown>,
    timeoutMs?: number,
  ): Promise<unknown> {
    void flowId;
    void outputCodec;
    void timeoutMs;
    throw laterPhase("Client transport");
  }

  public async stopFlow(flowId: string, options: StopFlowOptions = {}): Promise<void> {
    void flowId;
    void options;
    throw laterPhase("Client transport");
  }

  public async describeFlow(flowId: string): Promise<FlowInfo> {
    void flowId;
    throw laterPhase("Client transport");
  }

  public async resetFlow(flowId: string, options: ResetFlowOptions): Promise<string> {
    void flowId;
    void options;
    throw laterPhase("Client transport");
  }

  public async skipTimer(
    flowId: string,
    stepExecutionId: StepExecutionId,
    timerId: TimerId,
  ): Promise<void> {
    void flowId;
    void stepExecutionId;
    void timerId;
    throw laterPhase("Client transport");
  }

  public async waitForStepCompletion(
    flowId: string,
    stepExecutionId: StepExecutionId,
    timeoutMs: number,
  ): Promise<void> {
    void flowId;
    void stepExecutionId;
    void timeoutMs;
    throw laterPhase("Client transport");
  }

  public async updateFlowConfig(flowId: string, config: FlowConfig): Promise<void> {
    void flowId;
    void config;
    throw laterPhase("Client transport");
  }

  public async close(): Promise<void> {
    throw laterPhase("Client transport");
  }
}
