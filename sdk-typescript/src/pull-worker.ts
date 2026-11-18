// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomBytes } from "node:crypto";
import { constants as http2Constants, type Http2Stream } from "node:http2";
import { createRequire } from "node:module";
import { availableParallelism, hostname } from "node:os";
import { Duplex } from "node:stream";
import { debuglog } from "node:util";

import { BinaryReader, WireType } from "@bufbuild/protobuf/wire";
import {
  makeGenericClientConstructor,
  Metadata,
  status,
  type ClientDuplexStream,
  type ServiceError,
} from "@grpc/grpc-js";

import { ApiKeyCredential, credentialInterceptors, type Credential } from "./credential.js";
import type { FlowServiceChannel } from "./flow-service-tls.js";
import { registeredFlow, type RegisteredFlow, type Registry } from "./flow.js";
import {
  InvokeExecuteMethodOutput,
  InvokeWaitForMethodOutput,
  PollWorkerSlotKind,
  PollWorkerTaskRejectReason,
  PollWorkerTaskRequest,
  PollWorkerTaskResponse,
  WorkerTaskServiceClient,
  WorkerTaskServiceService,
  type PollWorkerTask,
  type PollWorkerTaskFrame,
  type StepStreamWrite,
  type Value,
} from "./gen/dex.js";
import { Timestamp } from "./gen/google/protobuf/timestamp.js";
import { workerErrorResponse } from "./grpc-status.js";
import type { StepOutputEmitter } from "./invocation-context.js";
import type { PullWorkerOptions } from "./options.js";
import { PullRegistration, PullRegistrationError } from "./pull-registration.js";
import {
  PullSlotStreams,
  defaultReservedRpcTasks,
  type PullSlotAttempt,
  type PullSlotStream,
  type PullSlotStreamHost,
  type PullSlotStreamState,
} from "./pull-slot-streams.js";
import { sdkVersion } from "./sdk-version.generated.js";
import { validatePullWorkerTaskQueue } from "./task-queue.js";
import { WorkerDispatcher } from "./worker-dispatcher.js";

const BUSY_PULL_STREAM_KEEPALIVE_MS = 20_000;
// Extra time after the drain deadline for a final output that Dex has not confirmed reading.
const FINAL_OUTPUT_FLUSH_GRACE_MS = 5_000;

class UndecodableTaskFrame {
  public constructor(public readonly failure: unknown) {}
}

const grpcJsVersion = readGrpcJsVersion();
// NODE_DEBUG=dex prints slot stream decisions.
const debugLog = debuglog("dex");

interface ActiveTask {
  readonly id: string;
  readonly controller: AbortController;
  readonly completion: Promise<void>;
  readonly deadlineCancellation: DeadlineCancellation | undefined;
}

interface PollStream extends PullSlotStream {
  readonly queue: string;
  readonly slotKind: PollWorkerSlotKind;
  // The current attempt in the slot stream state machine.
  slot: PullSlotAttempt;
  stream: ClientDuplexStream<PollWorkerTaskRequest, PollWorkerTaskResponse> | undefined;
  task: ActiveTask | undefined;
  retryTimer: ReturnType<typeof setTimeout> | undefined;
  halfCloseTimer: ReturnType<typeof setTimeout> | undefined;
  keepaliveTimer: ReturnType<typeof setInterval> | undefined;
  halfClosePromise: Promise<void> | undefined;
  halfClosedAtMs: number;
  resolveHalfClose: (() => void) | undefined;
  isRegistered: boolean;
  hasRecordedResult: boolean;
  // The permanent rejection of this runner before readiness, cleared when it registers.
  permanentRejectionBeforeStart: ServiceError | undefined;
  isDisconnected: boolean;
  isHalfClosed: boolean;
  isFinalFrameSent: boolean;
  hasUnconfirmedFinalOutput: boolean;
  http2Streams: readonly Http2Stream[];
  isDraining: boolean;
  isReconnectRequested: boolean;
  hasRetriedRejectedToken: boolean;
  backoffMs: number;
  failure: ServiceError | undefined;
  taskQueueMismatch: ServiceError | undefined;
}

export class PullWorker implements PullSlotStreamHost {
  public readonly appWorkerID: string;
  public readonly taskQueue: string;
  public readonly registration: PullRegistration;
  private readonly client: InstanceType<typeof WorkerTaskServiceClient>;
  private readonly hasRefreshableCredential: boolean;
  private readonly flowTypes: readonly string[];
  private readonly registeredFlows: ReadonlyMap<string, RegisteredFlow>;
  private readonly slots: PullSlotStreams;
  private readonly shouldWarnRpcCapacity: boolean;
  private readonly shouldWarnFloorRpcCapacity: boolean;
  private hasWarnedRpcCapacity = false;
  private isReconcileScheduled = false;
  private startupError: Error | undefined;
  private readonly startupWaiters = new Set<{ resolve: () => void; reject: (failure: Error) => void }>();
  private readonly stopDrainTimeoutMs: number;
  private readonly streams = new Set<PollStream>();
  private readonly activeTasks = new Set<Promise<void>>();
  private isStopping = false;
  private stopDeadlineMs: number | undefined;
  private hasLoggedUnsupportedServer = false;
  private busyTaskCount = 0;

