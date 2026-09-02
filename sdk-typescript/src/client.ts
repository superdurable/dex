// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { credentials, type ServiceError } from "@grpc/grpc-js";

import type { BlobCache } from "./blob-cache.js";
import { mapAttributeStoreNames, mapAttributeStoreSync } from "./attribute-store-sync.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import {
  ActiveStepSearchMode as ProtoActiveStepSearchMode,
  FlowErrorType as ProtoFlowErrorType,
  FlowTimeoutPolicy as ProtoFlowTimeoutPolicy,
  FlowResetStepMethod as ProtoFlowResetStepMethod,
  FlowResetType as ProtoFlowResetType,
  FlowServiceClient,
  FlowStatus as ProtoFlowStatus,
  IdReusePolicy as ProtoIdReusePolicy,
  IndexType as ProtoIndexType,
  ExecuteMethodFailurePolicy,
  type GetAttributesResponse,
  type GetChannelMessagesResponse,
  type GetFlowSummaryResponse,
  type InvokeRPCResponse,
  type ReadStreamResponse,
  type ResetFlowResponse,
  type SearchFlowsResponse,
  type SearchFlowsResponseEntry,
  type StartFlowResponse,
  StartFlowRequest,
  StepDurability as ProtoStepDurability,
  StepOptions as ProtoStepOptions,
  StopType as ProtoStopType,
  WaitForMethodFailurePolicy,
  type FlowServiceClient as FlowServiceClientType,
  type FlowConfig as ProtoFlowConfig,
  type FlowResult as ProtoFlowResult,
  type IndexConfig,
  type WaitForStepCompletionResponse,
} from "./gen/dex.js";
import type { Empty } from "./gen/google/protobuf/empty.js";
import {
  FlowErrorType,
  ValueMappingError,
  type FlowErrorType as FlowErrorTypeValue,
} from "./errors.js";
import {
  registeredFlow,
  registeredRPC,
  registeredStream,
  type Flow,
  type RegisteredFlow,
  type Registry,
} from "./flow.js";
import {
  createFlowResultFromProto,
  type FlowResult,
} from "./flow-result.js";
import { translateServiceError } from "./grpc-status.js";
import {
  ActiveStepSearchMode,
  FlowTimeoutPolicy,
  IdReusePolicy,
  TimeTravelStepMethod,
  TimeTravelType,
  StopType,
  type ClientOptions,
  type FlowConfig,
  type FlowInfo,
  type FlowStatus,
  type TimeTravelOptions,
  type SearchFlowEntry,
  type SearchFlowsPage,
  type StartFlowOptions,
  type StepExecutionId,
  type StopFlowOptions,
  type TimerId,
} from "./options.js";
import { Attribute, AttributeMap, IndexType } from "./persistence.js";
import type { RPCResult } from "./rpc.js";
import type { RetryPolicy, StepOptions } from "./step.js";
import { requireName } from "./validation.js";
import { codecOrJson, decodeUnknown, decodeValue, encodeValue, ValueHydrator } from "./value-mapper.js";
import { ChannelMap, type Channel, type ChannelMessage } from "./wait.js";
import type { Stream, StreamMessage } from "./stream.js";

const defaultServerAddress = "localhost:8801";

/**
 * Calls Dex FlowService through registered, typed Flow definitions.
 *
 * Methods perform asynchronous gRPC I/O. A Client owns its service connection but
 * not its Registry or BlobCache; call `close` during application shutdown.
 *
 * @example
 * ```ts
 * const client = new Client(registry, cache);
 * try {
 *   const runId = await client.startFlow(orders, "order-42", input);
 *   const result = await client.waitForFlow("order-42");
 *   const output = result.singleOutput(orderResultCodec);
 * } finally { await client.close(); }
 * ```
 */
export class Client {
  private readonly service: FlowServiceClientType;
  private readonly hydrator: ValueHydrator;

