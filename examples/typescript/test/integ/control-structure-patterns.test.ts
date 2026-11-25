/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
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

import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import {
  StepExecutionId,
  TimerId,
  stringCodec,
  type Flow,
  type FlowResult,
} from "@superdurable/dex";

import { stoppedCreditCheckFlowId } from "../../src/patterns/cancel-case/loan-application-flow.js";
import {
  contractEditFlow,
  type ContractEdit,
  type ContractEditRecord,
} from "../../src/patterns/critical-section/contract-edit-flow.js";
import {
  OFFER_RESPONSE_RECORDED,
  OFFER_RESPONSE_WINDOW_CLOSED,
  offerResponseFlow,
} from "../../src/patterns/deferred-choice/offer-response-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";
import {
  holdNextOfferResponse,
  offerDecisionGateProbeFlow,
  offerResponseWindowProbeFlow,
} from "./offer-response-window-probe-flow.js";

const WAIT_TIMEOUT_MS = 45_000;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

async function startPatternFlow<Input>(flow: Flow<Input>, prefix: string, input: Input): Promise<string> {
  const flowId = environment.newFlowId(prefix);
  await environment.client.startFlow(flow, flowId, input, environment.startOptions());
  return flowId;
}

async function waitForCompletedFlow(flowId: string): Promise<FlowResult> {
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", `${flowId}: ${result.errorMessage ?? ""}`);
  return result;
}

async function waitForStep(flowId: string, stepType: string): Promise<void> {
  await environment.client.waitForStepCompletion(flowId, StepExecutionId.of(stepType), {
    requestTimeoutMs: WAIT_TIMEOUT_MS,
  });
}

// Retries until the Timer of AwaitOfferResponse is registered.
async function skipOfferExpiry(flowId: string): Promise<void> {
  await awaitCondition(
    async () => {
      try {
        await environment.client.skipTimer(
          flowId,
          StepExecutionId.of("AwaitOfferResponse"),
          TimerId.byConditionIndex(0),
        );
        return true;
      } catch {
        return false;
      }
    },
    (isSkipped) => isSkipped,
    WAIT_TIMEOUT_MS,
    `the offer expiry Timer of ${flowId} was never skipped`,
  );
}

const deferredChoiceCases = [
  {
    name: "accept",
    respond: (flowId: string) =>
      environment.client.invokeRPC(offerResponseFlow.acceptCustomerOffer, flowId, "looks good"),
    expectedStep: "FulfillOffer",
    expectedOutput: "fulfilled offer-1",
  },
  {
    name: "reject",
    respond: (flowId: string) =>
      environment.client.invokeRPC(offerResponseFlow.rejectCustomerOffer, flowId, "too expensive"),
    expectedStep: "CloseRejectedOffer",
    expectedOutput: "rejected offer-1",
  },
  {
    name: "expire",
    respond: skipOfferExpiry,
    expectedStep: "ExpireOffer",
    expectedOutput: "expired offer-1",
  },
];

for (const testCase of deferredChoiceCases) {
  test(`deferred choice routes on the customer response: ${testCase.name}`, async () => {
    const flowId = await startPatternFlow(offerResponseFlow, "pattern-deferred-choice", "offer-1");
    await testCase.respond(flowId);
    const result = await waitForCompletedFlow(flowId);
    assert.deepEqual(
      result.completions.map((completion) => completion.stepType),
      [testCase.expectedStep],
    );
    assert.equal(result.completions[0]!.decode(stringCodec), testCase.expectedOutput);
  });
}

async function waitForExecuteWaitingForLocks(flowId: string, stepType: string): Promise<void> {
  await awaitCondition(
    () => environment.client.getFlowState(flowId),
    (state) =>
      state.activeStepExecutions.some(
        (execution) =>
          execution.stepType === stepType &&
          execution.invocation?.method === "execute" &&
          execution.invocation.pendingReason === "waitingForLocks",
      ),
    WAIT_TIMEOUT_MS,
    `${flowId}: ${stepType} execute never waited for the window lock`,
  );
}

function completionOutputs(result: FlowResult, stepType: string): string[] {
  return result.completions
    .filter((completion) => completion.stepType === stepType)
    .map((completion) => completion.decode(stringCodec));
}

