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
  Server,
  ServerCredentials,
  status,
  type ServerDuplexStream,
} from "@grpc/grpc-js";

import { Registry, StepList, Stream, Worker, PullRegistrationError, gracefulComplete, retryAfter, stringCodec, type AsyncContext, type Step, type StepDecision, type PullWorkerOptions, type BlobCache, type Flow } from "../src/index.js";
import {
  Context as ProtoContext,
  FlowServiceService,
  type FlowServiceServer,
  InvokeExecuteMethodRequest,
  InvokeWorkerRPCRequest,
  InvokeWorkerRPCResponse,
  InvokeWaitForMethodRequest,
  InvokeWaitForMethodResponse,
  PollWorkerTaskRejectReason,
  PollWorkerSlotKind,
  Value,
  WorkerTaskServiceService,
  type PollWorkerTaskRequest,
  type PollWorkerTaskResponse,
  type WorkerTaskServiceServer,
} from "../src/gen/dex.js";
import { workerErrorResponse } from "../src/grpc-status.js";
import { PullWorker } from "../src/pull-worker.js";
import type { StepOutputEmitter } from "../src/invocation-context.js";
import { encodeValue, type ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";

type TaskStream = ServerDuplexStream<PollWorkerTaskRequest, PollWorkerTaskResponse>;

class TestFlow implements Flow<void> {
  public getFlowType(): string { return "TestFlow"; }
  public getSteps(): StepList<void> { return StepList.empty(); }
}

test("pull options reject invalid capacity and Worker IDs", async () => {
  const registry = new Registry([new TestFlow()]);
  const cache = {} as BlobCache;
  assert.throws(() => new Worker(registry, cache, { pull: { maxConcurrentTasks: -1 } }), RangeError);
  assert.throws(() => new Worker(registry, cache, { pull: { idleStreamsPerQueue: 1.2 } }), RangeError);
  assert.throws(() => new Worker(registry, cache, { pull: { stopDrainTimeoutMs: -1 } }), RangeError);
  assert.throws(() => new Worker(registry, cache, { pull: { appWorkerID: "bad/id" } }), TypeError);
  assert.throws(() => new Worker(registry, cache, { pull: { appWorkerID: "TestFlow" } }), TypeError);
  const worker = new Worker(registry, cache);
  await worker.close();
});

test("unset reserve defaults to one and a one-slot Worker warns once", async (context) => {
  const warning = context.mock.method(console, "warn", () => {});
  for (const maxConcurrentTasks of [1, 4]) {
    const harness = await startHarness();
    const worker = harness.worker(async () => InvokeWorkerRPCResponse.create({}), { maxConcurrentTasks, reservedRpcTasks: undefined } as unknown as PullWorkerOptions);
    try {
      assert.equal(Reflect.get(worker, "reservedRpcTasks"), maxConcurrentTasks === 1 ? 0 : 1);
      worker.start();
      worker.start();
      const stream = await harness.nextStream();
      stream.call.write(registered(true));
    } finally {
      await worker.close();
      await harness.close();
    }
  }
  const reserveWarnings = warning.mock.calls.filter((call) => String(call.arguments[0]).includes("RPCs may wait"));
  assert.deepEqual(reserveWarnings.map((call) => call.arguments[0]), [
    "RPCs may wait behind long-running Steps; use maxConcurrentTasks >= 2 or set reservedRpcTasks explicitly",
  ]);
});

for (const reservedRpcTasks of [-1, 2, 3, 0.5, NaN, Infinity]) {
  test(`invalid RPC reserve ${reservedRpcTasks} is rejected`, () => {
    assert.throws(() => new PullWorker(new Registry([]), {} as WorkerDispatcher,
      "127.0.0.1:1", { maxConcurrentTasks: 2, reservedRpcTasks }), RangeError);
  });
}

test("generated appWorkerID uses host identity and eight base32 characters", async () => {
  const registry = new Registry([]);
  const dispatcher = {} as WorkerDispatcher;
  const first = new PullWorker(registry, dispatcher, "127.0.0.1:1", {});
  const second = new PullWorker(registry, dispatcher, "127.0.0.1:1", {});
  try {
    assert.match(first.appWorkerID, /^[A-Za-z0-9._-]{1,54}-[a-z2-7]{8}$/);
    assert.notEqual(first.appWorkerID, second.appWorkerID);
  } finally {
    await first.close();
    await second.close();
  }
});

test("generated appWorkerID uses HOSTNAME when higher-priority identities are absent", async () => {
  const previous = {
    dex: process.env.DEX_WORKER_INSTANCE,
    pod: process.env.POD_NAME,
    host: process.env.HOSTNAME,
  };
  delete process.env.DEX_WORKER_INSTANCE;
  delete process.env.POD_NAME;
  process.env.HOSTNAME = "Pull.Worker Example";
  let worker: PullWorker | undefined;
  try {
    worker = new PullWorker(new Registry([]), {} as WorkerDispatcher, "127.0.0.1:1", {});
    assert.match(worker.appWorkerID, /^pull-worker-example-[a-z2-7]{8}$/);
  } finally {
    await worker?.close();
    restoreEnvironment("DEX_WORKER_INSTANCE", previous.dex);
    restoreEnvironment("POD_NAME", previous.pod);
    restoreEnvironment("HOSTNAME", previous.host);
  }
});

test("reserved execution slots register all Flow types, reuse streams, and never decline capacity", async () => {
  for (const reservedRpcTasks of [0, 1, 2]) {
    const harness = await startHarness();
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const worker = harness.worker(async () => {
      await gate;
      return InvokeWorkerRPCResponse.create({});
    }, { maxConcurrentTasks: 3, reservedRpcTasks, idleStreamsPerQueue: 20 });
    Reflect.set(worker, "flowTypes", ["TestFlow", "OtherFlow"]);
    const streams: CapturedStream[] = [];
    try {
      worker.start();
      for (let slotNumber = 0; slotNumber < 3; slotNumber++) {
        const stream = await harness.nextStream();
        const registration = await stream.frame("register");
        assert.equal(registration.frame?.$case, "register");
        if (registration.frame?.$case !== "register") throw new Error("missing registration");
        assert.equal(registration.frame.value.queue, "");
        assert.equal(registration.frame.value.appWorkerId, worker.appWorkerID);
        assert.equal(registration.frame.value.supportsReservedSlotDispatch, true);
        assert.deepEqual(registration.frame.value.flowTypes, ["TestFlow", "OtherFlow"]);
        assert.equal(registration.frame.value.slotKind, slotNumber >= 3 - reservedRpcTasks
          ? PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
          : PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC);
        stream.call.write(registered(true));
        streams.push(stream);
      }
      await worker.registration.wait({ timeoutMs: 5_000 });
      for (const [slotNumber, stream] of streams.entries()) stream.call.write(rpcTask(`busy-${slotNumber}`));
      await until(() => harness.invocationCount === 3);
      assert.equal(harness.openedStreamCount, 3);
      release();
      for (const stream of streams) await stream.frame("taskFrame");
      streams[0]!.call.write(rpcTask("reuse"));
      await streams[0]!.frame("taskFrame", 2);
      assert.equal(harness.openedStreamCount, 3);
      assert.equal(worker.reservedRpcDeclines, 0);
      assert.equal(streams.some((stream) => stream.frames.some((request) => request.frame?.$case === "reject")), false);
    } finally { release(); await worker.close(); await harness.close(); }
  }
});

test("a Step sent to an RPC-only slot is executed without a capacity rejection", async () => {
  const harness = await startHarness();
  let stepCalls = 0;
  const worker = harness.worker(async () => InvokeWorkerRPCResponse.create(), { maxConcurrentTasks: 2 });
  Reflect.set(worker, "dispatcher", { invokeExecute: async () => { stepCalls++; return {}; } } as unknown as WorkerDispatcher);
  try {
    worker.start();
    const shared = await harness.nextStream();
    const reserved = await harness.nextStream();
    shared.call.write(registered(true));
    reserved.call.write(registered(true));
    // Real Servers route Steps only to shared slots; a misrouted assignment still consumes its reserved execution slot.
    reserved.call.write({ frame: { $case: "task", value: { taskId: "misrouted-step", deadline: undefined,
      request: { $case: "execute", value: InvokeExecuteMethodRequest.create({ flowType: "TestFlow", stepType: "TestStep" }) },
    } } });
    await reserved.frame("taskFrame");
    assert.equal(stepCalls, 1);
    assert.equal(reserved.frames.some((request) => request.frame?.$case === "reject"), false);
  } finally { await worker.close(); await harness.close(); }
});

test("unknown task rejection follows the Registered capability for each stream", async () => {
  const harness = await startHarness();
  const dispatcher = {
    async invokeWaitFor(): Promise<InvokeWaitForMethodResponse> {
      throw new Error("Step MissingStep is not registered");
    },
    async invokeExecute(): Promise<never> {
      throw new Error("Step MissingStep is not registered");
    },
    async invokeRPC(): Promise<InvokeWorkerRPCResponse> {
      throw new Error("RPC MissingRPC is not registered");
    },
  } as unknown as WorkerDispatcher;
  const worker = harness.workerWithDispatcher(dispatcher);
  try {
    worker.start();
    const legacy = await harness.nextStream();
    legacy.call.write(registered(false));
    legacy.call.write(unknownStepTask("legacy-step"));
    const legacyStep = await legacy.frame("taskFrame");
    legacy.call.write(unknownRPCTask("legacy-rpc"));
    const legacyRPC = await legacy.frame("taskFrame", 2);
    for (const frame of [legacyStep, legacyRPC]) {
      assert.equal(frame.frame?.$case, "taskFrame");
      if (frame.frame?.$case === "taskFrame") {
        assert.equal(frame.frame.value.output?.$case, "workerError");
        if (frame.frame.value.output?.$case === "workerError") {
          assert.match(frame.frame.value.output.value.detail, /not registered/);
        }
      }
    }
    assert.equal(legacy.frames.some((frame) => frame.frame?.$case === "reject"), false);
    legacy.call.write({ frame: { $case: "ping", value: {} } });
    await legacy.frame("pong");

    legacy.call.write({ frame: { $case: "reconnect", value: { reason: "test" } } });
    const capable = await harness.nextStream();
    capable.call.write(registered(true));
    capable.call.write(unknownStepTask("capable-step"));
    capable.call.write(unknownRPCTask("capable-rpc"));
    const stepRejection = await capable.frame("reject");
    const rpcRejection = await capable.frame("reject", 2);
    assert.equal(stepRejection.frame?.$case, "reject");
    assert.equal(rpcRejection.frame?.$case, "reject");
    if (stepRejection.frame?.$case === "reject") {
      assert.equal(stepRejection.frame.value.reason, PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP);
    }
    if (rpcRejection.frame?.$case === "reject") {
      assert.equal(rpcRejection.frame.value.reason, PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC);
    }

    capable.call.write({ frame: { $case: "reconnect", value: { reason: "test" } } });
    const reconnected = await harness.nextStream();
    reconnected.call.write(registered(false));
    reconnected.call.write(unknownStepTask("reconnected-step"));
    const reconnectedError = await reconnected.frame("taskFrame");
    assert.equal(reconnectedError.frame?.$case, "taskFrame");
    if (reconnectedError.frame?.$case === "taskFrame") {
      assert.equal(reconnectedError.frame.value.output?.$case, "workerError");
      if (reconnectedError.frame.value.output?.$case === "workerError") {
        assert.match(reconnectedError.frame.value.output.value.detail, /not registered/);
      }
    }
    assert.equal(reconnected.frames.some((frame) => frame.frame?.$case === "reject"), false);
  } finally {
    await worker.close();
    await harness.close();
  }
});

test("Reconnect backs off idle streams and drains a busy stream", async () => {
  const harness = await startHarness();
  let finishTask!: () => void;
  const gate = new Promise<void>((resolve) => { finishTask = resolve; });
  const worker = harness.worker(async () => {
    await gate;
    return InvokeWorkerRPCResponse.create({});
  });
  try {
    worker.start();
    const idle = await harness.nextStream();
    idle.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
    const reconnectRequestedAt = Date.now();
    idle.call.write({ frame: { $case: "reconnect", value: { reason: "move" } } });
    const replacement = await harness.nextStream();
    assert.ok(replacement.openedAt - reconnectRequestedAt >= 95);
    replacement.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
    replacement.call.write(rpcTask("busy"));
    replacement.call.write({ frame: { $case: "reconnect", value: { reason: "move" } } });
    replacement.call.write({ frame: { $case: "ping", value: {} } });
    await replacement.frame("pong");
    finishTask();
    await replacement.frame("taskFrame");
    await replacement.ended;
    const reconnected = await harness.nextStream();
    reconnected.call.write(registered(true));
    assert.equal(harness.openedStreamCount, 3);
    reconnected.call.write(rpcTask("after-reconnect"));
    await reconnected.frame("taskFrame");
  } finally {
    finishTask();
    await worker.close();
    await harness.close();
  }
});

test("busy stream sends keepalive Pong without changing RPC result", async () => {
  const harness = await startHarness();
  let finishTask!: () => void;
  const gate = new Promise<void>((resolve) => { finishTask = resolve; });
  const worker = harness.worker(async () => {
    await gate;
    return InvokeWorkerRPCResponse.create({});
  });
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write(registered(true));
    stream.call.write(rpcTask("busy-ping-rpc"));
    await until(() => harness.invocationCount === 1);
    stream.call.write({ frame: { $case: "ping", value: {} } });
    await stream.frame("pong");
    await stream.frame("pong", 2, 25_000);
    finishTask();
    const result = await stream.frame("taskFrame");
    assert.equal(result.frame?.$case, "taskFrame");
    if (result.frame?.$case === "taskFrame") {
      assert.equal(result.frame.value.taskId, "busy-ping-rpc");
      assert.equal(result.frame.value.output?.$case, "rpc");
    }
  } finally {
    finishTask();
    await worker.close();
    await harness.close();
  }
});