  /**
   * Constructs a Client and lazy plaintext gRPC connection.
   * @param registry - Flow definitions used for validation and routing.
   * @param blobCache - Open cache used to hydrate large response values.
   * @param options - Service address and default Worker target.
   */
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: ClientOptions = {},
  ) {
    this.service = new FlowServiceClient(
      options.serverAddress ?? defaultServerAddress,
      credentials.createInsecure(),
    );
    this.hydrator = new ValueHydrator(this.service, blobCache);
  }

  /**
   * Starts a Flow and returns after Dex accepts it.
   * @typeParam StartInput - Starting Step input type.
   * @param flow - Exact Flow instance registered with this Client.
   * @param flowId - Non-empty application ID stable across runs.
   * @param input - Starting Step input, or `undefined` when no start Step exists.
   * @param options - Timeout, reuse, retry, configuration, and initial state.
   * @returns The server-assigned run ID.
   * @throws {@link FlowAlreadyStartedError} when reuse policy rejects the Flow ID.
   * @throws {@link FlowDefinitionError} when `flow` is not registered.
   */
  public async startFlow<StartInput>(
    flow: Flow<StartInput>,
    flowId: string,
    input: StartInput,
    options: StartFlowOptions = {},
  ): Promise<string> {
    const registered = registeredFlow(this.registry, flow);
    const flowTimeoutSeconds = seconds(options.timeoutMs);
    const request = StartFlowRequest.create({
      flowId: requireName(flowId),
      flowType: registered.name,
      flowTimeoutSeconds,
      flowTimeoutPolicy: resolveFlowTimeoutPolicy(
        registered,
        flowTimeoutSeconds,
        options.timeoutPolicy,
      ),
      requestId: options.requestId ?? crypto.randomUUID(),
      flowStartOptions: {
        idReusePolicy: mapIdReusePolicy(options.idReusePolicy),
        flowStartDelaySeconds: seconds(options.startDelayMs),
        retryPolicy: mapFlowRetryPolicy(options.retryPolicy),
        attributes: (options.attributes ?? []).map((initial) => ({
          key: physicalName(initial.attribute.name, initial.instance),
          value: encodeValue(initial.attribute.codec, initial.value),
          indexConfig: mapIndex(initial.attribute.index),
          syncConfig: mapAttributeStoreSync(initial.attribute),
        })),
        flowConfigOverride: mapFlowConfig(options.configOverride, this.options),
        flowAlreadyStartedOptions: {
          ignoreAlreadyStartedError: options.ignoreAlreadyStarted ?? false,
        },
      },
    });
    if (registered.startStep === undefined) {
      if (input !== undefined) {
        throw new TypeError("Flow without a start Step requires undefined input");
      }
    } else {
      request.startStepType = registered.startStep.name;
      request.stepInput = encodeValue(codecOrJson(registered.startStep.step.inputCodec), input);
      request.stepOptions = mapStepOptions(
        registered.startStep.step.getStepOptions?.(),
        registered.startStep.step.waitFor === undefined,
        registered,
      );
    }
    return (await unary<StartFlowResponse>(
      { operation: "startFlow", flowId, requirement: "none" },
      (callback) => this.service.startFlow(request, callback),
    ))
      .runId;
  }

  /**
   * Invokes an RPC with typed input and output.
   * @typeParam Input - RPC input type.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method decorated with `rpc` on the registered Flow.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns The decoded RPCResult output.
   */
  public invokeRPC<Input, Output>(
    rpcMethod: (
      context: Context,
      input: Input,
    ) => RPCResult<Output> | Promise<RPCResult<Output>>,
    flowId: string,
    input: Input,
    runId?: string,
  ): Promise<Output>;

  /**
   * Invokes an input-free RPC with typed output.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method decorated with `rpc` on the registered Flow.
   * @param flowId - Non-empty target Flow ID.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns The decoded RPCResult output.
   */
  public invokeRPC<Output>(
    rpcMethod: (context: Context) => RPCResult<Output> | Promise<RPCResult<Output>>,
    flowId: string,
    runId?: string,
  ): Promise<Output>;

  /**
   * Invokes a typed-input RPC that returns no output.
   * @typeParam Input - RPC input type.
   * @param rpcMethod - Bound method decorated with `rpc` on the registered Flow.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns A promise resolved after successful handler completion.
   */
  public invokeRPC<Input>(
    rpcMethod: (context: Context, input: Input) => void | Promise<void>,
    flowId: string,
    input: Input,
    runId?: string,
  ): Promise<void>;

  /**
   * Invokes an input-free, output-free RPC.
   * @param rpcMethod - Bound method decorated with `rpc` on the registered Flow.
   * @param flowId - Non-empty target Flow ID.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns A promise resolved after successful handler completion.
   */
  public invokeRPC(
    rpcMethod: (context: Context) => void | Promise<void>,
    flowId: string,
    runId?: string,
  ): Promise<void>;

  /**
   * Dispatches a registered RPC using its decorator codecs and locks.
   * @param rpcMethod - Bound registered RPC method.
   * @param flowId - Non-empty target Flow ID.
   * @param inputOrRunId - Typed input, or run ID for an input-free RPC.
   * @param runId - Exact run for an input-bearing RPC; targets the active run when omitted.
   * @returns Decoded output, or `undefined` for an output-free RPC.
   * @throws {@link RpcLockConflictError} when locks cannot be acquired.
   * @throws {@link WorkerInvocationError} when the application handler fails.
   */
  public async invokeRPC(
    rpcMethod: Function,
    flowId: string,
    inputOrRunId?: unknown,
    runId = "",
  ): Promise<unknown> {
    const rpc = registeredRPC(this.registry, rpcMethod);
    const hasInput = rpc.hasInput;
    const response = await unary<InvokeRPCResponse>(
      { operation: "invokeRPC", flowId, requirement: "active" },
      (callback) =>
      this.service.invokeRpc(
        {
          flowId: requireName(flowId),
          runId: hasInput ? runId : (inputOrRunId as string | undefined) ?? "",
          rpcName: rpc.name,
          input: hasInput
            ? encodeValue(codecOrJson(rpc.options.inputCodec), inputOrRunId)
            : undefined,
          timeoutSeconds: seconds(rpc.options.timeoutMs),
          lockAttributeKeys: (rpc.options.lockAttributes ?? []).map((lock) =>
            physicalName(lock.attribute.name, lock.instance),
          ),
          requestId: crypto.randomUUID(),
          isTransactional: rpc.options.isTransactional ?? false,
        },
        callback,
      ),
    );
    if (rpc.options.outputCodec === undefined && response.output?.kind === undefined) {
      return undefined;
    }
    return decodeValue(
      codecOrJson(rpc.options.outputCodec),
      await this.hydrator.hydrate(response.output),
    );
  }

  /**
   * Reads a singleton Attribute.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty existing Flow ID.
   * @param attribute - Typed singleton Attribute definition.
   * @param runId - Optional exact run; targets the current run when omitted.
   * @returns The decoded value, or `undefined` when unset.
   */
  public getAttribute<T>(
    flowId: string,
    attribute: Attribute<T>,
    runId?: string,
  ): Promise<T | undefined>;

  /**
   * Reads one AttributeMap instance.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty existing Flow ID.
   * @param attribute - Typed AttributeMap definition.
   * @param instance - Non-empty logical map key.
   * @param runId - Optional exact run; targets the current run when omitted.
   * @returns The decoded value, or `undefined` when unset.
   */
  public getAttribute<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    runId?: string,
  ): Promise<T | undefined>;

  /**
   * Reads and decodes a singleton or map Attribute.
   * @param flowId - Non-empty existing Flow ID.
   * @param attribute - Typed Attribute definition.
   * @param instanceOrRunId - Map instance, or singleton run ID.
   * @param runId - Exact run for a map read.
   * @returns The decoded value, or `undefined` when unset.
   */
  public async getAttribute(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instanceOrRunId = "",
    runId = "",
  ): Promise<unknown> {
    const isMap = attribute instanceof AttributeMap;
    const response = await unary<GetAttributesResponse>(
      { operation: "getAttribute", flowId, requirement: "existing" },
      (callback) =>
      this.service.getAttributes(
        {
          flowId: requireName(flowId),
          runId: isMap ? runId : instanceOrRunId,
          keys: [physicalName(attribute.name, isMap ? instanceOrRunId : undefined)],
          allKeys: false,
        },
        callback,
      ),
    );
    const value = response.attributes[0]?.value;
    if (value === undefined) {
      return undefined;
    }
    return decodeValue(attribute.codec, await this.hydrator.hydrate(value));
  }

  /**
   * Writes a singleton Attribute on an active Flow.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Typed singleton Attribute definition.
   * @param value - Value encoded by the Attribute codec.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns A promise resolved after Dex applies the write.
   */
  public setAttribute<T>(
    flowId: string,
    attribute: Attribute<T>,
    value: T,
    runId?: string,
  ): Promise<void>;

  /**
   * Writes one AttributeMap instance on an active Flow.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Typed AttributeMap definition.
   * @param instance - Non-empty logical map key.
   * @param value - Value encoded by the Attribute codec.
   * @param runId - Optional exact run; targets the active run when omitted.
   * @returns A promise resolved after Dex applies the write.
   */
  public setAttribute<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    value: T,
    runId?: string,
  ): Promise<void>;

  /**
   * Encodes and writes a singleton or map Attribute.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Typed Attribute definition.
   * @param instanceOrValue - Map instance or singleton value.
   * @param valueOrRunId - Map value or singleton run ID.
   * @param runId - Exact run for a map write.
   * @returns A promise resolved after Dex applies the write.
   */
  public async setAttribute(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instanceOrValue: unknown,
    valueOrRunId?: unknown,
    runId = "",
  ): Promise<void> {
    const isMap = attribute instanceof AttributeMap;
    const instance = isMap ? String(instanceOrValue) : undefined;
    const value = isMap ? valueOrRunId : instanceOrValue;
    await unary<Empty>(
      { operation: "setAttribute", flowId, requirement: "active" },
      (callback) =>
      this.service.setAttributes(
        {
          flowId: requireName(flowId),
          runId: isMap ? runId : typeof valueOrRunId === "string" ? valueOrRunId : "",
          attributes: [
            {
              key: physicalName(attribute.name, instance),
              value: encodeValue(attribute.codec, value),
              indexConfig: mapIndex(attribute.index),
              syncConfig: mapAttributeStoreSync(attribute),
            },
          ],
          requestId: crypto.randomUUID(),
        },
        callback,
      ),
    );
  }

  /**
   * Publishes one or more values to a singleton Channel.
   * @typeParam T - Channel element type.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Typed singleton Channel definition.
   * @param values - Values appended in argument order.
   * @returns A promise resolved after Dex accepts the batch.
   */
  public publish<T>(flowId: string, channel: Channel<T>, ...values: readonly T[]): Promise<void>;

  /**
   * Publishes one or more values to a ChannelMap instance.
   * @typeParam T - Channel element type.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Typed ChannelMap definition.
   * @param instance - Non-empty logical map key.
   * @param values - Values appended in argument order.
   * @returns A promise resolved after Dex accepts the batch.
   */
  public publish<T>(
    flowId: string,
    channel: ChannelMap<T>,
    instance: string,
    ...values: readonly T[]
  ): Promise<void>;

  /**
   * Encodes and publishes a singleton or map Channel batch.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Typed Channel definition.
   * @param instanceAndValues - Optional map instance followed by values.
   * @returns A promise resolved after Dex accepts the batch.
   */
  public async publish(
    flowId: string,
    channel: Channel<unknown> | ChannelMap<unknown>,
    ...instanceAndValues: readonly unknown[]
  ): Promise<void> {
    const isMap = channel instanceof ChannelMap;
    const instance = isMap ? String(instanceAndValues[0]) : undefined;
    const values = isMap ? instanceAndValues.slice(1) : instanceAndValues;
    await unary<Empty>(
      { operation: "publish", flowId, requirement: "active" },
      (callback) =>
      this.service.publishToChannel(
        {
          flowId: requireName(flowId),
          runId: "",
          messages: values.map((value) => ({
            channelName: physicalName(channel.name, instance),
            value: encodeValue(channel.codec, value),
            messageId: "",
          })),
        },
        callback,
      ),
    );
  }

  /**
   * Returns every pending singleton Channel message in FIFO order.
   * @typeParam T - Channel value type.
   * @param flowId - Non-empty existing Flow ID.
   * @param channel - Registered singleton Channel.
   * @returns Typed pending message envelopes.
   */
  public getChannelMessages<T>(
    flowId: string,
    channel: Channel<T>,
  ): Promise<readonly ChannelMessage<T>[]>;

  /**
   * Returns every pending message for one ChannelMap instance in FIFO order.
   * @typeParam T - Channel value type.
   * @param flowId - Non-empty existing Flow ID.
   * @param channel - Registered ChannelMap.
   * @param instance - Non-empty ChannelMap instance.
   * @returns Typed pending message envelopes.
   */
  public getChannelMessages<T>(
    flowId: string,
    channel: ChannelMap<T>,
    instance: string,
  ): Promise<readonly ChannelMessage<T>[]>;

  /**
   * Implements singleton and ChannelMap pending-message reads.
   * @typeParam T - Channel value type.
   * @param flowId - Non-empty existing Flow ID.
   * @param channel - Registered singleton or map definition.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   * @returns Typed pending message envelopes in FIFO order.
   */
  public async getChannelMessages<T>(
    flowId: string,
    channel: Channel<T> | ChannelMap<T>,
    instance?: string,
  ): Promise<readonly ChannelMessage<T>[]> {
    const response = await unary<GetChannelMessagesResponse>(
      { operation: "getChannelMessages", flowId, requirement: "existing" },
      (callback) => this.service.getChannelMessages(
        {
          flowId: requireName(flowId),
          runId: "",
          channelName: physicalName(channel.name, instance),
        },
        callback,
      ),
    );
    return Promise.all(response.messages.map(async (message) => ({
      messageId: message.messageId,
      value: decodeValue(channel.codec, await this.hydrator.hydrate(message.value)),
    })));
  }

  /**
   * Deletes one pending singleton Channel message by its server-assigned ID.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Registered singleton Channel.
   * @param messageId - Non-empty server-assigned message ID.
   */
  public deleteChannelMessage(
    flowId: string,
    channel: Channel<unknown>,
    messageId: string,
  ): Promise<void>;

  /**
   * Deletes one pending message from a ChannelMap instance by server-assigned ID.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Registered ChannelMap.
   * @param instance - Non-empty ChannelMap instance.
   * @param messageId - Non-empty server-assigned message ID.
   */
  public deleteChannelMessage(
    flowId: string,
    channel: ChannelMap<unknown>,
    instance: string,
    messageId: string,
  ): Promise<void>;

  /**
   * Implements singleton and ChannelMap pending-message deletion.
   * @param flowId - Non-empty active Flow ID.
   * @param channel - Registered singleton or map definition.
   * @param instanceOrMessageId - Map instance or singleton message ID.
   * @param mapMessageId - Required message ID for a ChannelMap.
   * @returns A promise resolved after Dex accepts the deletion.
   */
  public async deleteChannelMessage(
    flowId: string,
    channel: Channel<unknown> | ChannelMap<unknown>,
    instanceOrMessageId: string,
    mapMessageId?: string,
  ): Promise<void> {
    const isMap = channel instanceof ChannelMap;
    const instance = isMap ? instanceOrMessageId : undefined;
    const messageId = isMap ? mapMessageId : instanceOrMessageId;
    await unary<Empty>(
      { operation: "deleteChannelMessage", flowId, requirement: "active" },
      (callback) => this.service.deleteChannelMessage(
        {
          flowId: requireName(flowId),
          runId: "",
          channelName: physicalName(channel.name, instance),
          messageId: requireName(messageId ?? ""),
          requestId: crypto.randomUUID(),
        },
        callback,
      ),
    );
  }

  /**
   * Appends one typed best-effort Stream message with source metadata.
   * @typeParam T - Stream message type.
   * @param flowId - Logical Flow instance ID; the Flow need not exist or be active.
   * @param stream - Stream registered in exactly one Flow schema.
   * @param source - Non-empty source metadata. Repeated values and `#` are allowed.
   * @param value - Typed message to append.
   */
  public async writeStream<T>(
    flowId: string,
    stream: Stream<T>,
    source: string,
    value: T,
  ): Promise<void> {
    if (source.length === 0) {
      throw new TypeError("Stream source is required");
    }
    const flow = registeredStream(this.registry, stream as Stream<unknown>);
    await unary<Empty>(
      { operation: "writeStream", flowId, requirement: "none" },
      (callback) => this.service.writeStream({
        flowId: requireName(flowId),
        flowType: flow.name,
        streamName: stream.name,
        streamCapacityBytes: BigInt(stream.streamCapacityBytes),
        value: encodeValue(stream.codec, value),
        source,
      }, callback),
    );
  }

  /**
   * Returns the next retained Stream message after an opaque resume token.
   * @typeParam T - Stream message type.
   * @param flowId - Logical Flow instance ID used as the Stream instance key.
   * @param stream - Stream registered in exactly one Flow schema.
   * @param resumeToken - Previous message token, or empty to start at the retained head.
   * @param timeoutMs - Optional server-side long-poll duration in milliseconds.
   * @returns Decoded value and resumable metadata for one retained message.
   */
  public async readStream<T>(
    flowId: string,
    stream: Stream<T>,
    resumeToken = "",
    timeoutMs?: number,
  ): Promise<StreamMessage<T>> {
    const flow = registeredStream(this.registry, stream as Stream<unknown>);
    const response = await unary<ReadStreamResponse>(
      { operation: "readStream", flowId, requirement: "none" },
      (callback) => this.service.readStream({
        flowId: requireName(flowId),
        flowType: flow.name,
        streamName: stream.name,
        resumeToken,
        waitTimeSeconds: seconds(timeoutMs),
      }, callback),
    );
    const message = response.message;
    if (message?.value === undefined || message.createdTime === undefined || message.resumeToken === "") {
      throw new TypeError("Dex returned an incomplete Stream message");
    }
    return {
      value: decodeValue(stream.codec, message.value),
      resumeToken: message.resumeToken,
      createdTime: message.createdTime,
      source: message.source,
    };
  }

  /**
   * Long-polls until a Flow closes and returns every output-bearing completion.
   * @param flowId - Non-empty existing Flow ID.
   * @param timeoutMs - Optional server-side long-poll duration in milliseconds.
   * @returns Immutable Step completions in server collection order.
   * @throws {@link LongPollTimeoutError} when the Flow remains open at timeout.
   * @throws {@link ValueMappingError} when Dex returns malformed blob-backed output.
   */
  public async waitForFlow(
    flowId: string,
    timeoutMs?: number,
  ): Promise<FlowResult> {
    const response = await unary<ProtoFlowResult>(
      { operation: "waitForFlow", flowId, requirement: "existing" },
      (callback) =>
        this.service.waitForFlow(
          {
            flowId: requireName(flowId),
            runId: "",
            needsResults: true,
            waitTimeSeconds: seconds(timeoutMs),
          },
          callback,
        ),
    );
    const values = await this.hydrator.hydrateAll(
      response.results.map((result) => result.completedStepOutput),
    );
    return createFlowResultFromProto(response, values);
  }

  /**
   * Requests cancellation, termination, or failure of an active Flow.
   * The promise resolves after acceptance and does not await terminal status.
   * @param flowId - Non-empty active Flow ID.
   * @param options - Stop mode and optional recorded reason.
   */
  public async stopFlow(flowId: string, options: StopFlowOptions = {}): Promise<void> {
    await unary<Empty>(
      { operation: "stopFlow", flowId, requirement: "active" },
      (callback) =>
      this.service.stopFlow(
        {
          flowId: requireName(flowId),
          runId: "",
          reason: options.reason ?? "",
          stopType: mapStopType(options.type),
        },
        callback,
      ),
    );
  }

  /**
   * Returns summary metadata for the current or latest Flow run.
   * @param flowId - Non-empty existing Flow ID.
   * @returns Flow ID, run ID, type, status, and UTC start time.
   */
  public async describeFlow(flowId: string): Promise<FlowInfo> {
    const response = await unary<GetFlowSummaryResponse>(
      { operation: "describeFlow", flowId, requirement: "existing" },
      (callback) => this.service.getFlowSummary({ flowId: requireName(flowId), runId: "" }, callback),
    );
    if (response.flowExecutionId === undefined || response.startTime === undefined) {
      throw new TypeError(`Dex returned an incomplete summary for Flow ${flowId}`);
    }
    return {
      flowId: response.flowExecutionId.flowId,
      runId: response.flowExecutionId.runId,
      flowType: response.flowType,
      status: mapFlowStatus(response.flowStatus),
      startedAt: response.startTime,
    };
  }

  /**
   * Returns one page of Flow runs matching a visibility query.
   * @param query - Dex visibility query; empty uses server defaults.
   * @param pageSize - Non-negative requested maximum result count.
   * @param nextPageToken - Opaque token from the preceding page, or empty first.
   * @returns Server-ordered entries and the next-page token.
   */
  public async searchFlows(
    query: string,
    pageSize: number,
    nextPageToken = "",
  ): Promise<SearchFlowsPage> {
    if (pageSize < 0) {
      throw new RangeError("search page size must not be negative");
    }
    const response = await unary<SearchFlowsResponse>(
      { operation: "searchFlows", requirement: "none" },
      (callback) => this.service.searchFlows({ query, pageSize, nextPageToken }, callback),
    );
    const flows = await Promise.all(
      response.flowRuns.map((entry) => this.mapSearchEntry(entry)),
    );
    return { flows, nextPageToken: response.nextPageToken };
  }

  private async mapSearchEntry(entry: SearchFlowsResponseEntry): Promise<SearchFlowEntry> {
    if (entry.startTime === undefined) {
      throw new TypeError(`Dex returned a search entry without a start time for Flow ${entry.flowId}`);
    }
    const indexedAttributes = new Map<string, unknown>();
    for (const attribute of entry.indexedAttributes) {
      indexedAttributes.set(attribute.key, decodeUnknown(await this.hydrator.hydrate(attribute.value)));
    }
    return {
      flowId: entry.flowId,
      runId: entry.runId,
      flowType: entry.flowType,
      status: mapFlowStatus(entry.flowStatus),
      startedAt: entry.startTime,
      closedAt: entry.closeTime,
      indexedAttributes,
    };
  }

  /**
   * Creates a new run from a selected point in existing Flow history.
   * @param flowId - Non-empty Flow ID whose history supplies the new run.
   * @param options - Time travel selector, reason, and replay controls.
   * @returns The new server-assigned run ID; the Flow ID remains unchanged.
   */
  public async timeTravel(flowId: string, options: TimeTravelOptions): Promise<string> {
    const response = await unary<ResetFlowResponse>(
      { operation: "timeTravel", flowId, requirement: "existing" },
      (callback) =>
      this.service.resetFlow(
        {
          flowId: requireName(flowId),
          runId: "",
          resetType: mapTimeTravelType(options.type),
          reason: options.reason ?? "",
          historyEventTime: options.historyEventTime?.toISOString() ?? "",
          stepType: options.stepType ?? "",
          stepExecutionId: options.stepExecutionId ?? "",
          skipWritesReapply: options.skipWritesReapply ?? false,
          stepMethod: mapTimeTravelStepMethod(options.stepMethod),
        },
        callback,
      ),
    );
    return response.runId;
  }

  /**
   * Makes one waiting Timer condition immediately ready.
   * @param flowId - Non-empty active Flow ID.
   * @param stepExecutionId - Step type and positive execution number.
   * @param timerId - Exactly one Timer condition ID or zero-based index.
   */
  public async skipTimer(
    flowId: string,
    stepExecutionId: StepExecutionId,
    timerId: TimerId,
  ): Promise<void> {
    await unary<Empty>(
      { operation: "skipTimer", flowId, requirement: "active" },
      (callback) =>
      this.service.skipTimer(
        {
          flowId: requireName(flowId),
          runId: "",
          stepExecutionId: `${stepExecutionId.stepType}-${stepExecutionId.number ?? 1}`,
          timerConditionId: timerId.conditionId ?? "",
          timerConditionIndex: timerId.conditionIndex,
        },
        callback,
      ),
    );
  }

  /**
   * Long-polls until one Step execution completes.
   * @param flowId - Non-empty active Flow ID.
   * @param stepExecutionId - Step type and positive execution number.
   * @param timeoutMs - Non-negative server-side wait duration in milliseconds.
   * @throws {@link LongPollTimeoutError} when completion is not observed in time.
   */
  public async waitForStepCompletion(
    flowId: string,
    stepExecutionId: StepExecutionId,
    timeoutMs: number,
  ): Promise<void> {
    await unary<WaitForStepCompletionResponse>(
      { operation: "waitForStepCompletion", flowId, requirement: "active" },
      (callback) =>
      this.service.waitForStepCompletion(
        {
          flowId: requireName(flowId),
          stepType: stepExecutionId.stepType,
          stepExecutionNumber: String(stepExecutionId.number ?? 1),
          waitTimeSeconds: seconds(timeoutMs),
          requestId: crypto.randomUUID(),
        },
        callback,
      ),
    );
  }

  /**
   * Waits until a singleton Attribute in the current run equals the expected value.
   * Generates a request ID and rejects JSON, bytes, and null before transport.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered singleton Attribute to observe.
   * @param expected - String, boolean, integer, or number value to await.
   * @param timeoutMs - Non-negative server-side wait duration in milliseconds.
   */
  public waitForAttributeEqual<T>(
    flowId: string,
    attribute: Attribute<T>,
    expected: T,
    timeoutMs: number,
  ): Promise<void>;

  /**
   * Waits until one AttributeMap instance in the current run matches.
   * Primitive-value restrictions and timeout errors match `waitForAttributeEqual`.
   * @typeParam T - AttributeMap value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered AttributeMap to observe.
   * @param instance - Non-empty logical map key to observe.
   * @param expected - String, boolean, integer, or number value to await.
   * @param timeoutMs - Non-negative server-side wait duration in milliseconds.
   */
  public waitForAttributeEqual<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    expected: T,
    timeoutMs: number,
  ): Promise<void>;

  /**
   * Waits until a singleton Attribute or AttributeMap instance equals the expected value.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered Attribute or AttributeMap to observe.
   * @param args - Expected value and timeout, optionally preceded by a map instance.
   */
  public async waitForAttributeEqual(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    ...args: unknown[]
  ): Promise<void> {
    if (attribute instanceof Attribute) {
      if (args.length !== 2 || typeof args[1] !== "number") {
        throw new TypeError("waitForAttributeEqual received invalid Attribute arguments");
      }
      await this.waitForAttributeValue(flowId, attribute, undefined, args[0], args[1]);
      return;
    }
    if (attribute instanceof AttributeMap) {
      if (args.length !== 3 || typeof args[0] !== "string" || typeof args[2] !== "number") {
        throw new TypeError("waitForAttributeEqual received invalid AttributeMap arguments");
      }
      await this.waitForAttributeValue(flowId, attribute, args[0], args[1], args[2]);
      return;
    }
    throw new TypeError("waitForAttributeEqual received an invalid Attribute definition");
  }

  private async waitForAttributeValue(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instance: string | undefined,
    expected: unknown,
    timeoutMs: number,
  ): Promise<void> {
    const value = encodeValue(attribute.codec, expected);
    if (
      value.kind?.$case !== "stringValue" &&
      value.kind?.$case !== "boolValue" &&
      value.kind?.$case !== "intValue" &&
      value.kind?.$case !== "doubleValue"
    ) {
      throw new TypeError(
        "waitForAttributeEqual supports only string, boolean, or number values",
      );
    }
    await unary<Empty>(
      { operation: "waitForAttributeEqual", flowId, requirement: "active" },
      (callback) =>
        this.service.waitForAttribute(
          {
            flowId: requireName(flowId),
            runId: "",
            condition: {
              kind: {
                $case: "equal",
                value: {
                  key: physicalName(attribute.name, instance),
                  value,
                },
              },
            },
            waitTimeSeconds: seconds(timeoutMs),
            requestId: crypto.randomUUID(),
          },
          callback,
        ),
     );
   }

  /**
   * Replaces mutable configuration for an active Flow.
   * The update affects later decisions and does not recall dispatched work.
   * @param flowId - Non-empty active Flow ID.
   * @param config - New optional Flow configuration fields.
   */
  public async updateFlowConfig(flowId: string, config: FlowConfig): Promise<void> {
    await unary<Empty>(
      { operation: "updateFlowConfig", flowId, requirement: "active" },
      (callback) =>
      this.service.updateFlowConfig(
        {
          flowId: requireName(flowId),
          runId: "",
          flowConfig: mapFlowConfig(config, this.options),
        },
        callback,
      ),
    );
  }

  /**
   * Requests that an active Flow roll its history into a new run.
   * The Flow ID stays the same; the request returns after Dex accepts it.
   * @param flowId - Non-empty active Flow ID.
   */
  public async triggerContinueAsNew(flowId: string): Promise<void> {
    await unary<Empty>(
      { operation: "triggerContinueAsNew", flowId, requirement: "active" },
      (callback) =>
        this.service.triggerContinueAsNew(
          { flowId: requireName(flowId), runId: "" },
          callback,
        ),
    );
  }

  /**
   * Closes the owned FlowService connection.
   * Registry and BlobCache ownership remains with the caller.
   */
  public async close(): Promise<void> {
    this.service.close();
  }
}