  public constructor(
    registry: Registry,
    private readonly dispatcher: WorkerDispatcher,
    channel: FlowServiceChannel,
    private readonly options: PullWorkerOptions,
    credential?: Credential,
    private readonly onPermanentRejection: () => void = () => {},
  ) {
    const maxConcurrentTasks = positiveOption(options.maxConcurrentTasks, availableParallelism(), "maxConcurrentTasks");
    const minOpenSlots = positiveOption(options.minOpenSlots, maxConcurrentTasks, "minOpenSlots");
    if (minOpenSlots > maxConcurrentTasks) {
      throw new RangeError(`minOpenSlots ${minOpenSlots} must not exceed maxConcurrentTasks ${maxConcurrentTasks}`);
    }
    const reservedRpcTasks = options.reservedRpcTasks ?? defaultReservedRpcTasks(maxConcurrentTasks, minOpenSlots);
    if (!Number.isInteger(reservedRpcTasks) || reservedRpcTasks < 0 || reservedRpcTasks >= maxConcurrentTasks) {
      throw new RangeError("reservedRpcTasks must be a nonnegative integer less than maxConcurrentTasks");
    }
    if (reservedRpcTasks + 1 > minOpenSlots) {
      throw new RangeError(`minOpenSlots ${minOpenSlots} must be at least reservedRpcTasks ${reservedRpcTasks} plus one Step-and-RPC slot`);
    }
    this.shouldWarnRpcCapacity = options.reservedRpcTasks === undefined && maxConcurrentTasks === 1;
    this.shouldWarnFloorRpcCapacity = options.reservedRpcTasks === undefined && maxConcurrentTasks >= 2 && minOpenSlots === 1;
    this.slots = new PullSlotStreams({
      maxConcurrentTasks, minOpenSlots, reservedRpcTasks,
      isMaxConcurrentTasksExplicit: options.maxConcurrentTasks !== undefined && options.maxConcurrentTasks !== 0,
      isMinOpenSlotsExplicit: options.minOpenSlots !== undefined && options.minOpenSlots !== 0,
      isReservedRpcTasksExplicit: options.reservedRpcTasks !== undefined,
    }, this);
    positiveOption(options.idleStreamsPerQueue, 1, "idleStreamsPerQueue");
    this.stopDrainTimeoutMs = options.stopDrainTimeoutMs === undefined || options.stopDrainTimeoutMs === 0
      ? 30_000 : options.stopDrainTimeoutMs;
    if (!Number.isFinite(this.stopDrainTimeoutMs) || this.stopDrainTimeoutMs < 0) {
      throw new RangeError("stopDrainTimeoutMs must not be negative");
    }
    this.registeredFlows = new Map(registry.flows.map((flow) => {
      const definition = registeredFlow(registry, flow);
      return [definition.name, definition] as const;
    }));
    this.flowTypes = [...this.registeredFlows.keys()].sort();
    this.appWorkerID = options.appWorkerID || generateAppWorkerID();
    if (!/^[A-Za-z0-9._-]{1,63}$/.test(this.appWorkerID)) {
      throw new TypeError("appWorkerID must contain 1-63 ASCII letters, digits, dots, underscores, or hyphens");
    }
    if (this.flowTypes.includes(this.appWorkerID)) {
      throw new TypeError(`appWorkerID conflicts with Flow type ${this.appWorkerID}`);
    }
    this.taskQueue = options.taskQueue ?? "";
    validatePullWorkerTaskQueue(this.taskQueue);
    this.registration = new PullRegistration(
      reservedRpcTasks > 0 ? ["STEP_AND_RPC", "RPC_ONLY"] : ["STEP_AND_RPC"],
      this.taskQueue,
    );
    this.client = createWorkerTaskClient(channel, credential);
    this.hasRefreshableCredential = credential instanceof ApiKeyCredential;
  }

  public get reservedRpcDeclines(): number {
    return 0;
  }

  /** The configured reserved RPC slot count, resolved at the first acknowledgement. */
  public get reservedRpcTasks(): number {
    return this.slots.reservedRpcTasks;
  }

  public start(): void {
    if (!this.hasWarnedRpcCapacity) {
      this.hasWarnedRpcCapacity = true;
      if (this.shouldWarnRpcCapacity) {
        console.warn("RPCs may wait behind long-running Steps; use maxConcurrentTasks >= 2 or set reservedRpcTasks explicitly");
      }
      if (this.shouldWarnFloorRpcCapacity) {
        console.warn("RPCs may wait behind long-running Steps; minOpenSlots 1 reserves no RPC slot, so use minOpenSlots >= 2 to reserve one");
      }
      this.slots.start();
    }
  }

  /** Returns an independent snapshot of the slot streams. */
  public slotStreamState(): PullSlotStreamState {
    return this.slots.snapshot();
  }

  public isWorkerStopping(): boolean {
    return this.isStopping;
  }

  public hasRegisteredAllSlotKinds(): boolean {
    return this.registration.hasRegisteredAllSlotKinds;
  }

  public listSlotStreams(): Iterable<PullSlotStream> {
    return this.streams;
  }

  public openSlotRunner(slotKind: PollWorkerSlotKind, attempt: PullSlotAttempt): PullSlotStream {
    const isRpcOnly = slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY;
    const poll: PollStream = {
      queue: isRpcOnly ? "RPC_ONLY" : "STEP_AND_RPC", slotKind, slot: attempt,
      stream: undefined, task: undefined, retryTimer: undefined,
      halfCloseTimer: undefined, keepaliveTimer: undefined, halfClosePromise: undefined, halfClosedAtMs: 0, resolveHalfClose: undefined,
      // A runner holds no call until it opens one, so stop has nothing to close for it.
      isRegistered: false, hasRecordedResult: true, permanentRejectionBeforeStart: undefined, isDisconnected: true,
      isHalfClosed: false, isFinalFrameSent: false, hasUnconfirmedFinalOutput: false, http2Streams: [], isDraining: false,
      isReconnectRequested: false, hasRetriedRejectedToken: false,
      backoffMs: 100, failure: undefined, taskQueueMismatch: undefined,
    };
    this.streams.add(poll);
    // The runner starts after the state machine finishes its current event, as a Go runner would.
    queueMicrotask(() => this.runSlotAttempt(poll));
    return poll;
  }

  public halfCloseSlotStream(stream: PullSlotStream, shouldCloseNow: boolean): void {
    const poll = stream as PollStream;
    poll.isDraining = true;
    if (shouldCloseNow) this.halfCloseStream(poll);
  }

