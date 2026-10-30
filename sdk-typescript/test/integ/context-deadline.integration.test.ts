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
import test from "node:test";

import {
  StepList, Wait, type RPCResult, gracefulComplete, rpc, voidCodec,
  type Context, type Flow, type Step, type StepDecision, type StepOptions,
} from "../../src/index.js";
import { flowId, withEnvironment } from "./environment.js";

class DeadlineStep implements Step<void> {
  public readonly inputCodec = voidCodec;
  public readonly observations: { method: string; attempt: number; remainingMs: number }[] = [];

  public constructor(private readonly totalDurationMs?: number) {}

  public getStepType(): string { return "DeadlineStep"; }

  public getStepOptions(): StepOptions {
    const retry = { initialIntervalMs: 1_000, maximumAttempts: 2, ...(this.totalDurationMs === undefined ? {} : { totalDurationMs: this.totalDurationMs }) };
    return {
      waitForMethodTimeoutMs: 10_000, executeMethodTimeoutMs: 10_000,
      waitForRetry: retry, executeRetry: retry,
    };
  }

  public async waitFor(context: Context, _input: void): Promise<Wait> {
    this.observeDeadline("waitFor", context);
    return Wait.skipImmediately();
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    this.observeDeadline("execute", context);
    return gracefulComplete();
  }

  private observeDeadline(method: string, context: Context): void {
    const startedAt = Date.now();
    assert.ok(context.deadline instanceof Date);
    this.observations.push({ method, attempt: context.attempt, remainingMs: context.deadline.getTime() - startedAt });
    if (context.attempt === 1) throw new Error("retry to observe the next attempt deadline");
  }
}

class DeadlineFlow implements Flow<void> {
  public readonly start: DeadlineStep;
  public constructor(totalDurationMs?: number) { this.start = new DeadlineStep(totalDurationMs); }
  public getFlowType(): string { return "DeadlineFlow"; }
  public getSteps() { return StepList.startStep(this.start); }
}

class DeadlineRPCFlow implements Flow<void> {
  public getFlowType(): string { return "DeadlineRPCFlow"; }
  public getSteps() { return StepList.empty<void>(); }
  @rpc()
  public getDeadlinePresence(context: Context): RPCResult<boolean> { return { output: context.deadline !== undefined }; }
}

for (const totalDurationMs of [undefined, 6_000]) {
  test(`Step deadlines include retries and total duration ${totalDurationMs ?? "unset"}; RPC has no deadline`, async () => {
    const flow = new DeadlineFlow(totalDurationMs);
    const rpcFlow = new DeadlineRPCFlow();
    await withEnvironment([flow, rpcFlow], async ({ client }) => {
      const id = flowId("context-deadline");
      await client.startFlow(flow, id, undefined);
      const result = await client.waitForFlow(id, 30_000);
      assert.equal(result.status, "completed", result.errorMessage);
      assert.deepEqual(flow.start.observations.map(({ method, attempt }) => [method, attempt]),
        [["waitFor", 1], ["waitFor", 2], ["execute", 1], ["execute", 2]]);
      for (const { attempt, remainingMs } of flow.start.observations) {
        const expectedMs = totalDurationMs === undefined ? 10_000 : totalDurationMs - (attempt - 1) * 1_000;
        assert.ok(remainingMs >= expectedMs - 2_000 && remainingMs <= expectedMs + 500, `${remainingMs}ms remaining, expected ${expectedMs}ms`);
      }
      const rpcId = flowId("rpc-deadline");
      await client.startFlow(rpcFlow, rpcId, undefined);
      assert.equal(await client.invokeRPC(rpcFlow.getDeadlinePresence, rpcId), false);
    });
  });
}
