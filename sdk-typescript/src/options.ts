// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Credential } from "./credential.js";
import type { FlowDefinitionDocument } from "./flow-definition.js";
import type { Attribute, AttributeLock, AttributeMap, AttributeMapLoad } from "./persistence.js";
import type { RetryPolicy, StepDurability, StepOptions, StepTarget } from "./step.js";
import type { Channel, ChannelMap, ChannelMapLoad } from "./wait.js";
import { requireMapInstance, requireName } from "./validation.js";

/** Configures FlowService connectivity and bearer credentials. */
export interface ClientOptions {
  /** Dex gRPC target, plaintext unless `flowServiceTls` is set; defaults to `localhost:8801`. */
  readonly serverAddress?: string;
  /** Bearer token provider for every FlowService call; omitted sends no authorization metadata. */
  readonly credential?: Credential;
  /**
   * TLS settings for the FlowService connection; omitted keeps plaintext gRPC. See {@link FlowServiceTlsOptions}.
   * @since 1.35.0
   */
  readonly flowServiceTls?: FlowServiceTlsOptions;
  /**
   * Largest gRPC message this Client exchanges with Dex, in bytes.
   *
   * Zero, the default, receives up to `DEFAULT_GRPC_MAX_MESSAGE_BYTES` (32 MiB) and leaves
   * sends unlimited, so the server's limit bounds them. A positive value limits both directions.
   * A negative or non-integer value makes the Client constructor throw `RangeError`. Raise it with
   * the server's `api.grpcMaxMessageBytes` when one message must carry more.
   *
   * @since 1.38.0
   */
  readonly grpcMaxMessageBytes?: number;
}

/**
 * Enables TLS for a Client or Worker FlowService connection.
 *
 * Set `ClientOptions.flowServiceTls` and `WorkerOptions.flowServiceTls` independently.
 * Omitting the option keeps plaintext gRPC, exactly as before.
 * Setting it, even to `{}`, uses TLS 1.2 or newer and verifies the server certificate.
 * The connection offers HTTP/2 through ALPN h2.
 * The endpoint or gateway must forward gRPC over HTTP/2.
 * A server that selects only http/1.1 through ALPN is rejected.
 * A server that negotiates no ALPN protocol is accepted, unlike the Go SDK.
 * Its certificate identity is still verified.
 *
 * Certificates are verified against the trust store that Node.js currently uses by default.
 * Without command-line flags, it is the bundled Mozilla roots plus `NODE_EXTRA_CA_CERTS`.
 * Flags such as `--use-system-ca` and `--use-openssl-ca` change those roots, and the SDK keeps them.
 * Use `additionalRootCAsPem` for a private CA.
 *
 * The `serverAddress` host is the default certificate name and SNI.
 * Without `serverName`, HTTP/2 :authority is the address host and port, as with plaintext.
 * An IPv6 address keeps its brackets in :authority.
 * With TLS, `serverAddress` accepts only `host:port`, `dns:///host:port`,
 * or `dns://authority/host:port` with an explicit DNS resolver authority.
 * The DNS resolver authority never becomes the certificate name or :authority.
 * Other gRPC resolver schemes, such as `unix:`, work only with plaintext connections.
 * An invalid address or option makes the Client or Worker constructor throw `TypeError`.
 *
 * Connections open on demand.
 * Certificate, hostname, and handshake failures therefore appear as `UNAVAILABLE`
 * on the first Client call, or as a rejection from `Worker.start`.
 * The Worker applies the settings to its FlowService calls and its pull task streams.
 * Bearer credentials such as `FileCredential` and `ApiKeyCredential`
 * are sent as call metadata on the TLS connection.
 * The `ApiKeyCredential` token endpoint is not affected; configure its `tokenUrl` and `fetch` separately.
 * An IP address target or IP `serverName` needs a matching IP SAN in the certificate.
 * Node.js 22 and 24 send that IP address as SNI and emit a DEP0123 warning.
 * Node.js 26 rejects IP SNI, so prefer a DNS name.
 *
 * @example
 * ```ts
 * import { readFileSync } from "node:fs";
 * import { Client, FileCredential, Worker, type FlowServiceTlsOptions } from "@superdurable/dex";
 *
 * const flowServiceTls: FlowServiceTlsOptions = {
 *   additionalRootCAsPem: readFileSync("/etc/dex/root-ca.pem"),
 * };
 * const credential = new FileCredential("/var/run/secrets/dex/token");
 * const serverAddress = "dex.example.com:443";
 * const worker = new Worker(registry, cache, { serverAddress, flowServiceTls, credential, pull: {} });
 * const client = new Client(registry, cache, { serverAddress, flowServiceTls, credential });
 * try {
 *   await worker.start();
 *   await worker.waitPullRegistered({ timeoutMs: 10_000 });
 *   await client.startFlow(orders, "order-42", input);
 * } finally {
 *   await client.close();
 *   await worker.close();
 * }
 * ```
 * @since 1.35.0
 */
export interface FlowServiceTlsOptions {
  /**
   * Adds PEM-encoded root CAs to the active Node.js default trust store.
   * Every root that the default trust store already trusts stays trusted.
   * On Node.js 22.0 to 22.14, the SDK reads `NODE_EXTRA_CA_CERTS` when its module loads, so changing that variable, the working directory, or the file before importing the SDK is unsupported.
   * Omitted or empty uses the default trust store alone.
   * The value is read when the Client or Worker is constructed; later file changes are not watched.
   * At least one certificate must parse from a nonempty bundle, or the constructor throws `TypeError`.
   * Non-certificate and unparsable PEM blocks are skipped when another certificate parses.
   * Without a default root or an added CA that trusts the server, verification fails on connection.
   */
  readonly additionalRootCAsPem?: string | Uint8Array;
  /**
   * Overrides the `serverAddress` host for certificate verification, SNI, and HTTP/2 :authority.
   * Omitted or empty uses the `serverAddress` host for verification and SNI, and its host and port for :authority.
   * Set it only when the certificate name differs from the address host.
   * A nonempty value is also sent as :authority, without a port.
   * A gateway that routes by :authority must accept that name.
   * Use a DNS name or IP address without a port.
   * An IP address requires a matching IP SAN and is sent as SNI; see the IP note above.
   * An IPv6 value is sent as an unbracketed :authority, which strict HTTP/2 servers reject.
   * For an IPv6 endpoint, omit it or use a DNS name that the certificate covers.
   * An invalid value makes the Client or Worker constructor throw `TypeError`.
   */
  readonly serverName?: string;
}

/**
 * Configures one durable Step completion wait.
 * The Server does not read `requestId` for this wait.
 * `requestTimeoutMs` bounds the entire SDK call across transparent transport reattachments.
 * The SDK transparently retries server long-poll timeouts within that budget.
 * These read waits do not append Flow history events.
 */
export interface WaitForStepCompletionOptions {
  /** Optional request ID sent with the wait; the Server does not read it. */
  readonly requestId?: string;
  /** Total SDK call budget. Omit or use zero to wait indefinitely. */
  readonly requestTimeoutMs?: number;
}

/**
 * Configures one durable Attribute match wait.
 * The Server does not read `requestId` for this wait.
 * `requestTimeoutMs` bounds the entire SDK call across transparent transport reattachments.
 * The SDK transparently retries server long-poll timeouts within that budget.
 * These read waits do not append Flow history events.
 */
export interface WaitForAttributeOptions {
  /** Optional request ID sent with the wait; the Server does not read it. */
  readonly requestId?: string;
  /** Total SDK call budget. Omit or use zero to wait indefinitely. */
  readonly requestTimeoutMs?: number;
}

/** Controls which active Steps are included in Flow search indexing. */
export const ActiveStepSearchMode = Object.freeze({
  /** Uses the Dex server's current default policy. */
  DEFAULT: "default",
  /** Indexes every active Step. */
  ALL: "all",
  /**
   * Indexes only active Steps that define `waitFor`.
   *
   * @since 1.39.0
   */
  WITH_WAIT_FOR: "with_wait_for",
  /**
   * Does not index active Steps.
   *
   * @since 1.39.0
   */
  DISABLED: "disabled",
} as const);