test("deferred choice answers window closed to a response after the decision", async () => {
  const flow = offerResponseWindowProbeFlow;
  const flowId = await startPatternFlow(flow, "pattern-deferred-choice-window", "offer-3");
  assert.equal(
    await environment.client.invokeRPC(flow.acceptProbeOffer, flowId, "yes"),
    OFFER_RESPONSE_RECORDED,
  );
  await waitForStep(flowId, "AwaitOfferResponse");

  assert.equal(
    await environment.client.invokeRPC(flow.rejectProbeOffer, flowId, "changed my mind"),
    OFFER_RESPONSE_WINDOW_CLOSED,
  );

  await environment.client.invokeRPC(flow.openProbeGate, flowId);
  const result = await waitForCompletedFlow(flowId);
  assert.deepEqual(completionOutputs(result, "FulfillOffer"), ["fulfilled offer-3"]);
  assert.deepEqual(completionOutputs(result, "CloseRejectedOffer"), []);
});

test("deferred choice records only the first of two responses sent before the wait starts", async () => {
  const flow = offerDecisionGateProbeFlow;
  const flowId = await startPatternFlow(flow, "pattern-deferred-choice-double", "offer-5");
  // AwaitOfferResponse starts only after the gate opens, so only the reply RPCs touch the window.
  assert.equal(
    await environment.client.invokeRPC(flow.acceptGatedOffer, flowId, "yes"),
    OFFER_RESPONSE_RECORDED,
  );
  assert.equal(
    await environment.client.invokeRPC(flow.rejectGatedOffer, flowId, "no"),
    OFFER_RESPONSE_WINDOW_CLOSED,
  );

  await environment.client.invokeRPC(flow.openDecisionGate, flowId);
  const result = await waitForCompletedFlow(flowId);
  assert.deepEqual(completionOutputs(result, "FulfillOffer"), ["fulfilled offer-5"]);
  assert.deepEqual(completionOutputs(result, "CloseRejectedOffer"), []);
  assert.deepEqual(completionOutputs(result, "ExpireOffer"), []);
});

test("deferred choice takes a response committed after the Timer won", async () => {
  const held = holdNextOfferResponse();
  const flow = offerResponseWindowProbeFlow;
  const flowId = await startPatternFlow(flow, "pattern-deferred-choice-late", "offer-4");
  await waitForStep(flowId, "OpenOfferProbe");

  const heldResponse = environment.client.invokeRPC(
    flow.acceptProbeOfferWhenReleased,
    flowId,
    "yes, just in time",
  );
  try {
    const enteredTimeout = new AbortController();
    try {
      await Promise.race([
        held.entered,
        delay(WAIT_TIMEOUT_MS, undefined, { signal: enteredTimeout.signal }).then(() => {
          throw new Error(`${flowId}: the held response RPC never ran`);
        }),
      ]);
    } finally {
      enteredTimeout.abort();
    }
    // The Timer wins while the RPC holds the window lock, so execute waits for the RPC.
    await skipOfferExpiry(flowId);
    await waitForExecuteWaitingForLocks(flowId, "AwaitOfferResponse");
  } finally {
    held.release();
  }
  assert.equal(await heldResponse, OFFER_RESPONSE_RECORDED);

  await waitForStep(flowId, "RouteResponseBeforeExpiry");
  await environment.client.invokeRPC(flow.openProbeGate, flowId);
  const result = await waitForCompletedFlow(flowId);
  assert.deepEqual(completionOutputs(result, "FulfillOffer"), ["fulfilled offer-4"]);
  assert.deepEqual(completionOutputs(result, "ExpireOffer"), []);
});

test("critical section runs one edit at a time", async () => {
  const edits: ContractEdit[] = [
    { reviewer: "legal" },
    { reviewer: "finance" },
    { reviewer: "procurement" },
  ];
  const flowId = await startPatternFlow(contractEditFlow, "pattern-critical-section", edits);
  const records = await contractEditRecords(flowId);
  assert.deepEqual(
    records.map((record) => record.reviewer).sort(),
    ["finance", "legal", "procurement"],
  );
  for (const record of records) {
    assert.equal(record.outcome, "saved");
  }
  assertContractEditsDoNotOverlap(records);
});

test("critical section returns the token when an edit fails", async () => {
  const edits: ContractEdit[] = [
    { reviewer: "legal", shouldFailEdit: true },
    { reviewer: "finance" },
  ];
  const flowId = await startPatternFlow(contractEditFlow, "pattern-critical-section-failure", edits);
  const records = await contractEditRecords(flowId);
  const outcomes = Object.fromEntries(records.map((record) => [record.reviewer, record.outcome]));
  assert.deepEqual(outcomes, { legal: "failed", finance: "saved" });
  assertContractEditsDoNotOverlap(records);
});

