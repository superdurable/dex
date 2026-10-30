// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {StepList, StepMovement, ExecuteFailure, StepRef, goTo, goToMany, gracefulComplete, withCancelingSteps, withCancelingSiblingSteps, type Flow, type PersistenceSchema, type Step, type StepDecision, type StepTarget, type Context} from "@superdurable/dex";

// dex:group group-id:instances group-label:"Instances"
// dex:explanation text:"Route the input to another registered Step instance."
class RoutedStep<InputT> implements Step<InputT> {
  constructor(private readonly stepType: string, private readonly target: StepTarget<InputT>, private readonly recovery: StepTarget<InputT>) {}

  getStepType(): string {
    return this.stepType;
  }

  getStepOptions() {
    return {executeFailure: ExecuteFailure.proceedTo(this.recovery)};
  }

  execute(context: Context, input: InputT): StepDecision {
    const destination = this.target;
    if (input === "instance") return goTo(destination, input);
    if (input === "reference") return goTo(new StepRef("Second"), input);
    if (input === "movement") return goToMany(StepMovement.of(this.target, input), StepMovement.of(new StepRef("First"), input));
    return withCancelingSiblingSteps(withCancelingSteps(gracefulComplete(), this.target, new StepRef("First")), new StepRef("Second"));
  }
}

// dex:group group-id:instances group-label:"Instances"
// dex:explanation text:"Complete the Flow after a routed Step fails."
class RecoveryStep implements Step<string> {
  getStepType(): string {
    return "Recovery";
  }

  execute(context: Context, input: string): StepDecision {
    return gracefulComplete();
  }
}

const recovery = new RecoveryStep();
const first = new RoutedStep<string>("First", new StepRef("Second"), recovery);

export class StepInstancesFlow implements Flow<string> {
  private readonly second = new RoutedStep<string>("Second", first, new StepRef("Recovery"));

  getFlowType(): string {
    return "StepInstancesFlow";
  }

  getSteps() {
    const start = first;
    return StepList.startStep(start).otherSteps(this.second, recovery);
  }

  getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}