test("reopened stream answers Ping after a task ends on a closed stream", async () => {
  const harness = await startHarness();
  let finishTask!: () => void;
  const gate = new Promise<void>((resolve) => { finishTask = resolve; });
  let hasCanceled = false;
  const worker = harness.worker(async (_request, signal) => {
    signal.addEventListener("abort", () => { hasCanceled = true; });
    await gate;
    return InvokeWorkerRPCResponse.create({});
  });
  try {
    worker.start();
    const first = await harness.nextStream();
    first.call.write(registered(false));
    first.call.write(rpcTask("closed-busy-rpc"));
    await until(() => harness.invocationCount === 1);
    first.call.emit("error", { code: status.UNAVAILABLE, details: "proxy reset" });
    await until(() => worker.registration.snapshot().queues.every((queue) => !queue.hasRegisteredStream));
    await until(() => hasCanceled);
    finishTask();
    const reopened = await harness.nextStream();
    reopened.call.write(registered(false));
    reopened.call.write({ frame: { $case: "ping", value: {} } });
    await reopened.frame("pong");
    assert.equal(first.frames.some((request) => request.frame?.$case === "taskFrame"), false);
    assert.equal(reopened.frames.some((request) => request.frame?.$case === "taskFrame"), false);
  } finally {
    finishTask();
    await worker.close();
    await harness.close();
  }
});