/** Represents a value from {@link ActiveStepSearchMode}. */
export type ActiveStepSearchMode =
  (typeof ActiveStepSearchMode)[keyof typeof ActiveStepSearchMode];

/** Controls whether `startFlow` may reuse a previously used Flow ID. */
export const IdReusePolicy = Object.freeze({
  /**
   * Rejects the start whenever the Flow ID has a run, the same as `DISALLOW`.
   * The server does not make this default configurable.
   */
  DEFAULT: "default",
  /**
   * Starts a new run only when the latest run closed abnormally: failed, canceled, or terminated.
   * A Flow timeout closes a run as failed or canceled under the fail or cancel timeout policy.
   * Under the handler policy, the timeout handler's outcome decides. A running or completed latest
   * run rejects the start.
   */
  ALLOW_IF_PREVIOUS_FAILED: "allowIfPreviousFailed",
  /** Reuses an ID after any closed run. */
  ALLOW_IF_NOT_RUNNING: "allowIfNotRunning",
  /** Terminates an active run before starting the replacement. */
  ALLOW_TERMINATE_IF_RUNNING: "allowTerminateIfRunning",
  /** Rejects every previously used Flow ID. */
  DISALLOW: "disallow",
} as const);

/** Represents a value from {@link IdReusePolicy}. */
export type IdReusePolicy = (typeof IdReusePolicy)[keyof typeof IdReusePolicy];

/** Controls how Dex responds when a positive soft Flow timeout expires. */
export const FlowTimeoutPolicy = Object.freeze({
  /** Invokes `Flow.handleTimeout` when present, and fails otherwise. */
  DEFAULT: "default",
  /** Fails with `FlowErrorType.FLOW_TIMEOUT`; the current Server rejects Flow retry policies, so no Flow retry follows. */
  FAIL: "fail",
  /** Cancels without retrying the Flow. */
  CANCEL: "cancel",
  /** Starts one logical `Flow.handleTimeout` execution after the durable timer fires or is skipped. */
  HANDLER: "handler",
} as const);

/** Represents a value from {@link FlowTimeoutPolicy}. */
export type FlowTimeoutPolicy =
  (typeof FlowTimeoutPolicy)[keyof typeof FlowTimeoutPolicy];

/** Routes exhausted Flow timeout-handler retries to an input-free Step. */
export interface FlowTimeoutHandlerFailure {
  /**
   * Fallback Step class, registered instance, or StepRef whose input codec is {@link voidCodec}.
   * Code that reads this field must narrow it before using class members.
   */
  readonly step: StepTarget<void>;
  /** Options applied when the fallback Step is scheduled. */
  readonly options?: StepOptions;
}

/** Creates Flow timeout-handler failure routing settings. */
export const FlowTimeoutHandlerFailure = Object.freeze({
  /**
   * Routes exhausted timeout-handler retries to an input-free Step.
   * Changed in SDK v1.28.0: accepts registered instances and StepRef as well as Step classes.
   *
   * The Step receives `undefined` and reads the terminal handler failure from `Context.recoveryError`.
   * Select the Step by class, registered instance, or StepRef. Use an instance or StepRef when the
   * Flow registers more than one instance of the fallback Step class. The target is validated when
   * Client or SubFlow start options are mapped, not when this function is called. An unknown,
   * ambiguous, or unregistered target fails that start with FlowDefinitionError.
   * @example
   * ```ts
   * const timeoutHandlerOptions: FlowTimeoutHandlerOptions = {
   *   retry: { maximumAttempts: 1 },
   *   failure: FlowTimeoutHandlerFailure.proceedTo(new StepRef("TimeoutRecovery")),
   * };
   * ```
   * @param step - Fallback class, registered instance, or StepRef with {@link voidCodec} as its input codec.
   * @param options - Optional options for the fallback Step movement.
   * @returns The timeout-handler failure-routing settings.
   */
  proceedTo(step: StepTarget<void>, options?: StepOptions): FlowTimeoutHandlerFailure {
    return { step, ...(options === undefined ? {} : { options }) };
  },
});

/** Configures execution and selective state loading for `Flow.handleTimeout`. */
export interface FlowTimeoutHandlerOptions {
  /** Per-attempt handler limit in milliseconds; uses the server default when omitted. */
  readonly methodTimeoutMs?: number;
  /** Heartbeat timeout in milliseconds; requires whole seconds and uses the server default when omitted. */
  readonly heartbeatTimeoutMs?: number;
  /** Retry policy for the timeout handler's logical execution. */
  readonly retry?: RetryPolicy;
  /** Routing after all timeout-handler attempts fail. */
  readonly failure?: FlowTimeoutHandlerFailure;
  /** Durability override for the handler response. */
  readonly durability?: StepDurability;
  /** Attribute locks held during each timeout-handler attempt. */
  readonly lockAttributes?: readonly AttributeLock[];
  /** AttributeMap definitions whose complete contents are loaded for the handler. */
  readonly loadAttributeMaps?: readonly AttributeMap<unknown>[];
  /** Exact AttributeMap instances loaded for the handler. */
  readonly loadAttributeMapInstances?: readonly AttributeMapLoad[];
  /** Channels whose pending messages are loaded for the handler. */
  readonly loadChannels?: readonly Channel<unknown>[];
  /** ChannelMap definitions whose complete pending contents are loaded for the handler. */
  readonly loadChannelMaps?: readonly ChannelMap<unknown>[];
  /** Exact ChannelMap instances whose pending messages are loaded for the handler. */
  readonly loadChannelMapInstances?: readonly ChannelMapLoad[];
}

/** Overrides mutable server behavior for one Flow execution. */
export interface FlowConfig {
  /** Optional active-Step visibility indexing policy. */
  readonly activeStepSearchMode?: ActiveStepSearchMode;
  /**
   * Selects server-configured Attribute Stores receiving opted-in Attribute writes.
   *
   * Omit this property to preserve the existing Flow setting. An explicit empty array is sent with
   * presence and disables later synchronization. Each enabled Attribute write is projected to every
   * selected store; projection failures never roll back Flow Attribute writes.
   */
  readonly attributeStoreNames?: readonly string[];
  /** Default durability for later Step handler results. */
  readonly stepDurability?: StepDurability;
  /**
   * Sets how many of the newest history events the run keeps, approximately.
   *
   * The run uses the kept events for history reads and Time Travel.
   * Omit it to use the server's `history.windowEvents` setting, which defaults to 10000. Raise it
   * for a run that you inspect or travel back in often. Lower it for a run that writes many events
   * and needs less disk.
   *
   * Dex prunes the oldest events in chunks, and only between commands, so a run keeps slightly
   * more or fewer events than the value. A command that changes the Flow but records no event
   * counts as one event. The first event of the run is always kept, so a reset to the beginning of
   * the run always works. History reads no longer return pruned events, and Time Travel targets
   * only commands inside the window.
   *
   * Set it at start through {@link StartFlowOptions.configOverride}, or change it on an active
   * Flow with `Client.updateFlowConfig`. A smaller value takes effect at the run's next pruning. A
   * larger value keeps more of the events recorded afterwards; pruned events never come back. Once
   * set, the value can be changed but not cleared back to the server default: an update that omits
   * it keeps the current value. A SubFlow inherits the value from its parent unless its own
   * `configOverride` sets it.
   *
   * The value must be a positive integer and at most the server's `history.maxWindowEvents`
   * setting, which defaults to 100000. The field is a signed 32-bit integer, so only 1 to
   * 2147483647 can be sent. A non-integer or a value outside that range fails while the SDK
   * serializes the request, before it reaches Dex, and the call rejects with an `INTERNAL`
   * error. `startFlow` and `updateFlowConfig` reject zero, a negative integer, or an integer above
   * the server limit with a `DexServiceError` whose `code` is `INVALID_ARGUMENT`. A Step that
   * starts a SubFlow with such an override is an invalid Worker response: the Step attempt fails
   * and Dex retries it under the Step's retry policy. Servers older than server-zd 0.1.21 ignore
   * the value.
   *
   * @example
   * ```ts
   * await client.updateFlowConfig("order-42", { historyWindowEvents: 50_000 });
   * ```
   *
   * @since 1.38.0
   */
  readonly historyWindowEvents?: number;