function mapStepOptions(
  options: StepOptions | undefined,
  skipWaitFor: boolean,
  flow: RegisteredFlow,
): ProtoStepOptions {
  const executeFailureStep = options?.executeFailure?.step;
  const executeFailureDefinition = executeFailureStep === undefined
    ? undefined
    : flow.stepsByClass.get(executeFailureStep);
  if (executeFailureStep !== undefined && executeFailureDefinition === undefined) {
    throw new TypeError("execute failure Step must belong to the Flow");
  }
  const executeFailureOptions =
    options?.executeFailure?.options ?? executeFailureDefinition?.step.getStepOptions?.();
  return ProtoStepOptions.create({
    waitForTimeoutSeconds: seconds(options?.waitForMethodTimeoutMs),
    executeTimeoutSeconds: seconds(options?.executeMethodTimeoutMs),
    heartbeatTimeoutSeconds: heartbeatSeconds(options?.heartbeatTimeoutMs),
    waitForRetryPolicy: mapRetryPolicy(options?.waitForRetry),
    executeRetryPolicy: mapRetryPolicy(options?.executeRetry),
    waitForFailurePolicy:
      options?.waitForFailure === "proceed"
        ? WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE
        : options?.waitForFailure === "failFlow"
          ? WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE
          : WaitForMethodFailurePolicy.WAIT_FOR_METHOD_FAILURE_POLICY_UNSPECIFIED,
    executeFailurePolicy:
      executeFailureDefinition === undefined
        ? ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED
        : ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
    executeFailureProceedStepType: executeFailureDefinition?.name ?? "",
    executeFailureProceedStepOptions:
      executeFailureDefinition === undefined
        ? undefined
        : mapStepOptions(
            executeFailureOptions,
            executeFailureDefinition.step.waitFor === undefined,
            flow,
          ),
    skipWaitFor,
    waitForDurabilityOverride: mapDurability(options?.waitForDurability),
    executeDurabilityOverride: mapDurability(options?.executeDurability),
    waitForLockAttributeKeys: (options?.waitForLockAttributes ?? []).map((lock) =>
      physicalName(lock.attribute.name, lock.instance),
    ),
    executeLockAttributeKeys: (options?.executeLockAttributes ?? []).map((lock) =>
      physicalName(lock.attribute.name, lock.instance),
    ),
  });
}