  public async close(): Promise<void> {
    const stopDeadlineMs = this.stopDeadlineMs ??= Date.now() + this.stopDrainTimeoutMs;
    this.isStopping = true;
    this.slots.stop();
    this.registration.stopped();
    if (!this.registration.hasRegisteredAllSlotKinds && this.startupError === undefined) {
      const failure = this.registration.permanentError ?? new Error("pull Worker stopped before registration");
      for (const waiter of this.startupWaiters) waiter.reject(failure);
      this.startupWaiters.clear();
    }
    for (const poll of this.streams) {
      poll.isDraining = true;
      if (poll.isHalfClosed) this.armHalfCloseTimer(poll, this.halfCloseResetDeadlineMs(poll));
      if (poll.retryTimer !== undefined) {
        clearTimeout(poll.retryTimer);
        poll.retryTimer = undefined;
      }
      if (poll.task === undefined && !poll.isDisconnected) {
        this.halfCloseStream(poll);
      }
    }
    if (this.activeTasks.size !== 0) {
      const drained = Promise.allSettled([...this.activeTasks]);
      let drainTimer: ReturnType<typeof setTimeout> | undefined;
      const didDrain = await Promise.race([
        drained.then(() => true),
        new Promise<boolean>((resolve) => {
          drainTimer = setTimeout(() => resolve(false), Math.max(stopDeadlineMs - Date.now(), 0));
          drainTimer.unref();
        }),
      ]);
      if (drainTimer !== undefined) clearTimeout(drainTimer);
      if (!didDrain) {
        for (const poll of this.streams) {
          // A slot whose final output is already written half-closes after cleanup instead.
          if (poll.task !== undefined && !poll.isFinalFrameSent) {
            this.disconnect(poll);
          }
        }
        await drained;
      }
    }
    await Promise.all([...this.streams].flatMap((poll) => poll.halfClosePromise === undefined ? [] : [poll.halfClosePromise]));
    for (const poll of this.streams) {
      if (!poll.isDisconnected) this.closeStream(poll);
    }
    this.streams.clear();
    this.client.close();
  }

  // D13: start resolves once every slot kind has registered, as in the other SDKs.
  public waitStarted(): Promise<void> {
    if (this.startupError !== undefined) return Promise.reject(this.startupError);
    if (this.registration.hasRegisteredAllSlotKinds) return Promise.resolve();
    if (this.isStopping) return Promise.reject(this.registration.permanentError ?? new Error("pull Worker stopped before registration"));
    return new Promise((resolve, reject) => this.startupWaiters.add({ resolve, reject }));
  }

  private resolveStartupIfReady(): void {
    if (!this.registration.hasRegisteredAllSlotKinds) return;
    for (const waiter of this.startupWaiters) waiter.resolve();
    this.startupWaiters.clear();
  }

  // Coalesces wake-ups like Go's wake channel; reconcile re-runs the reopen rule.
  private signal(): void {
    if (this.isReconcileScheduled) return;
    this.isReconcileScheduled = true;
    queueMicrotask(() => {
      this.isReconcileScheduled = false;
      this.slots.reconcile();
    });
  }

  // One attempt of a runner: admission after any backoff, then the call and its Register.
  private runSlotAttempt(poll: PollStream): void {
    if (this.isStopping || !this.slots.admitRegistration(poll)) {
      this.exitRunner(poll);
      return;
    }
    this.openStream(poll);
  }

  // A runner without a reopen grant leaves the Worker; a stream closed on purpose never reopens.
  private exitRunner(poll: PollStream): void {
    if (poll.retryTimer !== undefined) {
      clearTimeout(poll.retryTimer);
      poll.retryTimer = undefined;
    }
    this.slots.abandonAttempt(poll);
    this.streams.delete(poll);
    this.signal();
  }

  private takeReopenGrant(poll: PollStream): boolean {
    const isGranted = poll.slot.isReopenGranted;
    poll.slot.isReopenGranted = false;
    return isGranted;
  }

  private openStream(poll: PollStream): void {
    if (this.isStopping) return;
    poll.isRegistered = false;
    poll.hasRecordedResult = false;
    poll.isDisconnected = false;
    poll.isHalfClosed = false;
    poll.isFinalFrameSent = false;
    poll.hasUnconfirmedFinalOutput = false;
    poll.http2Streams = [];
    poll.failure = undefined;
    poll.taskQueueMismatch = undefined;
    poll.isDraining = false;
    poll.isReconnectRequested = false;
    const stream = this.client.pollWorkerTask();
    poll.stream = stream;
    stream.on("data", (response: PollWorkerTaskResponse | UndecodableTaskFrame) => {
      if (poll.stream !== stream) return;
      if (response instanceof UndecodableTaskFrame) {
        console.error("decode Worker task frame", response.failure);
        this.disconnect(poll);
        return;
      }
      try {
        this.handleFrame(poll, response);
      } catch (failure) {
        console.error("handle Worker task frame", failure);
        this.disconnect(poll);
      }
    });
    stream.on("error", (failure: ServiceError) => {
      if (poll.stream !== stream) return;
      poll.failure = failure;
      this.onStreamClosed(poll);
    });
    stream.on("end", () => {
      if (poll.stream === stream) this.onStreamClosed(poll);
    });
    const beforeRegisterSend = this.slots.settings.beforeRegisterSendForTest;
    if (beforeRegisterSend === undefined) {
      this.sendSlotRegistration(poll, stream);
      return;
    }
    beforeRegisterSend(poll).then(
      () => this.sendSlotRegistration(poll, stream),
      (failure: unknown) => {
        console.error("Worker task registration test hook failed", failure);
        this.sendSlotRegistration(poll, stream);
      },
    );
  }

  // Re-checks admission, marks the attempt as sending, and writes the Register in one synchronous block.
  private sendSlotRegistration(poll: PollStream, stream: ClientDuplexStream<PollWorkerTaskRequest, PollWorkerTaskResponse>): void {
    if (poll.stream !== stream || poll.isDisconnected) return;
    if (!this.slots.admitRegistrationForSend(poll)) {
      // No frame left, so the Server holds nothing for this stream.
      poll.stream = undefined;
      poll.isDisconnected = true;
      stream.cancel();
      this.exitRunner(poll);
      return;
    }
    try {
      this.send(poll, { frame: { $case: "register", value: {
        queue: "",
        appWorkerId: this.appWorkerID,
        flowTypes: [...this.flowTypes],
        sdkName: "typescript",
        sdkVersion,
        supportsReservedSlotDispatch: true,
        slotKind: poll.slotKind,
        taskQueue: this.taskQueue,
      } } });
    } catch (failure) {
      console.error("Worker task registration failed", failure);
      this.disconnect(poll);
    }
  }

