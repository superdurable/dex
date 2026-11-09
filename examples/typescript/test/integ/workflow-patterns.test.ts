/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Each test asserts the defining behavior of one Workflow Pattern (WCP).

import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as sleep } from "node:timers/promises";

import {
  StepExecutionId,
  ValueMappingError,
  doubleCodec,
  stringCodec,
  RpcLockConflictError,
  type Attribute,
  type Codec,
  type Flow,
  type FlowResult,
  type StepCompletion,
} from "@superdurable/dex";

import {
  generalizedAndJoinFlow,
  threadSplitMergeFlow,
} from "../../src/patterns/workflow-patterns/and-join-and-threads.js";
import {
  exclusiveChoiceSimpleMergeFlow,
  parallelSplitSynchronizationFlow,
  sequenceFlow,
} from "../../src/patterns/workflow-patterns/basic-control-flow.js";
import {
  cancelCaseFlow,
  cancelMultipleInstanceTaskFlow,
  cancelRegionFlow,
  cancelTaskFlow,
  completeMultipleInstanceTaskFlow,
  finishedTranslationCount,
  stoppedChildFlowIds,
} from "../../src/patterns/workflow-patterns/cancellation.js";
import {
  arbitraryCyclesFlow,
  factorialRecursionFlow,
  structuredLoopFlow,
} from "../../src/patterns/workflow-patterns/iteration.js";
import {
  generalSynchronizingMergeFlow,
  multiChoiceSynchronizingMergeFlow,
  multiMergeFlow,
} from "../../src/patterns/workflow-patterns/multi-choice-and-merges.js";
import {
  cancellingPartialJoinForMultipleInstancesFlow,
  designTimeMultipleInstancesFlow,
  dynamicPartialJoinForMultipleInstancesFlow,
  isBidAdmissionClosed,
  multipleInstancesWithoutSynchronizationFlow,
  noPriorKnowledgeMultipleInstancesFlow,
  runTimeMultipleInstancesFlow,
  staticPartialJoinForMultipleInstancesFlow,
} from "../../src/patterns/workflow-patterns/multiple-instances.js";
import {
  blockingPartialJoinFlow,
  cancellingPartialJoinFlow,
  structuredPartialJoinFlow,
} from "../../src/patterns/workflow-patterns/partial-joins.js";
import {
  criticalSectionFlow,
  deferredChoiceFlow,
  interleavedParallelRoutingFlow,
  interleavedRoutingFlow,
  isChangeWindowOpen,
  milestoneFlow,
  type TaskInterval,
} from "../../src/patterns/workflow-patterns/state-based.js";
import {
  explicitTerminationFlow,
  implicitTerminationFlow,
} from "../../src/patterns/workflow-patterns/termination.js";
import {
  isAwaitingMotionEvent,
  persistentTriggerFlow,
  transientTriggerFlow,
} from "../../src/patterns/workflow-patterns/triggers.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;
const LOCK_CONFLICT_RETRY_MS = 100;

interface Completion {
  readonly stepType: string;
  readonly output: unknown;
}

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

async function startPatternFlow<Input>(flow: Flow<Input>, prefix: string, input: Input): Promise<string> {
  const flowId = environment.newFlowId(`wcp-${prefix}`);
  await environment.client.startFlow(flow, flowId, input, environment.startOptions());
  return flowId;
}

async function waitForCompletedFlow(flowId: string): Promise<Completion[]> {
  const result: FlowResult = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  return result.completions.map((completion) => ({
    stepType: completion.stepType,
    output: decodeCompletionOutput(completion),
  }));
}

// Strings and numbers travel as Dex scalars; arrays and objects travel as JSON.
function decodeCompletionOutput(completion: StepCompletion): unknown {
  const codecs: readonly (Codec<unknown> | undefined)[] = [undefined, stringCodec, doubleCodec];
  for (const codec of codecs) {
    try {
      return completion.decode(codec);
    } catch (error) {
      if (!(error instanceof ValueMappingError)) {
        throw error;
      }
    }
  }
  throw new Error(`cannot decode the output of ${completion.stepExecutionId}`);
}

async function waitForStep(flowId: string, stepType: string, number = 1): Promise<void> {
  await environment.client.waitForStepCompletion(flowId, StepExecutionId.of(stepType, number), {
    requestTimeoutMs: WAIT_TIMEOUT_MS,
  });
}

async function assertRunning(flowId: string): Promise<void> {
  assert.equal((await environment.client.describeFlow(flowId)).status, "running");
}

