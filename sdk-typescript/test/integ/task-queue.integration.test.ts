// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Client,
  Registry,
  StepList,
  SubFlow,
  Wait,
  Worker,
  gracefulComplete,
  openBlobCache,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";
import { flowId } from "./environment.js";

const uniqueSuffix = randomUUID().replaceAll("-", "");
const probeFlowType = `TaskQueueProbeFlow${uniqueSuffix}`;
const parentFlowType = `TaskQueueParentFlow${uniqueSuffix}`;

class ReportWorkerStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public constructor(private readonly workerLabel: string) {}
  public getStepType(): string { return "ReportWorkerStep"; }
  public execute(_context: Context, _input: string): StepDecision { return gracefulComplete(this.workerLabel); }
}

class TaskQueueProbeFlow implements Flow<string> {
  private readonly start: ReportWorkerStep;
  public constructor(private readonly workerLabel: string) { this.start = new ReportWorkerStep(workerLabel); }
  public getFlowType(): string { return probeFlowType; }
  public getSteps() { return StepList.startStep(this.start); }

  @rpc({ outputCodec: stringCodec })
  public getWorkerLabel(_context: Context): RPCResult<string> {
    return { output: this.workerLabel };
  }
}

// The parent's input is the SubFlow task queue; the empty string inherits the parent's.
class StartQueuedChildStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public constructor(private readonly child: TaskQueueProbeFlow) {}
  public getStepType(): string { return "StartQueuedChildStep"; }
  public waitFor(_context: Context, input: string): Wait {
    return Wait.until(SubFlow.run(this.child, "child", input === "" ? {} : { taskQueue: input }));
  }
  public execute(context: Context, _input: string): StepDecision {
    return gracefulComplete(SubFlow.getConditionResults(context).singleOutput(stringCodec));
  }
}

class TaskQueueParentFlow implements Flow<string> {
  private readonly start: StartQueuedChildStep;
  public constructor(child: TaskQueueProbeFlow) { this.start = new StartQueuedChildStep(child); }
  public getFlowType(): string { return parentFlowType; }
  public getSteps() { return StepList.startStep(this.start); }
}

test("named task queues route Flows, RPCs, and SubFlows only to their Workers", async () => {
  const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
  const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-task-queue-cache-"));
  const cache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1_024 * 1_024 });
  const queueA = `q-a-${randomUUID()}`;
  const queueB = `q-b-${randomUUID()}`;
  const newWorker = (label: string, taskQueue: string | undefined) => {
    const probe = new TaskQueueProbeFlow(label);
    return new Worker(new Registry([probe, new TaskQueueParentFlow(probe)]), cache, {
      serverAddress,
      pull: { appWorkerID: `ts-q-${label}-${flowId("w")}`, maxConcurrentTasks: 2, taskQueue: taskQueue ?? "" },
    });
  };
  const workerA = newWorker("A", queueA);
  const workerB = newWorker("B", queueB);
  const workerDefault = newWorker("default", undefined);
  const clientProbe = new TaskQueueProbeFlow("client");
  const clientParent = new TaskQueueParentFlow(clientProbe);
  const client = new Client(new Registry([clientProbe, clientParent]), cache, { serverAddress });
  const runProbe = async (taskQueue: string | undefined, beforeWait: () => Promise<void> = async () => {}) => {
    const id = flowId("task-queue-probe");
    await client.startFlow(clientProbe, id, "probe", taskQueue === undefined ? {} : { taskQueue });
    await beforeWait();
    return { id, label: await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)) };
  };
  const runParent = async (parentTaskQueue: string, childTaskQueue: string) => {
    const id = flowId("task-queue-parent");
    await client.startFlow(clientParent, id, childTaskQueue, { taskQueue: parentTaskQueue });
    return client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec));
  };
  try {
    await workerB.start();
    await workerB.waitPullRegistered({ timeoutMs: 30_000 });
    assert.deepEqual(workerB.pullRegistrationState().queues.map((entry) => entry.taskQueue), [queueB, queueB]);

    // Idle Worker B would take a mis-routed Flow before Worker A starts.
    const onA = await runProbe(queueA, async () => {
      await delay(500);
      await workerA.start();
    });
    assert.equal(onA.label, "A");
    assert.equal(await client.invokeRPC(clientProbe.getWorkerLabel, onA.id), "A");

    // Idle Workers A and B would take a mis-routed default-queue Flow before the default Worker starts.
    const onDefault = await runProbe(undefined, async () => {
      await delay(500);
      await workerDefault.start();
    });
    assert.equal(onDefault.label, "default");
    assert.equal((await runProbe("dex.default")).label, "default");
    assert.equal((await runProbe(queueB)).label, "B");

    assert.equal(await runParent(queueA, ""), "A");
    assert.equal(await runParent(queueA, queueB), "B");
    assert.equal(await runParent(queueA, "dex.default"), "default");
  } finally {
    await client.close();
    await workerA.close();
    await workerB.close();
    await workerDefault.close();
    cache.close();
    rmSync(cacheDirectory, { recursive: true, force: true });
  }
});

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
