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
  FlowDefinitionError,
  Registry,
  StepExecutionId,
  WorkerInvocationError,
  jsonCodec,
  stringCodec,
  type Client,
} from "../../src/index.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import {
  JoinAllOfFlow,
  JoinCancelJoinFlow,
  JoinConditionValidationFlow,
  JoinDoubleForkFlow,
  JoinFirstWinFlow,
  JoinIsolationFlow,
  JoinNameConflictFlow,
  JoinPartialFlow,
  JoinRPCForkFlow,
  JoinTimerFlow,
  JoinZeroFlow,
  type ConditionValidationCase,
  type JoinOutcome,
  type PartialRequest,
  type PartialState,
  type TimerOutcome,
  type ZeroOutcome,
} from "./join_flow.js";

const flowResultTimeoutMs = 30_000;

async function waitForStep(client: Client, id: string, stepType: string, executionNumber = 1): Promise<void> {
  await client.waitForStepCompletion(id, StepExecutionId.of(stepType, executionNumber), {
    requestId: `${id}-${stepType}-${executionNumber}`,
    requestTimeoutMs: flowResultTimeoutMs,
  });
}

async function flowOutput<T>(client: Client, id: string): Promise<T> {
  const result = await client.waitForFlow(id, flowResultTimeoutMs);
  return result.singleOutput(jsonCodec<T>());
}

test("all-branches join counts results reported before it waits, in report order", async () => {
  const flow = new JoinAllOfFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-all-of");
    await client.startFlow(flow, id, ["A", "B"]);
    await client.invokeRPC(flow.releaseBranch, id, "B");
    await waitForStep(client, id, "JoinAllOfBranch", 2);
    await client.invokeRPC(flow.releaseBranch, id, "A");
    await waitForStep(client, id, "JoinAllOfBranch", 1);
    // The join Step starts only now, after both results were committed.
    await client.invokeRPC(flow.openJoinGate, id);
    assert.deepEqual(await flowOutput<JoinOutcome>(client, id), { branchCount: 2, results: ["B", "A"] });
  });
});

test("multiple-instance fork counts branches from the builder list", async () => {
  const flow = new JoinAllOfFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-multiple-instances");
    const reviewers = ["r1", "r2", "r3", "r4"];
    await client.startFlow(flow, id, reviewers);
    for (const reviewer of reviewers) {
      await client.invokeRPC(flow.releaseBranch, id, reviewer);
    }
    await client.invokeRPC(flow.openJoinGate, id);
    const outcome = await flowOutput<JoinOutcome>(client, id);
    assert.equal(outcome.branchCount, reviewers.length);
    assert.deepEqual([...outcome.results].sort(), reviewers);
  });
});

test("partial join resets before the next round and blocks its enablement", async () => {
  const flow = new JoinPartialFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-partial");
    const request: PartialRequest = {
      requiredResults: 2,
      roundNumber: 1,
      roundCount: 2,
      acceptedRounds: [],
      lateResults: [],
    };
    await client.startFlow(flow, id, request);
    await client.invokeRPC(flow.releaseBranch, id, "A");
    await waitForStep(client, id, "JoinPartialBranchA");
    await client.invokeRPC(flow.releaseBranch, id, "B");
    await waitForStep(client, id, "JoinPartialAccept");

    // Round 2's enablement for A arrives while round 1 still waits for C.
    await client.invokeRPC(flow.releaseBranch, id, "A");
    const blocked = await client.invokeRPC(flow.getPartialState, id) as PartialState;
    assert.deepEqual(blocked, { splitExecutions: 1, pendingReleasesOfA: 1 });

    await client.invokeRPC(flow.releaseBranch, id, "C");
    await waitForStep(client, id, "JoinPartialBranchA", 2);
    await client.invokeRPC(flow.releaseBranch, id, "B");
    const outcome = await flowOutput<PartialRequest>(client, id);
    assert.deepEqual(outcome.acceptedRounds, [["A", "B"], ["A", "B"]]);
    assert.deepEqual(outcome.lateResults, ["C"]);
  });
});

test("results reported for another round key do not satisfy the round", async () => {
  const flow = new JoinIsolationFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-isolation");
    await client.startFlow(flow, id, undefined);
    await client.invokeRPC(flow.reportForeignResult, id);
    assert.equal(await client.invokeRPC(flow.getPendingResultCount, id, "foreign-round"), 1);
    await client.invokeRPC(flow.releaseBranch, id, "own");
    assert.deepEqual(await flowOutput<readonly string[]>(client, id), ["own"]);
  });
});

test("first-branch join cancels sibling branches and leaves the late result pending", async () => {
  const flow = new JoinFirstWinFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-first-win");
    await client.startFlow(flow, id, undefined);
    await client.invokeRPC(flow.releaseBranch, id, "A");
    await waitForStep(client, id, "JoinFirstWinBranchA");
    await client.invokeRPC(flow.releaseBranch, id, "B");
    await waitForStep(client, id, "JoinFirstWinBranchB");
    await client.invokeRPC(flow.openJoinGate, id);
    await waitForStep(client, id, "JoinFirstWinAccept");
    // C's release finds no active branch, because the join canceled it.
    await client.invokeRPC(flow.releaseBranch, id, "C");
    const roundKey = "JoinFirstWinSplit-1";
    assert.equal(await client.invokeRPC(flow.getPendingResultCount, id, roundKey), 1);
    assert.deepEqual(
      await client.invokeRPCWithOptions(flow.getPendingResults, id, roundKey, {
        loadChannelMapInstances: [flow.pendingResultsLoad(roundKey)],
      }),
      ["B"],
    );
    // Graceful completion would wait forever if branch C were still active.
    await client.invokeRPC(flow.finish, id);
    assert.equal((await client.waitForFlow(id, flowResultTimeoutMs)).singleOutput(stringCodec), "A");
  });
});