function outputsOf(completions: readonly Completion[], stepType: string): unknown[] {
  return completions.filter((completion) => completion.stepType === stepType).map(
    (completion) => completion.output,
  );
}

function stepTypesOf(completions: readonly Completion[]): string[] {
  return completions.map((completion) => completion.stepType).sort();
}

async function waitForAttributeValue<T>(
  flowId: string,
  attribute: Attribute<T>,
  expected: T,
): Promise<void> {
  await awaitCondition(
    async () => (await environment.client.getAttributes(flowId, attribute))[0],
    (value) => value === expected,
    WAIT_TIMEOUT_MS,
    `${attribute.name} never became ${String(expected)}`,
  );
}

async function awaitRpcValue<T>(load: () => Promise<T>, expected: T): Promise<void> {
  await awaitCondition(load, (value) => value === expected, WAIT_TIMEOUT_MS, `never reached ${String(expected)}`);
}

// The RPC fails fast while a Step holds the same Attribute lock.
async function retryOnLockConflict<T>(invoke: () => Promise<T>): Promise<T> {
  const deadline = Date.now() + WAIT_TIMEOUT_MS;
  for (;;) {
    try {
      return await invoke();
    } catch (error) {
      if (!(error instanceof RpcLockConflictError) || Date.now() >= deadline) {
        throw error;
      }
      await sleep(LOCK_CONFLICT_RETRY_MS);
    }
  }
}

function assertNoOverlap(intervals: readonly TaskInterval[]): void {
  const ordered = [...intervals].sort((left, right) => left.startedAtMillis - right.startedAtMillis);
  for (let index = 1; index < ordered.length; index++) {
    assert.ok(
      ordered[index - 1]!.finishedAtMillis <= ordered[index]!.startedAtMillis,
      `${ordered[index - 1]!.task} overlaps ${ordered[index]!.task}`,
    );
  }
}

test("WCP-1 Sequence runs Steps one after another", async () => {
  const flowId = await startPatternFlow(sequenceFlow, "sequence", []);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(completions, [
    { stepType: "ShipOrder", output: ["ReceiveOrder", "CheckInventory", "ShipOrder"] },
  ]);
});

test("WCP-2 Parallel Split and WCP-3 Synchronization wait for every branch", async () => {
  const flowId = await startPatternFlow(parallelSplitSynchronizationFlow, "parallel-split", undefined);
  await waitForStep(flowId, "PackItems");
  await assertRunning(flowId);
  await environment.client.invokeRPC(parallelSplitSynchronizationFlow.approvePayment, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(completions, [
    { stepType: "SynchronizeFulfillment", output: ["ChargePayment", "PackItems"] },
  ]);
});

test("WCP-4 Exclusive Choice and WCP-5 Simple Merge run exactly one branch", async () => {
  const autoFlowId = await startPatternFlow(exclusiveChoiceSimpleMergeFlow, "exclusive-auto", 250);
  assert.deepEqual(await waitForCompletedFlow(autoFlowId), [
    { stepType: "CloseClaim", output: ["RouteClaim", "AutoApprove", "CloseClaim"] },
  ]);
  const manualFlowId = await startPatternFlow(exclusiveChoiceSimpleMergeFlow, "exclusive-manual", 5000);
  assert.deepEqual(await waitForCompletedFlow(manualFlowId), [
    { stepType: "CloseClaim", output: ["RouteClaim", "ManualReview", "CloseClaim"] },
  ]);
});

test("WCP-6 Multi-Choice, WCP-7 and WCP-37 synchronizing merges wait only for chosen branches", async () => {
  const flowId = await startPatternFlow(multiChoiceSynchronizingMergeFlow, "multi-choice", {
    sendEmail: true,
    sendSms: false,
    sendPush: true,
  });
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "SynchronizingMerge", output: ["SendEmail", "SendPush"] },
  ]);
});

test("WCP-8 Multi-Merge runs the merged Step once per arrival", async () => {
  const flowId = await startPatternFlow(multiMergeFlow, "multi-merge", undefined);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(stepTypesOf(completions), ["RecordReview", "RecordReview"]);
  assert.deepEqual(outputsOf(completions, "RecordReview").sort(), ["FinanceReview", "LegalReview"]);
});

test("WCP-38 General Synchronizing Merge waits for a review added by rework", async () => {
  const flowId = await startPatternFlow(generalSynchronizingMergeFlow, "general-merge-rework", {
    isLegalCheckNeeded: true,
    isFinanceCheckNeeded: true,
    financeReworkRounds: 1,
    isFinanceArchived: false,
  });
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    {
      stepType: "PublishDocument",
      output: ["CheckFinanceTerms-1", "CheckLegalTerms-0", "CheckLegalTerms-1"],
    },
  ]);
});