function mapRetryPolicy(retry: RetryPolicy | undefined) {
  if (retry === undefined) {
    return undefined;
  }
  return {
    initialIntervalSeconds: seconds(retry.initialIntervalMs),
    backoffCoefficient: retry.backoffCoefficient ?? 0,
    maximumIntervalSeconds: seconds(retry.maximumIntervalMs),
    maximumAttempts: retry.maximumAttempts ?? 0,
    totalDurationSeconds: seconds(retry.totalDurationMs),
  };
}

function mapFlowRetryPolicy(retry: RetryPolicy | undefined) {
  if (retry === undefined) {
    return undefined;
  }
  return {
    initialIntervalSeconds: seconds(retry.initialIntervalMs),
    backoffCoefficient: retry.backoffCoefficient ?? 0,
    maximumIntervalSeconds: seconds(retry.maximumIntervalMs),
    maximumAttempts: retry.maximumAttempts ?? 0,
  };
}

function mapFlowConfig(
  config: FlowConfig | undefined,
  clientOptions: ClientOptions,
): ProtoFlowConfig | undefined {
  const workerTarget = config?.workerTarget ?? clientOptions.workerTarget;
  if (config === undefined && workerTarget === undefined) {
    return undefined;
  }
  return {
    activeStepSearchMode:
      config?.activeStepSearchMode === undefined
        ? undefined
        : config.activeStepSearchMode === ActiveStepSearchMode.ALL
          ? ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL
          : ProtoActiveStepSearchMode.ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED,
    attributeStoreNames: mapAttributeStoreNames(config),
    continueAsNewThreshold: config?.continueAsNewThreshold,
    continueAsNewPageSizeInBytes: config?.continueAsNewPageSizeBytes,
    stepDurability:
      config?.stepDurability === undefined ? undefined : mapDurability(config.stepDurability),
    workerTarget:
      workerTarget === undefined
        ? undefined
        : { address: workerTarget.address, isHeadlessAddress: workerTarget.headless ?? false },
  };
}