  /**
   * Has no effect. The SDK does not send it to the server.
   *
   * server-zd 0.1.23 and later ignore the field for runs that start with them. Such a run, when
   * it offloads its history, always deletes the objects of expired history segments about one
   * hour after the history window passes them.
   *
   * A run that started under server-zd 0.1.22 still follows the value it recorded then. When that
   * value is false, the run's expired history objects stay in object storage until its retention
   * ends, and a run that never closes keeps them. To delete them sooner, set the field to true for
   * that run with UpdateFlowConfig through an SDK older than 1.40.0 or a direct gRPC request. See
   * https://docs.superdurable.io/reference/server/configuration#history-offload.
   *
   * The field remains so existing code still compiles.
   *
   * @since 1.39.0
   * @deprecated Deprecated in 1.40.0. Has no effect, because the SDK no longer sends it and runs
   * that start with server-zd 0.1.23 or later ignore it. Remove it from your code.
   */
  readonly deleteExpiredHistoryObjects?: boolean;

  /**
   * Sets whether Dex offloads the run's history to object storage.
   *
   * Omit it, which means `true`, for most runs.
   * An offloaded run uploads its history to object storage in segment objects. Once a segment is
   * uploaded and a grace period has passed, Dex deletes its history records and superseded values
   * from the local disk. History reads then fetch them from object storage. When the history
   * window passes a whole segment, Dex deletes the segment's object about one hour later.
   *
   * A run that does not offload keeps its history on the local disk. It never writes a history
   * object. The history window still applies: Dex prunes the oldest events and deletes the
   * superseded values that the window passed.
   *
   * A long-running run that offloads keeps only its newest history and current state on the local
   * disk. Set it to `false` for many short runs that close soon after they start. Each offloaded
   * run writes at least one history object when it closes. It deletes that object when its
   * retention ends. Short runs at a high rate therefore add object storage requests, while their
   * small history costs little local disk.
   * A run that does not offload keeps every superseded value its history window
   * covers on the local disk, with no byte limit; for a run that rewrites large
   * values, keep offload or set a smaller history window.
   *
   * Set it only at start, through {@link StartFlowOptions.configOverride}, or for a SubFlow through
   * its own `configOverride`. A SubFlow inherits the value from its parent unless its own override
   * sets it. The run keeps the value for its whole life. `Client.updateFlowConfig` with this field
   * set rejects with a `DexServiceError` whose `code` is `INVALID_ARGUMENT`. Runs that started
   * before server-zd 0.1.23 keep the history offload behavior that their server gave them. Servers
   * older than server-zd 0.1.23 ignore the value.
   *
   * @example
   * ```ts
   * await client.startFlow(notificationFlow, "notification-42", input, {
   *   configOverride: { offloadHistory: false },
   * });
   * ```
   *
   * @since 1.40.0
   */
  readonly offloadHistory?: boolean;
}

/**
 * Describes one Attribute value written atomically when a Flow starts.
 * @typeParam T - Attribute value type.
 */
export interface InitialAttribute<T> {
  /** Registered singleton Attribute or AttributeMap definition. */
  readonly attribute: Attribute<T> | AttributeMap<T>;
  /** The map instance. Omit it for a singleton Attribute. Slash is prohibited because it is a reserved character. */
  readonly instance?: string;
  /** Typed initial value. */
  readonly value: T;
}

/** Creates type-safe initial Attribute writes for {@link StartFlowOptions}. */
export const InitialAttribute = Object.freeze({
  /**
   * Creates a singleton Attribute initialization.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered singleton Attribute.
   * @param value - Typed initial value.
   * @returns The Attribute initialization value.
   */
  of<T>(attribute: Attribute<T>, value: T): InitialAttribute<T> {
    return { attribute, value };
  },
  /**
   * Creates an AttributeMap instance initialization.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered AttributeMap.
   * @param instance - The map instance. Slash is prohibited because it is a reserved character.
   * @param value - Typed initial value.
   * @returns The AttributeMap initialization value.
   */
  mapValue<T>(
    attribute: AttributeMap<T>,
    instance: string,
    value: T,
  ): InitialAttribute<T> {
    requireMapInstance(instance);
    return { attribute, instance, value };
  },
});

/**
 * Configures creation of a new Flow execution. All durations use milliseconds.
 *
 * To retry one logical start safely, for example after a lost response, set a stable
 * `requestId` and `ignoreAlreadyStarted: true` together. Reuse the same values on every retry.
 *
 * @example
 * ```ts
 * const status = new Attribute("status", stringCodec);
 * const options: StartFlowOptions = {
 *   timeoutMs: 30 * 60_000,
 *   idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING,
 *   attributes: [InitialAttribute.of(status, "queued")],
 *   configOverride: { activeStepSearchMode: ActiveStepSearchMode.ALL },
 * };
 * ```
 *
 * @example
 * ```ts
 * const runId = await client.startFlow(orderFlow, `order-${orderId}`, input, {
 *   ignoreAlreadyStarted: true,
 *   requestId: `start-order-${orderId}`,
 * });
 * ```
 */
export interface StartFlowOptions {
  /** Dex durable soft timeout in milliseconds; omitted or zero disables it. */
  readonly timeoutMs?: number;
  /** Action taken when a positive timeout expires; defaults from the Flow's hook. */
  readonly timeoutPolicy?: FlowTimeoutPolicy;
  /** Execution settings for `Flow.handleTimeout`; valid only with a positive handler-policy timeout. */
  readonly timeoutHandlerOptions?: FlowTimeoutHandlerOptions;
  /**
   * Delay before the starting Step becomes eligible, in milliseconds.
   * The current Server rejects positive values. Omit it or use zero; wait on a Timer in the starting Step instead.
   */
  readonly startDelayMs?: number;
  /** Flow ID reuse policy; uses `DEFAULT` when omitted. */
  readonly idReusePolicy?: IdReusePolicy;
  /**
   * Optional Flow-level retry policy.
   * The current Server rejects any supplied policy. Omit it and configure waitForRetry or executeRetry on Steps.
   */
  readonly retryPolicy?: RetryPolicy;
  /** Initial Attribute writes applied atomically with Flow creation. */
  readonly attributes?: readonly InitialAttribute<any>[];
  /** Flow configuration applied over registered defaults. */
  readonly configOverride?: FlowConfig;
  /**
   * Named task queue whose Workers execute this Flow; omitted or the empty string uses the default task queue.
   *
   * Only Workers whose `PullWorkerOptions.taskQueue` equals this name run the Flow's Steps and RPCs.
   * The Flow keeps its task queue for its whole run; `Client.updateFlowConfig` cannot change it.
   * Its SubFlows inherit it unless {@link SubFlowOptions.taskQueue} names another one. When no
   * Worker pulls the task queue, Steps wait durably for one, and RPCs fail after the Server's
   * Worker dispatch wait.
   *
   * A name has 1 to 63 ASCII letters, digits, dots, underscores, or hyphens. Names starting with
   * `dex.` are reserved; `dex.default` explicitly selects the default task queue. `startFlow` throws
   * `TypeError` for an invalid name. A non-empty name requires Server protocol 5, and `startFlow`
   * throws `Error` before sending the request to an older Server.
   *
   * @example
   * ```ts
   * await client.startFlow(orderFlow, "order-42", input, { taskQueue: "blue" });
   * ```
   * @since 1.45.2
   */
  readonly taskQueue?: string;
  /**
   * Returns the existing run instead of raising an already-started error.
   *
   * It applies only when `requestId` matches the request ID that started the existing run.
   * A different or generated request ID still throws {@link FlowAlreadyStartedError}.
   */
  readonly ignoreAlreadyStarted?: boolean;
  /**
   * Non-empty idempotency key; generates a UUID when omitted.
   *
   * A retried start returns the existing run only when `ignoreAlreadyStarted` is `true` and this
   * request ID matches the request that started that run. Without that option, the retry throws
   * {@link FlowAlreadyStartedError} even when the first attempt created the Flow. The match
   * uses the latest run of the Flow ID and takes precedence over the ID reuse policy. A retry
   * never starts a second run or terminates the run it started. Dex compares only request IDs,
   * not input or other options, so never reuse a request ID for a different logical start. See
   * {@link StartFlowOptions} for an example that sets both options.
   */
  readonly requestId?: string;
}