test("WCP-38 General Synchronizing Merge is not blocked by an archived branch", async () => {
  const flowId = await startPatternFlow(generalSynchronizingMergeFlow, "general-merge-archive", {
    isLegalCheckNeeded: true,
    isFinanceCheckNeeded: true,
    financeReworkRounds: 0,
    isFinanceArchived: true,
  });
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(stepTypesOf(completions), ["ArchiveDocument", "PublishDocument"]);
  assert.deepEqual(outputsOf(completions, "PublishDocument"), [["CheckLegalTerms-0"]]);
});

test("WCP-38 General Synchronizing Merge fires once, after the last thread", async () => {
  const flowId = await startPatternFlow(generalSynchronizingMergeFlow, "general-merge-once", {
    isLegalCheckNeeded: false,
    isFinanceCheckNeeded: true,
    financeReworkRounds: 2,
    isFinanceArchived: false,
  });
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    {
      stepType: "PublishDocument",
      output: ["CheckFinanceTerms-2", "CheckLegalTerms-1", "CheckLegalTerms-2"],
    },
  ]);
});

test("WCP-9 Structured Discriminator fires on the first quote and resets for a second round", async () => {
  const flowId = await startPatternFlow(structuredPartialJoinFlow, "discriminator", {
    requiredQuotes: 1,
    round: 1,
    roundCount: 2,
  });
  await releaseSupplierQuotes(flowId, "A");
  await waitForStep(flowId, "PlaceOrder");
  await releaseSupplierQuotes(flowId, "B", "C");
  await waitForStep(flowId, "RequestQuotes", 2);
  await releaseSupplierQuotes(flowId, "B");
  await waitForStep(flowId, "PlaceOrder", 2);
  await releaseSupplierQuotes(flowId, "A", "C");
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "PlaceOrder"), [
    { round: 1, acceptedQuotes: ["A"] },
    { round: 2, acceptedQuotes: ["B"] },
  ]);
  assert.deepEqual(outputsOf(completions, "ResetQuoteJoin"), [{ round: 2, ignoredQuotes: ["A", "C"] }]);
});

test("WCP-30 Structured Partial Join fires after two of three quotes in each round", async () => {
  const flowId = await startPatternFlow(structuredPartialJoinFlow, "partial-join", {
    requiredQuotes: 2,
    round: 1,
    roundCount: 2,
  });
  await releaseSupplierQuotes(flowId, "A");
  await waitForStep(flowId, "QuoteFromSupplierA");
  await releaseSupplierQuotes(flowId, "B");
  await waitForStep(flowId, "PlaceOrder");
  await releaseSupplierQuotes(flowId, "C");
  await waitForStep(flowId, "RequestQuotes", 2);
  await releaseSupplierQuotes(flowId, "C");
  await waitForStep(flowId, "QuoteFromSupplierC", 2);
  await releaseSupplierQuotes(flowId, "A");
  await waitForStep(flowId, "PlaceOrder", 2);
  await releaseSupplierQuotes(flowId, "B");
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "PlaceOrder"), [
    { round: 1, acceptedQuotes: ["A", "B"] },
    { round: 2, acceptedQuotes: ["A", "C"] },
  ]);
  assert.deepEqual(outputsOf(completions, "ResetQuoteJoin"), [{ round: 2, ignoredQuotes: ["B"] }]);
});

async function releaseSupplierQuotes(flowId: string, ...suppliers: string[]): Promise<void> {
  for (const supplier of suppliers) {
    await environment.client.invokeRPC(structuredPartialJoinFlow.releaseSupplierQuote, flowId, supplier);
  }
}

async function sendBranchSignals(flowId: string, ...branches: string[]): Promise<void> {
  for (const branch of branches) {
    await environment.client.invokeRPC(blockingPartialJoinFlow.sendBranchSignal, flowId, branch);
  }
}

test("WCP-28 Blocking Discriminator blocks a next-round signal until the join resets", async () => {
  const flowId = await startPatternFlow(blockingPartialJoinFlow, "blocking-discriminator", {
    requiredSignals: 1,
    round: 1,
  });
  await sendBranchSignals(flowId, "A");
  await waitForStep(flowId, "ProcessRound");
  await sendBranchSignals(flowId, "A");
  assert.equal(
    await environment.client.invokeRPC(blockingPartialJoinFlow.getQueuedBranchSignalCount, flowId),
    1,
  );
  await sendBranchSignals(flowId, "B", "C");
  await waitForStep(flowId, "ProcessRound", 2);
  await sendBranchSignals(flowId, "B", "C");
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "ProcessRound"), [
    { round: 1, branches: ["A"] },
    { round: 2, branches: ["A"] },
  ]);
});

