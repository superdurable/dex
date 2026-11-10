// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Metadata, status, type ServiceError } from "@grpc/grpc-js";

import { appendInitialWorkQueuePermissions } from "./action.js";
import type { BlobCache } from "./blob-cache.js";
import { AttributeMatch } from "./attribute-match.js";
import { credentialInterceptors } from "./credential.js";
import { createFlowServiceChannel } from "./flow-service-tls.js";
import {
  isHardDeleteOnNullDefinition,
  requireHardDeleteAttributeWriteSupport,
  requireHardDeleteOnNullValueKind,
  requireNotHardDeleteOnNull,
} from "./attribute-hard-delete.js";
import { mapActiveStepSearchMode } from "./active-step-search.js";
import { mapAttributeStoreNames, mapAttributeStoreSync } from "./attribute-store-sync.js";
import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import {
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
  type GetFlowSummaryResponse,
  type InvokeRPCResponse,
  type ListStreamMessagesResponse,
  type ReadStreamResponse,
  type ResetFlowResponse,
  type SearchFlowsResponse,
  type SearchFlowsResponseEntry,
  type StartFlowResponse,
  type AttributeWrite,
  type ServerInfo,
  StartFlowRequest,
  StepDurability as ProtoStepDurability,
  StepOptions as ProtoStepOptions,
  FlowTimeoutHandlerOptions as ProtoFlowTimeoutHandlerOptions,
  StopType as ProtoStopType,
  WaitForMethodFailurePolicy,
  type FlowServiceClient as FlowServiceClientType,
  type FlowConfig as ProtoFlowConfig,
  type FlowResult as ProtoFlowResult,
  type IndexConfig,
  type WaitForStepCompletionResponse,
  type WaitForAttributeResponse,
  type Value as ProtoValue,
} from "./gen/dex.js";
import type { Empty } from "./gen/google/protobuf/empty.js";
import {
  ErrorSubStatus,
  BlobMessageTooLargeError,
  DexServiceError,
  FlowDefinitionError,
  FlowErrorType,
  LongPollTimeoutError,
  RequestTimeoutError,
  ValueMappingError,
  type FlowErrorType as FlowErrorTypeValue,
} from "./errors.js";
import {
  registeredAttributeFlow,
  registeredFlow,
  registeredStepTarget,
  registeredRPC,
  registeredRPCFlow,
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
  FlowTimeoutPolicy,
  IdReusePolicy,
  TimeTravelStepMethod,
  TimeTravelType,
  StopType,
  type ClientOptions,
  type FlowConfig,
  type FlowInfo,
  type FlowTimeoutHandlerOptions,
  type FlowStatus,
  type InitialAttribute,
  type TimeTravelOptions,
  type SearchFlowEntry,
  type SearchFlowsPage,
  type StartFlowOptions,
  type StepExecutionId,
  type StopFlowOptions,
  type TimerId,
  type WaitForAttributeOptions,
  type WaitForStepCompletionOptions,
} from "./options.js";
import {
  Attribute,
  AttributeMap,
  AttributeMapInstance,
  IndexType,
  type AttributeLock,
  type AttributeRead,
  type AttributeReadValues,
} from "./persistence.js";
import type { RPCInvokeOptions, RPCResult } from "./rpc.js";
import { sdkVersion } from "./sdk-version.generated.js";
import { negotiateServerProtocol } from "./server-protocol.js";
import type { RetryPolicy, StepOptions } from "./step.js";
import { physicalMapName, requireName } from "./validation.js";
import { voidCodec } from "./codec.js";
import { codecOrJson, decodeUnknown, decodeValue, encodeValue, ValueHydrator } from "./value-mapper.js";
import { Channel, ChannelMap } from "./wait.js";
import type { Stream, StreamMessage, StreamMessagesPage } from "./stream.js";

const defaultServerAddress = "localhost:8801";