function mapIndex(index: Attribute<unknown>["index"] | undefined): IndexConfig | undefined {
  if (index === undefined) {
    return undefined;
  }
  const types: Record<IndexType, ProtoIndexType> = {
    [IndexType.KEYWORD]: ProtoIndexType.INDEX_TYPE_KEYWORD,
    [IndexType.FULL_TEXT]: ProtoIndexType.INDEX_TYPE_TEXT,
    [IndexType.KEYWORD_ARRAY]: ProtoIndexType.INDEX_TYPE_KEYWORD_ARRAY,
    [IndexType.INT]: ProtoIndexType.INDEX_TYPE_INT,
    [IndexType.DOUBLE]: ProtoIndexType.INDEX_TYPE_DOUBLE,
    [IndexType.BOOL]: ProtoIndexType.INDEX_TYPE_BOOL,
    [IndexType.DATETIME]: ProtoIndexType.INDEX_TYPE_DATETIME,
  };
  return {
    enable: true,
    type: types[index.type],
    indexKey: index.indexKey ?? "",
  };
}

function mapIdReusePolicy(policy: IdReusePolicy | undefined): ProtoIdReusePolicy {
  switch (policy) {
    case IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED:
      return ProtoIdReusePolicy.ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY;
    case IdReusePolicy.ALLOW_IF_NOT_RUNNING:
      return ProtoIdReusePolicy.ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING;
    case IdReusePolicy.ALLOW_TERMINATE_IF_RUNNING:
      return ProtoIdReusePolicy.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING;
    case IdReusePolicy.DISALLOW:
      return ProtoIdReusePolicy.ID_REUSE_POLICY_DISALLOW_REUSE;
    default:
      return ProtoIdReusePolicy.ID_REUSE_POLICY_UNSPECIFIED;
  }
}