test("CancelTask and task deadline abort the handler", async () => {
  const harness = await startHarness();
  const cancellations: string[] = [];
  const worker = harness.worker(async (_request, signal) => new Promise<InvokeWorkerRPCResponse>((_resolve, reject) => {
    const onAbort = () => {
      cancellations.push("aborted");
      reject(Object.assign(new Error("cancelled"), { name: "AbortError" }));
    };
    if (signal.aborted) onAbort();
    else signal.addEventListener("abort", onAbort, { once: true });
  }));
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
    stream.call.write(rpcTask("cancel"));
    stream.call.write({ frame: { $case: "cancelTask", value: { taskId: "cancel" } } });
    const cancelled = await stream.frame("taskFrame");
    assert.equal(cancelled.frame?.$case, "taskFrame");
    if (cancelled.frame?.$case === "taskFrame") {
      assert.equal(cancelled.frame.value.output?.$case, "workerError");
      if (cancelled.frame.value.output?.$case === "workerError") {
        assert.equal(cancelled.frame.value.output.value.grpcStatus, status.CANCELLED);
      }
    }
    stream.call.write(rpcTask("deadline", new Date(Date.now() + 20)));
    const timedOut = await stream.frame("taskFrame", 2);
    assert.equal(timedOut.frame?.$case, "taskFrame");
    if (timedOut.frame?.$case === "taskFrame") {
      assert.equal(timedOut.frame.value.output?.$case, "workerError");
      if (timedOut.frame.value.output?.$case === "workerError") {
        assert.equal(timedOut.frame.value.output.value.grpcStatus, status.DEADLINE_EXCEEDED);
      }
    }
    stream.call.write(rpcTask("expired", new Date(Date.now() - 1_000)));
    await stream.frame("taskFrame", 3);
    assert.equal(cancellations.length, 3);
  } finally {
    await worker.close();
    await harness.close();
  }
});

test("pull WaitFor sends heartbeat and stream write before its final result", async () => {
  const harness = await startHarness();
  const dispatcher = {
    async invokeWaitFor(_request: InvokeWaitForMethodRequest, _signal: AbortSignal, output: {
      recordHeartbeat(value: undefined): Promise<void>;
      writeStream(write: { streamName: string; streamCapacityBytes: bigint; value: undefined }): void;
    }) {
      await output.recordHeartbeat(undefined);
      output.writeStream({ streamName: "progress", streamCapacityBytes: 1024n, value: undefined });
      return InvokeWaitForMethodResponse.create({});
    },
  } as WorkerDispatcher;
  const worker = harness.workerWithDispatcher(dispatcher);
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
    stream.call.write({ frame: { $case: "task", value: {
      taskId: "wait-for", deadline: undefined,
      request: { $case: "waitFor", value: InvokeWaitForMethodRequest.create({ flowType: "TestFlow", stepType: "TestStep" }) },
    } } });
    await stream.frame("taskFrame", 3);
    const outputs = stream.frames.filter((candidate) => candidate.frame?.$case === "taskFrame")
      .map((candidate) => candidate.frame?.$case === "taskFrame" ? candidate.frame.value.output : undefined);
    assert.deepEqual(outputs.map((output) => output?.$case), ["waitFor", "waitFor", "waitFor"]);
    assert.deepEqual(outputs.map((output) => output?.$case === "waitFor" ? output.value.output?.$case : undefined),
      ["heartbeat", "streamWrite", "result"]);
  } finally {
    await worker.close();
    await harness.close();
  }
});