test("WCP-31 Blocking Partial Join blocks next-round signals until the join resets", async () => {
  const flowId = await startPatternFlow(blockingPartialJoinFlow, "blocking-partial-join", {
    requiredSignals: 2,
    round: 1,
  });
  await sendBranchSignals(flowId, "A", "B");
  await waitForStep(flowId, "ProcessRound");
  await sendBranchSignals(flowId, "A", "B");
  assert.equal(
    await environment.client.invokeRPC(blockingPartialJoinFlow.getQueuedBranchSignalCount, flowId),
    2,
  );
  await sendBranchSignals(flowId, "C");
  await waitForStep(flowId, "ProcessRound", 2);
  await sendBranchSignals(flowId, "C");
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "ProcessRound"), [
    { round: 1, branches: ["A", "B"] },
    { round: 2, branches: ["A", "B"] },
  ]);
});

test("WCP-29 Cancelling Discriminator stops the other carriers from running", async () => {
  const flowId = await startPatternFlow(cancellingPartialJoinFlow, "cancelling-discriminator", 1);
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "B");
  await waitForStep(flowId, "AcceptBids");
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "A");
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "C");
  assert.equal(
    await environment.client.invokeRPC(cancellingPartialJoinFlow.getQueuedCarrierReleaseCount, flowId),
    2,
  );
  await environment.client.invokeRPC(cancellingPartialJoinFlow.confirmCarrierBooking, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ConfirmCarrierBooking", output: ["B"] },
  ]);
});

test("WCP-32 Cancelling Partial Join stops the last carrier from running", async () => {
  const flowId = await startPatternFlow(cancellingPartialJoinFlow, "cancelling-partial-join", 2);
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "A");
  await waitForStep(flowId, "BidFromCarrierA");
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "C");
  await waitForStep(flowId, "AcceptBids");
  await environment.client.invokeRPC(cancellingPartialJoinFlow.releaseCarrierBid, flowId, "B");
  assert.equal(
    await environment.client.invokeRPC(cancellingPartialJoinFlow.getQueuedCarrierReleaseCount, flowId),
    1,
  );
  await environment.client.invokeRPC(cancellingPartialJoinFlow.confirmCarrierBooking, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ConfirmCarrierBooking", output: ["A", "C"] },
  ]);
});

test("WCP-33 Generalised AND-Join keeps an extra invoice for the next firing", async () => {
  const flowId = await startPatternFlow(generalizedAndJoinFlow, "and-join", {
    expectedMatches: 2,
    matchedCount: 0,
  });
  await environment.client.invokeRPC(generalizedAndJoinFlow.deliverInvoice, flowId, "I1");
  await environment.client.invokeRPC(generalizedAndJoinFlow.deliverInvoice, flowId, "I2");
  await environment.client.invokeRPC(generalizedAndJoinFlow.deliverPayment, flowId, "P1");
  await waitForStep(flowId, "RecordMatch");
  await assertRunning(flowId);
  await environment.client.invokeRPC(generalizedAndJoinFlow.deliverPayment, flowId, "P2");
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "RecordMatch"), [
    { invoiceId: "I1", paymentId: "P1" },
    { invoiceId: "I2", paymentId: "P2" },
  ]);
});

test("WCP-42 Thread Split and WCP-41 Thread Merge merge every parcel thread", async () => {
  const flowId = await startPatternFlow(threadSplitMergeFlow, "threads", 3);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "MergeParcels", output: [0, 1, 2] },
  ]);
});

test("WCP-12 Multiple Instances without Synchronization continue without waiting", async () => {
  const flowId = await startPatternFlow(
    multipleInstancesWithoutSynchronizationFlow,
    "instances-unsynchronized",
    ["ana", "ben"],
  );
  await waitForStep(flowId, "ConfirmRegistration");
  await assertRunning(flowId);
  await environment.client.invokeRPC(
    multipleInstancesWithoutSynchronizationFlow.releaseInvitations,
    flowId,
    2,
  );
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "SendInvitation").sort(), ["ana", "ben"]);
  assert.deepEqual(outputsOf(completions, "ConfirmRegistration"), ["ConfirmRegistration"]);
});