/**
 * Calls Dex FlowService through registered, typed Flow definitions.
 *
 * Methods perform asynchronous gRPC I/O. A Client owns its service connection but
 * not its Registry or BlobCache; call `close` during application shutdown.
 * Before its first request, every method shares lazy Server protocol negotiation.
 * The constructor performs no I/O. This SDK requires protocol 4.
 * After an incompatible Server answers, every later call fails with a new Error that has the same message.
 * A transport failure during negotiation rejects with the usual DexServiceError and is retried on the next call.
 * Wait methods with a request timeout bound negotiation by that timeout and reject with RequestTimeoutError.
 * Service errors retain code, subStatus, detail, operation, flowId, and the original cause.
 * Changed in SDK v1.35.0: protocol 4 requires server-zd v0.1.17 or later; upgrade the Server first.
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
  private serverInfoRequest: Promise<ServerInfo> | undefined;

  /**
   * Constructs a Client and lazy gRPC connection, plaintext unless `options.flowServiceTls` is set.
   * Changed in 1.38.0: the default gRPC receive limit rose from 4 MiB to
   * `DEFAULT_GRPC_MAX_MESSAGE_BYTES`, and a LoadBlobs batch over the SDK's gRPC limit is split
   * instead of failing.
   * @param registry - Flow definitions used for validation and routing.
   * @param blobCache - Open cache used to hydrate large response values.
   * @param options - Service address, optional bearer credential, optional TLS settings, and optional gRPC message size limit.
   * @throws {TypeError} When `flowServiceTls` is set and the address or TLS settings are invalid.
   * @throws {RangeError} When `grpcMaxMessageBytes` is negative or not an integer.
   */
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: ClientOptions = {},
  ) {
    const channel = createFlowServiceChannel(
      options.serverAddress ?? defaultServerAddress,
      options.flowServiceTls,
      options.grpcMaxMessageBytes,
    );
    this.service = new FlowServiceClient(
      channel.target,
      channel.channelCredentials,
      { ...channel.channelOptions, interceptors: credentialInterceptors(options.credential) },
    );
    this.hydrator = new ValueHydrator(this.service, blobCache);
  }

  /**
   * Starts a Flow and returns after Dex accepts it.
   * Acceptance does not mean the starting Step has run; use waitForFlow for terminal results.
   * Omitted reuse policy rejects any previously used Flow ID.
   * Omit retryPolicy and positive startDelayMs; the current Server rejects those options.
   * Use Step retries for failures and a Timer condition in a starting Step for a durable delay.
   * Changed in SDK v1.35.0: a Step waits durably for a compatible Worker instead of failing
   * because none is available. That initial wait consumes no Step retry or method timeout.
   * @typeParam StartInput - Starting Step input type.
   * @param flow - Exact Flow instance registered with this Client.
   * @param flowId - Non-empty application ID stable across runs.
   * @param input - Starting Step input, or `undefined` when no start Step exists.
   * @param options - Timeout, reuse, retry, configuration, and initial state.
   * @returns The server-assigned run ID.
   * @throws {@link FlowAlreadyStartedError} when reuse policy rejects the Flow ID.
   * @throws {@link FlowDefinitionError} when `flow` is not registered.
   * @throws {@link TypeError} when a hard-delete initial Attribute is not the definition registered with `flow`.
   * @throws {@link ValueMappingError} when a hard-delete initial value encodes to a number or Boolean.
   * @throws {@link Error} when the Server is incompatible with required protocol version 4.
   * @throws {@link DexServiceError} for other transport or service failures, including INVALID_ARGUMENT
   * when a Flow retry policy or positive start delay is supplied.
   */
  public async startFlow<StartInput>(
    flow: Flow<StartInput>,
    flowId: string,
    input: StartInput,
    options: StartFlowOptions = {},
  ): Promise<string> {
    const registered = registeredFlow(this.registry, flow);
    const flowTimeoutSeconds = seconds(options.timeoutMs);
    const flowTimeoutPolicy = resolveFlowTimeoutPolicy(
      registered,
      flowTimeoutSeconds,
      options.timeoutPolicy,
    );
    const initialAttributeWrites = appendInitialWorkQueuePermissions(
      registered.actions,
      (options.attributes ?? []).map((initial) => mapInitialAttributeWrite(registered, initial)),
    );
    const request = StartFlowRequest.create({
      flowId: requireName(flowId),
      flowType: registered.name,
      flowTimeoutSeconds,
      flowTimeoutPolicy,
      requestId: options.requestId ?? crypto.randomUUID(),
      flowStartOptions: {
        idReusePolicy: mapIdReusePolicy(options.idReusePolicy),
        flowStartDelaySeconds: seconds(options.startDelayMs),
        retryPolicy: mapFlowRetryPolicy(options.retryPolicy),
        attributes: initialAttributeWrites,
        flowConfigOverride: {
          ...mapFlowConfig(options.configOverride),
          workerTarget: { isPullMode: true },
        },
        flowAlreadyStartedOptions: {
          ignoreAlreadyStartedError: options.ignoreAlreadyStarted ?? false,
        },
        timeoutHandlerOptions: mapFlowTimeoutHandlerOptions(
          registered,
          flowTimeoutSeconds,
          flowTimeoutPolicy,
          options.timeoutHandlerOptions,
        ),
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
    await this.requireHardDeleteAttributeWriteSupport(initialAttributeWrites);
    return (await this.unary<StartFlowResponse>(
      { operation: "startFlow", flowId, requirement: "none" },
      (callback) => this.service.startFlow(request, callback),
    ))
      .runId;
  }

  private async requireHardDeleteAttributeWriteSupport(
    writes: readonly AttributeWrite[],
  ): Promise<void> {
    if (!writes.some((write) => write.isHardDeleteOnNull)) {
      return;
    }
    requireHardDeleteAttributeWriteSupport(await this.negotiateServerProtocol(), writes);
  }

  private async negotiateServerProtocol(): Promise<number> {
    return negotiateServerProtocol(await this.sharedServerInfo(), sdkVersion);
  }

  private async negotiateServerProtocolWithinBudget(
    requestBudget: ClientRequestBudget,
    operation: string,
    flowId: string,
  ): Promise<number> {
    const remainingMs = requestBudget.remainingMs(operation, flowId);
    if (remainingMs === undefined) return this.negotiateServerProtocol();
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const serverInfo = await Promise.race([
        this.sharedServerInfo(),
        new Promise<never>((_resolve, reject) => {
          // Re-arms in steps because Node fires longer setTimeout delays immediately.
          const expireAtBudget = (): void => {
            try {
              const leftMs = requestBudget.remainingMs(operation, flowId) ?? remainingMs;
              timer = setTimeout(expireAtBudget, Math.min(leftMs, 2_147_483_647));
            } catch (failure) {
              reject(failure);
            }
          };
          expireAtBudget();
        }),
      ]);
      return negotiateServerProtocol(serverInfo, sdkVersion);
    } finally {
      clearTimeout(timer);
    }
  }

  private sharedServerInfo(): Promise<ServerInfo> {
    if (this.serverInfoRequest === undefined) {
      const request = this.getServerInfo();
      this.serverInfoRequest = request;
      // Transient failures are not cached; the next call asks the Server again.
      request.catch(() => {
        if (this.serverInfoRequest === request) this.serverInfoRequest = undefined;
      });
    }
    return this.serverInfoRequest;
  }

  private async unary<Response>(
    target: Parameters<typeof unary>[0],
    invoke: (callback: (error: ServiceError | null, response: Response) => void) => unknown,
  ): Promise<Response> {
    await this.negotiateServerProtocol();
    return unary(target, invoke);
  }

  private getServerInfo(): Promise<ServerInfo> {
    return unary<ServerInfo>(
      { operation: "getServerInfo", requirement: "none" },
      (callback) => this.service.getServerInfo({}, callback),
    );
  }

  /**
   * Invokes an RPC with typed input and output.
   * A non-transactional RPC without Attribute locks starts from a read snapshot. A retained
   * terminal execution can serve it when the handler returns no durable effects. Locks,
   * transactional execution, returned effects, or server policy can require an active execution.
   * The call waits for handler completion and application of its durable effects before returning output.
   * Lock acquisition is nonblocking; retry a lock conflict only when the operation is safe to repeat.
   * @example
   * ```ts
   * try {
   *   const output = await client.invokeRPC(orders.describeOrder, "order-42");
   *   console.log(output);
   * } catch (error) {
   *   if (error instanceof FlowNotActiveOrNotFoundError) {
   *     console.log("Flow is missing or unavailable for this RPC");
   *   } else {
   *     throw error;
   *   }
   * }
   * ```
   * @throws {@link RpcLockConflictError} when the requested locks are held.
   * @throws {@link BlobMessageTooLargeError} when one output blob alone exceeds a gRPC message size limit.
   * @throws {@link WorkerInvocationError} when Worker dispatch or application execution fails.
   * @throws {@link FlowNotActiveOrNotFoundError} when the target is missing or this RPC requires an active Flow.
   * @throws {@link FlowClosingError} when the Flow is closing and rejects the operation.
   * @throws {@link DexServiceError} for other transport or service failures.
   * @throws {@link FlowDefinitionError} when the method is not registered.
   * @throws {@link ValueMappingError} when input encoding or output decoding fails.
   * @typeParam Input - RPC input type.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
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
   * A retained terminal execution can serve a query-only RPC. Other execution paths can require
   * an active execution.
   * See the typed-input invokeRPC overload for completion semantics and error handling.
   * @throws {@link RpcLockConflictError} when the requested locks are held.
   * @throws {@link BlobMessageTooLargeError} when one output blob alone exceeds a gRPC message size limit.
   * @throws {@link WorkerInvocationError} when Worker dispatch or application execution fails.
   * @throws {@link FlowNotActiveOrNotFoundError} when missing or unable to use a closed execution.
   * @throws {@link DexServiceError} for other service or transport failures.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
   * @returns The decoded RPCResult output.
   */
  public invokeRPC<Output>(
    rpcMethod: (context: Context) => RPCResult<Output> | Promise<RPCResult<Output>>,
    flowId: string,
    runId?: string,
  ): Promise<Output>;

  /**
   * Invokes a typed-input RPC that returns no output.
   * A retained terminal execution can serve a query-only RPC. Other execution paths can require
   * an active execution.
   * See the typed-input invokeRPC overload for completion semantics and error handling.
   * @throws {@link RpcLockConflictError} when the requested locks are held.
   * @throws {@link WorkerInvocationError} when Worker dispatch or application execution fails.
   * @throws {@link FlowNotActiveOrNotFoundError} when missing or unable to use a closed execution.
   * @throws {@link DexServiceError} for other service or transport failures.
   * @typeParam Input - RPC input type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
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
   * A retained terminal execution can serve a query-only RPC. Other execution paths can require
   * an active execution.
   * See the typed-input invokeRPC overload for completion semantics and error handling.
   * @throws {@link RpcLockConflictError} when the requested locks are held.
   * @throws {@link BlobMessageTooLargeError} when one output blob alone exceeds a gRPC message size limit.
   * @throws {@link WorkerInvocationError} when Worker dispatch or application execution fails.
   * @throws {@link FlowNotActiveOrNotFoundError} when missing or unable to use a closed execution.
   * @throws {@link DexServiceError} for other service or transport failures.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
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
   * @param runId - Exact run for an input-bearing RPC; the server resolves the current execution
   * when omitted.
   * @returns Decoded output, or `undefined` for an output-free RPC.
   * @throws {@link RpcLockConflictError} when locks cannot be acquired.
   * @throws {@link WorkerInvocationError} when the application handler fails.
   * @throws {@link FlowNotActiveOrNotFoundError} when the Flow is missing or the selected path
   * cannot use its closed execution.
   */
  public async invokeRPC(
    rpcMethod: Function,
    flowId: string,
    inputOrRunId?: unknown,
    runId = "",
  ): Promise<unknown> {
    const rpc = registeredRPC(this.registry, rpcMethod);
    return this.doInvokeRPC(
      rpcMethod,
      flowId,
      rpc.hasInput ? inputOrRunId : undefined,
      {},
      rpc.hasInput ? runId : (inputOrRunId as string | undefined) ?? "",
    );
  }

  /**
   * Invokes an input-bearing RPC with additive runtime map-instance selections.
   * @typeParam Input - RPC input type.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param options - Runtime AttributeMap locks and exact map-instance loads.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
   * @returns The decoded RPCResult output.
   */
  public invokeRPCWithOptions<Input, Output>(
    rpcMethod: (
      context: Context,
      input: Input,
    ) => RPCResult<Output> | Promise<RPCResult<Output>>,
    flowId: string,
    input: Input,
    options: RPCInvokeOptions,
    runId?: string,
  ): Promise<Output>;

  /**
   * Invokes an input-free RPC with additive runtime map-instance selections.
   * @typeParam Output - RPC output type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param options - Runtime AttributeMap locks and exact map-instance loads.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
   * @returns The decoded RPCResult output.
   */
  public invokeRPCWithOptions<Output>(
    rpcMethod: (context: Context) => RPCResult<Output> | Promise<RPCResult<Output>>,
    flowId: string,
    options: RPCInvokeOptions,
    runId?: string,
  ): Promise<Output>;

  /**
   * Invokes an input-bearing, output-free RPC with runtime map-instance selections.
   * @typeParam Input - RPC input type.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param input - Typed handler input.
   * @param options - Runtime AttributeMap locks and exact map-instance loads.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
   * @returns A promise resolved after successful handler completion.
   */
  public invokeRPCWithOptions<Input>(
    rpcMethod: (context: Context, input: Input) => void | Promise<void>,
    flowId: string,
    input: Input,
    options: RPCInvokeOptions,
    runId?: string,
  ): Promise<void>;

  /**
   * Invokes an input-free, output-free RPC with runtime map-instance selections.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param options - Runtime AttributeMap locks and exact map-instance loads.
   * @param runId - Optional exact run; the server resolves the current execution when omitted.
   * @returns A promise resolved after successful handler completion.
   */
  public invokeRPCWithOptions(
    rpcMethod: (context: Context) => void | Promise<void>,
    flowId: string,
    options: RPCInvokeOptions,
    runId?: string,
  ): Promise<void>;

  /**
   * Implements RPC invocation with additive runtime map-instance selections.
   * @param rpcMethod - Bound method of the registered Flow, declared with the `rpc` decorator or `declareRPCMethod`.
   * @param flowId - Non-empty target Flow ID.
   * @param inputOrOptions - Typed input for an input-bearing RPC, or options for an input-free RPC.
   * @param optionsOrRunId - Runtime options for an input-bearing RPC, or an optional run ID for an input-free RPC.
   * @param runId - Optional exact run for an input-bearing RPC.
   * @returns The decoded RPC output, or `undefined` after an output-free RPC completes.
   */
  public async invokeRPCWithOptions(
    rpcMethod: Function,
    flowId: string,
    inputOrOptions: unknown,
    optionsOrRunId?: RPCInvokeOptions | string,
    runId = "",
  ): Promise<unknown> {
    const rpc = registeredRPC(this.registry, rpcMethod);
    if (rpc.hasInput) {
      return this.doInvokeRPC(
        rpcMethod,
        flowId,
        inputOrOptions,
        optionsOrRunId as RPCInvokeOptions,
        runId,
      );
    }
    return this.doInvokeRPC(
      rpcMethod,
      flowId,
      undefined,
      inputOrOptions as RPCInvokeOptions,
      (optionsOrRunId as string | undefined) ?? "",
    );
  }

  private async doInvokeRPC(
    rpcMethod: Function,
    flowId: string,
    input: unknown,
    invokeOptions: RPCInvokeOptions,
    runId: string,
  ): Promise<unknown> {
    const rpc = registeredRPC(this.registry, rpcMethod);
    const flow = registeredRPCFlow(this.registry, rpcMethod);
    const invocationLocks = (invokeOptions.lockAttributeMapInstances ?? []).map((lock) => {
      if (!(lock.attribute instanceof AttributeMap) || lock.instance === undefined) {
        throw new FlowDefinitionError(
          "RPCInvokeOptions lock must target an AttributeMap instance",
        );
      }
      requireRPCInvokeDefinition(flow, lock.attribute, "AttributeMap");
      return physicalName(lock.attribute.name, lock.instance);
    });
    const invocationAttributeMapLoads = (invokeOptions.loadAttributeMapInstances ?? []).map(
      (load) => {
        requireRPCInvokeDefinition(flow, load.attributeMap, "AttributeMap");
        return physicalName(load.attributeMap.name, load.instance);
      },
    );
    const invocationChannelMapLoads = (invokeOptions.loadChannelMapInstances ?? []).map(
      (load) => {
        requireRPCInvokeDefinition(flow, load.channelMap, "ChannelMap");
        return physicalName(load.channelMap.name, load.instance);
      },
    );
    const response = await this.unary<InvokeRPCResponse>(
      { operation: "invokeRPC", flowId, requirement: "active" },
      (callback) =>
      this.service.invokeRpc(
        {
          flowId: requireName(flowId),
          runId,
          rpcName: rpc.name,
          input: rpc.hasInput
            ? encodeValue(codecOrJson(rpc.options.inputCodec), input)
            : undefined,
          timeoutSeconds: seconds(rpc.options.timeoutMs),
          lockAttributeKeys: uniqueSorted([
            ...(rpc.options.lockAttributes ?? []).map((lock) =>
              physicalName(lock.attribute.name, lock.instance),
            ),
            ...invocationLocks,
          ]),
          requestId: crypto.randomUUID(),
          isTransactional: rpc.options.isTransactional ?? false,
          loadAttributeMapInstances: uniqueSorted([
            ...(rpc.options.loadAttributeMaps ?? []).map((attributeMap) =>
              `${attributeMap.name}/`),
            ...(rpc.options.loadAttributeMapInstances ?? []).map((load) =>
              physicalName(load.attributeMap.name, load.instance)),
            ...invocationAttributeMapLoads,
          ]),
          loadChannelNames: (rpc.options.loadChannels ?? [])
            .map((channel) => channel.name)
            .sort(),
          loadChannelMapInstances: uniqueSorted([
            ...(rpc.options.loadChannelMaps ?? []).map((channelMap) =>
              `${channelMap.name}/`),
            ...(rpc.options.loadChannelMapInstances ?? []).map((load) =>
              physicalName(load.channelMap.name, load.instance)),
            ...invocationChannelMapLoads,
          ]),
        },
        callback,
      ),
    );
    if (rpc.options.outputCodec === undefined && response.output?.kind === undefined) {
      return undefined;
    }
    return decodeValue(
      codecOrJson(rpc.options.outputCodec),
      await this.hydrator.hydrate(flowId, response.output),
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
    await this.unary<Empty>(
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
    const response = await this.unary<ReadStreamResponse>(
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
   * Returns one newest-first page of retained Stream messages without waiting for writes.
   *
   * An empty token starts at the retained tail. Pass `nextPageToken` unchanged to read the next,
   * older page. Stream trimming may remove messages between pages.
   *
   * @typeParam T - Stream message type.
   * @param flowId - Logical Flow instance ID used as the Stream instance key.
   * @param stream - Stream registered in exactly one Flow schema.
   * @param pageSize - Positive maximum count within the server-configured limit.
   * @param beforePageToken - Opaque token from the preceding page, or empty for the first page.
   * @returns Decoded messages in newest-first order and the next-page token.
   */
  public async listStreamMessages<T>(
    flowId: string,
    stream: Stream<T>,
    pageSize: number,
    beforePageToken = "",
  ): Promise<StreamMessagesPage<T>> {
    if (!Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > 2_147_483_647) {
      throw new RangeError("Stream page size must be a positive 32-bit integer");
    }
    const flow = registeredStream(this.registry, stream as Stream<unknown>);
    const response = await this.unary<ListStreamMessagesResponse>(
      { operation: "listStreamMessages", flowId, requirement: "none" },
      (callback) => this.service.listStreamMessages({
        flowId: requireName(flowId),
        flowType: flow.name,
        streamName: stream.name,
        pageSize,
        beforePageToken,
      }, callback),
    );
    return {
      messages: response.messages.map((message) => {
        if (message.value === undefined || message.createdTime === undefined || message.resumeToken === "") {
          throw new TypeError("Dex returned an incomplete Stream message");
        }
        return {
          value: decodeValue(stream.codec, message.value),
          resumeToken: message.resumeToken,
          createdTime: message.createdTime,
          source: message.source,
        };
      }),
      nextPageToken: response.nextPageToken,
    };
  }

  /**
   * Long-polls until a Flow closes and returns every output-bearing completion.
   * Failed, canceled, and terminated Flows return a FlowResult too; inspect its status and error fields.
   * A long-poll timeout does not stop the Flow; call again to continue waiting.
   * @param flowId - Non-empty existing Flow ID.
   * @param timeoutMs - Optional server-side long-poll duration in milliseconds.
   * @returns The closed Flow's status, error fields, and immutable Step completions in server collection order.
   * @throws {@link FlowNotFoundError} when no retained target exists.
   * @throws {@link LongPollTimeoutError} when the Flow remains open at timeout.
   * @throws {@link ValueMappingError} when Dex returns malformed blob-backed output.
   * @throws {@link BlobMessageTooLargeError} when one output blob alone exceeds a gRPC message size limit.
   */
  public async waitForFlow(
    flowId: string,
    timeoutMs?: number,
  ): Promise<FlowResult> {
    const response = await this.unary<ProtoFlowResult>(
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
      flowId,
      response.results.map((result) => result.completedStepOutput),
    );
    return createFlowResultFromProto(response, values);
  }

  /**
   * Requests cancellation, termination, or failure of an active Flow.
   * The promise resolves after acceptance and does not await terminal status.
   * Omitted type requests cooperative cancellation. TERMINATE closes immediately; FAIL records failure.
   * A handler must observe cancellationSignal; stopping cannot undo an external side effect.
   * Use waitForFlow to observe the terminal result after acceptance.
   * @example
   * ```ts
   * await client.stopFlow("order-42", { type: StopType.CANCEL, reason: "Order withdrawn" });
   * const result = await client.waitForFlow("order-42");
   * ```
   * @throws {@link FlowNotActiveOrNotFoundError} when the Flow is closed or missing.
   * @throws {@link DexServiceError} for other service or transport failures.
   * @param flowId - Non-empty active Flow ID.
   * @param options - Stop mode and optional recorded reason.
   */
  public async stopFlow(flowId: string, options: StopFlowOptions = {}): Promise<void> {
    await this.unary<Empty>(
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
    const response = await this.unary<GetFlowSummaryResponse>(
      { operation: "describeFlow", flowId, requirement: "existing" },
      (callback) => this.service.getFlowSummary({ flowId: requireName(flowId), runId: "" }, callback),
    );
    if (response.flowRunId === undefined || response.startTime === undefined) {
      throw new TypeError(`Dex returned an incomplete summary for Flow ${flowId}`);
    }
    return {
      flowId: response.flowRunId.flowId,
      runId: response.flowRunId.runId,
      flowType: response.flowType,
      status: mapFlowStatus(response.flowStatus),
      startedAt: response.startTime,
    };
  }

  /**
   * Reads committed Attribute and AttributeMap instance values from the current or latest Flow run.
   *
   * Use `getAttributes` to read Flow state from outside the Flow without defining an RPC. Pass one
   * read per value: a singleton {@link Attribute}, or an AttributeMap instance selected with
   * {@link AttributeMap.instance}. The returned Promise resolves to a tuple with one element per
   * read, in read order. Each element has the read's value type, or `undefined` when that Attribute
   * or AttributeMap instance is not set. Writing `null` or `undefined` deletes an Attribute, so it
   * then reads as `undefined`. A stored value that its codec decodes to `null` stays `null`. Falsy
   * values such as `""`, `0n`, and `false` are set values.
   *
   * The Client sends all keys in one FlowService request, so every value comes from the same
   * committed state. No Worker runs. The read targets the current run, or the latest run when no
   * run is open. A closed run stays readable until Dex purges it. The call sees only committed
   * state. It does not see writes staged by an RPC or Step that is still running, unlike an
   * Attribute read inside an RPC handler.
   *
   * Hard-delete Attributes from {@link Attribute.hardDeleteOnNull} are supported; Dex returns their
   * current stored values. The Client hydrates large values through LoadBlobs and its BlobCache,
   * then decodes every value with its definition's codec. The Promise resolves only after every
   * value decodes. On any error it rejects and returns no values.
   *
   * Every definition must be registered by some Flow in this Client's Registry. Registered means
   * the exact definition object returned by that Flow's `getPersistenceSchema`. A different object
   * with the same name, such as one with another codec or one created by calling
   * `hardDeleteOnNull()` again, is rejected. The definition need not belong to the target Flow's
   * type; Dex reads the key by name. A same-named Attribute with another value type in the target
   * Flow fails to decode.
   *
   * The reads parameter requires at least one read at compile time. A plain array can be empty, so
   * spreading it alone does not compile. Pass its first read separately, as in
   * `client.getAttributes(flowId, first, ...rest)`. The result then has one element for `first`,
   * followed by one element per read in `rest`.
   *
   * @example
   * ```ts
   * type Note = { readonly text: string };
   * const orderStatus = new Attribute("order-status", stringCodec);
   * const notes = new AttributeMap("notes", jsonCodec<Note>());
   * // Register orderStatus and notes in a Flow's getPersistenceSchema.
   *
   * try {
   *   const [status, note] = await client.getAttributes(
   *     "order-42",
   *     orderStatus,
   *     notes.instance("n1"),
   *   );
   *   console.log(status ?? "new", note?.text ?? "no note yet");
   * } catch (error) {
   *   if (error instanceof FlowNotFoundError) {
   *     console.log(`order ${error.flowId} does not exist`);
   *   } else {
   *     throw error;
   *   }
   * }
   * ```
   * @typeParam Reads - Tuple of reads. TypeScript infers it from the arguments, and the result
   * tuple maps each read to its value type or `undefined`.
   * @param flowId - Non-empty Flow ID.
   * @param reads - At least one Attribute or AttributeMap instance selection. Two reads of the same
   * Attribute or AttributeMap instance are rejected.
   * @returns A tuple with one decoded value or `undefined` per read, in read order.
   * @throws {@link TypeError} before any request when `flowId` is empty, no read is passed, a read
   * is not an Attribute or AttributeMap instance selection, or two reads select the same value.
   * @throws {@link FlowDefinitionError} before any request when a definition is not registered by a
   * Flow in the Client's Registry, or differs from the registered definition object.
   * @throws {@link FlowNotFoundError} when the Flow does not exist or its latest run is being purged.
   * @throws {@link ValueMappingError} when a value cannot be hydrated or decoded with its codec.
   * @throws {@link BlobMessageTooLargeError} when one blob alone exceeds a gRPC message size limit.
   * @throws {@link DexServiceError} for other FlowService failures, including a failed LoadBlobs
   * request. The error keeps the gRPC `code` and the server `detail`, and its `operation` is
   * `loadBlobs` for a LoadBlobs failure.
   * @throws {@link Error} when Dex returns an invalid response, or the BlobCache fails while large
   * values are hydrated.
   * Every error message names the Flow ID. An error about one read or value also names its
   * Attribute name or physical key.
   * @since 1.29.0
   */
  public async getAttributes<
    Reads extends readonly [AttributeRead<unknown>, ...AttributeRead<unknown>[]],
  >(flowId: string, ...reads: Reads): Promise<AttributeReadValues<Reads>> {
    const selected = this.selectAttributeReads(flowId, reads);
    const keys = selected.map((read) => read.key);
    const values = await this.fetchAttributeValues(flowId, keys);
    const decoded = selected.map((read) => {
      const value = values.get(read.key);
      if (value === undefined) {
        return undefined;
      }
      try {
        return decodeValue(read.codec, value);
      } catch (failure) {
        throw new ValueMappingError(
          "decode",
          `getAttributes Flow ${flowId} Attribute ${read.key}: ${valueMappingDetail(failure)}`,
          { cause: failure },
        );
      }
    });
    return decoded as AttributeReadValues<Reads>;
  }

  private selectAttributeReads(
    flowId: string,
    reads: readonly AttributeRead<unknown>[],
  ): SelectedAttributeRead[] {
    if (flowId.trim().length === 0) {
      throw new TypeError(`getAttributes requires a non-empty Flow ID; received ${JSON.stringify(flowId)}`);
    }
    if (reads.length === 0) {
      throw new TypeError(`getAttributes Flow ${flowId} requires at least one Attribute read`);
    }
    const seen = new Set<string>();
    return reads.map((read, position) => {
      const selected = selectAttributeRead(flowId, read, position);
      try {
        registeredAttributeFlow(this.registry, selected.definition);
      } catch (failure) {
        if (failure instanceof FlowDefinitionError) {
          throw new FlowDefinitionError(`getAttributes Flow ${flowId}: ${failure.message}`, { cause: failure });
        }
        throw failure;
      }
      if (seen.has(selected.key)) {
        throw new TypeError(`getAttributes Flow ${flowId} has duplicate Attribute read ${selected.key}`);
      }
      seen.add(selected.key);
      return selected;
    });
  }

  private async fetchAttributeValues(
    flowId: string,
    keys: readonly string[],
  ): Promise<Map<string, ProtoValue>> {
    const joinedKeys = keys.join(", ");
    const response = await this.unary<GetAttributesResponse>(
      {
        operation: "getAttributes",
        flowId,
        requirement: "existing",
        messagePrefix: `getAttributes Flow ${flowId} Attributes ${joinedKeys}`,
      },
      (callback) => this.service.getAttributes(
        { flowId, runId: "", keys: [...keys], allKeys: false },
        callback,
      ),
    );
    const requested = new Set(keys);
    const returned = new Set<string>();
    const returnedKeys: string[] = [];
    const returnedValues: ProtoValue[] = [];
    for (const attribute of response.attributes) {
      if (!requested.has(attribute.key)) {
        throw new Error(`getAttributes Flow ${flowId} returned unexpected Attribute ${attribute.key}`);
      }
      if (returned.has(attribute.key)) {
        throw new Error(`getAttributes Flow ${flowId} returned duplicate Attribute ${attribute.key}`);
      }
      if (attribute.value?.kind === undefined) {
        throw new Error(`getAttributes Flow ${flowId} returned Attribute ${attribute.key} without a value`);
      }
      returned.add(attribute.key);
      returnedKeys.push(attribute.key);
      returnedValues.push(attribute.value);
    }
    let hydrated: ProtoValue[];
    try {
      hydrated = await this.hydrator.hydrateAll(flowId, returnedValues);
    } catch (failure) {
      if (failure instanceof BlobMessageTooLargeError) {
        throw failure;
      }
      const context = `getAttributes Flow ${flowId} Attributes ${returnedKeys.join(", ")}`;
      if (failure instanceof ValueMappingError) {
        throw new ValueMappingError("hydrate", `${context}: ${valueMappingDetail(failure)}`, { cause: failure });
      }
      if (isGrpcServiceError(failure)) {
        throw translateServiceError(failure, "loadBlobs", flowId, "none", `${context} LoadBlobs`);
      }
      const detail = failure instanceof Error ? failure.message : String(failure);
      throw new Error(`${context}: ${detail}`, { cause: failure });
    }
    return new Map(returnedKeys.map((key, index) => [key, hydrated[index] as ProtoValue]));
  }

  /**
   * Returns one page of Flow runs matching a visibility query.
   * @param query - Dex visibility query; empty uses server defaults.
   * @param pageSize - Non-negative requested maximum result count.
   * @param nextPageToken - Opaque token from the preceding page, or empty first.
   * @returns Server-ordered entries and the next-page token.
   * @throws {@link BlobMessageTooLargeError} when one Indexed Attribute blob alone exceeds a gRPC message size limit.
   */
  public async searchFlows(
    query: string,
    pageSize: number,
    nextPageToken = "",
  ): Promise<SearchFlowsPage> {
    if (pageSize < 0) {
      throw new RangeError("search page size must not be negative");
    }
    const response = await this.unary<SearchFlowsResponse>(
      { operation: "searchFlows", requirement: "none" },
      (callback) => this.service.searchFlows({
        query, pageSize, nextPageToken,
      }, callback),
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
      indexedAttributes.set(
        attribute.key,
        decodeUnknown(await this.hydrator.hydrate(entry.flowId, attribute.value)),
      );
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
   * Rewinds a Flow to a selected point in its history and re-runs it from there.
   * Time travel keeps the Flow ID and run ID and starts a new run attempt.
   * Re-run Steps reuse their Step execution IDs, so keys built only from IDs repeat.
   * @param flowId - Non-empty Flow ID whose history supplies the time travel point.
   * @param options - Time travel selector, reason, and replay controls.
   * @returns The Flow's server-assigned run ID, which time travel leaves unchanged.
   */
  public async timeTravel(flowId: string, options: TimeTravelOptions): Promise<string> {
    const response = await this.unary<ResetFlowResponse>(
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
          runAttempt: 0,
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
    await this.unary<Empty>(
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
   * Waits until one Step execution completes or its request budget expires.
   * The current Server reads state without Request ID deduplication; requestId is a compatibility field.
   * An omitted or zero request timeout waits indefinitely across transparent reattachments.
   * @param flowId - Non-empty active Flow ID.
   * @param stepExecutionId - Step type and positive execution number.
   * @param options - Optional Request ID, request timeout, and internal handler timeout.
   * @throws {@link FlowClosingError} when Flow close cleanup rejects the wait.
   * @throws {@link FlowNotActiveOrNotFoundError} when no active target exists.
   * @throws {@link RequestTimeoutError} when a positive request timeout expires first.
   */
  public async waitForStepCompletion(
    flowId: string,
    stepExecutionId: StepExecutionId,
    options: WaitForStepCompletionOptions,
  ): Promise<void> {
    const requestBudget = new ClientRequestBudget(options.requestTimeoutMs);
    while (true) {
      await this.negotiateServerProtocolWithinBudget(requestBudget, "waitForStepCompletion", flowId);
      const requestAttempt = requestBudget.nextAttempt("waitForStepCompletion", flowId);
      try {
        await unary<WaitForStepCompletionResponse>(
          { operation: "waitForStepCompletion", flowId, requirement: "active" },
          (callback) => {
            const request = {
              flowId: requireName(flowId),
              stepType: stepExecutionId.stepType,
              stepExecutionNumber: String(stepExecutionId.number ?? 1),
              requestTimeoutSeconds: requestAttempt.requestTimeoutSeconds,
              requestId: options.requestId ?? "",
            };
            if (requestAttempt.deadline === undefined) {
              return this.service.waitForStepCompletion(request, callback);
            }
            return this.service.waitForStepCompletion(
              request,
              new Metadata(),
              { deadline: requestAttempt.deadline },
              callback,
            );
          },
        );
        return;
      } catch (error) {
        if (error instanceof LongPollTimeoutError) {
          continue;
        }
        if (
          error instanceof DexServiceError
          && (error.code === status.DEADLINE_EXCEEDED || error.code === status.CANCELLED)
          && requestBudget.hasDeadline()
        ) {
          throw requestBudget.timeoutError("waitForStepCompletion", flowId);
        }
        throw error;
      }
    }
  }

  /**
   * Waits until a singleton Attribute in the current run satisfies a match.
   * Returns the current value observed by the successful wait operation.
   * The current Server reads state without Request ID deduplication; requestId is a compatibility field.
   * Request timeout covers transparent long-poll retries. The current Server ignores internal handler timeout.
   * Only inline scalar values can be compared. Blob-backed or noncomparable values fail with
   * FAILED_PRECONDITION; keep a separate small scalar Attribute for the condition.
   * An omitted or zero request timeout waits indefinitely.
   * @throws {@link DexServiceError} when the value cannot be compared or another service failure occurs.
   * @throws {@link RequestTimeoutError} when the request budget expires.
   * @throws {@link FlowClosingError} when Flow close cleanup rejects the wait.
   * @throws {@link FlowNotActiveOrNotFoundError} when no active target exists.
   * @typeParam T - Attribute value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered singleton Attribute to observe.
   * @param match - Scalar predicate whose operand has the Attribute value type.
   * @param options - Optional Request ID override and request and internal handler timeout controls.
   * @returns The matched current Attribute value.
   * @throws {@link TypeError} when `attribute` uses {@link Attribute.hardDeleteOnNull}; no request is sent.
   */
  public waitForAttributeMatch<T>(
    flowId: string,
    attribute: Attribute<T>,
    match: AttributeMatch<T>,
    options: WaitForAttributeOptions,
  ): Promise<T>;

  /**
   * Waits until one AttributeMap instance in the current run satisfies a match.
   * Primitive-value restrictions, options, and timeout errors match `waitForAttributeMatch`.
   * @typeParam T - AttributeMap value type.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered AttributeMap to observe.
   * @param instance - The map instance to observe. Slash is prohibited because it is a reserved character.
   * @param match - Scalar predicate whose operand has the AttributeMap value type.
   * @param options - Optional Request ID override and request and internal handler timeout controls.
   * @returns The matched current AttributeMap value.
   * @throws {@link TypeError} when `attribute` uses {@link AttributeMap.hardDeleteOnNull}; no request is sent.
   */
  public waitForAttributeMatch<T>(
    flowId: string,
    attribute: AttributeMap<T>,
    instance: string,
    match: AttributeMatch<T>,
    options: WaitForAttributeOptions,
  ): Promise<T>;

  /**
   * Waits until a singleton Attribute or AttributeMap instance satisfies a match.
   * @param flowId - Non-empty active Flow ID.
   * @param attribute - Registered Attribute or AttributeMap to observe.
   * @param args - Match and options, optionally preceded by a map instance.
   * @returns The matched current value.
   */
  public async waitForAttributeMatch(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    ...args: unknown[]
  ): Promise<unknown> {
    requireNotHardDeleteOnNull(attribute, "waitForAttributeMatch");
    if (attribute instanceof Attribute) {
      if (args.length !== 2 || !isWaitForAttributeOptions(args[1])) {
        throw new TypeError("waitForAttributeMatch received invalid Attribute arguments");
      }
      return this.waitForAttributeValue(
        flowId,
        attribute,
        undefined,
        args[0] as AttributeMatch<unknown>,
        args[1],
      );
    }
    if (attribute instanceof AttributeMap) {
      if (args.length !== 3 || typeof args[0] !== "string" || !isWaitForAttributeOptions(args[2])) {
        throw new TypeError("waitForAttributeMatch received invalid AttributeMap arguments");
      }
      return this.waitForAttributeValue(
        flowId,
        attribute,
        args[0],
        args[1] as AttributeMatch<unknown>,
        args[2],
      );
    }
    throw new TypeError("waitForAttributeMatch received an invalid Attribute definition");
  }

  private async waitForAttributeValue(
    flowId: string,
    attribute: Attribute<unknown> | AttributeMap<unknown>,
    instance: string | undefined,
    match: AttributeMatch<unknown>,
    options: WaitForAttributeOptions,
  ): Promise<unknown> {
    if (!(match instanceof AttributeMatch)) {
      throw new TypeError("waitForAttributeMatch requires an AttributeMatch");
    }
    const encoded = match.encode(attribute.codec);
    const requestBudget = new ClientRequestBudget(options.requestTimeoutMs);
    while (true) {
      await this.negotiateServerProtocolWithinBudget(requestBudget, "waitForAttributeMatch", flowId);
      const requestAttempt = requestBudget.nextAttempt("waitForAttributeMatch", flowId);
      try {
        const response = await unary<WaitForAttributeResponse>(
          { operation: "waitForAttributeMatch", flowId, requirement: "active" },
          (callback) => {
            const request = {
              flowId: requireName(flowId),
              match: {
                key: physicalName(attribute.name, instance),
                operator: encoded.operator,
                operand: encoded.operand,
              },
              requestTimeoutSeconds: requestAttempt.requestTimeoutSeconds,
              requestId: options.requestId ?? "",
            };
            if (requestAttempt.deadline === undefined) {
              return this.service.waitForAttribute(request, callback);
            }
            return this.service.waitForAttribute(
              request,
              new Metadata(),
              { deadline: requestAttempt.deadline },
              callback,
            );
          },
        );
        if (response.matchedValue === undefined) {
          throw new Error("waitForAttributeMatch response is incomplete");
        }
        return decodeValue(attribute.codec, response.matchedValue);
      } catch (error) {
        if (error instanceof LongPollTimeoutError) {
          continue;
        }
        if (
          error instanceof DexServiceError
          && (error.code === status.DEADLINE_EXCEEDED || error.code === status.CANCELLED)
          && requestBudget.hasDeadline()
        ) {
          throw requestBudget.timeoutError("waitForAttributeMatch", flowId);
        }
        throw error;
      }
    }
  }

  /**
   * Updates mutable configuration for an active Flow.
   * The update affects later decisions and does not recall dispatched work.
   * Updates preserve the existing Worker target, including when config is empty.
   * @param flowId - Non-empty active Flow ID.
   * @param config - New optional Flow configuration fields.
   */
  public async updateFlowConfig(flowId: string, config: FlowConfig): Promise<void> {
    await this.unary<Empty>(
      { operation: "updateFlowConfig", flowId, requirement: "active" },
      (callback) =>
      this.service.updateFlowConfig(
        {
          flowId: requireName(flowId),
          runId: "",
          flowConfig: mapFlowConfig(config),
        },
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
    : registeredStepTarget(flow, executeFailureStep);
  const executeFailureOptions =
    options?.executeFailure?.options ?? executeFailureDefinition?.step.getStepOptions?.();
  const waitForLoads = mapHandlerStateLoads(flow, {
    attributeMaps: options?.waitForLoadAttributeMaps,
    attributeMapInstances: options?.waitForLoadAttributeMapInstances,
    channels: options?.waitForLoadChannels,
    channelMaps: options?.waitForLoadChannelMaps,
    channelMapInstances: options?.waitForLoadChannelMapInstances,
  }, "WaitFor");
  const executeLoads = mapHandlerStateLoads(flow, {
    attributeMaps: options?.executeLoadAttributeMaps,
    attributeMapInstances: options?.executeLoadAttributeMapInstances,
    channels: options?.executeLoadChannels,
    channelMaps: options?.executeLoadChannelMaps,
    channelMapInstances: options?.executeLoadChannelMapInstances,
  }, "Execute");
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
    waitForLoadAttributeMapInstances: waitForLoads.attributeMaps,
    waitForLoadChannelNames: waitForLoads.channels,
    waitForLoadChannelMapInstances: waitForLoads.channelMaps,
    executeLoadAttributeMapInstances: executeLoads.attributeMaps,
    executeLoadChannelNames: executeLoads.channels,
    executeLoadChannelMapInstances: executeLoads.channelMaps,
  });
}

function mapFlowTimeoutHandlerOptions(
  flow: RegisteredFlow,
  timeoutSeconds: number,
  timeoutPolicy: ProtoFlowTimeoutPolicy,
  options: FlowTimeoutHandlerOptions | undefined,
): ProtoFlowTimeoutHandlerOptions | undefined {
  if (options === undefined) {
    return undefined;
  }
  if (
    timeoutSeconds <= 0 ||
    timeoutPolicy !== ProtoFlowTimeoutPolicy.FLOW_TIMEOUT_POLICY_HANDLER
  ) {
    throw new TypeError(
      "timeout handler options require a positive timeout with handler policy",
    );
  }
  const failureStep = options.failure?.step;
  const failureDefinition = failureStep === undefined
    ? undefined
    : registeredStepTarget(flow, failureStep);
  if (failureDefinition !== undefined && failureDefinition.step.inputCodec !== voidCodec) {
    throw new TypeError("timeout handler failure Step must use voidCodec");
  }
  const loads = mapHandlerStateLoads(flow, {
    attributeMaps: options.loadAttributeMaps,
    attributeMapInstances: options.loadAttributeMapInstances,
    channels: options.loadChannels,
    channelMaps: options.loadChannelMaps,
    channelMapInstances: options.loadChannelMapInstances,
  }, "timeout handler");
  return ProtoFlowTimeoutHandlerOptions.create({
    methodTimeoutSeconds: seconds(options.methodTimeoutMs),
    heartbeatTimeoutSeconds: heartbeatSeconds(options.heartbeatTimeoutMs),
    retryPolicy: mapRetryPolicy(options.retry),
    failurePolicy: failureDefinition === undefined
      ? ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED
      : ExecuteMethodFailurePolicy.EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
    failureProceedStepType: failureDefinition?.name ?? "",
    failureProceedStepOptions: failureDefinition === undefined
      ? undefined
      : mapStepOptions(
          options.failure?.options ?? failureDefinition.step.getStepOptions?.(),
          failureDefinition.step.waitFor === undefined,
          flow,
        ),
    durabilityOverride: mapDurability(options.durability),
    lockAttributeKeys: mapAttributeLocks(flow, options.lockAttributes ?? [], "timeout handler"),
    loadAttributeMapInstances: loads.attributeMaps,
    loadChannelNames: loads.channels,
    loadChannelMapInstances: loads.channelMaps,
  });
}

interface HandlerStateLoads {
  readonly attributeMaps: readonly AttributeMap<unknown>[] | undefined;
  readonly attributeMapInstances: readonly {
    readonly attributeMap: AttributeMap<unknown>;
    readonly instance: string;
  }[] | undefined;
  readonly channels: readonly Channel<unknown>[] | undefined;
  readonly channelMaps: readonly ChannelMap<unknown>[] | undefined;
  readonly channelMapInstances: readonly {
    readonly channelMap: ChannelMap<unknown>;
    readonly instance: string;
  }[] | undefined;
}

function mapHandlerStateLoads(
  flow: RegisteredFlow,
  loads: HandlerStateLoads,
  source: string,
): { attributeMaps: string[]; channels: string[]; channelMaps: string[] } {
  const attributeMaps = mapDefinitionLoads(
    flow,
    loads.attributeMaps ?? [],
    AttributeMap,
    source,
    (definition) => `${definition.name}/`,
  );
  attributeMaps.push(...mapDefinitionLoads(
    flow,
    loads.attributeMapInstances ?? [],
    AttributeMap,
    source,
    (load) => physicalName(load.attributeMap.name, load.instance),
    (load) => load.attributeMap,
    new Set(attributeMaps),
  ));
  const channels = mapDefinitionLoads(
    flow,
    loads.channels ?? [],
    Channel,
    source,
    (definition) => definition.name,
  );
  const channelMaps = mapDefinitionLoads(
    flow,
    loads.channelMaps ?? [],
    ChannelMap,
    source,
    (definition) => `${definition.name}/`,
  );
  channelMaps.push(...mapDefinitionLoads(
    flow,
    loads.channelMapInstances ?? [],
    ChannelMap,
    source,
    (load) => physicalName(load.channelMap.name, load.instance),
    (load) => load.channelMap,
    new Set(channelMaps),
  ));
  return {
    attributeMaps: attributeMaps.sort(),
    channels: channels.sort(),
    channelMaps: channelMaps.sort(),
  };
}

function mapDefinitionLoads<Load, Definition extends { readonly name: string }>(
  flow: RegisteredFlow,
  loads: readonly Load[],
  expected: abstract new (...arguments_: any[]) => Definition,
  source: string,
  physicalNameFor: (load: Load) => string,
  definitionFor: (load: Load) => Definition = (load) => load as unknown as Definition,
  seen = new Set<string>(),
): string[] {
  const mapped: string[] = [];
  for (const load of loads) {
    const definition = definitionFor(load);
    if (
      !(definition instanceof expected) ||
      flow.persistence.get(definition.name) !== (definition as unknown)
    ) {
      throw new TypeError(`${source} state load does not belong to Flow ${flow.name}`);
    }
    const name = physicalNameFor(load);
    if (seen.has(name)) {
      throw new TypeError(`${source} has duplicate state load ${name}`);
    }
    seen.add(name);
    mapped.push(name);
  }
  return mapped;
}

function mapAttributeLocks(
  flow: RegisteredFlow,
  locks: readonly AttributeLock[],
  source: string,
): string[] {
  return locks.map((lock) => {
    if (flow.persistence.get(lock.attribute.name) !== lock.attribute) {
      throw new TypeError(`${source} lock does not belong to Flow ${flow.name}`);
    }
    return physicalName(lock.attribute.name, lock.instance);
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
): ProtoFlowConfig {
  return {
    activeStepSearchMode: mapActiveStepSearchMode(config),
    attributeStoreNames: mapAttributeStoreNames(config),
    historyWindowEvents: config?.historyWindowEvents,
    offloadHistory: config?.offloadHistory,
    stepDurability:
      config?.stepDurability === undefined ? undefined : mapDurability(config.stepDurability),
    workerTarget: undefined,
  };
}

function mapInitialAttributeWrite(
  registered: RegisteredFlow,
  initial: InitialAttribute<unknown>,
): AttributeWrite {
  const attribute = initial.attribute;
  const registeredDefinition = registered.persistence.get(attribute.name);
  const isHardDeleteOnNull = isHardDeleteOnNullDefinition(attribute);
  if (
    (isHardDeleteOnNull
      || (registeredDefinition !== undefined && isHardDeleteOnNullDefinition(registeredDefinition)))
    && registeredDefinition !== attribute
  ) {
    throw new TypeError(
      `hard-delete Attribute ${attribute.name} must use the definition registered with Flow ${registered.name}`,
    );
  }
  const key = physicalName(attribute.name, initial.instance);
  return {
    key,
    value: requireHardDeleteOnNullValueKind(attribute, key, encodeValue(attribute.codec, initial.value)),
    indexConfig: mapIndex(attribute.index),
    syncConfig: mapAttributeStoreSync(attribute),
    isHardDeleteOnNull,
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
  return physicalMapName(name, instance);
}

interface SelectedAttributeRead {
  readonly definition: Attribute<unknown> | AttributeMap<unknown>;
  readonly codec: Codec<unknown>;
  readonly key: string;
}

function selectAttributeRead(
  flowId: string,
  read: AttributeRead<unknown>,
  position: number,
): SelectedAttributeRead {
  if (read instanceof Attribute) {
    return { definition: read, codec: read.codec, key: physicalName(read.name) };
  }
  if (read instanceof AttributeMapInstance) {
    return {
      definition: read.attributeMap,
      codec: read.attributeMap.codec,
      key: physicalName(read.attributeMap.name, read.instance),
    };
  }
  const value: unknown = read;
  if (value instanceof AttributeMap) {
    throw new TypeError(
      `getAttributes Flow ${flowId} AttributeMap ${value.name} requires an instance; ` +
        "pass attributeMap.instance(key)",
    );
  }
  throw new TypeError(
    `getAttributes Flow ${flowId} read ${position} must be an Attribute or an AttributeMap instance selection`,
  );
}

function isGrpcServiceError(failure: unknown): failure is ServiceError {
  return failure instanceof Error
    && typeof (failure as Partial<ServiceError>).code === "number"
    && (failure as Partial<ServiceError>).metadata instanceof Metadata;
}

function valueMappingDetail(failure: unknown): string {
  if (failure instanceof ValueMappingError) {
    const prefix = `Cannot ${failure.operation} Dex Value: `;
    return failure.message.startsWith(prefix) ? failure.message.slice(prefix.length) : failure.message;
  }
  return failure instanceof Error ? failure.message : String(failure);
}

function requireRPCInvokeDefinition(
  flow: RegisteredFlow,
  definition: { readonly name: string },
  kind: string,
): void {
  if (flow.persistence.get(definition.name) !== definition) {
    throw new FlowDefinitionError(
      `Flow ${flow.name} does not register ${kind} ${definition.name}`,
    );
  }
}

function uniqueSorted(values: readonly string[]): string[] {
  return [...new Set(values)].sort();
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

function isWaitForAttributeOptions(value: unknown): value is WaitForAttributeOptions {
  return typeof value === "object" && value !== null;
}

interface ClientRequestAttempt {
  readonly requestTimeoutSeconds: number;
  readonly deadline?: Date;
}

class ClientRequestBudget {
  private readonly deadlineMs: number | undefined;

  public constructor(requestTimeoutMs: number | undefined) {
    const requestTimeoutSeconds = seconds32(requestTimeoutMs);
    this.deadlineMs = requestTimeoutSeconds === 0
      ? undefined
      : performance.now() + requestTimeoutSeconds * 1_000;
  }

  public remainingMs(operation: string, flowId: string): number | undefined {
    if (this.deadlineMs === undefined) {
      return undefined;
    }
    const remainingMs = this.deadlineMs - performance.now();
    if (remainingMs <= 0) {
      throw this.timeoutError(operation, flowId);
    }
    return remainingMs;
  }

  public nextAttempt(operation: string, flowId: string): ClientRequestAttempt {
    if (this.deadlineMs === undefined) {
      return { requestTimeoutSeconds: 0 };
    }
    const remainingMs = this.deadlineMs - performance.now();
    if (remainingMs <= 0) {
      throw this.timeoutError(operation, flowId);
    }
    return {
      requestTimeoutSeconds: Math.ceil(remainingMs / 1_000),
      deadline: new Date(Date.now() + remainingMs),
    };
  }

  public hasDeadline(): boolean {
    // Trust the transport's deadline expiry; its timer can precede the monotonic budget.
    return this.deadlineMs !== undefined;
  }

  public timeoutError(operation: string, flowId: string): RequestTimeoutError {
    return new RequestTimeoutError(
      status.DEADLINE_EXCEEDED,
      ErrorSubStatus.REQUEST_TIMEOUT,
      "request timed out",
      operation,
      flowId,
    );
  }
}

function seconds32(milliseconds: number | undefined): number {
  const value = seconds(milliseconds);
  if (value > 2_147_483_647) {
    throw new RangeError("duration exceeds the int32 seconds range");
  }
  return value;
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
    readonly messagePrefix?: string;
  },
  invoke: (callback: (error: ServiceError | null, response: Response) => void) => unknown,
): Promise<Response> {
  return new Promise((resolve, reject) => {
    invoke((error, response) => {
      if (error !== null) {
        reject(translateServiceError(
          error,
          target.operation,
          target.flowId,
          target.requirement,
          target.messagePrefix,
        ));
        return;
      }
      resolve(response);
    });
  });
}
