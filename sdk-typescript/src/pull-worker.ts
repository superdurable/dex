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

import { BinaryReader, WireType } from "@bufbuild/protobuf/wire";
import {
  makeGenericClientConstructor,
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
import { sdkVersion } from "./sdk-version.generated.js";
import { WorkerDispatcher } from "./worker-dispatcher.js";

const BUSY_PULL_STREAM_KEEPALIVE_MS = 20_000;
// Extra time after the drain deadline for a final output that Dex has not confirmed reading.
const FINAL_OUTPUT_FLUSH_GRACE_MS = 5_000;
// Bounds every task frame, including after grpc-js decompresses it.
const MAX_TASK_FRAME_BYTES = 4 * 1024 * 1024;

class UndecodableTaskFrame {
  public constructor(public readonly failure: unknown) {}
}

// The SDK encodes and decodes frames itself, so grpc-js never cancels a stream for a codec failure.
const ResettableWorkerTaskServiceClient = makeGenericClientConstructor({
  pollWorkerTask: {
    ...WorkerTaskServiceService.pollWorkerTask,
    requestSerialize: (value: Buffer): Buffer => value,
    responseDeserialize: decodeTaskFrame,
  },
}, "dex.WorkerTaskService") as unknown as typeof WorkerTaskServiceClient;
const grpcJsVersion = readGrpcJsVersion();

interface ActiveTask {
  readonly id: string;
  readonly controller: AbortController;
  readonly completion: Promise<void>;
  readonly deadlineCancellation: DeadlineCancellation | undefined;
}

interface PollStream {
  readonly queue: string;
  readonly slotKind: PollWorkerSlotKind;
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
  hasPermanentRejectionBeforeStart: boolean;
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
}

export class PullWorker {
  public readonly appWorkerID: string;
  public readonly registration: PullRegistration;
  private readonly client: InstanceType<typeof WorkerTaskServiceClient>;
  private readonly hasRefreshableCredential: boolean;
  private readonly flowTypes: readonly string[];
  private readonly registeredFlows: ReadonlyMap<string, RegisteredFlow>;
  private readonly maxConcurrentTasks: number;
  private readonly reservedRpcTasks: number;
  private hasWarnedRpcCapacity = false;
  private hasRegisteredSlot = false;
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
    this.maxConcurrentTasks = positiveOption(options.maxConcurrentTasks, availableParallelism(), "maxConcurrentTasks");
    this.reservedRpcTasks = options.reservedRpcTasks ?? (this.maxConcurrentTasks >= 2 ? 1 : 0);
    if (!Number.isInteger(this.reservedRpcTasks) || this.reservedRpcTasks < 0 || this.reservedRpcTasks >= this.maxConcurrentTasks) {
      throw new RangeError("reservedRpcTasks must be a nonnegative integer less than maxConcurrentTasks");
    }
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
    this.registration = new PullRegistration(this.reservedRpcTasks > 0 ? ["STEP_AND_RPC", "RPC_ONLY"] : ["STEP_AND_RPC"]);
    this.client = createWorkerTaskClient(channel, credential);
    this.hasRefreshableCredential = credential instanceof ApiKeyCredential;
  }

  public get reservedRpcDeclines(): number {
    return 0;
  }

  public start(): void {
    if (this.maxConcurrentTasks === 1 && this.options.reservedRpcTasks === undefined && !this.hasWarnedRpcCapacity) {
      this.hasWarnedRpcCapacity = true;
      console.warn("RPCs may wait behind long-running Steps; use maxConcurrentTasks >= 2 or set reservedRpcTasks explicitly");
    }
    this.reconcile();
  }

  public async close(): Promise<void> {
    const stopDeadlineMs = this.stopDeadlineMs ??= Date.now() + this.stopDrainTimeoutMs;
    this.isStopping = true;
    this.registration.stopped();
    if (!this.hasRegisteredSlot && this.startupError === undefined) {
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

  public waitStarted(): Promise<void> {
    if (this.startupError !== undefined) return Promise.reject(this.startupError);
    if (this.hasRegisteredSlot) return Promise.resolve();
    if (this.isStopping) return Promise.reject(this.registration.permanentError ?? new Error("pull Worker stopped before registration"));
    return new Promise((resolve, reject) => this.startupWaiters.add({ resolve, reject }));
  }

  private reconcile(): void {
    if (this.isStopping || this.streams.size > 0) return;
    for (let slotNumber = 0; slotNumber < this.maxConcurrentTasks; slotNumber++) {
      const isRpcOnly = slotNumber >= this.maxConcurrentTasks - this.reservedRpcTasks;
      const poll: PollStream = {
        queue: isRpcOnly ? "RPC_ONLY" : "STEP_AND_RPC",
        slotKind: isRpcOnly ? PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
          : PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        stream: undefined, task: undefined, retryTimer: undefined,
        halfCloseTimer: undefined, keepaliveTimer: undefined, halfClosePromise: undefined, halfClosedAtMs: 0, resolveHalfClose: undefined,
        isRegistered: false, hasRecordedResult: false, hasPermanentRejectionBeforeStart: false, isDisconnected: false,
        isHalfClosed: false, isFinalFrameSent: false, hasUnconfirmedFinalOutput: false, http2Streams: [], isDraining: false,
        isReconnectRequested: false, hasRetriedRejectedToken: false,
        backoffMs: 100, failure: undefined,
      };
      this.streams.add(poll);
      this.openStream(poll);
    }
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
    try {
      this.send(poll, { frame: { $case: "register", value: {
        queue: "",
        appWorkerId: this.appWorkerID,
        flowTypes: [...this.flowTypes],
        sdkName: "typescript",
        sdkVersion,
        supportsReservedSlotDispatch: true,
        slotKind: poll.slotKind,
      } } });
    } catch (failure) {
      console.error("Worker task registration failed", failure);
      this.disconnect(poll);
    }
  }

  private handleFrame(poll: PollStream, response: PollWorkerTaskResponse): void {
    if (poll.isDisconnected || this.isStopping && poll.task === undefined) return;
    const frame = response.frame;
    if (!poll.isRegistered) {
      if (frame?.$case === "reconnect") {
        poll.isReconnectRequested = true;
        this.halfCloseStream(poll);
      } else if (frame?.$case === "registered") {
        if (!this.installResetHook(poll)) return;
        const limit = frame.value.maxConcurrentTasksPerAppWorker;
        const isOverLimit = limit !== 0 && limit < this.maxConcurrentTasks;
        if (!this.hasRegisteredSlot) {
          if (isOverLimit) {
            this.failStartup(new Error(`maxConcurrentTasks ${this.maxConcurrentTasks} exceeds Server matching.maxConcurrentTasksCap ${limit}`));
            return;
          }
          this.hasRegisteredSlot = true;
          for (const waiter of this.startupWaiters) waiter.resolve();
          this.startupWaiters.clear();
        } else if (isOverLimit) {
          // Only startup fails; a running Worker keeps its slots and lets Dex reject extra streams.
          console.warn(`maxConcurrentTasks ${this.maxConcurrentTasks} exceeds Server matching.maxConcurrentTasksCap ${limit} after reconnect`);
        }
        poll.isRegistered = true;
        poll.hasPermanentRejectionBeforeStart = false;
        this.registration.registered(poll.queue);
        poll.backoffMs = 100;
        poll.hasRetriedRejectedToken = false;
        this.reconcile();
      } else {
        console.error("Worker task stream did not register");
        this.closeStream(poll);
        this.scheduleReconnect(poll);
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
        if (poll.task === undefined) {
          this.halfCloseStream(poll);
        } else {
          this.reconcile();
        }
        break;
      default:
        break;
    }
  }

  private startTask(poll: PollStream, task: PollWorkerTask): void {
    if (this.isStopping || poll.isDisconnected || poll.isHalfClosed || poll.isDraining) return;
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
    this.reconcile();
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
    if (poll.isDisconnected) {
      if (!this.isStopping) this.scheduleReconnect(poll);
    } else if (this.isStopping || poll.isDraining) {
      this.halfCloseStream(poll);
    }
    this.reconcile();
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
    if (this.hasRegisteredSlot) {
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
    if (this.isStopping) return;
    if (poll.task === undefined) this.scheduleReconnect(poll);
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
    if (!this.isStopping && poll.task === undefined) this.scheduleReconnect(poll);
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
    if (!this.isStopping) this.scheduleReconnect(poll);
  }

  private scheduleReconnect(poll: PollStream): void {
    if (this.isStopping || poll.retryTimer !== undefined) return;
    if (poll.isReconnectRequested) poll.backoffMs = 100;
    const failure = poll.failure;
    if (failure?.code === status.UNAUTHENTICATED && this.hasRefreshableCredential && !poll.hasRetriedRejectedToken) {
      poll.hasRetriedRejectedToken = true;
      queueMicrotask(() => this.openStream(poll));
      return;
    }
    let delayMs: number;
    if (failure?.code === status.UNIMPLEMENTED) {
      if (!this.hasLoggedUnsupportedServer) {
        this.hasLoggedUnsupportedServer = true;
        console.error("pull Worker polling is unsupported by this Dex server", failure);
      }
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
    poll.retryTimer = setTimeout(() => {
      poll.retryTimer = undefined;
      poll.hasRetriedRejectedToken = false;
      this.openStream(poll);
    }, delayMs);
  }

  private recordStreamResult(poll: PollStream): void {
    if (poll.hasRecordedResult) return;
    poll.hasRecordedResult = true;
    const failure = poll.failure;
    const isPermanent = failure !== undefined && [
      status.INVALID_ARGUMENT, status.RESOURCE_EXHAUSTED, status.UNIMPLEMENTED,
      status.PERMISSION_DENIED, status.UNAUTHENTICATED,
    ].includes(failure.code) && !(failure.code === status.UNAUTHENTICATED
      && this.hasRefreshableCredential && !poll.hasRetriedRejectedToken);
    const isRejected = this.registration.ended(poll.queue, poll.isRegistered, isPermanent ? failure : undefined);
    poll.isRegistered = false;
    if (isPermanent && !this.hasRegisteredSlot) {
      poll.hasPermanentRejectionBeforeStart = true;
      this.rejectStartupIfEveryStreamRejected(failure);
    }
    if (isRejected && this.options.stopOnPermanentStreamRejection === true && !this.isStopping) {
      queueMicrotask(this.onPermanentRejection);
    }
  }

  private rejectStartupIfEveryStreamRejected(lastFailure: ServiceError): void {
    if (this.startupWaiters.size === 0) return;
    for (const poll of this.streams) {
      if (!poll.hasPermanentRejectionBeforeStart) return;
    }
    const failure = this.registration.permanentError ?? new PullRegistrationError("STEP_AND_RPC", lastFailure);
    for (const waiter of this.startupWaiters) waiter.reject(failure);
    this.startupWaiters.clear();
  }

  private failStartup(failure: Error): void {
    this.startupError = failure;
    this.stopDeadlineMs ??= Date.now() + this.stopDrainTimeoutMs;
    this.isStopping = true;
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
  return new ResettableWorkerTaskServiceClient(channel.target, channel.channelCredentials, {
    ...channel.channelOptions,
    interceptors: credentialInterceptors(credential),
    "grpc.max_receive_message_length": MAX_TASK_FRAME_BYTES,
  });
}

/**
 * Decodes one task frame without throwing, so a bad frame resets the stream instead of a grpc-js cancel.
 * @returns The decoded frame, or an UndecodableTaskFrame when it is oversized or malformed.
 */
function decodeTaskFrame(value: Buffer): PollWorkerTaskResponse | UndecodableTaskFrame {
  if (value.length > MAX_TASK_FRAME_BYTES) {
    return new UndecodableTaskFrame(new RangeError(`Worker task frame of ${value.length} bytes exceeds ${MAX_TASK_FRAME_BYTES} bytes`));
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