test("WCP-13 Multiple Instances with Design-Time Knowledge run three reviews at once", async () => {
  const flowId = await startPatternFlow(designTimeMultipleInstancesFlow, "instances-design", undefined);
  await awaitRpcValue(
    () => environment.client.invokeRPC(designTimeMultipleInstancesFlow.getStartedDesignTimeReviewCount, flowId),
    3,
  );
  await assertRunning(flowId);
  await environment.client.invokeRPC(designTimeMultipleInstancesFlow.openDesignTimeReviewGate, flowId, 3);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "CollectReviews", output: ["finance", "legal", "security"] },
  ]);
});

test("WCP-14 Multiple Instances with Run-Time Knowledge run every review at once", async () => {
  const flowId = await startPatternFlow(runTimeMultipleInstancesFlow, "instances-runtime", [
    "dee",
    "ana",
    "cy",
    "ben",
  ]);
  await awaitRpcValue(
    () => environment.client.invokeRPC(runTimeMultipleInstancesFlow.getStartedRunTimeReviewCount, flowId),
    4,
  );
  await assertRunning(flowId);
  await environment.client.invokeRPC(runTimeMultipleInstancesFlow.openRunTimeReviewGate, flowId, 4);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "CollectReviews", output: ["ana", "ben", "cy", "dee"] },
  ]);
});

test("WCP-15 Multiple Instances without a priori Run-Time Knowledge add instances while others run", async () => {
  const flow = noPriorKnowledgeMultipleInstancesFlow;
  const flowId = await startPatternFlow(flow, "instances-open", []);
  assert.equal(await environment.client.invokeRPC(flow.addSample, flowId, "s1"), true);
  assert.equal(await environment.client.invokeRPC(flow.addSample, flowId, "s2"), true);
  await awaitRpcValue(() => environment.client.invokeRPC(flow.getStartedAnalysisCount, flowId), 2);
  assert.equal(await environment.client.invokeRPC(flow.addSample, flowId, "s3"), true);
  await awaitRpcValue(() => environment.client.invokeRPC(flow.getStartedAnalysisCount, flowId), 3);
  await environment.client.invokeRPC(flow.closeSampleIntake, flowId);
  assert.equal(await environment.client.invokeRPC(flow.addSample, flowId, "s4"), false);
  await assertRunning(flowId);
  await environment.client.invokeRPC(flow.openSampleGate, flowId, 3);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ReportSamples", output: ["s1", "s2", "s3"] },
  ]);
});

test("WCP-34 Static Partial Join for Multiple Instances fires after two of three inspections each round", async () => {
  const flow = staticPartialJoinForMultipleInstancesFlow;
  const flowId = await startPatternFlow(flow, "instances-static-join", {
    inspectionCount: 3,
    requiredInspections: 2,
    round: 1,
    roundCount: 2,
  });
  await environment.client.invokeRPC(flow.releaseInspections, flowId, 2);
  await waitForStep(flowId, "ApproveBatch");
  await assertRunning(flowId);
  await environment.client.invokeRPC(flow.releaseInspections, flowId, 1);
  await waitForStep(flowId, "StartInspections", 2);
  await environment.client.invokeRPC(flow.releaseInspections, flowId, 2);
  await waitForStep(flowId, "ApproveBatch", 2);
  await environment.client.invokeRPC(flow.releaseInspections, flowId, 1);
  const completions = await waitForCompletedFlow(flowId);
  const approved = outputsOf(completions, "ApproveBatch") as { round: number; unitIndexes: number[] }[];
  assert.deepEqual(approved.map((batch) => batch.round), [1, 2]);
  for (const batch of approved) {
    assert.equal(new Set(batch.unitIndexes).size, 2);
  }
  const reset = outputsOf(completions, "ResetInspectionJoin") as { round: number; unitIndexes: number[] }[];
  assert.equal(reset.length, 1);
  assert.equal(reset[0]!.round, 2);
  assert.deepEqual([...approved[1]!.unitIndexes, ...reset[0]!.unitIndexes].sort(), [0, 1, 2]);
});