  private handleFrame(poll: PollStream, response: PollWorkerTaskResponse): void {
    if (poll.isDisconnected || this.isStopping && poll.task === undefined) {
      if (response.frame?.$case === "task") this.slots.recordTaskDropped(poll);
      return;
    }
    const frame = response.frame;
    if (!poll.isRegistered) {
      if (frame?.$case === "reconnect") {
        poll.isReconnectRequested = true;
        this.halfCloseStream(poll);
      } else if (frame?.$case === "registered") {
        if (!this.installResetHook(poll)) return;
        if (frame.value.taskQueue !== this.taskQueue) {
          this.rejectTaskQueueMismatch(poll, frame.value.taskQueue);
          return;
        }
        const limit = frame.value.maxConcurrentTasksPerAppWorker;
        const isOverLimit = limit !== 0 && limit < this.slots.maxConcurrentTasks;
        if (!this.slots.hasCheckedRegistrationLimit) {
          const failure = this.slots.resolveCeilingAtFirstAcknowledgement(limit);
          if (failure !== undefined) {
            this.failStartup(failure);
            return;
          }
          if (this.slots.reservedRpcTasks === 0) this.registration.removeSlotKind("RPC_ONLY");
        } else if (isOverLimit && !this.slots.isAdaptive) {
          // Only startup fails; a running Worker keeps its slots and lets Dex reject extra streams.
          console.warn(`maxConcurrentTasks ${this.slots.maxConcurrentTasks} exceeds Server matching.maxConcurrentTasksCap ${limit} after reconnect`);
        }
        poll.isRegistered = true;
        poll.permanentRejectionBeforeStart = undefined;
        this.registration.registered(poll.queue);
        poll.backoffMs = 100;
        poll.hasRetriedRejectedToken = false;
        this.resolveStartupIfReady();
        this.rejectStartupIfSlotKindRejected();
        this.slots.recordRegistered(poll, limit);
        this.signal();
      } else {
        console.error("Worker task stream did not register");
        this.closeStream(poll);
        this.afterStreamEnded(poll);
      }
      return;
    }
    // Dex assigns the next task only after it has read the earlier final output.
    if (frame?.$case === "task") poll.hasUnconfirmedFinalOutput = false;
    switch (frame?.$case) {
      case "task":
        this.startTask(poll, frame.value);
        break;
      case "cancelTask":
        if (poll.task?.id === frame.value.taskId) poll.task.controller.abort();
        break;
      case "ping":
        if (poll.task !== undefined && poll.isFinalFrameSent) break;
        try {
          this.send(poll, { frame: { $case: "pong", value: {} } });
        } catch (failure) {
          console.error("send Worker task Pong", failure);
          this.disconnect(poll);
        }
        break;
      case "reconnect":
        poll.isDraining = true;
        poll.isReconnectRequested = true;
        this.slots.recordReconnectRequested(poll);
        if (poll.task === undefined) this.halfCloseStream(poll);
        this.signal();
        break;
      default:
        break;
    }
  }

  // A Server without named task queues silently registers the default queue; refuse that stream.
  private rejectTaskQueueMismatch(poll: PollStream, registeredTaskQueue: string): void {
    const message = `Server registered task queue ${JSON.stringify(registeredTaskQueue)} `
      + `instead of ${JSON.stringify(this.taskQueue)}; it may not support named task queues`;
    console.error("Worker task stream registered the wrong task queue", poll.queue, message);
    poll.taskQueueMismatch = Object.assign(new Error(`${status.FAILED_PRECONDITION} FAILED_PRECONDITION: ${message}`), {
      code: status.FAILED_PRECONDITION, details: message, metadata: new Metadata(),
    });
    this.recordStreamResult(poll);
    // Half-close so Dex returns tasks sent after Registered without consuming an attempt.
    this.halfCloseStream(poll);
  }

  private startTask(poll: PollStream, task: PollWorkerTask): void {
    if (this.isStopping || poll.isDisconnected || poll.isHalfClosed || poll.isDraining) {
      this.slots.recordTaskDropped(poll);
      return;
    }
    const runningTask = poll.task;
    if (runningTask !== undefined) {
      if (poll.isFinalFrameSent) {
        // Dex may reuse the slot before local cleanup finishes; start after it, rechecking stop.
        const stream = poll.stream;
        void runningTask.completion.then(() => {
          if (poll.stream === stream) this.startTask(poll, task);
        });
      } else {
        this.disconnect(poll);
      }
      return;
    }
    if (!isWellFormedTask(task)) {
      console.error("Worker task frame is malformed; resetting its stream", task.taskId);
      this.disconnect(poll);
      return;
    }
    const unknownReason = this.unknownTaskReason(task);
    if (unknownReason !== undefined) {
      this.rejectTask(poll, task.taskId, unknownReason);
      return;
    }
    const controller = new AbortController();
    const deadlineMs = task.deadline?.getTime();
    const deadlineCancellation = deadlineMs === undefined
      ? undefined
      : new DeadlineCancellation(deadlineMs, controller);
    poll.isFinalFrameSent = false;
    poll.keepaliveTimer = setInterval(() => {
      if (poll.isDisconnected || poll.isFinalFrameSent || poll.task === undefined) return;
      try {
        this.send(poll, { frame: { $case: "pong", value: {} } });
      } catch (failure) {
        console.error("send Worker keepalive Pong", failure);
        this.disconnect(poll);
      }
    }, BUSY_PULL_STREAM_KEEPALIVE_MS);
    poll.keepaliveTimer.unref();
    const completion = this.executeTask(poll, task, controller.signal).finally(() => this.finishTask(poll));
    poll.task = { id: task.taskId, controller, completion, deadlineCancellation };
    this.activeTasks.add(completion);
    this.busyTaskCount++;
    this.slots.recordTaskStarted(poll);
    this.signal();
  }