function mapStopType(type: StopType | undefined): ProtoStopType {
  switch (type) {
    case StopType.TERMINATE:
      return ProtoStopType.STOP_TYPE_TERMINATE;
    case StopType.FAIL:
      return ProtoStopType.STOP_TYPE_FAIL;
    default:
      return ProtoStopType.STOP_TYPE_CANCEL;
  }
}

function mapTimeTravelType(type: TimeTravelType): ProtoFlowResetType {
  const types: Record<TimeTravelType, ProtoFlowResetType> = {
    [TimeTravelType.BEGINNING]: ProtoFlowResetType.FLOW_RESET_TYPE_BEGINNING,
    [TimeTravelType.HISTORY_EVENT_TIME]: ProtoFlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_TIME,
    [TimeTravelType.STEP_TYPE]: ProtoFlowResetType.FLOW_RESET_TYPE_STEP_TYPE,
    [TimeTravelType.STEP_EXECUTION_ID]: ProtoFlowResetType.FLOW_RESET_TYPE_STEP_EXECUTION_ID,
  };
  return types[type];
}

function mapTimeTravelStepMethod(
  method: TimeTravelStepMethod | undefined,
): ProtoFlowResetStepMethod {
  switch (method) {
    case TimeTravelStepMethod.WAIT_FOR:
      return ProtoFlowResetStepMethod.FLOW_RESET_STEP_METHOD_WAIT_FOR;
    case TimeTravelStepMethod.EXECUTE:
      return ProtoFlowResetStepMethod.FLOW_RESET_STEP_METHOD_EXECUTE;
    default:
      return ProtoFlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED;
  }
}

