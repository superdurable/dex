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
  Registry, StepRef, StepList, doubleCodec, goTo, jsonCodec, stringCodec, type StepTarget,
} from "../src/index.js";
import { Context as ProtoContext, InvokeExecuteMethodRequest } from "../src/gen/dex.js";
import { encodeValue, type ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";
import { registeredFlow, registeredStepTarget } from "../src/flow.js";
import {
  FailingInstanceStep, NumberFailureRecoveryFlow, StepInstancesFlow, TypedInstanceStep,
} from "./integ/step-instances-flow.js";

test("Registry rejects duplicate Step types", () => {
  class DuplicateTypesFlow extends StepInstancesFlow {
    public override getSteps() { return super.getSteps().otherSteps(this.numbers); }
  }
  assert.throws(() => new Registry([new DuplicateTypesFlow()]), /duplicate Step Numbers/);
});

for (const target of [new StepRef("Missing"), TypedInstanceStep]) {
  test(`Registry validates declared failure target ${target instanceof StepRef ? "type" : "class"}`, () => {
    class InvalidFailureFlow extends StepInstancesFlow {
      public override getSteps() {
        return super.getSteps().otherSteps(new FailingInstanceStep(target as StepTarget<void>));
      }
    }
    assert.throws(
      () => new Registry([new InvalidFailureFlow()]),
      target instanceof StepRef ? /not registered: Missing/ : /ambiguous/,
    );
  });
}

test("Registry rejects an execute failure target with a different built-in input codec", () => {
  assert.throws(
    () => new Registry([new NumberFailureRecoveryFlow(stringCodec)]),
    /execute failure target Recovery input string does not match number/,
  );
  new Registry([new NumberFailureRecoveryFlow(doubleCodec)]);
  new Registry([new NumberFailureRecoveryFlow(jsonCodec({ typeName: "Number" }))]);
});

test("runtime target resolution rejects unknown, ambiguous, and unregistered instances", () => {
  const flow = new StepInstancesFlow();
  const registered = registeredFlow(new Registry([flow]), flow);
  assert.throws(() => registeredStepTarget(registered, new StepRef("Missing")), /not registered: Missing/);
  assert.throws(() => registeredStepTarget(registered, TypedInstanceStep), /ambiguous/);
  assert.throws(() => registeredStepTarget(registered, new StepInstancesFlow().numbers), /instance is not registered: TypedInstanceStep Numbers/);
  assert.equal(registeredStepTarget(registered, flow.numbers).name, "Numbers");
  assert.equal(registeredStepTarget(registered, new StepRef("Strings")).name, "Strings");
});

for (const target of [new StepRef("Missing"), TypedInstanceStep]) {
  test(`dispatch reports invalid movement ${target instanceof StepRef ? "type" : "class"}`, async () => {
    const start = new TypedInstanceStep("Start", doubleCodec,
      (input: number) => goTo(target as StepTarget<number>, input));
    class InvalidMovementFlow extends StepInstancesFlow {
      public override getSteps() {
        return StepList.startStep(start).otherSteps(this.numbers, this.strings);
      }
    }
    const flow = new InvalidMovementFlow();
    const hydrator = {
      hydrateAll: async (_flowId: string, values: readonly unknown[]) => values,
    } as unknown as ValueHydrator;
    const dispatcher = new WorkerDispatcher(new Registry([flow]), hydrator, () => 3);
    await assert.rejects(
      dispatcher.invokeExecute(InvokeExecuteMethodRequest.create({
        context: ProtoContext.create(), flowType: flow.getFlowType(), stepType: "Start", stepInput: encodeValue(doubleCodec, 0),
      })),
      target instanceof StepRef ? /not registered: Missing/ : /ambiguous/,
    );
  });
}