  private unknownTaskReason(task: PollWorkerTask): PollWorkerTaskRejectReason | undefined {
    switch (task.request?.$case) {
      case "waitFor":
      case "execute": {
        const request = task.request.value;
        const flow = this.registeredFlows.get(request.flowType);
        if (task.request.$case === "execute" && request.stepType === "sys:timeout_handler") {
          return flow?.hasTimeoutHandler === true
            ? undefined : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP;
        }
        return flow?.steps.some((step) => step.name === request.stepType) === true
          ? undefined : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP;
      }
      case "rpc": {
        const request = task.request.value;
        const flow = this.registeredFlows.get(request.flowType);
        return flow?.rpcs.some((rpc) => rpc.name === request.rpcName) === true
          ? undefined : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC;
      }
      default:
        return undefined;
    }
  }

  private rejectTask(poll: PollStream, taskId: string, reason: PollWorkerTaskRejectReason): void {
    try {
      this.send(poll, { frame: { $case: "reject", value: { taskId, reason } } });
    } catch (failure) {
      console.error("reject Worker task", failure);
      this.disconnect(poll);
    }
  }

  private async executeTask(poll: PollStream, task: PollWorkerTask, signal: AbortSignal): Promise<void> {
    let emitter: PullStepOutputEmitter | undefined;
    try {
      switch (task.request?.$case) {
        case "waitFor": {
          emitter = new PullStepOutputEmitter(poll, task.taskId, "waitFor", signal, (frame) => this.sendTaskFrame(poll, frame));
          const result = await this.dispatcher.invokeWaitFor(task.request.value, signal, emitter, task.deadline);
          this.sendTaskFrame(poll, { taskId: task.taskId, output: {
            $case: "waitFor", value: InvokeWaitForMethodOutput.create({ output: { $case: "result", value: result } }),
          } });
          break;
        }
        case "execute": {
          emitter = new PullStepOutputEmitter(poll, task.taskId, "execute", signal, (frame) => this.sendTaskFrame(poll, frame));
          const result = await this.dispatcher.invokeExecute(task.request.value, signal, emitter, task.deadline);
          this.sendTaskFrame(poll, { taskId: task.taskId, output: {
            $case: "execute", value: InvokeExecuteMethodOutput.create({ output: { $case: "result", value: result } }),
          } });
          break;
        }
        case "rpc": {
          const result = await this.dispatcher.invokeRPC(task.request.value, signal);
          this.sendTaskFrame(poll, { taskId: task.taskId, output: { $case: "rpc", value: result } });
          break;
        }
        default:
          throw new TypeError("unsupported Worker task request");
      }
    } catch (failure) {
      if (!poll.isDisconnected) {
        try {
          const grpcStatus = poll.task?.deadlineCancellation?.hasExpired === true
            ? status.DEADLINE_EXCEEDED : signal.aborted ? status.CANCELLED : status.UNKNOWN;
          this.sendTaskFrame(poll, { taskId: task.taskId, output: {
            $case: "workerError", value: workerErrorResponse(failure, grpcStatus),
          } });
        } catch (sendFailure) {
          console.error("send Worker error", sendFailure);
          this.disconnect(poll);
        }
      }
    } finally {
      emitter?.close();
    }
  }

  private finishTask(poll: PollStream): void {
    this.stopKeepalive(poll);
    const task = poll.task;
    if (task === undefined) return;
    task.deadlineCancellation?.stop();
    poll.task = undefined;
    poll.isFinalFrameSent = false;
    this.activeTasks.delete(task.completion);
    this.busyTaskCount--;
    // A surplus stream is marked draining, so it half-closes below instead of staying idle.
    this.slots.recordTaskCompleted(poll, poll.isDraining);
    if (poll.isDisconnected) {
      this.afterStreamEnded(poll);
    } else if (this.isStopping || poll.isDraining) {
      this.halfCloseStream(poll);
    }
    this.signal();
  }

  private sendTaskFrame(poll: PollStream, frame: PollWorkerTaskFrame): void {
    if (frame.output?.$case === "rpc" || frame.output?.$case === "workerError"
      || frame.output?.$case === "waitFor" && frame.output.value.output?.$case === "result"
      || frame.output?.$case === "execute" && frame.output.value.output?.$case === "result") {
      this.stopKeepalive(poll);
      poll.isFinalFrameSent = true;
      poll.hasUnconfirmedFinalOutput = true;
    }
    this.send(poll, { frame: { $case: "taskFrame", value: frame } });
  }

  private stopKeepalive(poll: PollStream): void {
    if (poll.keepaliveTimer !== undefined) clearInterval(poll.keepaliveTimer);
    poll.keepaliveTimer = undefined;
  }

  private send(poll: PollStream, request: PollWorkerTaskRequest): void {
    if (poll.isDisconnected || poll.stream === undefined) {
      throw new Error("Worker task stream is closed");
    }
    let encoded: Buffer;
    try {
      encoded = Buffer.from(PollWorkerTaskRequest.encode(request).finish());
    } catch (failure) {
      console.error("encode Worker task frame", failure);
      this.disconnect(poll);
      throw failure;
    }
    poll.stream.write(encoded as unknown as PollWorkerTaskRequest);
  }

  private installResetHook(poll: PollStream): boolean {
    const http2Streams = poll.stream === undefined ? [] : this.findCallHttp2Streams(poll.stream);
    if (http2Streams.length > 0) {
      for (const http2Stream of http2Streams) installResetOnlyClose(http2Stream);
      poll.http2Streams = http2Streams;
      return true;
    }
    const failure = new Error(`cannot find the HTTP/2 stream below @grpc/grpc-js ${grpcJsVersion}; `
      + "this SDK supports @grpc/grpc-js ~1.14.4 and refuses the slot because it could not reset it safely");
    console.error("Worker task stream reset hook unavailable", failure);
    if (this.slots.hasCheckedRegistrationLimit) {
      // The slot is idle and unregistered, so a clean half-close cannot drop a started task.
      this.halfCloseStream(poll);
    } else {
      this.failStartup(failure);
    }
    return false;
  }