for (const method of ["waitFor", "execute"] as const) {
  test(`pull ${method} rejects late progress after cancellation, completion, and stream reassignment`, async () => {
    const harness = await startHarness();
    class RetainedOutputDispatcher {
      public readonly outputs: StepOutputEmitter[] = [];
      public readonly signals: AbortSignal[] = [];
      public release!: () => void;
      private gate = this.resetGate();

      public resetGate(): Promise<void> {
        this.gate = new Promise<void>((resolve) => { this.release = resolve; });
        return this.gate;
      }

      public async invokeWaitFor(_request: unknown, signal: AbortSignal, output: StepOutputEmitter) {
        this.outputs.push(output);
        this.signals.push(signal);
        await this.gate;
        return {};
      }

      public async invokeExecute(request: unknown, signal: AbortSignal, output: StepOutputEmitter) {
        return this.invokeWaitFor(request, signal, output);
      }
    }
    const dispatcher = new RetainedOutputDispatcher();
    const worker = harness.workerWithDispatcher(dispatcher as unknown as WorkerDispatcher);
    const task = (taskId: string): PollWorkerTaskResponse => ({ frame: { $case: "task", value: {
      taskId, deadline: undefined,
      request: method === "waitFor"
        ? { $case: "waitFor", value: InvokeWaitForMethodRequest.create({ flowType: "TestFlow", stepType: "TestStep" }) }
        : { $case: "execute", value: InvokeExecuteMethodRequest.create({ flowType: "TestFlow", stepType: "TestStep" }) },
    } } });
    const assertInactive = async (output: StepOutputEmitter) => {
      assert.throws(() => output.writeStream({ streamName: "progress", streamCapacityBytes: 1024n, value: undefined }),
        /no longer active/);
      await assert.rejects(output.recordHeartbeat(undefined), /no longer active/);
    };
    try {
      worker.start();
      const stream = await harness.nextStream();
      stream.call.write(registered(false));
      stream.call.write(task("cancelled"));
      await until(() => dispatcher.outputs.length === 1);
      stream.call.write({ frame: { $case: "cancelTask", value: { taskId: "cancelled" } } });
      await until(() => dispatcher.signals[0]!.aborted);
      await assertInactive(dispatcher.outputs[0]!);
      dispatcher.release();
      await stream.frame("taskFrame");
      await until(() => Reflect.get(worker, "busyTaskCount") === 0);
      await assertInactive(dispatcher.outputs[0]!);

      dispatcher.resetGate();
      stream.call.write(task("reassigned"));
      await until(() => dispatcher.outputs.length === 2);
      await assertInactive(dispatcher.outputs[0]!);
      await dispatcher.outputs[1]!.recordHeartbeat(undefined);
      await stream.frame("taskFrame", 2);
      dispatcher.release();
      await stream.frame("taskFrame", 3);
      await until(() => Reflect.get(worker, "busyTaskCount") === 0);
      await assertInactive(dispatcher.outputs[1]!);
      stream.call.write({ frame: { $case: "ping", value: {} } });
      await stream.frame("pong");
      assert.deepEqual(stream.frames.flatMap((request) => request.frame?.$case === "taskFrame"
        ? [request.frame.value.taskId] : []), ["cancelled", "reassigned", "reassigned"]);
    } finally {
      dispatcher.release();
      await worker.close();
      await harness.close();
    }
  });
}

test("pull Execute streams ordered progress, exactly one result, and failure after progress", async () => {
  class StreamingPullStep implements Step<string> {
    public readonly inputCodec = stringCodec;
    public readonly progress = new Stream("progress", stringCodec, 1024);
    public completedContext: AsyncContext | undefined;

    public getStepType(): string { return "StreamingPullStep"; }

    public async execute(context: AsyncContext, input: string): Promise<StepDecision> {
      this.completedContext = context;
      await context.recordHeartbeat({ input });
      if (input === "failure") throw new Error("handler failed after progress");
      this.progress.write(context, "execute-1");
      await context.recordHeartbeat(undefined);
      this.progress.write(context, "execute-2");
      return gracefulComplete(input);
    }
  }
  const step = new StreamingPullStep();
  const flow: Flow<string> = {
    getFlowType: () => "StreamingPullFlow",
    getSteps: () => StepList.startStep(step),
    getPersistenceSchema: () => ({ streams: [step.progress] }),
  };
  const hydrator = { hydrateAll: async (_flowId: string, values: readonly unknown[]) => values } as ValueHydrator;
  const dispatcher = new WorkerDispatcher(new Registry([flow]), hydrator, () => 3);
  const harness = await startHarness();
  const worker = harness.workerWithDispatcher(dispatcher);
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write(registered(false));
    for (const input of ["success", "failure"]) {
      stream.call.write({ frame: { $case: "task", value: {
        taskId: input, deadline: undefined,
        request: { $case: "execute", value: InvokeExecuteMethodRequest.create({
          context: ProtoContext.create({ flowId: `stream-${input}`, stepExecutionId: "step-1" }),
          flowType: flow.getFlowType(), stepType: step.getStepType(), stepInput: encodeValue(stringCodec, input),
        }) },
      } } });
      await stream.frame("taskFrame", input === "success" ? 5 : 7);
      await until(() => Reflect.get(worker, "busyTaskCount") === 0);
      assert.throws(() => step.progress.write(step.completedContext!, "late"), /no longer active/);
      await assert.rejects(step.completedContext!.recordHeartbeat("late"), /no longer active/);
    }
    stream.call.write({ frame: { $case: "ping", value: {} } });
    await stream.frame("pong");
    const frames = stream.frames.flatMap((request) => request.frame?.$case === "taskFrame" ? [request.frame.value] : []);
    assert.deepEqual(frames.map((frame) => frame.taskId), ["success", "success", "success", "success", "success", "failure", "failure"]);
    assert.deepEqual(frames.map((frame) => frame.output?.$case === "execute"
      ? frame.output.value.output?.$case : frame.output?.$case),
    ["heartbeat", "streamWrite", "heartbeat", "streamWrite", "result", "heartbeat", "workerError"]);
    assert.equal(frames[2]?.output?.$case === "execute" && frames[2].output.value.output?.$case === "heartbeat"
      && frames[2].output.value.output.value.value, undefined);
    assert.equal(frames[6]?.output?.$case === "workerError" && frames[6].output.value.grpcStatus, status.UNKNOWN);
  } finally {
    await worker.close();
    await harness.close();
  }
});