test("critical section returns the token when the handoff fails", async () => {
  const edits: ContractEdit[] = [
    { reviewer: "legal", shouldFailHandoff: true },
    { reviewer: "finance" },
  ];
  const flowId = await startPatternFlow(contractEditFlow, "pattern-critical-section-handoff", edits);
  const records = await contractEditRecords(flowId);
  const outcomes = Object.fromEntries(records.map((record) => [record.reviewer, record.outcome]));
  assert.deepEqual(outcomes, { legal: "failed", finance: "saved" });
  assertContractEditsDoNotOverlap(records);
});

test("critical section returns the token on an early exit", async () => {
  const edits: ContractEdit[] = [
    { reviewer: "legal", shouldWithdraw: true },
    { reviewer: "finance" },
  ];
  const flowId = await startPatternFlow(contractEditFlow, "pattern-critical-section-withdraw", edits);
  const records = await contractEditRecords(flowId);
  const outcomes = Object.fromEntries(records.map((record) => [record.reviewer, record.outcome]));
  assert.deepEqual(outcomes, { legal: "withdrawn", finance: "saved" });
  assertContractEditsDoNotOverlap(records);
});

test("cancel case stops the running child Flow", async () => {
  const flowId = await startPatternFlow(environment.loanApplicationFlow, "pattern-cancel-case", {
    applicantId: "applicant-1",
    creditReportDelaySeconds: 3600,
  });
  await environment.client.invokeRPC(environment.loanApplicationFlow.cancelApplication, flowId, "applicant withdrew");
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed", result.errorMessage);
  assert.match(result.errorMessage ?? "", /application canceled: applicant withdrew/);
  const [childFlowId] = await environment.client.getAttributes(flowId, stoppedCreditCheckFlowId);
  assert.ok(childFlowId, "the stopped child Flow ID is empty");
  await awaitCondition(
    async () => (await environment.client.describeFlow(childFlowId)).status,
    (status) => status === "cancelled",
    WAIT_TIMEOUT_MS,
    `child Flow ${childFlowId} was not canceled`,
  );
});

test("cancel case after the child Flow finished", async () => {
  const flowId = await startPatternFlow(environment.loanApplicationFlow, "pattern-cancel-case-late", {
    applicantId: "applicant-2",
    creditReportDelaySeconds: 1,
  });
  await waitForStep(flowId, "RunCreditCheck");
  await environment.client.invokeRPC(environment.loanApplicationFlow.cancelApplication, flowId, "applicant withdrew");
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed", result.errorMessage);
  assert.match(result.errorMessage ?? "", /application canceled: applicant withdrew/);
});

test("cancel case completes with the underwriter decision", async () => {
  const flowId = await startPatternFlow(environment.loanApplicationFlow, "pattern-cancel-case-decided", {
    applicantId: "applicant-3",
    creditReportDelaySeconds: 1,
  });
  await waitForStep(flowId, "RunCreditCheck");
  await environment.client.invokeRPC(environment.loanApplicationFlow.decideApplication, flowId, "approved");
  const result = await waitForCompletedFlow(flowId);
  assert.deepEqual(
    result.completions
      .filter((completion) => completion.stepType === "AwaitUnderwriterDecision")
      .map((completion) => completion.decode(stringCodec)),
    ["approved after credit report for applicant-3"],
  );
});

async function contractEditRecords(flowId: string): Promise<ContractEditRecord[]> {
  const result = await waitForCompletedFlow(flowId);
  const outputs = result.completions
    .filter((completion) => completion.stepType === "FinishContractEdits")
    .map((completion) => completion.decode<ContractEditRecord[]>());
  assert.equal(outputs.length, 1);
  return outputs[0]!;
}

function assertContractEditsDoNotOverlap(records: readonly ContractEditRecord[]): void {
  for (const [leftIndex, left] of records.entries()) {
    for (const right of records.slice(leftIndex + 1)) {
      const isDisjoint =
        left.finishedAtMillis <= right.startedAtMillis || right.finishedAtMillis <= left.startedAtMillis;
      assert.ok(isDisjoint, `${JSON.stringify(left)} overlaps ${JSON.stringify(right)}`);
    }
  }
}