  private disconnect(poll: PollStream): void {
    this.stopKeepalive(poll);
    if (poll.isDisconnected) return;
    this.recordStreamResult(poll);
    poll.isDisconnected = true;
    this.resetStream(poll);
    poll.task?.controller.abort();
    this.afterStreamEnded(poll);
  }

  private onStreamClosed(poll: PollStream): void {
    this.stopKeepalive(poll);
    if (poll.isHalfClosed) {
      this.completeHalfClose(poll);
      return;
    }
    if (poll.isDisconnected) return;
    this.recordStreamResult(poll);
    poll.isDisconnected = true;
    // grpc-js may answer a Server-ended call with a clean end; Dex already treats its assignment as disconnected.
    poll.task?.controller.abort();
    this.afterStreamEnded(poll);
  }

  private halfCloseStream(poll: PollStream): void {
    this.stopKeepalive(poll);
    if (poll.isHalfClosed || poll.isDisconnected || poll.task !== undefined || poll.stream === undefined) return;
    poll.isDisconnected = true;
    poll.isHalfClosed = true;
    poll.halfClosePromise = new Promise((resolve) => { poll.resolveHalfClose = resolve; });
    poll.halfClosedAtMs = Date.now();
    const stream = poll.stream;
    this.armHalfCloseTimer(poll, this.halfCloseResetDeadlineMs(poll));
    stream.end();
  }

  private halfCloseResetDeadlineMs(poll: PollStream): number {
    const drainDeadlineMs = this.stopDeadlineMs ?? poll.halfClosedAtMs + this.stopDrainTimeoutMs;
    return poll.hasUnconfirmedFinalOutput ? drainDeadlineMs + FINAL_OUTPUT_FLUSH_GRACE_MS : drainDeadlineMs;
  }

  private armHalfCloseTimer(poll: PollStream, deadlineMs: number): void {
    if (poll.halfCloseTimer !== undefined) clearTimeout(poll.halfCloseTimer);
    const remainingMs = deadlineMs - Date.now();
    poll.halfCloseTimer = setTimeout(() => {
      if (!poll.isHalfClosed) return;
      if (Date.now() < deadlineMs) {
        this.armHalfCloseTimer(poll, deadlineMs);
        return;
      }
      this.resetStream(poll);
      this.completeHalfClose(poll);
    }, Math.min(Math.max(remainingMs, 0), 2_147_483_647));
    poll.halfCloseTimer.unref();
  }

  private completeHalfClose(poll: PollStream): void {
    if (!poll.isHalfClosed) return;
    this.recordStreamResult(poll);
    poll.isHalfClosed = false;
    if (poll.halfCloseTimer !== undefined) clearTimeout(poll.halfCloseTimer);
    poll.halfCloseTimer = undefined;
    poll.resolveHalfClose?.();
    poll.resolveHalfClose = undefined;
    poll.halfClosePromise = undefined;
    poll.stream = undefined;
    this.afterStreamEnded(poll);
  }

  // Continues a runner after its stream ended and its handler returned; only a reopen grant reopens it.
  private afterStreamEnded(poll: PollStream): void {
    if (this.isStopping || poll.retryTimer !== undefined || poll.task !== undefined) return;
    if (poll.slot.isSoftRejected) {
      // A soft rejection waits for another registered stream to end or for the next shrink window.
      debugLog("Worker task stream reached the Server stream limit queue=%s", poll.queue);
      this.exitRunner(poll);
      return;
    }
    if (poll.isReconnectRequested) poll.backoffMs = 100;
    const failure = poll.failure;
    if (failure?.code === status.UNAUTHENTICATED && this.hasRefreshableCredential && !poll.hasRetriedRejectedToken) {
      if (!this.takeReopenGrant(poll)) {
        this.exitRunner(poll);
        return;
      }
      poll.hasRetriedRejectedToken = true;
      queueMicrotask(() => this.runSlotAttempt(poll));
      return;
    }
    let delayMs: number;
    if (failure?.code === status.UNIMPLEMENTED) {
      if (!this.hasLoggedUnsupportedServer) {
        this.hasLoggedUnsupportedServer = true;
        console.error("pull Worker polling is unsupported by this Dex server", failure);
      }
      delayMs = 60_000 + Math.floor(Math.random() * 30_000);
    } else if (poll.taskQueueMismatch !== undefined) {
      delayMs = 60_000 + Math.floor(Math.random() * 30_000);
    } else if (failure !== undefined && [
      status.UNAUTHENTICATED, status.PERMISSION_DENIED, status.INVALID_ARGUMENT,
      status.RESOURCE_EXHAUSTED,
    ].includes(failure.code)) {
      console.error("Worker task stream rejected", poll.queue, failure);
      delayMs = 60_000 + Math.floor(Math.random() * 30_000);
    } else {
      if (failure !== undefined && failure.code !== status.CANCELLED) {
        console.warn("Worker task stream disconnected", poll.queue, failure);
      }
      delayMs = poll.backoffMs + Math.floor(Math.random() * poll.backoffMs / 2);
      poll.backoffMs = Math.min(poll.backoffMs * 2, 5_000);
    }
    if (!this.takeReopenGrant(poll)) {
      this.exitRunner(poll);
      return;
    }
    poll.retryTimer = setTimeout(() => {
      poll.retryTimer = undefined;
      poll.hasRetriedRejectedToken = false;
      this.runSlotAttempt(poll);
    }, delayMs);
  }