for (const shouldReconnect of [false, true]) {
  test(`close preserves a large final result after ${shouldReconnect ? "busy Reconnect" : "normal completion"}`, async () => {
    const harness = await startHarness();
    let finishTask!: () => void;
    const gate = new Promise<void>((resolve) => { finishTask = resolve; });
    const payload = "x".repeat(3_000_000);
    const worker = harness.worker(async () => {
      await gate;
      return InvokeWorkerRPCResponse.create({ output: Value.create({
        kind: { $case: "stringValue", value: payload },
      }) });
    }, { stopDrainTimeoutMs: 1_000 });
    try {
      worker.start();
      const stream = await harness.nextStream();
      stream.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
      stream.call.write(rpcTask("large-result"));
      await until(() => harness.invocationCount === 1);
      if (shouldReconnect) {
        stream.call.write({ frame: { $case: "reconnect", value: { reason: "move" } } });
        stream.call.write({ frame: { $case: "ping", value: {} } });
        await stream.frame("pong");
      }
      finishTask();
      await new Promise<void>((resolve) => setImmediate(resolve));
      const closing = worker.close();
      const result = await stream.frame("taskFrame");
      assert.equal(result.frame?.$case, "taskFrame");
      if (result.frame?.$case === "taskFrame") {
        assert.equal(result.frame.value.output?.$case, "rpc");
        if (result.frame.value.output?.$case === "rpc") {
          assert.equal(result.frame.value.output.value.output?.kind?.$case, "stringValue");
          if (result.frame.value.output.value.output?.kind?.$case === "stringValue") {
            assert.equal(result.frame.value.output.value.output.kind.value.length, payload.length);
          }
        }
      }
      await closing;
    } finally {
      finishTask();
      await worker.close();
      await harness.close();
    }
  });
}

for (const shutdown of ["Stop drain", "busy Reconnect", "busy Reconnect then Stop"] as const) {
  test(`large final result survives a delayed gRPC read during ${shutdown}`, async () => {
    const harness = await startHarness();
    let stream: CapturedStream | undefined;
    let finishTask!: () => void;
    const taskGate = new Promise<void>((resolve) => { finishTask = resolve; });
    const payload = "x".repeat(3_000_000);
    const worker = harness.worker(async () => {
      await taskGate;
      return InvokeWorkerRPCResponse.create({ output: Value.create({
        kind: { $case: "stringValue", value: payload },
      }) });
    }, { stopDrainTimeoutMs: shutdown === "busy Reconnect then Stop" ? 4_000 : 10_000 });
    try {
      worker.start();
      const capturedStream = await harness.nextStream();
      stream = capturedStream;
      capturedStream.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
      capturedStream.call.write(rpcTask("delayed-result"));
      await until(() => harness.invocationCount === 1);
      if (shutdown !== "Stop drain") {
        capturedStream.call.write({ frame: { $case: "reconnect", value: { reason: "move" } } });
        capturedStream.call.write({ frame: { $case: "ping", value: {} } });
        await capturedStream.frame("pong");
      }
      capturedStream.call.pause();
      finishTask();
      await new Promise<void>((resolve) => setImmediate(resolve));
      const closing = shutdown === "Stop drain" ? worker.close() : undefined;
      const delayedRead = new Promise<void>((resolve) => setTimeout(() => {
        capturedStream.call.resume();
        resolve();
      }, 6_100));
      const stopDuringReconnect = shutdown === "busy Reconnect then Stop"
        ? new Promise<void>((resolve) => setTimeout(resolve, 3_000)).then(() => worker.close())
        : undefined;
      await delayedRead;
      const result = await capturedStream.frame("taskFrame", 1, 10_000);
      assert.equal(result.frame?.$case, "taskFrame");
      if (result.frame?.$case === "taskFrame") {
        assert.equal(result.frame.value.output?.$case, "rpc");
        if (result.frame.value.output?.$case === "rpc") {
          assert.equal(result.frame.value.output.value.output?.kind?.$case, "stringValue");
          if (result.frame.value.output.value.output?.kind?.$case === "stringValue") {
            assert.equal(result.frame.value.output.value.output.kind.value.length, payload.length);
          }
        }
      }
      await closing;
      await stopDuringReconnect;
    } finally {
      stream?.call.resume();
      finishTask();
      await worker.close();
      await harness.close();
    }
  });
}

test("Worker error frame preserves status, type, stack, and retry delay", () => {
  const failure = Object.assign(new Error("failed"), { code: status.INVALID_ARGUMENT });
  const pull = workerErrorResponse(failure);
  assert.equal(pull.grpcStatus, status.UNKNOWN);
  assert.equal(pull.detail, "failed");
  assert.equal(pull.errorType, "Error");
  assert.match(pull.stackTrace, /failed/);
  const delayed = workerErrorResponse(retryAfter(2, failure));
  assert.equal(delayed.grpcStatus, status.UNKNOWN);
  assert.equal(delayed.retryAfterSeconds, 2);
});

test("close drains a task, then resets its stream and waits for a handler that ignores cancellation", async (context) => {
  const harness = await startHarness();
  let finishTask!: () => void;
  const gate = new Promise<void>((resolve) => { finishTask = resolve; });
  let hasAborted = false;
  const worker = harness.worker(async (_request, signal) => {
    signal.addEventListener("abort", () => { hasAborted = true; });
    await gate;
    return InvokeWorkerRPCResponse.create({});
  }, { stopDrainTimeoutMs: 10 });
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write({ frame: { $case: "registered", value: { supportsUnknownNameRejection: false, maxConcurrentTasksPerAppWorker: 0 } } });
    stream.call.write(rpcTask("blocked"));
    await until(() => harness.invocationCount === 1);
    const poll = [...(Reflect.get(worker, "streams") as Set<{ stream: { end: () => void } }>)][0]!;
    const halfClose = context.mock.method(poll.stream, "end");
    let hasClosed = false;
    const closing = worker.close().then(() => { hasClosed = true; });
    await until(() => hasAborted);
    assert.equal(hasClosed, false);
    await until(() => stream.call.cancelled);
    assert.equal(halfClose.mock.callCount(), 0);
    assert.equal(stream.frames.some((request) => request.frame?.$case === "taskFrame"), false);
    finishTask();
    await closing;
    assert.equal(hasClosed, true);
    assert.equal(halfClose.mock.callCount(), 0);
  } finally {
    finishTask();
    await worker.close();
    await harness.close();
  }
});