/**
 * Describes the lifecycle state of one Flow run.
 * `serverSideTimeoutInternalOnly` is reserved for backend hard-timeout reporting.
 */
export type FlowStatus =
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "terminated"
  | "serverSideTimeoutInternalOnly";

/** Summarizes the current or latest run for one Flow ID. */
export interface FlowInfo {
  /** Stable application-assigned Flow ID. */
  readonly flowId: string;
  /** Server-assigned run ID. */
  readonly runId: string;
  /** Registered Flow type. */
  readonly flowType: string;
  /** Current lifecycle state. */
  readonly status: FlowStatus;
  /** UTC run start timestamp. */
  readonly startedAt: Date;
}

/**
 * Describes the live Step state of the current or latest run of one Flow.
 *
 * `Client.getFlowState` returns it. Read it to see which Step executions are open and whether one of them is retrying after a
 * failed attempt. See `Client.getFlowState` for an example.
 * @since 1.45.1
 */
export interface FlowState {
  /**
   * Open Step executions of the run, in server order.
   * The array is empty when the run is closed or has no open Step execution.
   */
  readonly activeStepExecutions: readonly ActiveStepExecution[];
  /**
   * The task queue whose Workers run this Flow's Steps and RPCs.
   * The empty string means the default task queue.
   * Dex fixes the task queue when the Flow starts, so it does not change during a run.
   * Read it from a Client without a Worker to see which Workers must serve the Flow.
   * See {@link StartFlowOptions.taskQueue}.
   * @since 1.45.3
   */
  readonly taskQueue: string;
}

/**
 * Tells where an open Step execution stands.
 *
 * - `active`: the WaitFor or Execute method is pending, running, or waiting to retry. Read
 *   {@link ActiveStepExecution.invocation} for details.
 * - `waiting`: WaitFor completed and the Step execution waits for its conditions. No Step method
 *   call is open.
 * @since 1.45.1
 */
export type ActiveStepExecutionPhase = "active" | "waiting";

/**
 * Describes one open Step execution in a {@link FlowState}.
 * @since 1.45.1
 */
export interface ActiveStepExecution {
  /**
   * The Step execution ID, such as `ChargeStep-1`.
   * It equals `Context.stepExecutionId` inside the Step's handlers.
   */
  readonly stepExecutionId: string;
  /** Registered Step type. */
  readonly stepType: string;
  /**
   * Positive execution number of the Step type in this run.
   * Pass `StepExecutionId.of(stepType, executionNumber)` to `Client.waitForStepCompletion` or
   * `Client.skipTimer` to address this Step execution.
   */
  readonly executionNumber: number;
  /** Whether a Step method call is open or the Step waits for its conditions. */
  readonly phase: ActiveStepExecutionPhase;
  /**
   * The open WaitFor or Execute method call, or `undefined` when no call is open.
   * No call is open in the `waiting` phase.
   */
  readonly invocation: StepMethodInvocation | undefined;
  /**
   * The latest failed attempt of the open Step method call, or `undefined` when none failed.
   * A value here while {@link StepMethodInvocation.phase} is `backoff` or a later
   * attempt is pending or running means that the Step is retrying after a failure.
   */
  readonly lastFailure: StepMethodFailureInfo | undefined;
}

/**
 * Names the Step method that an open Step method call runs.
 * @since 1.45.1
 */
export type StepMethodName = "waitFor" | "execute";

/**
 * Tells where the current attempt of an open Step method call stands.
 *
 * - `pending`: the attempt waits for its locks or for a Worker. Read
 *   {@link StepMethodInvocation.pendingReason}.
 * - `running`: a Worker received the attempt.
 * - `backoff`: the last attempt failed, and the next attempt waits for its retry backoff.
 * - `unspecified`: the server reported no phase.
 * @since 1.45.1
 */
export type StepMethodInvocationPhase = "pending" | "running" | "backoff" | "unspecified";

/**
 * Tells why a pending Step method attempt has not reached a Worker yet.
 *
 * - `waitingForWorker`: the attempt waits for a Worker of its Flow type.
 * - `waitingForSupportingWorker`: Workers of the Flow type are connected, but each one declined
 *   the Step type.
 * - `waitingForLocks`: another Step or a transactional RPC holds one of the attempt's locks, or an
 *   earlier Step waits for one of them.
 * @since 1.45.1
 */
export type StepMethodPendingReason =
  | "waitingForWorker"
  | "waitingForSupportingWorker"
  | "waitingForLocks";

/**
 * Describes the open WaitFor or Execute method call of an {@link ActiveStepExecution}.
 * @since 1.45.1
 */
export interface StepMethodInvocation {
  /** The Step method that this call runs. */
  readonly method: StepMethodName;
  /** Where the current attempt stands. */
  readonly phase: StepMethodInvocationPhase;
  /**
   * The 1-based pending or running attempt.
   * In the `backoff` phase it is the next attempt, so it is one more than
   * {@link StepMethodFailureInfo.attempt}.
   */
  readonly attempt: number;
  /** Why a `pending` attempt waits, or `undefined` in other phases. */
  readonly pendingReason: StepMethodPendingReason | undefined;
  /** When the current attempt became pending, or `undefined` in the `backoff` phase. */
  readonly pendingSince: Date | undefined;
  /** When a Worker received the `running` attempt, or `undefined` in other phases. */
  readonly attemptStartedAt: Date | undefined;
  /**
   * When the `backoff` phase ends and the next attempt becomes pending.
   * It is `undefined` in other phases.
   */
  readonly backoffFiresAt: Date | undefined;
}

/**
 * Describes the latest failed attempt of an open Step method call.
 *
 * Dex keeps only the latest failure. It does not report when that attempt failed; read
 * {@link StepMethodInvocation.backoffFiresAt} for when the next attempt starts.
 * @since 1.45.1
 */
export interface StepMethodFailureInfo {
  /** The 1-based attempt that failed. */
  readonly attempt: number;
  /**
   * The error type the Worker reported, such as the thrown error's class name.
   * It is empty when Dex failed the attempt itself, for example when no Worker was available.
   */
  readonly errorType: string;
  /**
   * The failure message.
   * It is the Worker's error message, or the server's description when the Worker reported none.
   */
  readonly detail: string;
  /** The Worker-language stack trace, or an empty string when the Worker sent none. */
  readonly stackTrace: string;
  /**
   * The Dex failure category, such as `FLOW_ERROR_TYPE_WORKER_API_FAIL`.
   * Use it only for logs; its spellings can differ between server versions.
   */
  readonly backendError: string;
}

/** Represents one indexed Flow run returned by `searchFlows`. */
export interface SearchFlowEntry {
  /** Stable application-assigned Flow ID. */
  readonly flowId: string;
  /** Server-assigned run ID for this entry. */
  readonly runId: string;
  /** Registered Flow type. */
  readonly flowType: string;
  /** Indexed lifecycle state. */
  readonly status: FlowStatus;
  /** UTC start timestamp. */
  readonly startedAt: Date;
  /** UTC close timestamp, or `undefined` for open or unknown runs. */
  readonly closedAt: Date | undefined;
  /** Hydrated values keyed by physical search-index name. */
  readonly indexedAttributes: ReadonlyMap<string, unknown>;
}

