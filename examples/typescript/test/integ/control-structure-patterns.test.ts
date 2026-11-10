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

import {
  StepExecutionId,
  TimerId,
  stringCodec,
  type Flow,
  type FlowResult,
} from "@superdurable/dex";

import {
  loanApplicationFlow,
  stoppedCreditCheckFlowId,
} from "../../src/patterns/cancel-case/loan-application-flow.js";
import {
  contractEditFlow,
  type ContractEdit,
  type ContractEditRecord,
} from "../../src/patterns/critical-section/contract-edit-flow.js";
import { offerResponseFlow } from "../../src/patterns/deferred-choice/offer-response-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

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

test("deferred choice consumes only the winning response", async () => {
  const flowId = await startPatternFlow(offerResponseFlow, "pattern-deferred-choice-race", "offer-2");
  await environment.client.invokeRPC(offerResponseFlow.acceptCustomerOffer, flowId, "yes");
  await environment.client.invokeRPC(offerResponseFlow.rejectCustomerOffer, flowId, "no");
  const result = await waitForCompletedFlow(flowId);
  assert.equal(result.completions.length, 1);
  assert.ok(["FulfillOffer", "CloseRejectedOffer"].includes(result.completions[0]!.stepType));
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

test("cancel case stops the running child Flow", async () => {
  const flowId = await startPatternFlow(loanApplicationFlow, "pattern-cancel-case", {
    applicantId: "applicant-1",
    creditReportDelaySeconds: 3600,
  });
  await environment.client.invokeRPC(loanApplicationFlow.cancelApplication, flowId, "applicant withdrew");
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
  const flowId = await startPatternFlow(loanApplicationFlow, "pattern-cancel-case-late", {
    applicantId: "applicant-2",
    creditReportDelaySeconds: 1,
  });
  await waitForStep(flowId, "RunCreditCheck");
  await environment.client.invokeRPC(loanApplicationFlow.cancelApplication, flowId, "applicant withdrew");
  const result = await environment.client.waitForFlow(flowId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed", result.errorMessage);
  assert.match(result.errorMessage ?? "", /application canceled: applicant withdrew/);
});

test("cancel case completes with the underwriter decision", async () => {
  const flowId = await startPatternFlow(loanApplicationFlow, "pattern-cancel-case-decided", {
    applicantId: "applicant-3",
    creditReportDelaySeconds: 1,
  });
  await waitForStep(flowId, "RunCreditCheck");
  await environment.client.invokeRPC(loanApplicationFlow.decideApplication, flowId, "approved");
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