test("WCP-35 Cancelling Partial Join for Multiple Instances stops the last inspection from running", async () => {
  const flow = cancellingPartialJoinForMultipleInstancesFlow;
  const flowId = await startPatternFlow(flow, "instances-cancelling-join", {
    inspectionCount: 3,
    requiredInspections: 2,
  });
  await environment.client.invokeRPC(flow.releaseQuorumInspections, flowId, 2);
  await waitForStep(flowId, "AcceptQuorumInspections");
  await environment.client.invokeRPC(flow.releaseQuorumInspections, flowId, 1);
  assert.equal(await environment.client.invokeRPC(flow.getQueuedQuorumReleaseCount, flowId), 1);
  await environment.client.invokeRPC(flow.certifyQuorum, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(stepTypesOf(completions), ["CertifyQuorum"]);
  assert.equal(new Set(outputsOf(completions, "CertifyQuorum")[0] as number[]).size, 2);
});

test("WCP-36 Dynamic Partial Join for Multiple Instances closes admission once two bids are accepted", async () => {
  const flow = dynamicPartialJoinForMultipleInstancesFlow;
  const flowId = await startPatternFlow(flow, "instances-dynamic-join", {
    requiredAcceptedBids: 2,
    acceptedBids: [],
  });
  const addBid = (amount: number) =>
    retryOnLockConflict(() => environment.client.invokeRPC(flow.addBid, flowId, amount));
  assert.equal(await addBid(500), true);
  assert.equal(await addBid(1500), true);
  await awaitRpcValue(() => environment.client.invokeRPC(flow.getStartedEvaluationCount, flowId), 2);
  assert.equal(await addBid(700), true);
  await awaitRpcValue(() => environment.client.invokeRPC(flow.getStartedEvaluationCount, flowId), 3);
  await environment.client.invokeRPC(flow.openBidGate, flowId, 3);
  await waitForAttributeValue(flowId, isBidAdmissionClosed, true);
  assert.equal(await addBid(400), false);
  await environment.client.invokeRPC(flow.signContract, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "AwardContract", output: [500, 700] },
  ]);
});

test("WCP-16 Deferred Choice runs the branch of the response that arrives", async () => {
  const flowId = await startPatternFlow(deferredChoiceFlow, "deferred-decline", undefined);
  await environment.client.invokeRPC(deferredChoiceFlow.presentOffer, flowId);
  await waitForStep(flowId, "PresentOffer");
  await environment.client.invokeRPC(deferredChoiceFlow.declineOffer, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ArchiveOffer", output: "ArchiveOffer" },
  ]);
});