  // Ends the stream's attempt once; a soft rejection (F5) is not recorded as a permanent error.
  private recordStreamResult(poll: PollStream): void {
    if (poll.hasRecordedResult) return;
    poll.hasRecordedResult = true;
    const failure = poll.taskQueueMismatch ?? poll.failure;
    const isSoftRejection = !this.isStopping && failure !== undefined && failure !== poll.taskQueueMismatch
      && this.slots.isSoftRejection(poll, failure.code);
    const isPermanent = !isSoftRejection && failure !== undefined && (failure === poll.taskQueueMismatch || [
      status.INVALID_ARGUMENT, status.RESOURCE_EXHAUSTED, status.UNIMPLEMENTED,
      status.PERMISSION_DENIED, status.UNAUTHENTICATED,
    ].includes(failure.code) && !(failure.code === status.UNAUTHENTICATED
      && this.hasRefreshableCredential && !poll.hasRetriedRejectedToken));
    const isRejected = this.registration.ended(poll.queue, poll.isRegistered, isPermanent ? failure : undefined);
    poll.isRegistered = false;
    if (isSoftRejection) this.slots.recordRegistrationRejected(poll);
    if (isPermanent && !this.registration.hasRegisteredAllSlotKinds) {
      poll.permanentRejectionBeforeStart = failure;
      this.rejectStartupIfSlotKindRejected();
    }
    if (isRejected && this.options.stopOnPermanentStreamRejection === true && !this.isStopping) {
      queueMicrotask(this.onPermanentRejection);
    }
    this.slots.recordStreamEnded(poll);
  }

  // Start fails when every stream of a slot kind was rejected, once no first acknowledgement can fail it instead.
  private rejectStartupIfSlotKindRejected(): void {
    if (this.startupWaiters.size === 0 || this.registration.hasRegisteredAllSlotKinds) return;
    const streams = [...this.streams];
    const isEveryStreamRejected = streams.every((poll) => poll.permanentRejectionBeforeStart !== undefined);
    if (!isEveryStreamRejected && !this.slots.hasCheckedRegistrationLimit) return;
    for (const queue of ["STEP_AND_RPC", "RPC_ONLY"]) {
      const rejections = streams.filter((poll) => poll.queue === queue).map((poll) => poll.permanentRejectionBeforeStart);
      const lastRejection = rejections[rejections.length - 1];
      if (lastRejection === undefined || rejections.some((rejection) => rejection === undefined)) continue;
      const failure = this.registration.permanentError
        ?? new PullRegistrationError(queue, lastRejection, this.taskQueue);
      for (const waiter of this.startupWaiters) waiter.reject(failure);
      this.startupWaiters.clear();
      return;
    }
  }

  private failStartup(failure: Error): void {
    this.startupError = failure;
    this.stopDeadlineMs ??= Date.now() + this.stopDrainTimeoutMs;
    this.isStopping = true;
    this.slots.stop();
    this.registration.stopped(failure);
    for (const waiter of this.startupWaiters) waiter.reject(failure);
    this.startupWaiters.clear();
    // Half-close so Dex returns tasks sent after Registered without consuming an attempt.
    for (const poll of this.streams) {
      if (poll.retryTimer !== undefined) {
        clearTimeout(poll.retryTimer);
        poll.retryTimer = undefined;
      }
      this.halfCloseStream(poll);
    }
  }

  private findCallHttp2Streams(stream: ClientDuplexStream<PollWorkerTaskRequest, PollWorkerTaskResponse>): Http2Stream[] {
    return findHttp2Streams(stream);
  }

  private resetStream(poll: PollStream): void {
    // A stream without a hooked HTTP/2 stream never registered, so no handler ran on it.
    for (const http2Stream of poll.http2Streams) {
      if (!http2Stream.destroyed) http2Stream.destroy(new Error("Worker task stream reset"));
    }
    poll.stream?.cancel();
  }

  private closeStream(poll: PollStream): void {
    this.recordStreamResult(poll);
    poll.isDisconnected = true;
    this.resetStream(poll);
    if (poll.isHalfClosed) this.completeHalfClose(poll);
    if (poll.retryTimer !== undefined) {
      clearTimeout(poll.retryTimer);
      poll.retryTimer = undefined;
    }
  }
}

class PullStepOutputEmitter implements StepOutputEmitter {
  private isActive = true;

  public constructor(
    private readonly poll: PollStream,
    private readonly taskID: string,
    private readonly method: "waitFor" | "execute",
    private readonly signal: AbortSignal,
    private readonly sendFrame: (frame: PollWorkerTaskFrame) => void,
  ) {}

  public close(): void {
    this.isActive = false;
  }

  public async recordHeartbeat(value: Value | undefined): Promise<void> {
    this.sendOutput({ $case: "heartbeat", value: { value } });
  }

  public writeStream(write: StepStreamWrite): void {
    this.sendOutput({ $case: "streamWrite", value: write });
  }

  private sendOutput(output: InvokeWaitForMethodOutput["output"]): void {
    if (!this.isActive || this.signal.aborted) throw new Error("Worker task is no longer active");
    if (this.poll.isDisconnected) throw new Error("Worker task stream is closed");
    const value = this.method === "waitFor"
      ? { $case: "waitFor" as const, value: InvokeWaitForMethodOutput.create({ output }) }
      : { $case: "execute" as const, value: InvokeExecuteMethodOutput.create({ output }) };
    this.sendFrame({ taskId: this.taskID, output: value });
  }
}

class DeadlineCancellation {
  public hasExpired = false;
  private timer: ReturnType<typeof setTimeout> | undefined;

  public constructor(
    private readonly deadlineMs: number,
    private readonly controller: AbortController,
  ) {
    this.schedule();
  }

  public stop(): void {
    if (this.timer !== undefined) clearTimeout(this.timer);
  }

  private schedule(): void {
    const remainingMs = this.deadlineMs - Date.now();
    if (remainingMs <= 0) {
      this.hasExpired = true;
      this.controller.abort();
    } else {
      this.timer = setTimeout(() => this.schedule(), Math.min(remainingMs, 2_147_483_647));
    }
  }
}

/**
 * Finds the HTTP/2 streams below a grpc-js call by walking its internal call chain.
 * @returns The HTTP/2 streams found; empty when the call has none or grpc-js internals changed.
 */
function findHttp2Streams(root: object): Http2Stream[] {
  const found: Http2Stream[] = [];
  const pending: unknown[] = [root];
  const visited = new Set<unknown>();
  while (pending.length > 0 && visited.size < 64) {
    const candidate = pending.pop();
    if (typeof candidate !== "object" || candidate === null || visited.has(candidate)) continue;
    visited.add(candidate);
    if (Array.isArray(candidate)) {
      pending.push(...candidate);
      continue;
    }
    const http2Stream = Reflect.get(candidate, "http2Stream") as unknown;
    if (http2Stream instanceof Duplex && "rstCode" in http2Stream) found.push(http2Stream as Http2Stream);
    for (const key of ["call", "nextCall", "child", "underlyingCalls"]) pending.push(Reflect.get(candidate, key));
  }
  return found;
}