function mapDurability(value: "sync" | "async" | undefined): ProtoStepDurability {
  if (value === "sync") {
    return ProtoStepDurability.STEP_DURABILITY_SYNC;
  }
  if (value === "async") {
    return ProtoStepDurability.STEP_DURABILITY_ASYNC;
  }
  return ProtoStepDurability.STEP_DURABILITY_UNSPECIFIED;
}

function mapFlowStatus(status: ProtoFlowStatus): FlowStatus {
  switch (status) {
    case ProtoFlowStatus.FLOW_STATUS_RUNNING:
      return "running";
    case ProtoFlowStatus.FLOW_STATUS_COMPLETED:
      return "completed";
    case ProtoFlowStatus.FLOW_STATUS_FAILED:
      return "failed";
    case ProtoFlowStatus.FLOW_STATUS_SERVER_SIDE_TIMEOUT_INTERNAL_ONLY:
      return "serverSideTimeoutInternalOnly";
    case ProtoFlowStatus.FLOW_STATUS_TERMINATED:
      return "terminated";
    case ProtoFlowStatus.FLOW_STATUS_CANCELED:
      return "cancelled";
    case ProtoFlowStatus.FLOW_STATUS_CONTINUED_AS_NEW:
      return "continuedAsNew";
    default:
      throw new TypeError(`unsupported Flow status ${status}`);
  }
}