test("WCP-16 Deferred Choice runs exactly one branch when responses compete", async () => {
  const flowId = await startPatternFlow(deferredChoiceFlow, "deferred-competing", undefined);
  await environment.client.invokeRPC(deferredChoiceFlow.acceptOffer, flowId);
  await environment.client.invokeRPC(deferredChoiceFlow.declineOffer, flowId);
  await environment.client.invokeRPC(deferredChoiceFlow.presentOffer, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.equal(completions.length, 1);
  assert.ok(["FulfillOffer", "ArchiveOffer"].includes(completions[0]!.stepType));
});

test("WCP-17 Interleaved Parallel Routing never overlaps tasks and keeps their order", async () => {
  const flowId = await startPatternFlow(interleavedParallelRoutingFlow, "interleaved-parallel", undefined);
  const completions = await waitForCompletedFlow(flowId);
  const intervals = outputsOf(completions, "CompleteOnboardingTasks")[0] as TaskInterval[];
  assert.deepEqual(intervals.map((interval) => interval.task).sort(), [
    "PrepareContract",
    "RunCreditCheck",
    "SignContract",
  ]);
  assertNoOverlap(intervals);
  const prepare = intervals.find((interval) => interval.task === "PrepareContract")!;
  const sign = intervals.find((interval) => interval.task === "SignContract")!;
  assert.ok(prepare.finishedAtMillis <= sign.startedAtMillis);
});

test("WCP-18 Milestone accepts a change only while the window is open", async () => {
  const flowId = await startPatternFlow(milestoneFlow, "milestone", undefined);
  await waitForAttributeValue(flowId, isChangeWindowOpen, true);
  assert.equal(
    await environment.client.invokeRPC(milestoneFlow.requestConfigChange, flowId, "raise-limit"),
    true,
  );
  await environment.client.invokeRPC(milestoneFlow.approveRelease, flowId);
  await waitForAttributeValue(flowId, isChangeWindowOpen, false);
  assert.equal(
    await environment.client.invokeRPC(milestoneFlow.requestConfigChange, flowId, "late-change"),
    false,
  );
  await environment.client.invokeRPC(milestoneFlow.finalizeRelease, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(outputsOf(completions, "ApplyConfigChange"), ["raise-limit"]);
});

test("WCP-39 Critical Section never overlaps the two sections", async () => {
  const flowId = await startPatternFlow(criticalSectionFlow, "critical-section", undefined);
  const completions = await waitForCompletedFlow(flowId);
  const intervals = outputsOf(completions, "FinishCriticalSections")[0] as TaskInterval[];
  assert.deepEqual(intervals.map((interval) => interval.task).sort(), ["inventory", "pricing"]);
  assertNoOverlap(intervals);
});

test("WCP-40 Interleaved Routing runs each audit once and never two at a time", async () => {
  const flowId = await startPatternFlow(interleavedRoutingFlow, "interleaved", undefined);
  const completions = await waitForCompletedFlow(flowId);
  const intervals = outputsOf(completions, "CompleteAudits")[0] as TaskInterval[];
  assert.deepEqual(intervals.map((interval) => interval.task).sort(), [
    "AuditExpenses",
    "AuditPayroll",
    "AuditTaxes",
  ]);
  assertNoOverlap(intervals);
});

test("WCP-19 Cancel Task withdraws the waiting pickup", async () => {
  const flowId = await startPatternFlow(cancelTaskFlow, "cancel-task", undefined);
  await environment.client.invokeRPC(cancelTaskFlow.cancelCourierPickup, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ArrangeSelfDelivery", output: "ArrangeSelfDelivery" },
  ]);
});

test("WCP-20 Cancel Case stops the running child Flow and the parent's other Steps", async () => {
  const flowId = await startPatternFlow(cancelCaseFlow, "cancel-case", undefined);
  await waitForStep(flowId, "StartApplication");
  await environment.client.invokeRPC(cancelCaseFlow.cancelCase, flowId, "applicant withdrew");
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed");
  assert.ok(!result.completions.some((completion) => completion.stepType === "ReviewApplication"));
  const [childFlowIds] = await environment.client.getAttributes(flowId, stoppedChildFlowIds);
  assert.equal(childFlowIds?.length, 1);
  await awaitCondition(
    async () => (await environment.client.describeFlow(childFlowIds![0]!)).status,
    (status) => status === "cancelled",
    WAIT_TIMEOUT_MS,
    "the child Flow was never stopped",
  );
});

test("WCP-25 Cancel Region withdraws two checks and keeps the third", async () => {
  const flowId = await startPatternFlow(cancelRegionFlow, "cancel-region", undefined);
  await waitForStep(flowId, "StartApplicationChecks");
  await environment.client.invokeRPC(cancelRegionFlow.rejectApplication, flowId);
  await waitForStep(flowId, "NotifyRejection");
  await assertRunning(flowId);
  await environment.client.invokeRPC(cancelRegionFlow.completeSanctionsScreening, flowId);
  assert.deepEqual(stepTypesOf(await waitForCompletedFlow(flowId)), [
    "NotifyRejection",
    "ScreenSanctions",
  ]);
});

test("WCP-26 Cancel Multiple Instance Activity keeps the finished translation", async () => {
  const flowId = await startPatternFlow(cancelMultipleInstanceTaskFlow, "cancel-instances", 3);
  await waitForStep(flowId, "StartTranslations");
  await environment.client.invokeRPC(cancelMultipleInstanceTaskFlow.releaseTranslation, flowId);
  await waitForAttributeValue(flowId, finishedTranslationCount, 1);
  await environment.client.invokeRPC(cancelMultipleInstanceTaskFlow.cancelTranslations, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.equal(outputsOf(completions, "TranslateDocument").length, 1);
  assert.deepEqual(outputsOf(completions, "ReportTranslations"), [1]);
});

test("WCP-27 Complete Multiple Instance Activity withdraws the rest and continues", async () => {
  const flowId = await startPatternFlow(completeMultipleInstanceTaskFlow, "complete-instances", 3);
  await waitForStep(flowId, "StartPeerReviews");
  await environment.client.invokeRPC(completeMultipleInstanceTaskFlow.releasePeerReview, flowId);
  await awaitCondition(
    () => environment.client.invokeRPC(completeMultipleInstanceTaskFlow.getSubmittedPeerReviewCount, flowId),
    (count) => count === 1,
    WAIT_TIMEOUT_MS,
    "the first peer review was never submitted",
  );
  await environment.client.invokeRPC(completeMultipleInstanceTaskFlow.completePeerReviewsEarly, flowId);
  const completions = await waitForCompletedFlow(flowId);
  assert.deepEqual(stepTypesOf(completions), ["PublishReviewResults"]);
  assert.equal((outputsOf(completions, "PublishReviewResults")[0] as number[]).length, 1);
});

test("WCP-10 Arbitrary Cycles enter at either Step and leave at either exit", async () => {
  const newFlowId = await startPatternFlow(arbitraryCyclesFlow, "cycles-new", {
    hasDraft: false,
    shouldAbandon: false,
  });
  assert.deepEqual(await waitForCompletedFlow(newFlowId), [
    {
      stepType: "PublishArticle",
      output: [
        "RouteSubmission",
        "DraftArticle",
        "EditArticle",
        "DraftArticle",
        "EditArticle",
        "PublishArticle",
      ],
    },
  ]);
  const draftFlowId = await startPatternFlow(arbitraryCyclesFlow, "cycles-draft", {
    hasDraft: true,
    shouldAbandon: false,
  });
  assert.deepEqual(await waitForCompletedFlow(draftFlowId), [
    {
      stepType: "PublishArticle",
      output: ["RouteSubmission", "EditArticle", "DraftArticle", "EditArticle", "PublishArticle"],
    },
  ]);
  const abandonFlowId = await startPatternFlow(arbitraryCyclesFlow, "cycles-abandon", {
    hasDraft: false,
    shouldAbandon: true,
  });
  assert.deepEqual(await waitForCompletedFlow(abandonFlowId), [
    {
      stepType: "AbandonArticle",
      output: ["RouteSubmission", "DraftArticle", "EditArticle", "DraftArticle", "AbandonArticle"],
    },
  ]);
});

test("WCP-21 Structured Loop tests its condition before each pass", async () => {
  const threeFlowId = await startPatternFlow(structuredLoopFlow, "loop-three", {
    remainingBatches: 3,
    processedBatches: [],
  });
  assert.deepEqual(await waitForCompletedFlow(threeFlowId), [
    { stepType: "FinishBatches", output: ["batch-3", "batch-2", "batch-1"] },
  ]);
  const zeroFlowId = await startPatternFlow(structuredLoopFlow, "loop-zero", {
    remainingBatches: 0,
    processedBatches: [],
  });
  assert.deepEqual(await waitForCompletedFlow(zeroFlowId), [{ stepType: "FinishBatches", output: [] }]);
});

test("WCP-22 Recursion computes a factorial through SubFlows of the same Flow type", async () => {
  const flowId = await startPatternFlow(factorialRecursionFlow, "recursion", 4);
  assert.deepEqual(await waitForCompletedFlow(flowId), [{ stepType: "ComputeFactorial", output: 24 }]);
});

test("WCP-11 Implicit Termination completes after the last active Step", async () => {
  const flowId = await startPatternFlow(implicitTerminationFlow, "implicit-termination", undefined);
  await waitForStep(flowId, "CreateAccount");
  await waitForStep(flowId, "SendWelcomeEmail");
  await assertRunning(flowId);
  await environment.client.invokeRPC(implicitTerminationFlow.completeProfile, flowId);
  assert.deepEqual(stepTypesOf(await waitForCompletedFlow(flowId)), [
    "AwaitProfileCompletion",
    "CreateAccount",
    "SendWelcomeEmail",
  ]);
});

test("WCP-43 Explicit Termination completes at once and withdraws waiting Steps", async () => {
  const flowId = await startPatternFlow(explicitTerminationFlow, "explicit-termination", undefined);
  await waitForStep(flowId, "StartIncident");
  await environment.client.invokeRPC(explicitTerminationFlow.resolveIncident, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "ResolveIncident", output: "ResolveIncident" },
  ]);
});