// Node's close(code) sends END_STREAM before RST_STREAM, so grpc-js cancels become RST-only destroys.
function installResetOnlyClose(http2Stream: Http2Stream): void {
  const closeWithCode = http2Stream.close.bind(http2Stream);
  const resetOnlyClose = (code?: number, callback?: () => void): void => {
    if (code === undefined || code === http2Constants.NGHTTP2_NO_ERROR) {
      closeWithCode(code, callback);
      return;
    }
    if (callback !== undefined) http2Stream.once("close", callback);
    if (!http2Stream.destroyed) http2Stream.destroy(new Error(`Worker task stream reset with HTTP/2 code ${code}`));
  };
  Reflect.set(http2Stream, "close", resetOnlyClose);
}

function readGrpcJsVersion(): string {
  try {
    const manifest = createRequire(import.meta.url)("@grpc/grpc-js/package.json") as { version?: unknown };
    return typeof manifest.version === "string" ? manifest.version : "unknown";
  } catch (failure) {
    console.warn("read @grpc/grpc-js version", failure);
    return "unknown";
  }
}

function isWellFormedTask(task: PollWorkerTask): boolean {
  const deadlineMs = task.deadline?.getTime();
  return task.taskId !== "" && task.request !== undefined && (deadlineMs === undefined || Number.isFinite(deadlineMs));
}

function positiveOption(value: number | undefined, defaultValue: number, name: string): number {
  if (value === undefined || value === 0) return defaultValue;
  if (!Number.isSafeInteger(value) || value < 0) throw new RangeError(`${name} must be a non-negative integer`);
  return value;
}

function generateAppWorkerID(): string {
  const identity = [process.env.DEX_WORKER_INSTANCE, process.env.POD_NAME, process.env.HOSTNAME, hostname()]
    .find((candidate) => candidate !== undefined && candidate.trim() !== "") ?? "worker";
  const sanitized = identity.toLowerCase().replace(/[^a-z0-9-]+/g, "-").slice(0, 54).replace(/^-+|-+$/g, "") || "worker";
  const alphabet = "abcdefghijklmnopqrstuvwxyz234567";
  const bytes = randomBytes(5);
  let bits = 0n;
  for (const byte of bytes) bits = bits << 8n | BigInt(byte);
  let suffix = "";
  for (let shift = 35n; shift >= 0n; shift -= 5n) suffix += alphabet[Number(bits >> shift & 31n)];
  return `${sanitized}-${suffix}`;
}

function createWorkerTaskClient(channel: FlowServiceChannel, credential: Credential | undefined): InstanceType<typeof WorkerTaskServiceClient> {
  // The SDK encodes and decodes frames itself, so grpc-js never cancels a stream for a codec failure.
  const ResettableWorkerTaskServiceClient = makeGenericClientConstructor({
    pollWorkerTask: {
      ...WorkerTaskServiceService.pollWorkerTask,
      requestSerialize: (value: Buffer): Buffer => value,
      responseDeserialize: (value: Buffer) => decodeTaskFrame(value, channel.maxReceiveMessageBytes),
    },
  }, "dex.WorkerTaskService") as unknown as typeof WorkerTaskServiceClient;
  return new ResettableWorkerTaskServiceClient(channel.target, channel.channelCredentials, {
    ...channel.channelOptions,
    interceptors: credentialInterceptors(credential),
    // grpc-js shares connections between equal options; this keeps task streams off the FlowService one.
    "dex.worker_task_channel": 1,
  });
}

/**
 * Decodes one task frame without throwing, so a bad frame resets the stream instead of a grpc-js cancel.
 * @returns The decoded frame, or an UndecodableTaskFrame when it is oversized or malformed.
 */
function decodeTaskFrame(value: Buffer, maxTaskFrameBytes: number): PollWorkerTaskResponse | UndecodableTaskFrame {
  // Bounds every task frame, including after grpc-js decompresses it.
  if (value.length > maxTaskFrameBytes) {
    return new UndecodableTaskFrame(new RangeError(`Worker task frame of ${value.length} bytes exceeds ${maxTaskFrameBytes} bytes`));
  }
  try {
    const response = PollWorkerTaskResponse.decode(value);
    if (response.frame?.$case === "task" && !hasValidTaskDeadline(value)) {
      return new UndecodableTaskFrame(new RangeError(`Worker task ${response.frame.value.taskId} has an invalid deadline`));
    }
    return response;
  } catch (failure) {
    return new UndecodableTaskFrame(failure);
  }
}

/**
 * Checks the raw task deadline, because the generated Date conversion hides out-of-range fields.
 * @returns Whether the deadline is absent or a valid Timestamp between years 1 and 9999.
 */
function hasValidTaskDeadline(responseBytes: Uint8Array): boolean {
  const taskBytes = readLastLengthDelimitedField(responseBytes, 2);
  const deadlineBytes = taskBytes === undefined ? undefined : readLastLengthDelimitedField(taskBytes, 2);
  if (deadlineBytes === undefined) return true;
  const deadline = Timestamp.decode(deadlineBytes);
  const seconds = Number(deadline.seconds);
  return Number.isSafeInteger(seconds) && seconds >= -62_135_596_800 && seconds <= 253_402_300_799
    && Number.isInteger(deadline.nanos) && deadline.nanos >= 0 && deadline.nanos < 1_000_000_000;
}

function readLastLengthDelimitedField(message: Uint8Array, fieldNumber: number): Uint8Array | undefined {
  const reader = new BinaryReader(message);
  let value: Uint8Array | undefined;
  while (reader.pos < reader.len) {
    const [field, wireType] = reader.tag();
    if (field === fieldNumber && wireType === WireType.LengthDelimited) value = reader.bytes();
    else reader.skip(wireType);
  }
  return value;
}