test("Worker start rejects a concurrency limit below its total slot count without retrying", async () => {
  const harness = await startHarness();
  const worker = new Worker(new Registry([]), {} as BlobCache, {
    serverAddress: harness.address, pull: { maxConcurrentTasks: 3, reservedRpcTasks: 1 },
  });
  try {
    const starting = assert.rejects(worker.start(), /maxConcurrentTasks 3 exceeds Server matching.maxConcurrentTasksCap 2/);
    const stream = await harness.nextStream();
    stream.call.write({ frame: { $case: "registered", value: {
      supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 2,
    } } });
    await starting;
    const pullWorker = Reflect.get(worker, "pullWorker") as PullWorker;
    assert.equal((Reflect.get(pullWorker, "streams") as Set<unknown>).size, 0);
  } finally { await worker.close(); await harness.close(); }
});

test("closing during initial registration settles Worker startup without opening replacement streams", async () => {
  const harness = await startHarness();
  const worker = new Worker(new Registry([]), {} as BlobCache, {
    serverAddress: harness.address, pull: { maxConcurrentTasks: 1, reservedRpcTasks: 0 },
  });
  try {
    const starting = assert.rejects(worker.start(), /stopped before registration/);
    const stream = await harness.nextStream();
    await worker.close();
    await starting;
    await stream.ended;
    assert.equal(harness.openedStreamCount, 1);
  } finally { await worker.close(); await harness.close(); }
});

test("TA25 stop finishes busy output before half-close, half-closes idle, and drops late tasks", async () => {
  const harness = await startHarness(false);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const worker = harness.worker(async () => { await gate; return InvokeWorkerRPCResponse.create(); }, {
    maxConcurrentTasks: 2, reservedRpcTasks: 1, stopDrainTimeoutMs: 5_000,
  });
  let closing: Promise<void> | undefined;
  try {
    worker.start();
    const busy = await harness.nextStream();
    const idle = await harness.nextStream();
    busy.call.write(registered(true));
    idle.call.write(registered(true));
    busy.call.write(rpcTask("started-before-stop"));
    await until(() => harness.invocationCount === 1);
    closing = worker.close();
    await idle.ended;
    assert.equal(busy.call.readableEnded, false);
    assert.equal(busy.frames.some((request) => request.frame?.$case === "taskFrame"), false);
    idle.call.write(rpcTask("after-idle-half-close"));
    busy.call.write(rpcTask("after-stop-before-half-close"));
    busy.call.write({ frame: { $case: "ping", value: {} } });
    await busy.frame("pong");
    assert.equal(harness.invocationCount, 1);
    release();
    await busy.ended;
    const final = await busy.frame("taskFrame");
    assert.equal(final.frame?.$case === "taskFrame" && final.frame.value.taskId, "started-before-stop");
    busy.call.write(rpcTask("after-busy-half-close"));
    let hasClosed = false;
    void closing.then(() => { hasClosed = true; });
    await new Promise<void>((resolve) => setImmediate(resolve));
    assert.equal(hasClosed, false);
    idle.call.end();
    busy.call.end();
    await closing;
    assert.equal(harness.invocationCount, 1);
    assert.equal([busy, idle].some((stream) => stream.frames.some((request) => request.frame?.$case === "reject")), false);
  } finally { release(); await worker.close(); await closing; await harness.close(); }
});

test("TA25 an idle half-closed stream keeps reading beyond the handler drain timeout until Server EOF", async () => {
  const harness = await startHarness(false);
  const worker = harness.worker(async () => InvokeWorkerRPCResponse.create(), {
    maxConcurrentTasks: 1, reservedRpcTasks: 0, stopDrainTimeoutMs: 10,
  });
  let stream: CapturedStream | undefined;
  let closing: Promise<void> | undefined;
  try {
    worker.start();
    stream = await harness.nextStream();
    stream.call.write(registered(true));
    await worker.waitStarted();
    const stopStartedAt = Date.now();
    let hasClosed = false;
    closing = worker.close().then(() => { hasClosed = true; });
    await stream.ended;
    await until(() => Date.now() >= stopStartedAt + 30);
    assert.equal(stream.call.cancelled, false);
    assert.equal(hasClosed, false);
    stream.call.write(rpcTask("after-drain-deadline"));
    stream.call.end();
    await closing;
    assert.equal(harness.invocationCount, 0);
  } finally {
    stream?.call.end();
    await worker.close();
    await closing;
    await harness.close();
  }
});

interface CapturedStream {
  readonly call: TaskStream;
  readonly frames: PollWorkerTaskRequest[];
  readonly openedAt: number;
  readonly ended: Promise<void>;
  frame(kind: NonNullable<PollWorkerTaskRequest["frame"]>["$case"], occurrence?: number, timeoutMs?: number): Promise<PollWorkerTaskRequest>;
}

