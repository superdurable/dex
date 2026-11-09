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

// Workflow Patterns WCP-1 to WCP-5: sequence, parallel split, synchronization,
// exclusive choice, and simple merge.

import {
  Channel,
  StepList,
  StepMovement,
  Wait,
  deadEnd,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

// WCP-1 Sequence: each Step starts after the previous one completes.

class ReceiveOrder implements Step<string[]> {
  public getStepType(): string {
    return "ReceiveOrder";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return goTo(CheckInventory, [...trail, "ReceiveOrder"]);
  }
}

class CheckInventory implements Step<string[]> {
  public getStepType(): string {
    return "CheckInventory";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return goTo(ShipOrder, [...trail, "CheckInventory"]);
  }
}

class ShipOrder implements Step<string[]> {
  public getStepType(): string {
    return "ShipOrder";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return gracefulComplete([...trail, "ShipOrder"]);
  }
}

export class SequenceFlow implements Flow<string[]> {
  private readonly receiveOrder = new ReceiveOrder();
  private readonly checkInventory = new CheckInventory();
  private readonly shipOrder = new ShipOrder();

  public getFlowType(): string {
    return "SequenceFlow";
  }

  public getSteps(): StepList<string[]> {
    return StepList.startStep(this.receiveOrder).otherSteps(this.checkInventory, this.shipOrder);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

// WCP-2 Parallel Split and WCP-3 Synchronization: GoToMany starts the branches,
// and the join Step waits for one Channel message from each branch.

const fulfillmentBranchCompleted = new Channel("fulfillmentBranchCompleted", stringCodec);
const paymentApproved = new Channel("paymentApproved", stringCodec);
const FULFILLMENT_BRANCH_COUNT = 2;

class SplitOrder implements Step<void> {
  public getStepType(): string {
    return "SplitOrder";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(PackItems, undefined),
      StepMovement.of(ChargePayment, undefined),
      StepMovement.of(SynchronizeFulfillment, undefined),
    );
  }
}

class PackItems implements Step<void> {
  public getStepType(): string {
    return "PackItems";
  }

  public execute(context: Context, _input: void): StepDecision {
    fulfillmentBranchCompleted.publish(context, "PackItems");
    return deadEnd();
  }
}

class ChargePayment implements Step<void> {
  public getStepType(): string {
    return "ChargePayment";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(paymentApproved.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    fulfillmentBranchCompleted.publish(context, "ChargePayment");
    return deadEnd();
  }
}

class SynchronizeFulfillment implements Step<void> {
  public getStepType(): string {
    return "SynchronizeFulfillment";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(fulfillmentBranchCompleted.forN(FULFILLMENT_BRANCH_COUNT));
  }

  public execute(context: Context, _input: void): StepDecision {
    return gracefulComplete([...fulfillmentBranchCompleted.results(context)].sort());
  }
}

export class ParallelSplitSynchronizationFlow implements Flow<void> {
  private readonly splitOrder = new SplitOrder();
  private readonly packItems = new PackItems();
  private readonly chargePayment = new ChargePayment();
  private readonly synchronizeFulfillment = new SynchronizeFulfillment();

  public getFlowType(): string {
    return "ParallelSplitSynchronizationFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.startStep(this.splitOrder).otherSteps(
      this.packItems,
      this.chargePayment,
      this.synchronizeFulfillment,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [fulfillmentBranchCompleted, paymentApproved] };
  }

  @rpc()
  public approvePayment(context: Context): void {
    paymentApproved.publish(context, "approved");
  }
}

// WCP-4 Exclusive Choice and WCP-5 Simple Merge: Execute picks exactly one
// branch, and both branches go to the same next Step.

const MANUAL_REVIEW_THRESHOLD = 1000;

class RouteClaim implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "RouteClaim";
  }

  public execute(_context: Context, claimAmount: number): StepDecision {
    if (claimAmount >= MANUAL_REVIEW_THRESHOLD) {
      return goTo(ManualReview, ["RouteClaim"]);
    }
    return goTo(AutoApprove, ["RouteClaim"]);
  }
}

class ManualReview implements Step<string[]> {
  public getStepType(): string {
    return "ManualReview";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return goTo(CloseClaim, [...trail, "ManualReview"]);
  }
}

class AutoApprove implements Step<string[]> {
  public getStepType(): string {
    return "AutoApprove";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return goTo(CloseClaim, [...trail, "AutoApprove"]);
  }
}

class CloseClaim implements Step<string[]> {
  public getStepType(): string {
    return "CloseClaim";
  }

  public execute(_context: Context, trail: string[]): StepDecision {
    return gracefulComplete([...trail, "CloseClaim"]);
  }
}

export class ExclusiveChoiceSimpleMergeFlow implements Flow<number> {
  private readonly routeClaim = new RouteClaim();
  private readonly manualReview = new ManualReview();
  private readonly autoApprove = new AutoApprove();
  private readonly closeClaim = new CloseClaim();

  public getFlowType(): string {
    return "ExclusiveChoiceSimpleMergeFlow";
  }

  public getSteps(): StepList<number> {
    return StepList.startStep(this.routeClaim).otherSteps(
      this.manualReview,
      this.autoApprove,
      this.closeClaim,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const sequenceFlow = new SequenceFlow();
export const parallelSplitSynchronizationFlow = new ParallelSplitSynchronizationFlow();
export const exclusiveChoiceSimpleMergeFlow = new ExclusiveChoiceSimpleMergeFlow();