test("cancel the join with its branches", async () => {
  const flow = new JoinCancelJoinFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-cancel-join");
    await client.startFlow(flow, id, undefined);
    await client.invokeRPC(flow.releaseBranch, id, "A");
    await waitForStep(client, id, "JoinCancelJoinBranchA");
    await client.invokeRPC(flow.cancelJoinWithBranches, id);
    // Branch B was canceled, so its release stays pending and nothing consumes A's result.
    await client.invokeRPC(flow.releaseBranch, id, "B");
    assert.equal(await client.invokeRPC(flow.getPendingReleaseCount, id, "B"), 1);
    assert.equal(await client.invokeRPC(flow.getPendingResultCount, id, "JoinCancelJoinSplit-1"), 1);
    await client.invokeRPC(flow.openGate, id);
    assert.deepEqual(await flowOutput<readonly string[]>(client, id), ["join canceled"]);
  });
});

test("forking one Join twice in one attempt fails the Step", async () => {
  const flow = new JoinDoubleForkFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-double-fork");
    await client.startFlow(flow, id, undefined);
    const detail = (await client.waitForFlow(id, flowResultTimeoutMs)).singleOutput(stringCodec);
    assert.match(detail, /Join joinDoubleForkResult was already forked by Step execution JoinDoubleForkSplit-1/);
  });
});

test("an RPC forks only with an explicit slash-free round key", async () => {
  const flow = new JoinRPCForkFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-rpc-fork");
    await client.startFlow(flow, id, undefined);
    const forkFailure = await expectError(client.invokeRPC(flow.startRound, id, "fork"), WorkerInvocationError);
    assert.match(forkFailure.workerErrorDetail, /use forkWithRoundKey in an RPC/);
    const slashFailure = await expectError(client.invokeRPC(flow.startRound, id, "slash"), WorkerInvocationError);
    assert.match(slashFailure.workerErrorDetail, /must not contain '\/'/);
    await client.invokeRPC(flow.startRound, id, "key");
    await client.invokeRPC(flow.reportHandoffResult, id, {
      round: { roundKey: "rpc-round-1", branchCount: 2 },
      result: "handoff-result",
    });
    assert.deepEqual(
      await flowOutput<{ readonly roundKey: string; readonly results: readonly string[] }>(client, id),
      { roundKey: "rpc-round-1", results: ["A", "handoff-result"] },
    );
  });
});

const invalidConditionCases: readonly ConditionValidationCase[] = [
  "forBranchesZero",
  "forBranchesAboveCount",
  "forRemainingNegative",
  "forRemainingAboveCount",
  "forFirstBranchWithoutBranches",
];

test("out-of-range join counts fail waitFor", async () => {
  const flow = new JoinConditionValidationFlow();
  await withEnvironment([flow], async ({ client }) => {
    const validId = flowId("typescript-join-validation-valid");
    await client.startFlow(flow, validId, "forAllBranches");
    assert.equal((await client.waitForFlow(validId, flowResultTimeoutMs)).singleOutput(stringCodec), "ready");
    for (const validationCase of invalidConditionCases) {
      const id = flowId(`typescript-join-validation-${validationCase}`);
      await client.startFlow(flow, id, validationCase);
      assert.equal(
        (await client.waitForFlow(id, flowResultTimeoutMs)).singleOutput(stringCodec),
        "waitForFailed",
        validationCase,
      );
    }
  });
});

test("zero-count join Conditions are ready immediately", async () => {
  const flow = new JoinZeroFlow();
  await withEnvironment([flow], async ({ client }) => {
    const emptyId = flowId("typescript-join-zero-branches");
    await client.startFlow(flow, emptyId, 0);
    assert.deepEqual(await flowOutput<ZeroOutcome>(client, emptyId), {
      branchCount: 0,
      acceptedCount: 0,
      remainingCount: 0,
    });
    const fullId = flowId("typescript-join-nothing-remaining");
    await client.startFlow(flow, fullId, 2);
    assert.deepEqual(await flowOutput<ZeroOutcome>(client, fullId), {
      branchCount: 2,
      acceptedCount: 2,
      remainingCount: 0,
    });
  });
});

test("a Join and a ChannelMap with the same name fail registration", () => {
  assert.throws(() => new Registry([new JoinNameConflictFlow()]), FlowDefinitionError);
});

test("a Timer ends an all-branches join when a branch never reports", async () => {
  const flow = new JoinTimerFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("typescript-join-timer");
    await client.startFlow(flow, id, undefined);
    assert.deepEqual(await flowOutput<TimerOutcome>(client, id), { hasTimerFired: true, results: [] });
  });
});