async function startHarness(shouldEndOnHalfClose = true) {
  const server = new Server();
  const streams: CapturedStream[] = [];
  let consumed = 0;
  let invocationCount = 0;
  server.addService(FlowServiceService, {
    getServerInfo(_call, callback) {
      callback(null, { serverVersion: "test", minimumSupportedProtocolVersion: 1, currentProtocolVersion: 4, serverEngine: 0 });
    },
    syncAttributeIndexes(_call, callback) { callback(null, {}); },
  } as Partial<FlowServiceServer> as FlowServiceServer);
  server.addService(WorkerTaskServiceService, {
    pollWorkerTask(call: TaskStream) {
      const frames: PollWorkerTaskRequest[] = [];
      let resolveEnd!: () => void;
      const ended = new Promise<void>((resolve) => { resolveEnd = resolve; });
      call.on("data", (frame: PollWorkerTaskRequest) => frames.push(frame));
      call.on("end", () => { resolveEnd(); if (shouldEndOnHalfClose) call.end(); });
      call.on("error", () => {});
      streams.push({
        call, frames, openedAt: Date.now(), ended,
        async frame(kind, occurrence = 1, timeoutMs = 5_000) {
          await until(() => frames.filter((candidate) => candidate.frame?.$case === kind).length >= occurrence, timeoutMs);
          return frames.filter((candidate) => candidate.frame?.$case === kind)[occurrence - 1]!;
        },
      });
    },
  } as WorkerTaskServiceServer);
  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, boundPort) => {
      if (error !== null) reject(error);
      else resolve(boundPort);
    });
  });
  return {
    address: `127.0.0.1:${port}`,
    get invocationCount() { return invocationCount; },
    get openedStreamCount() { return streams.length; },
    worker(invokeRPC: (request: InvokeWorkerRPCRequest, signal: AbortSignal) => Promise<InvokeWorkerRPCResponse>, options: PullWorkerOptions = {}, onPermanentRejection: () => void = () => {}) {
      const dispatcher = { invokeRPC: async (request: InvokeWorkerRPCRequest, signal: AbortSignal) => {
        invocationCount++;
        return invokeRPC(request, signal);
      } } as WorkerDispatcher;
      const worker = new PullWorker(new Registry([]), dispatcher, `127.0.0.1:${port}`, { maxConcurrentTasks: 1, reservedRpcTasks: 0, ...options }, undefined, onPermanentRejection);
      Reflect.set(worker, "registeredFlows", new Map([["TestFlow", {
        steps: [{ name: "TestStep" }], rpcs: [{ name: "getWorker" }],
      }]]));
      return worker;
    },
    workerWithDispatcher(dispatcher: WorkerDispatcher) {
      const worker = new PullWorker(new Registry([]), dispatcher, `127.0.0.1:${port}`, { maxConcurrentTasks: 1, reservedRpcTasks: 0 });
      Reflect.set(worker, "registeredFlows", new Map([["TestFlow", {
        steps: [{ name: "TestStep" }], rpcs: [{ name: "getWorker" }],
      }]]));
      return worker;
    },
    async nextStream(): Promise<CapturedStream> {
      await until(() => streams.length > consumed);
      return streams[consumed++]!;
    },
    async close(): Promise<void> {
      for (const stream of streams) {
        assert.equal(stream.frames.some((request) => request.frame?.$case === "reject"
          && request.frame.value.reason === PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_NO_CAPACITY), false);
      }
      await new Promise<void>((resolve, reject) => server.tryShutdown((error) => error === undefined ? resolve() : reject(error)));
    },
  };
}

function rpcTask(taskId: string, deadline?: Date): PollWorkerTaskResponse {
  return { frame: { $case: "task", value: {
    taskId,
    deadline,
    request: { $case: "rpc", value: InvokeWorkerRPCRequest.create({ flowType: "TestFlow", rpcName: "getWorker" }) },
  } } };
}

function registered(supportsUnknownNameRejection: boolean): PollWorkerTaskResponse {
  return { frame: { $case: "registered", value: { supportsUnknownNameRejection, maxConcurrentTasksPerAppWorker: 0 } } };
}

function unknownStepTask(taskId: string): PollWorkerTaskResponse {
  return { frame: { $case: "task", value: {
    taskId,
    deadline: undefined,
    request: { $case: "waitFor", value: InvokeWaitForMethodRequest.create({ flowType: "TestFlow", stepType: "MissingStep" }) },
  } } };
}

function unknownRPCTask(taskId: string): PollWorkerTaskResponse {
  return { frame: { $case: "task", value: {
    taskId,
    deadline: undefined,
    request: { $case: "rpc", value: InvokeWorkerRPCRequest.create({ flowType: "TestFlow", rpcName: "MissingRPC" }) },
  } } };
}

async function until(condition: () => boolean, timeoutMs = 5_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() >= deadline) throw new Error("timed out waiting for poll stream event");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

function restoreEnvironment(name: string, value: string | undefined): void {
  if (value === undefined) delete process.env[name];
  else process.env[name] = value;
}


test("registration waits for both slot kinds and survives reconnects and Stop", async () => {
  const harness = await startHarness();
  const worker = new Worker(new Registry([new TestFlow()]), {} as BlobCache, {
    serverAddress: harness.address, pull: { maxConcurrentTasks: 2 },
  });
  try {
    const waiting = worker.waitPullRegistered({ timeoutMs: 5_000 });
    const starting = worker.start();
    const first = await harness.nextStream();
    const second = await harness.nextStream();
    first.call.write(registered(false));
    await starting;
    await until(() => worker.pullRegistrationState().queues.some((queue) => queue.hasRegisteredStream));
    assert.equal(worker.pullRegistrationState().isReady, false);
    second.call.write(registered(false));
    await waiting;
    const snapshot = worker.pullRegistrationState();
    assert.equal(snapshot.isReady, true);
    first.call.emit("error", { code: status.UNAVAILABLE, details: "transient" });
    await until(() => worker.pullRegistrationState().queues.some((queue) => !queue.hasRegisteredStream));
    assert.equal(worker.pullRegistrationState().isReady, true);
    assert.equal(snapshot.queues.every((queue) => queue.hasRegisteredStream), true);
    await worker.close();
    await worker.waitPullRegistered();
    assert.equal(worker.pullRegistrationState().isReady, true);
  } finally { await worker.close(); await harness.close(); }
});

for (const code of [status.INVALID_ARGUMENT, status.RESOURCE_EXHAUSTED, status.UNIMPLEMENTED, status.PERMISSION_DENIED, status.UNAUTHENTICATED]) {
  test(`registration rejects promptly for permanent status ${code}`, async () => {
    const harness = await startHarness();
    const worker = harness.worker(async () => InvokeWorkerRPCResponse.create());
    try {
      const waiting = assert.rejects(worker.registration.wait({ timeoutMs: 5_000 }), (failure: unknown) =>
        failure instanceof PullRegistrationError && failure.queue === "STEP_AND_RPC");
      worker.start();
      const stream = await harness.nextStream();
      stream.call.emit("error", { code, details: "rejected" });
      await waiting;
      assert.equal(worker.registration.snapshot().isReady, false);
      assert.equal(worker.registration.snapshot().queues[0]?.lastPermanentError?.code, code);
    } finally { await worker.close(); await harness.close(); }
  });
}