test("WCP-23 Transient Trigger accepts one event only while a Step waits", async () => {
  const flowId = await startPatternFlow(transientTriggerFlow, "transient-trigger", undefined);
  const sendMotionEvent = (value: number) =>
    retryOnLockConflict(() => environment.client.invokeRPC(transientTriggerFlow.sendMotionEvent, flowId, value));
  assert.equal(await sendMotionEvent(1), false);
  await environment.client.invokeRPC(transientTriggerFlow.armMotionSensor, flowId);
  await waitForAttributeValue(flowId, isAwaitingMotionEvent, true);
  assert.equal(await sendMotionEvent(2), true);
  assert.equal(await sendMotionEvent(3), false);
  await environment.client.invokeRPC(transientTriggerFlow.stopMotionMonitoring, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [{ stepType: "AwaitMonitoringStop", output: 2 }]);
});

test("WCP-24 Persistent Trigger keeps a label sent before the Step waits", async () => {
  const flowId = await startPatternFlow(persistentTriggerFlow, "persistent-trigger", undefined);
  await environment.client.invokeRPC(persistentTriggerFlow.sendShipmentLabel, flowId, "label-1");
  await environment.client.invokeRPC(persistentTriggerFlow.markCarrierReady, flowId);
  assert.deepEqual(await waitForCompletedFlow(flowId), [
    { stepType: "AwaitShipmentLabel", output: "label-1" },
  ]);
});
