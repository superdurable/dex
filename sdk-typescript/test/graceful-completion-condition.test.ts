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
  Channel,
  ConditionCombination,
  GracefulCompletion,
  GracefulCompletionConditionUnsupportedError,
  InvalidStepResultError,
  Registry,
  StepList,
  Wait,
  deadEnd,
  goTo,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../src/index.js";
import {
  ConditionStatus,
  Context as ProtoContext,
  InvokeExecuteMethodRequest,
  InvokeWaitForMethodRequest,
  WaitingConditionType,
  type StepDecision as ProtoStepDecision,
} from "../src/gen/dex.js";
import type { ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";

const messages = new Channel("messages", stringCodec);

class GracefulWaitStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "GracefulWaitStep";
  }

  public waitFor(_context: Context, input: string): Wait {
    switch (input) {
      case "twice":
        return Wait.anyOf(GracefulCompletion.requested(), GracefulCompletion.requested("second"));
      case "combination":
        return Wait.anyCombinationOf(
          ConditionCombination.of(messages.forOne("message"), GracefulCompletion.requested("graceful")),
        );
      default:
        return Wait.anyOf(messages.forOne(), GracefulCompletion.requested("graceful"));
    }
  }

  public execute(context: Context, _input: string): StepDecision {
    return context.isGracefulCompletionConditionCompleted() ? deadEnd() : goTo(this, "again");
  }
}

class GracefulUnitFlow implements Flow<string> {
  private readonly start = new GracefulWaitStep();

  public getFlowType(): string {
    return "GracefulUnitFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [messages] };
  }
}

test("GracefulCompletion.requested maps to the WaitingCondition at protocol 7", async () => {
  const dispatcher = newDispatcher(7);

  const anyOf = await dispatcher.invokeWaitFor(waitForRequest("single"));
  assert.equal(
    anyOf.waitingCondition?.waitingConditionType,
    WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMPLETED,
  );
  assert.deepEqual(anyOf.waitingCondition?.gracefulCompletionConditions, [{ conditionId: "graceful" }]);
  assert.equal(anyOf.waitingCondition?.channelConditions.length, 1);

  const combination = await dispatcher.invokeWaitFor(waitForRequest("combination"));
  assert.deepEqual(combination.waitingCondition?.conditionCombinations[0]?.conditionIds, ["message", "graceful"]);
  assert.deepEqual(combination.waitingCondition?.gracefulCompletionConditions, [{ conditionId: "graceful" }]);
});

test("GracefulCompletion.requested rejects an empty condition ID", () => {
  assert.throws(() => GracefulCompletion.requested(""), TypeError);
});

test("waitFor fails below Server protocol 7 instead of sending the graceful completion condition", async () => {
  const dispatcher = newDispatcher(6);

  await assert.rejects(dispatcher.invokeWaitFor(waitForRequest("single")), (failure: unknown) => {
    assert.ok(failure instanceof GracefulCompletionConditionUnsupportedError);
    assert.equal(failure.negotiatedServerProtocolVersion, 6);
    assert.equal(failure.requiredServerProtocolVersion, 7);
    assert.match(failure.message, /requires Server protocol 7 or later, but the Server negotiated 6/);
    return true;
  });
});

test("waitFor rejects a second graceful completion condition in one Wait", async () => {
  const dispatcher = newDispatcher(7);

  await assert.rejects(dispatcher.invokeWaitFor(waitForRequest("twice")), (failure: unknown) => {
    assert.ok(failure instanceof InvalidStepResultError);
    assert.match(failure.message, /at most one graceful completion condition/);
    return true;
  });
});

test("isGracefulCompletionConditionCompleted reads the graceful completion result in execute", async () => {
  const dispatcher = newDispatcher(7);

  const completed = await dispatcher.invokeExecute(executeRequest(ConditionStatus.CONDITION_STATUS_COMPLETED));
  assert.deepEqual(nextStepTypes(completed.stepDecision), []);

  const waiting = await dispatcher.invokeExecute(executeRequest(ConditionStatus.CONDITION_STATUS_WAITING));
  assert.deepEqual(nextStepTypes(waiting.stepDecision), ["GracefulWaitStep"]);

  const absent = await dispatcher.invokeExecute(executeRequest(undefined));
  assert.deepEqual(nextStepTypes(absent.stepDecision), ["GracefulWaitStep"]);
});

function newDispatcher(negotiatedServerProtocolVersion: number): WorkerDispatcher {
  return new WorkerDispatcher(
    new Registry([new GracefulUnitFlow()]),
    passthroughHydrator(),
    () => negotiatedServerProtocolVersion,
  );
}

function waitForRequest(input: string): InvokeWaitForMethodRequest {
  return InvokeWaitForMethodRequest.create({
    context: ProtoContext.create({ flowId: "flow-1", stepExecutionId: "step-1" }),
    flowType: "GracefulUnitFlow",
    stepType: "GracefulWaitStep",
    stepInput: { kind: { $case: "stringValue", value: input } },
    attributes: [],
  });
}

function executeRequest(status: ConditionStatus | undefined): InvokeExecuteMethodRequest {
  return InvokeExecuteMethodRequest.create({
    context: ProtoContext.create({ flowId: "flow-1", stepExecutionId: "step-1" }),
    flowType: "GracefulUnitFlow",
    stepType: "GracefulWaitStep",
    stepInput: { kind: { $case: "stringValue", value: "single" } },
    attributes: [],
    conditionResults: {
      channelResults: [],
      timerResults: [],
      waitForFailed: false,
      subFlowResults: [],
      gracefulCompletionResults:
        status === undefined ? [] : [{ conditionId: "graceful", conditionStatus: status }],
    },
  });
}

function nextStepTypes(decision: ProtoStepDecision | undefined): string[] {
  return (decision?.nextSteps ?? []).map((movement) => movement.stepType);
}

function passthroughHydrator(): ValueHydrator {
  return {
    hydrateAll: async (_flowId: string, values: readonly unknown[]) => values,
  } as unknown as ValueHydrator;
}