/** Contains one server-ordered page of Flow search results. */
export interface SearchFlowsPage {
  /** Result entries in server-defined query order. */
  readonly flows: readonly SearchFlowEntry[];
  /** Opaque next-page token, or an empty string on the final page. */
  readonly nextPageToken: string;
}

/** Identifies one numbered execution of a registered Step type. */
export interface StepExecutionId {
  /** Registered Step type. */
  readonly stepType: string;
  /** Positive execution number; defaults to the first execution. */
  readonly number?: number;
}

/** Creates normalized {@link StepExecutionId} values. */
export const StepExecutionId = Object.freeze({
  /**
   * Creates a Step execution identifier.
   * @param stepType - Non-empty registered Step type.
   * @param number - Positive execution number; defaults to one.
   * @returns The normalized identifier.
   */
  of(stepType: string, number?: number): StepExecutionId {
    return { stepType, number: number ?? 1 };
  },
});

/** Selects one Timer condition within a Step execution. */
export interface TimerId {
  /** Stable condition ID, mutually exclusive with `conditionIndex`. */
  readonly conditionId?: string;
  /** Zero-based condition index, mutually exclusive with `conditionId`. */
  readonly conditionIndex?: number;
}

/** Creates Timer selectors by stable ID or Wait-tree position. */
export const TimerId = Object.freeze({
  /**
   * Selects a Timer by stable condition ID.
   * @param conditionId - Non-empty ID assigned by `Timer.byDuration`.
   * @returns An ID-based Timer selector.
   */
  byConditionId(conditionId: string): TimerId {
    requireName(conditionId);
    return { conditionId };
  },
  /**
   * Selects a Timer by zero-based Wait-tree position.
   * @param conditionIndex - Non-negative flattened Timer index.
   * @returns An index-based Timer selector.
   */
  byConditionIndex(conditionIndex: number): TimerId {
    return { conditionIndex };
  },
});

/** Selects the historical point from which time travel re-runs the Flow in a new run attempt. */
export const TimeTravelType = Object.freeze({
  /** Restarts from the beginning of the Flow. */
  BEGINNING: "beginning",
  /** Resumes at the first event at or after a timestamp. */
  HISTORY_EVENT_TIME: "historyEventTime",
  /** Resumes at the latest execution of a Step type. */
  STEP_TYPE: "stepType",
  /** Resumes at one exact Step execution ID. */
  STEP_EXECUTION_ID: "stepExecutionId",
  /**
   * Resumes right after the change that recorded one history event, named by `eventId`.
   *
   * Events recorded together, such as a Step completion and the Steps it starts, share one point,
   * so the restored state includes them all. An event inside a Step attempt resolves to the next
   * point where the Flow can stop. The event must still be in the run's history window.
   * @since 1.45.5
   */
  EVENT_ID: "eventId",
  /**
   * Selects the latest committed state of the source run's current run attempt.
   *
   * Only `Client.cloneForkFlow` accepts it, and it is that call's default.
   * `Client.timeTravel` throws `TypeError` for it, because a Flow cannot return to the state it
   * is in.
   * @since 1.45.5
   */
  CURRENT: "current",
} as const);

/** Represents a value from {@link TimeTravelType}. */
export type TimeTravelType = (typeof TimeTravelType)[keyof typeof TimeTravelType];

/** Selects the Step method used as a Step execution time travel boundary. */
export const TimeTravelStepMethod = Object.freeze({
  /** Reruns WaitFor and everything after it. */
  WAIT_FOR: "waitFor",
  /** Keeps the WaitFor result and reruns Execute and everything after it. */
  EXECUTE: "execute",
} as const);

/** Represents a value from {@link TimeTravelStepMethod}. */
export type TimeTravelStepMethod = (typeof TimeTravelStepMethod)[keyof typeof TimeTravelStepMethod];

/** Configures a time travel that re-runs a Flow from its existing history under the same run ID. */
export interface TimeTravelOptions {
  /** Time travel point selector kind. */
  readonly type: TimeTravelType;
  /** Timestamp used with `HISTORY_EVENT_TIME`. */
  readonly historyEventTime?: Date;
  /** Registered Step type used with `STEP_TYPE`. */
  readonly stepType?: string;
  /** Exact execution ID used with `STEP_EXECUTION_ID`. */
  readonly stepExecutionId?: string;
  /** WaitFor or Execute boundary required with `STEP_EXECUTION_ID`. */
  readonly stepMethod?: TimeTravelStepMethod;
  /** Optional operator-readable time travel reason. */
  readonly reason?: string;
  /** Prevents reapplication of RPCs, Channel publications, and Attribute writes after the selected point. */
  readonly skipWritesReapply?: boolean;
  /**
   * History event ID used with `EVENT_ID`; a positive safe integer. Omit it for the other types.
   * @since 1.45.5
   */
  readonly eventId?: number;
  /**
   * Decides what happens to the SubFlow conditions that restored Steps wait on.
   * Omitted or `DEFAULT` uses `ATTACH_ELSE_RESTART`.
   * @since 1.45.5
   */
  readonly subFlowResetPolicy?: SubFlowResetPolicy;
}

/**
 * Decides what happens to a SubFlow condition that a restored Step still waits on.
 *
 * `Client.timeTravel` and `Client.cloneForkFlow` restore Steps from an earlier state. A restored
 * Step whose WaitFor waits on SubFlow conditions names, in each condition, the child Flow that the
 * Step started. The policy decides, per condition, whether the restored wait attaches to that
 * child, restarts the child, fails the condition, or rejects the whole call.
 *
 * Attaching keeps the condition on its original child. When the child run has ended, its result
 * completes the condition. When the child still runs, the condition keeps waiting for it. A child
 * whose run no longer exists cannot be attached; the `ATTACH_ELSE_*` policies then take their
 * second action. Under `cloneForkFlow`, the source run's children report only to the source run,
 * so a child that still runs cannot be attached either.
 *
 * Restarting starts the child again with the start it originally received. Under `timeTravel`, the
 * child keeps its Flow ID, and a still-running run of it is terminated first. Under
 * `cloneForkFlow`, the restart starts a new child Flow whose Flow ID derives from the new Flow ID,
 * and the source run's child never changes. A child whose run no longer exists cannot be
 * restarted, and the call fails.
 *
 * Failing completes the condition with a failed child result whose error type is
 * `SUB_FLOW_ABANDONED`; the Step's Execute receives it like any failed child. Under `timeTravel`, a
 * child that still runs is terminated.
 *
 * Rejecting fails the call with a `DexServiceError` whose code is FAILED_PRECONDITION, and nothing
 * changes. Its detail names each Step execution and child Flow ID that caused it.
 *
 * @example
 * ```ts
 * await client.timeTravel("order-42", {
 *   type: TimeTravelType.STEP_TYPE,
 *   stepType: "ShipOrder",
 *   subFlowResetPolicy: SubFlowResetPolicy.ATTACH_ELSE_FAIL,
 * });
 * ```
 * @since 1.45.5
 */
export const SubFlowResetPolicy = Object.freeze({
  /** Leaves the choice to the Server, which uses `ATTACH_ELSE_RESTART`. */
  DEFAULT: "default",
  /** Restarts every child, whether it ended or still runs. */
  ALWAYS_RESTART: "alwaysRestart",
  /** Fails every condition, whether its child ended or still runs. */
  ALWAYS_FAIL: "alwaysFail",
  /** Rejects the call when any restored Step waits on a SubFlow condition. */
  ALWAYS_REJECT: "alwaysReject",
  /** Attaches each condition to its child, and restarts a child that cannot be attached. */
  ATTACH_ELSE_RESTART: "attachElseRestart",
  /** Attaches each condition to its child, and fails a condition whose child cannot be attached. */
  ATTACH_ELSE_FAIL: "attachElseFail",
  /** Attaches each condition to its child, and rejects the call when a child cannot be attached. */
  ATTACH_ELSE_REJECT: "attachElseReject",
} as const);

/**
 * Represents a value from {@link SubFlowResetPolicy}.
 * @since 1.45.5
 */
