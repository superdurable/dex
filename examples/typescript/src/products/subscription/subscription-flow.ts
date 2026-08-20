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
  Channel,
  StepList,
  StepMovement,
  Timer,
  Wait,
  booleanCodec,
  doubleCodec,
  forceComplete,
  goTo,
  goToMulti,
  jsonCodec,
  rpc,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import {
  myDependencyService,
  type MyDependencyService,
} from "../../shared/my-dependency-service.js";
import { SubscriptionBilling } from "./subscription-billing.js";
import { decodeCustomer, type Customer, type Subscription } from "./models.js";

const SUBSCRIPTION_OVER_KEY = "subscription-over";

const customerCodec = jsonCodec<Customer>({
  typeName: "Customer",
  decode: decodeCustomer,
});

export const cancelSubscription = new Channel("cancel-subscription", voidCodec);
export const updateChargeAmount = new Channel("update-charge-amount", doubleCodec);

export class SubscriptionFlow implements Flow<Customer> {
  public readonly billingPeriodNumber = new Attribute("billing-period-number", doubleCodec);
  public readonly customerDetails = new Attribute("customer", customerCodec);

  public readonly initialize = new Initialize(this);
  public readonly trial = new Trial(this);
  public readonly chargeCurrentBill = new ChargeCurrentBill(this);
  public readonly cancel = new Cancel(this);
  public readonly updateChargeAmountStep = new UpdateChargeAmount(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "SubscriptionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initialize).otherSteps(
      this.trial,
      this.chargeCurrentBill,
      this.cancel,
      this.updateChargeAmountStep,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.billingPeriodNumber, this.customerDetails],
      channels: [cancelSubscription, updateChargeAmount],
    };
  }

  @rpc()
  public describe(context: Context): RPCResult<Subscription> {
    return { output: this.customerDetails.get(context).subscription };
  }
}

class Initialize implements Step<Customer> {
  public constructor(private readonly flow: SubscriptionFlow) {}

  public getStepType(): string {
    return "Initialize";
  }

  public execute(context: Context, customer: Customer): StepDecision {
    this.flow.customerDetails.set(context, customer);
    return goToMulti(
      StepMovement.of(this.flow.trial, undefined),
      StepMovement.of(this.flow.cancel, undefined),
      StepMovement.of(this.flow.updateChargeAmountStep, undefined),
    );
  }
}

class Trial implements Step<void> {
  public constructor(private readonly flow: SubscriptionFlow) {}

  public getStepType(): string {
    return "Trial";
  }

  public waitFor(context: Context, _input: void): Wait {
    const customer = this.flow.customerDetails.get(context);
    SubscriptionBilling.sendWelcomeEmail(customer, this.flow.service);
    return Wait.until(Timer.byDuration(SubscriptionBilling.trialPeriod(customer)));
  }

  public execute(context: Context, _input: void): StepDecision {
    this.flow.billingPeriodNumber.set(context, 0);
    return goTo(this.flow.chargeCurrentBill, undefined);
  }
}

class ChargeCurrentBill implements Step<void> {
  public constructor(private readonly flow: SubscriptionFlow) {}

  public getStepType(): string {
    return "ChargeCurrentBill";
  }

  public waitFor(context: Context, _input: void): Wait {
    const customer = this.flow.customerDetails.get(context);
    const periodNumber = this.flow.billingPeriodNumber.get(context);
    if (SubscriptionBilling.isSubscriptionOver(customer, periodNumber)) {
      context.setStepExecutionLocal(SUBSCRIPTION_OVER_KEY, true, booleanCodec);
      return Wait.skipImmediately();
    }
    this.flow.billingPeriodNumber.set(context, periodNumber + 1);
    return Wait.until(Timer.byDuration(SubscriptionBilling.billingPeriod(customer)));
  }

  public execute(context: Context, _input: void): StepDecision {
    const customer = this.flow.customerDetails.get(context);
    const subscriptionOver =
      context.getStepExecutionLocal(SUBSCRIPTION_OVER_KEY, booleanCodec) === true;
    if (subscriptionOver) {
      SubscriptionBilling.sendSubscriptionOverEmail(customer, this.flow.service);
      return forceComplete("subscription ended");
    }
    SubscriptionBilling.chargeCurrentPeriod(customer, this.flow.service);
    return goTo(this.flow.chargeCurrentBill, undefined);
  }
}

class Cancel implements Step<void> {
  public constructor(private readonly flow: SubscriptionFlow) {}

  public getStepType(): string {
    return "Cancel";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(cancelSubscription.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const customer = this.flow.customerDetails.get(context);
    SubscriptionBilling.sendCanceledEmail(customer, this.flow.service);
    return forceComplete("subscription canceled");
  }
}

class UpdateChargeAmount implements Step<void> {
  public constructor(private readonly flow: SubscriptionFlow) {}

  public getStepType(): string {
    return "UpdateChargeAmount";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(updateChargeAmount.forOne());
  }

  public execute(context: Context, _input: void): StepDecision {
    const amounts = updateChargeAmount.results(context);
    const amount = SubscriptionBilling.requireSingleChargeAmount(amounts);
    const customer = this.flow.customerDetails.get(context);
    SubscriptionBilling.applyChargeAmount(customer, amount);
    this.flow.customerDetails.set(context, customer);
    return goTo(this.flow.updateChargeAmountStep, undefined);
  }
}

export const subscriptionFlow = new SubscriptionFlow();