test("permanent rejection preserves readiness while another stream holds the queue", async () => {
  const harness = await startHarness();
  let rejectionCount = 0;
  const worker = harness.worker(async () => InvokeWorkerRPCResponse.create(), {
    maxConcurrentTasks: 2, reservedRpcTasks: 0, idleStreamsPerQueue: 2, stopOnPermanentStreamRejection: true,
  }, () => { rejectionCount++; });
  try {
    worker.start();
    const first = await harness.nextStream();
    const second = await harness.nextStream();
    first.call.write(registered(false));
    await worker.registration.wait({ timeoutMs: 5_000 });
    second.call.emit("error", { code: status.PERMISSION_DENIED, details: "rejected" });
    await until(() => worker.registration.snapshot().queues[0]?.lastPermanentError !== undefined);
    assert.equal(worker.registration.snapshot().isReady, true);
    assert.equal(worker.registration.permanentError, undefined);
    assert.equal(rejectionCount, 0);
    first.call.emit("error", { code: status.UNAVAILABLE, details: "disconnected" });
    await until(() => !worker.registration.snapshot().queues[0]?.hasRegisteredStream);
    assert.equal(worker.registration.snapshot().isReady, false);
    await worker.registration.wait();
    const replacement = await harness.nextStream();
    replacement.call.write(registered(false));
    await until(() => worker.registration.snapshot().isReady);
    assert.equal(worker.registration.snapshot().queues[0]?.lastPermanentError, undefined);
    assert.equal(rejectionCount, 0);
  } finally { await worker.close(); await harness.close(); }
});

for (const hasRegistered of [false, true]) {
  test(`opt-in rejection drains run and leaves close safe, registered=${hasRegistered}`, async () => {
    const harness = await startHarness();
    const worker = new Worker(new Registry([]), {} as BlobCache, {
      serverAddress: harness.address, pull: { maxConcurrentTasks: 1, reservedRpcTasks: 0, stopOnPermanentStreamRejection: true, stopDrainTimeoutMs: 100 },
    });
    try {
      const running = assert.rejects(worker.run(), PullRegistrationError);
      const stream = await harness.nextStream();
      if (hasRegistered) {
        stream.call.write(registered(false));
        await worker.waitPullRegistered({ timeoutMs: 5_000 });
      }
      stream.call.emit("error", { code: status.INVALID_ARGUMENT, details: "rejected" });
      await running;
      await worker.close();
      if (hasRegistered) await worker.waitPullRegistered();
      else await assert.rejects(worker.waitPullRegistered(), PullRegistrationError);
      assert.equal(worker.pullRegistrationState().isReady, false);
    } finally { await worker.close(); await harness.close(); }
  });
}

test("registration wait handles startup failure, cancellation, timeout, and default Workers", async () => {
  const worker = new Worker(new Registry([]), {} as BlobCache, {
    serverAddress: "127.0.0.1:1", attributeIndexSyncTimeoutMs: 100,
  });
  let registrationFailure: unknown;
  let registrationFailureMessage: string | undefined;
  const waiting = assert.rejects(worker.waitPullRegistered({ timeoutMs: 5_000 }), (failure: unknown) => {
    registrationFailure = failure;
    registrationFailureMessage = (failure as Error).message;
    return true;
  });
  await assert.rejects(worker.start(), (failure: unknown) =>
    failure === registrationFailure && (failure as Error).message === registrationFailureMessage &&
    registrationFailureMessage.includes("127.0.0.1:1"));
  await waiting;
  await worker.close();
  const pending = new Worker(new Registry([]), {} as BlobCache);
  const controller = new AbortController();
  const cancelled = assert.rejects(pending.waitPullRegistered({ signal: controller.signal }), { name: "AbortError" });
  controller.abort();
  await cancelled;
  await assert.rejects(pending.waitPullRegistered({ timeoutMs: 1 }), { name: "TimeoutError" });
  await assert.rejects(pending.waitPullRegistered({ timeoutMs: -1 }), RangeError);
  const stopped = assert.rejects(pending.waitPullRegistered(), /stopped before registration/);
  await pending.close();
  await stopped;
});

test("Context heartbeat rejects without synchronous throws after its task stream closes", async () => {
  class DisconnectedHeartbeatStep implements Step<string> {
    public readonly inputCodec = stringCodec;
    public context: AsyncContext | undefined;
    public release!: () => void;
    private readonly completion = new Promise<void>((resolve) => { this.release = resolve; });

    public getStepType(): string { return "DisconnectedHeartbeatStep"; }

    public async execute(context: AsyncContext, _input: string): Promise<StepDecision> {
      this.context = context;
      await this.completion;
      return gracefulComplete();
    }
  }
  const step = new DisconnectedHeartbeatStep();
  const flow: Flow<string> = {
    getFlowType: () => "DisconnectedHeartbeatFlow",
    getSteps: () => StepList.startStep(step),
  };
  const hydrator = { hydrateAll: async (_flowId: string, values: readonly unknown[]) => values } as ValueHydrator;
  const dispatcher = new WorkerDispatcher(new Registry([flow]), hydrator, () => 3);
  const harness = await startHarness();
  const worker = harness.workerWithDispatcher(dispatcher);
  try {
    worker.start();
    const stream = await harness.nextStream();
    stream.call.write(registered(false));
    stream.call.write({ frame: { $case: "task", value: {
      taskId: "disconnected-heartbeat", deadline: undefined,
      request: { $case: "execute", value: InvokeExecuteMethodRequest.create({
        context: ProtoContext.create({ flowId: "disconnected-flow", stepExecutionId: "step-1" }),
        flowType: flow.getFlowType(), stepType: step.getStepType(), stepInput: encodeValue(stringCodec, "input"),
      }) },
    } } });
    await until(() => step.context !== undefined);
    stream.call.emit("error", { code: status.UNAVAILABLE, details: "stream closed" });
    await until(() => step.context!.cancellationSignal.aborted);
    let heartbeat: Promise<void> | undefined;
    assert.doesNotThrow(() => { heartbeat = step.context!.recordHeartbeat("late"); });
    assert.ok(heartbeat instanceof Promise);
    await assert.rejects(heartbeat, Error);
  } finally {
    step.release();
    await worker.close();
    await harness.close();
  }
});