export type SubFlowResetPolicy = (typeof SubFlowResetPolicy)[keyof typeof SubFlowResetPolicy];

/**
 * Configures `Client.cloneForkFlow` with a copied point and the new Flow's start.
 *
 * Omitted options copy the latest committed state of the source Flow's latest run, keep the source
 * run's Flow configuration, task queue, and Flow timeout duration, and send a new SDK-generated
 * request ID.
 *
 * The point options work as in {@link TimeTravelOptions}. `type` selects which one Dex reads:
 * `CURRENT` (the default) copies the latest committed state of the current run attempt;
 * `BEGINNING` copies the state right after the source run started; `HISTORY_EVENT_TIME`,
 * `STEP_TYPE`, `STEP_EXECUTION_ID` with `stepMethod`, and `EVENT_ID` select a point as time travel
 * does. Every point other than `CURRENT` must be inside the source run's history window, and the
 * state at the point must be running.
 *
 * @example
 * ```ts
 * const result = await client.cloneForkFlow(sessionFlow, "session-1", "session-1-branch", {
 *   type: TimeTravelType.EVENT_ID,
 *   eventId: 42,
 *   requestId: "branch-session-1",
 *   ignoreAlreadyStarted: true,
 *   reason: "try another answer",
 * });
 * console.log(result.runId, result.sourceEventId);
 * ```
 * @since 1.45.5
 */
export interface CloneForkFlowOptions {
  /** Run ID of the source run; omitted or empty selects the latest run. The source run may be closed. */
  readonly sourceRunId?: string;
  /**
   * Run attempt of the source run whose history holds the point.
   * Omitted or zero selects the current run attempt; `CURRENT` accepts only zero.
   */
  readonly sourceRunAttempt?: number;
  /** Copied point selector kind; omitted means `CURRENT`. */
  readonly type?: TimeTravelType;
  /** Timestamp used with `HISTORY_EVENT_TIME`. */
  readonly historyEventTime?: Date;
  /** Registered Step type used with `STEP_TYPE`. */
  readonly stepType?: string;
  /** Exact execution ID used with `STEP_EXECUTION_ID`. */
  readonly stepExecutionId?: string;
  /** WaitFor or Execute boundary required with `STEP_EXECUTION_ID`. */
  readonly stepMethod?: TimeTravelStepMethod;
  /** History event ID used with `EVENT_ID`; a positive safe integer. */
  readonly eventId?: number;
  /** Decides whether the call may start a run for a Flow ID that already has a run, as for `startFlow`. */
  readonly idReusePolicy?: IdReusePolicy;
  /**
   * Returns the existing run instead of raising an already-started error.
   * It applies only when `requestId` matches the request that created the existing run.
   */
  readonly ignoreAlreadyStarted?: boolean;
  /**
   * Non-empty idempotency key; generates a UUID when omitted.
   *
   * A retry with the request ID of the new Flow's latest run copies nothing again. Set it together
   * with `ignoreAlreadyStarted` to retry a call whose outcome is unknown.
   */
  readonly requestId?: string;
  /** Changes the Flow configuration copied from the source run; omitted fields keep the source run's values. */
  readonly configOverride?: FlowConfig;
  /**
   * Task queue of the new Flow; omitted or empty keeps the source run's task queue.
   *
   * `dex.default` selects the default task queue. Names follow {@link StartFlowOptions.taskQueue}.
   * Set one when Workers pick their Flows by task queue, so that the source run's Workers do not
   * run the new Flow's Steps.
   */
  readonly taskQueue?: string;
  /**
   * Attribute writes applied after the copy, in the same change that creates the new Flow.
   * Build them from the Attributes of the Flow passed to `cloneForkFlow`.
   */
  readonly attributes?: readonly InitialAttribute<any>[];
  /**
   * Flow timeout of the new Flow in milliseconds, counted from the call.
   *
   * It must be a non-negative whole number of seconds. Omitted or zero keeps the source run's Flow timeout duration, also counted from
   * the call; a source run without a Flow timeout gives a new Flow without one. The new Flow keeps
   * the source run's timeout policy and handler options.
   */
  readonly timeoutMs?: number;
  /** Operator-readable reason, recorded with the new Flow's start. */
  readonly reason?: string;
  /**
   * Decides what happens to the SubFlow conditions that copied Steps wait on.
   * Omitted or `DEFAULT` uses `ATTACH_ELSE_RESTART`. A child that ended attaches; a child that still
   * runs takes the policy's second action.
   */
  readonly subFlowResetPolicy?: SubFlowResetPolicy;
}

/**
 * Describes the Flow run that `Client.cloneForkFlow` created and the copied point of the source run.
 * @since 1.45.5
 */
export interface CloneForkFlowResult {
  /** Run ID of the new Flow's run. */
  readonly runId: string;
  /** Run ID of the source run. */
  readonly sourceRunId: string;
  /** Run attempt of the source run that holds the copied point. */
  readonly sourceRunAttempt: number;
  /** ID of the last source history event that the copied point includes. */
  readonly sourceEventId: number;
}

/** Selects how an active Flow should close. */
export const StopType = Object.freeze({
  /** Requests cooperative cancellation. */
  CANCEL: "cancel",
  /** Forces immediate termination. */
  TERMINATE: "terminate",
  /** Closes the Flow as failed. */
  FAIL: "fail",
  /**
   * Completes the Flow once no Step is active, like a Step that returns `gracefulComplete`.
   *
   * Use it to end a Flow successfully from outside its Steps, for example from a Step that ends its
   * own Flow through a `Client`. Active Steps keep running. Their decisions still apply, so a Step
   * that moves to another Step keeps the Flow running. The Flow closes as `COMPLETED` when the last
   * active Step finishes, or at once when no Step is active. {@link StopFlowOptions.output} becomes
   * one of the Flow results. A Step waiting for Channel messages or a Timer stays active until its
   * wait ends.
   *
   * A later stop of any type still applies while the Flow waits. Another graceful completion adds
   * its output to the results. The server ignores the reason.
   *
   * It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument error.
   *
   * @example
   * ```ts
   * await client.stopFlow("session-42", {
   *   type: StopType.GRACEFUL_COMPLETE,
   *   output: { summary: "user ended the session" },
   * });
   * ```
   * @since 1.45.6
   */
  GRACEFUL_COMPLETE: "gracefulComplete",
  /**
   * Completes the Flow at once, like a Step that returns `forceComplete`.
   *
   * Use it to end a Flow successfully without waiting for its Steps. The server cancels every
   * active Step and discards their results. Results that Steps already returned through graceful
   * completion stay, and {@link StopFlowOptions.output} is added after them. The Flow closes as
   * `COMPLETED`. Like the Step decision, the close waits until pending Attribute Sync writes and
   * transactional RPCs finish. The server ignores the reason.
   *
   * It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument error.
   *
   * @example
   * ```ts
   * await client.stopFlow("session-42", {
   *   type: StopType.FORCE_COMPLETE,
   *   output: "closed by operator",
   * });
   * ```
   * @since 1.45.6
   */
  FORCE_COMPLETE: "forceComplete",
} as const);

/** Represents a value from {@link StopType}. */
export type StopType = (typeof StopType)[keyof typeof StopType];

/** Configures a `stopFlow` request. */
export interface StopFlowOptions {
  /** Stop behavior; defaults to cooperative cancellation. */
  readonly type?: StopType;
  /**
   * Optional operator-readable reason.
   *
   * `TERMINATE` keeps it in the Flow closed history event, and `FAIL` uses it as the Flow error
   * message. The other stop types ignore it.
   */
  readonly reason?: string;
  /**
   * Becomes a Flow result when `type` is `GRACEFUL_COMPLETE` or `FORCE_COMPLETE`.
   *
   * The SDK encodes it like a Step decision output, so `waitForFlow` returns it as a Step
   * completion whose Step type and Step execution ID are empty. `undefined` adds no result. The
   * server rejects an output that exceeds its maximum value size. `stopFlow` rejects with a
   * `TypeError`, before it sends the request, when `output` is set for any other stop type.
   *
   * @example
   * ```ts
   * await client.stopFlow("session-42", {
   *   type: StopType.FORCE_COMPLETE,
   *   output: { summary: "closed by operator" },
   * });
   * ```
   * @since 1.45.6
   */
  readonly output?: unknown;
}

