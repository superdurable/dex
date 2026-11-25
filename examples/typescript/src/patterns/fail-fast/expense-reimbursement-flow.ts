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

import {
  Attribute,
  StepList,
  goTo,
  gracefulComplete,
  jsonCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import {
  PayoutRejectedError,
  type ExpenseClaim,
  type FakePaymentProvider,
} from "./fake-payment-provider.js";

export interface ClaimRejection {
  readonly reason: string;
  readonly submitAttempt: number;
}

export interface PayoutOutcome {
  readonly status: "PAID" | "REJECTED";
  readonly detail: string;
  readonly submitAttempt: number;
}

export const payoutOutcome = new Attribute("PayoutOutcome", jsonCodec<PayoutOutcome>());

// dex:group group-id:rejection group-label:"Rejection"
// dex:explanation text:"Record the rejected claim and its reason."
class RecordRejectedClaim implements Step<ClaimRejection> {
  public readonly inputCodec = jsonCodec<ClaimRejection>();

  public getStepType(): string {
    return "RecordRejectedClaim";
  }

  public execute(context: Context, rejection: ClaimRejection): StepDecision {
    payoutOutcome.set(context, {
      status: "REJECTED",
      detail: rejection.reason,
      submitAttempt: rejection.submitAttempt,
    });
    return gracefulComplete(`rejected: ${rejection.reason}`);
  }
}

// dex:group group-id:payout group-label:"Payout"
// dex:explanation text:"Submit the payout; retry a timeout and route a rejection at once."
export class SubmitPayout implements Step<ExpenseClaim> {
  public readonly inputCodec = jsonCodec<ExpenseClaim>();

  public constructor(private readonly provider: FakePaymentProvider) {}

  public getStepType(): string {
    return "SubmitPayout";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        initialIntervalMs: 1_000,
        backoffCoefficient: 2,
        maximumIntervalMs: 5 * 60_000,
        totalDurationMs: 60 * 60_000,
      },
    };
  }

  public execute(context: Context, claim: ExpenseClaim): StepDecision {
    let payoutId: string;
    try {
      payoutId = this.provider.submitPayout(context.flowId, claim);
    } catch (error) {
      if (error instanceof PayoutRejectedError) {
        return goTo(RecordRejectedClaim, { reason: error.message, submitAttempt: context.attempt });
      }
      throw error;
    }
    payoutOutcome.set(context, { status: "PAID", detail: payoutId, submitAttempt: context.attempt });
    return gracefulComplete(payoutId);
  }
}

export class ExpenseReimbursementFlow implements Flow<ExpenseClaim> {
  public readonly submitPayout: SubmitPayout;
  private readonly recordRejectedClaim = new RecordRejectedClaim();

  public constructor(provider: FakePaymentProvider) {
    this.submitPayout = new SubmitPayout(provider);
  }

  public getFlowType(): string {
    return "ExpenseReimbursementFlow";
  }

  public getSteps(): StepList<ExpenseClaim> {
    return StepList.startStep(this.submitPayout).otherSteps(this.recordRejectedClaim);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [payoutOutcome] };
  }
}