function mapFlowErrorType(type: ProtoFlowErrorType): FlowErrorTypeValue | undefined {
  switch (type) {
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW:
      return FlowErrorType.STEP_DECISION_FAILED;
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW:
      return FlowErrorType.CLIENT_API_FAILED;
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_WORKER_API_FAIL:
      return FlowErrorType.WORKER_API_FAILED;
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE:
      return FlowErrorType.INVALID_USER_FLOW_CODE;
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_INTERNAL:
      return FlowErrorType.INTERNAL;
    case ProtoFlowErrorType.FLOW_ERROR_TYPE_FLOW_TIMEOUT:
      return FlowErrorType.FLOW_TIMEOUT;
    default:
      return undefined;
  }
}

function resolveFlowTimeoutPolicy(
  flow: RegisteredFlow,
  timeoutSeconds: number,
  policy: FlowTimeoutPolicy | undefined,
): ProtoFlowTimeoutPolicy {
  const requested = policy ?? FlowTimeoutPolicy.DEFAULT;
  if (timeoutSeconds === 0) {
    if (requested !== FlowTimeoutPolicy.DEFAULT) {
      throw new RangeError("Flow timeout policy requires a positive timeout");
    }
    return ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_UNSPECIFIED;
  }
  const resolved = requested === FlowTimeoutPolicy.DEFAULT
    ? flow.hasTimeoutHandler
      ? FlowTimeoutPolicy.HANDLER
      : FlowTimeoutPolicy.FAIL
    : requested;
  if (resolved === FlowTimeoutPolicy.HANDLER && !flow.hasTimeoutHandler) {
    throw new TypeError(`Flow ${flow.name} does not implement handleTimeout`);
  }
  return {
    [FlowTimeoutPolicy.DEFAULT]: ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_UNSPECIFIED,
    [FlowTimeoutPolicy.FAIL]: ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_FAIL,
    [FlowTimeoutPolicy.CANCEL]: ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_CANCEL,
    [FlowTimeoutPolicy.HANDLER]: ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_HANDLER,
  }[resolved];
}

function physicalName(name: string, instance?: string): string {
  if (instance === undefined) {
    return name;
  }
  requireName(instance);
  const encoded = encodeURIComponent(instance).replace(/[!'()*]/g, (character) =>
    `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
  );
  return `${name}/${encoded}`;
}

function seconds(milliseconds: number | undefined): number {
  if (milliseconds === undefined) {
    return 0;
  }
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds % 1_000 !== 0) {
    throw new RangeError("duration must be a non-negative whole number of seconds");
  }
  return milliseconds / 1_000;
}

function heartbeatSeconds(milliseconds: number | undefined): number {
  const value = seconds(milliseconds);
  if (value > 2_147_483_647) {
    throw new RangeError("heartbeat timeout exceeds int32 seconds");
  }
  return value;
}

function number64(value: bigint | undefined): number {
  if (value === undefined) {
    return 0;
  }
  const number = Number(value);
  if (!Number.isSafeInteger(number)) {
    throw new RangeError("history event ID exceeds JavaScript's safe integer range");
  }
  return number;
}

function unary<Response>(
  target: {
    readonly operation: string;
    readonly flowId?: string;
    readonly requirement: "none" | "existing" | "active";
  },
  invoke: (callback: (error: ServiceError | null, response: Response) => void) => unknown,
): Promise<Response> {
  return new Promise((resolve, reject) => {
    invoke((error, response) => {
      if (error !== null) {
        reject(translateServiceError(error, target.operation, target.flowId, target.requirement));
        return;
      }
      resolve(response);
    });
  });
}