/**
 * Configures Worker startup and outbound task streams.
 *
 * Every Worker pulls tasks over an outbound connection to the Dex Server.
 * See {@link PullWorkerOptions} for capacity and shutdown settings.
 *
 * Dex Web draws a run's graph from the Flow definition that the Server stores for its Flow type.
 * A Worker configured with `flowDefinitionPaths` or `flowDefinitions` uploads them once per start,
 * after protocol negotiation and Attribute index synchronization and before it opens task streams.
 * All upload requests share one deadline, `attributeIndexSyncTimeoutMs`, and `close` cancels an upload
 * in flight.
 * It uploads only definitions whose Flow type this Worker registers and logs the skipped ones. It
 * rewrites an absolute `source.path` inside each document to a path relative to
 * the current working directory, or to the file name.
 *
 * The Server replaces a stored definition only with a different one generated later. The Worker
 * never writes upload messages to stdout, so they never mix with protocol output there. It writes
 * skipped, created, and replaced definitions to stderr as lines starting with `dex info: `, and logs
 * a kept newer definition, an unknown outcome, or a failure with `console.warn`. It does not log
 * unchanged definitions. Upload failures, including a Server without this API, never stop the Worker from
 * starting. Use
 * `Client.uploadFlowDefinitions` with `force` to roll a definition back.
 *
 * The Worker constructor throws for a missing or unreadable path, an explicitly listed file or an
 * in-memory document that is not a JSON object with a `schemaVersion` starting with `"2."` and a
 * `flow.name` string, a `generatedAt` that is not strict RFC 3339 (`T` or `t`, then `Z`, `z`,
 * `+HH:MM`, or `-HH:MM`), also inside an in-memory document, an in-memory document whose `flow.name` differs from its Flow
 * type, or two definitions for the same Flow type from any sources.
 *
 * Changed in 1.45.0: added `flowDefinitionPaths` and `flowDefinitions`.
 *
 * @example
 * ```ts
 * const worker = new Worker(registry, cache, {
 *   serverAddress: "dex.internal:8801",
 *   flowDefinitionPaths: ["build/flow-definitions"],
 * });
 * ```
 */
export interface WorkerOptions {
  /** Task capacity and shutdown settings; omitted uses the defaults in {@link PullWorkerOptions}. */
  readonly pull?: PullWorkerOptions;
  /** Dex FlowService target; defaults to `localhost:8801`. */
  readonly serverAddress?: string;
  /** Bearer token provider for startup, hydration, and pull streams; omitted sends no authorization metadata. */
  readonly credential?: Credential;
  /**
   * TLS settings for startup, hydration, and pull streams; omitted keeps plaintext gRPC. See {@link FlowServiceTlsOptions}.
   * @since 1.35.0
   */
  readonly flowServiceTls?: FlowServiceTlsOptions;
  /** Startup Indexed Attribute synchronization deadline in milliseconds. Defaults to 120000. */
  readonly attributeIndexSyncTimeoutMs?: number;
  /**
   * Largest gRPC message this Worker exchanges with Dex, in bytes, on its FlowService calls and pull streams.
   *
   * Zero, the default, receives up to `DEFAULT_GRPC_MAX_MESSAGE_BYTES` (32 MiB) and leaves
   * sends unlimited, so the server's limit bounds them. A positive value limits both directions.
   * A negative or non-integer value makes the Worker constructor throw `RangeError`. Raise it with
   * the server's `api.grpcMaxMessageBytes` when one message must carry more.
   *
   * @since 1.38.0
   */
  readonly grpcMaxMessageBytes?: number;
  /**
   * Flow Definition Graph (FDG 2.x) files or directories that the Worker uploads when it starts.
   *
   * Defaults to none. A directory is searched recursively for files whose extension is `.json` in any
   * letter case; a JSON file there that is not an FDG 2.x document, such as `package.json`, is skipped
   * and logged. An explicitly listed file must be an FDG 2.x document whatever its extension.
   * Each file's `flow.name` is its Flow type. Its top-level `generatedAt` (strict RFC 3339) is the
   * generation time; without it the file's modification time is used. Either time is truncated to
   * whole milliseconds. The Worker constructor reads
   * the files. See {@link WorkerOptions} for when and how the Worker uploads them.
   *
   * @since 1.45.0
   */
  readonly flowDefinitionPaths?: readonly string[];
  /**
   * In-memory FDG documents that the Worker uploads when it starts.
   *
   * Defaults to none. Use them when the application renders its graph in code. See
   * {@link FlowDefinitionDocument}.
   *
   * @since 1.45.0
   */
  readonly flowDefinitions?: readonly FlowDefinitionDocument[];
}

/**
 * Configures an outbound pull Worker.
 *
 * Pass this as `WorkerOptions.pull` to customize task capacity and shutdown.
 * Each stream handles one task, while handlers on different streams can run concurrently.
 * Changed in SDK v1.35.0: exactly maxConcurrentTasks long-lived streams replace per-queue idle streams.
 * Slots are STEP_AND_RPC or RPC_ONLY, and every assigned task runs without a capacity decline.
 * Requires Server protocol 4 (server-zd v0.1.17 or later); upgrade the Server before the SDK.
 *
 * @example
 * ```ts
 * const worker = new Worker(registry, cache, { pull: { maxConcurrentTasks: 8 } });
 * await worker.start();
 * const client = new Client(registry, cache);
 * try { await client.startFlow(flow, flowId); } finally { await client.close(); await worker.close(); }
 * ```
 */
export interface PullWorkerOptions {
  /** Stops and drains the Worker when a permanent rejection leaves a slot kind without registered streams. Defaults to false, retrying indefinitely. Worker.run rejects with PullRegistrationError after draining; Worker.start waits for the successful registration of each slot kind and the Server concurrency-limit check. In an adaptive Worker, a soft rejection at the Server stream limit never triggers this option; see `minOpenSlots`. */
  readonly stopOnPermanentStreamRejection?: boolean;
  /**
   * Number of execution slots, each represented by one long-lived poll stream.
   * Zero or omission uses the logical CPU count. Negative values are invalid.
   * For a fixed Worker with this value set explicitly, Worker.start fails if it exceeds the Server
   * per-Worker limit, matching.maxConcurrentTasksCap.
   * Requires a nonnegative safe integer; construction throws RangeError otherwise.
   * Changed in SDK v1.35.0: this is also the exact stream count, and exceeding the cap fails startup.
   *
   * Changed in 1.45.7: this is the ceiling of the slot streams, including the RPC-only slots.
   * A fixed Worker, whose `minOpenSlots` is unset or equal to this value, keeps this many streams
   * open. An adaptive Worker, whose `minOpenSlots` is below it, opens streams up to it only while
   * every open Step-and-RPC stream is busy. Two cases now clamp the value to the Server cap with a
   * warning that names both values, instead of failing `Worker.start`:
   * - a fixed Worker with this option unset, whose CPU-count default exceeds the cap;
   * - an adaptive Worker, with this option set or unset.
   *
   * A fixed Worker with this option unset therefore opens one stream first, learns the cap from its
   * acknowledgement, and then opens the others. That adds one round trip to `Worker.start`. A fixed
   * Worker with an explicit value above the cap still fails `Worker.start`, and it opens all its
   * streams at once. After a clamp, the `reservedRpcTasks` and `minOpenSlots` defaults come from the
   * clamped value. In an adaptive Worker, a lower cap reported on a reconnect lowers its capacity:
   * it half-closes the surplus streams, RPC-only streams first, and keeps running. A fixed Worker
   * keeps the earlier behavior.
   */
  readonly maxConcurrentTasks?: number;
  /**
   * Fewest slot streams the Worker keeps open.
   *
   * It uses the same unit as `maxConcurrentTasks`, so it counts the reserved RPC-only slots too.
   * Zero or omission, the default, uses the resolved `maxConcurrentTasks`: every slot keeps a
   * stream, the Worker is fixed, and it behaves as earlier releases did. Negative values and
   * non-integers are invalid.
   *
   * A value below `maxConcurrentTasks` makes the Worker adaptive. Use it when idle Workers should
   * hold fewer Server streams, such as in a large fleet or with a high `maxConcurrentTasks`.
   * An adaptive Worker:
   * - starts with `minOpenSlots - reservedRpcTasks` Step-and-RPC streams, and opens the RPC-only
   *   streams after the first registration;
   * - doubles its Step-and-RPC streams, one batch at a time, when a task takes the last idle one,
   *   up to `maxConcurrentTasks`;
   * - every 30 seconds half-closes half of the idle streams above the window's peak busy count
   *   plus 25% headroom, never below the floor and never its last idle stream;
   * - keeps the RPC-only streams at `reservedRpcTasks`.
   *
   * A burst larger than the open streams waits a few registration round trips while the Worker
   * grows. A task that reaches a stream while it closes returns to the Server without spending a
   * Step retry, and the Server dispatches it again. When the Server rejects a stream at its stream
   * limit (`RESOURCE_EXHAUSTED`), an adaptive Worker treats the rejection of a growth stream, or of
   * a reconnect after readiness, as a soft limit: it records no {@link PullRegistrationError}.
   * While growth rejections persist, it sends at most one growth batch per 30-second window. A
   * soft-rejected floor or reconnect stream retries when another registered stream of the Worker
   * ends, or at the next window. Use `Worker.pullSlotStreamState` to observe the streams.
   *
   * The Worker constructor throws `RangeError` unless
   * `reservedRpcTasks + 1 <= minOpenSlots <= maxConcurrentTasks`, using the resolved values. When
   * `reservedRpcTasks` is unset, its default reserves one slot only when both `maxConcurrentTasks`
   * and `minOpenSlots` are at least two. `Worker.start` fails at the first registration when the
   * Server cap lowers `maxConcurrentTasks` below `minOpenSlots`, or to `reservedRpcTasks` or less;
   * the error names the values and the cap.
   *
   * @example
   * ```ts
   * // Between 8 and 64 slot streams, one of them reserved for RPCs.
   * const worker = new Worker(registry, cache, { pull: { maxConcurrentTasks: 64, minOpenSlots: 8 } });
   * await worker.start();
   * try {
   *   console.log(worker.pullSlotStreamState().open); // 7 Step-and-RPC streams at rest
   * } finally {
   *   await worker.close();
   * }
   * ```
   * @since 1.45.7
   */
  readonly minOpenSlots?: number;
  /**
   * Number of long-lived slots that accept only RPCs.
   * Unset reserves one slot when maxConcurrentTasks resolves to at least two and minOpenSlots >= 2, otherwise zero.
   * Set zero to let every slot accept Steps and RPCs. Steps are never declined for capacity.
   * With one slot and no explicit reserve, start warns once that RPCs may wait behind Steps.
   * Requires an integer at least zero and less than maxConcurrentTasks; construction throws RangeError otherwise.
   * RPCs can use shared slots and reserved slots. Reservation is capacity, not a latency guarantee.
   * A reserved slot cannot bypass an Attribute lock held by a Step; narrow locks or shorten the method.
   * @example
   * ```ts
   * const pull: PullWorkerOptions = { maxConcurrentTasks: 8, reservedRpcTasks: 2 };
   * ```
   * Changed in SDK v1.35.0: reserved slots open RPC_ONLY streams that take only RPCs,
   * and Steps are no longer declined for capacity. Earlier versions declined Step tasks
   * once busy handlers reached maxConcurrentTasks minus this reserve.
   * Changed in 1.45.7: unset also reserves zero when `minOpenSlots` is 1, and start then warns
   * once that RPCs may wait behind Steps. The value must also leave one Step-and-RPC slot within
   * `minOpenSlots`.
   */
  readonly reservedRpcTasks?: number;
  /**
   * Legacy per-queue idle stream option; zero or omission formerly used one.
   * Still accepts nonnegative integers; invalid values throw RangeError.
   * Changed in SDK v1.35.0: retained for source compatibility with no effect.
   * @deprecated Has no effect. `maxConcurrentTasks` and `minOpenSlots` determine the number of slot streams.
   */
  readonly idleStreamsPerQueue?: number;
  /** Process-unique Worker ID; defaults to host identity plus eight random base32 characters. Use a new ID after every restart. Values contain 1-63 ASCII letters, digits, dots, underscores, or hyphens and cannot equal a registered Flow type. */
  readonly appWorkerID?: string;
  /**
   * Named task queue this Worker pulls from; omitted or the empty string pulls the default task queue.
   *
   * A task queue separates which Workers execute a Flow from the Flow's type. A Flow started with
   * {@link StartFlowOptions.taskQueue} runs its Steps and RPCs only on Workers that pull that task
   * queue and register its Flow type. A Flow started without one runs only on Workers of the default
   * task queue, as before. Workers on different task queues never take each other's work, even for the
   * same Flow type. A sticky Worker on another task queue is skipped.
   *
   * One Worker pulls exactly one task queue. To serve several task queues, construct one Worker per
   * task queue; they can share the Registry and blob cache. Each Worker needs its own `appWorkerID`
   * when you set one.
   *
   * A name has 1 to 63 ASCII letters, digits, dots, underscores, or hyphens. Names starting with
   * `dex.` are reserved, including `dex.default`; the Worker constructor throws `TypeError` for them
   * and for any other invalid name.
   *
   * A non-empty task queue requires Server protocol 5. `Worker.start` rejects before opening task
   * streams when the Server is older. When the Server confirms a stream for a different task queue,
   * the Worker half-closes that stream and treats it as a permanent rejection with the gRPC code
   * `FAILED_PRECONDITION`. `waitPullRegistered` rejects with a {@link PullRegistrationError} whose
   * `cause` is that gRPC error. `Worker.start` does too when every stream is rejected before any
   * registers, and so does `Worker.run` after draining when `stopOnPermanentStreamRejection` is set.
   * In `pullRegistrationState`, the slot kind's `lastPermanentError` holds the gRPC error itself.
   *
   * @example
   * ```ts
   * const blueWorker = new Worker(registry, cache, { pull: { taskQueue: "blue" } });
   * const greenWorker = new Worker(registry, cache, { pull: { taskQueue: "green" } });
   * await Promise.all([blueWorker.start(), greenWorker.start()]);
   * const client = new Client(registry, cache);
   * try {
   *   // Only blueWorker runs this Flow's Steps and RPCs.
   *   await client.startFlow(orderFlow, "order-42", input, { taskQueue: "blue" });
   * } finally {
   *   await client.close();
   *   await Promise.all([blueWorker.close(), greenWorker.close()]);
   * }
   * ```
   * @since 1.45.2
   */
  readonly taskQueue?: string;
  /**
   * Milliseconds allowed for graceful shutdown and busy Reconnect final-frame delivery; defaults to 30000.
   * Requires a non-negative finite value; zero selects the default.
   * On stop, idle streams half-close immediately; busy streams send final output before half-closing.
   * Tasks arriving after stop begins are dropped. Half-closed streams keep reading until the Server ends them.
   * On expiry, streams whose handler has not written final output are reset and their handlers canceled;
   * close waits for handlers to return. A handler whose final output is written still half-closes.
   * Half-closed streams without Server EOF are reset at the deadline. A stream whose final output the Server
   * has not yet confirmed gets 5 more seconds, so close can return that much later. A Server-requested
   * reconnect uses the same timeout and grace.
   * Dex treats a task dropped on such a reset stream as possibly run.
   * Changed in SDK v1.35.0: graceful shutdown returns unstarted tasks without spending a retry;
   * reset or disconnected tasks can still be treated as possibly run. See Worker.close.
   */
  readonly stopDrainTimeoutMs?: number;
}
