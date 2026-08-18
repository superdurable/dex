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
  ExecuteFailure,
  StepList,
  doubleCodec,
  forceFail,
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
  failureRecoveryWorkflowInputCodec,
  type FailureRecoveryWorkflowInput,
} from "./failure-recovery-workflow-input.js";

export const WORKFLOW_INPUT_KEY = "workflow-input-data-attribute-key";

const workflowInputCodec = jsonCodec<FailureRecoveryWorkflowInput>(
  failureRecoveryWorkflowInputCodec,
);
const quantityInputCodec = doubleCodec;

class DatabaseConnection {
  public reduceQuantity(_itemName: string, quantity: number): void {
    console.log(`Reducing quantity: ${quantity}`);
    if (quantity > Math.floor(Math.random() * 10)) {
      throw new Error("not enough items available");
    }
  }

  public increaseQuantity(_itemName: string, quantity: number): void {
    console.log(`Increasing quantity: ${quantity}`);
  }

  public getItemPrice(_itemName: string): number {
    return 3.14;
  }
}

class PaymentProcessor {
  public processPayment(_price: number): void {
    throw new Error("Payment could not be processed");
  }

  public voidPayment(price: number): void {
    console.log(`Voiding payment for $ ${price.toFixed(2)}`);
  }
}

class UpdateItemQuantity implements Step<FailureRecoveryWorkflowInput> {
  public readonly inputCodec = workflowInputCodec;

  public constructor(
    private readonly flow: FailureRecoveryFlow,
    private readonly database: DatabaseConnection,
  ) {}

  public getStepType(): string {
    return "UpdateItemQuantity";
  }

  public getStepOptions(): StepOptions {
    return {
      executeFailure: ExecuteFailure.proceedTo(this.flow.updateQuantityRecoveryStep, {
        executeRetry: { maximumAttempts: 5 },
      }),
      executeRetry: { maximumAttempts: 5 },
    };
  }

  public execute(context: Context, input: FailureRecoveryWorkflowInput): StepDecision {
    this.flow.workflowInput.set(context, input);
    this.database.reduceQuantity(input.itemName, input.requestedQuantity);
    return goTo(this.flow.chargeForItemsStep, input.requestedQuantity);
  }
}

class ChargeForItems implements Step<number> {
  public readonly inputCodec = quantityInputCodec;

  public constructor(
    private readonly flow: FailureRecoveryFlow,
    private readonly database: DatabaseConnection,
    private readonly paymentProcessor: PaymentProcessor,
  ) {}

  public getStepType(): string {
    return "ChargeForItems";
  }

  public getStepOptions(): StepOptions {
    return {
      executeFailure: ExecuteFailure.proceedTo(this.flow.voidPaymentRecoveryStep, {
        executeRetry: { maximumAttempts: 5 },
      }),
      executeRetry: { maximumAttempts: 5 },
    };
  }

  public execute(context: Context, _quantityRequested: number): StepDecision {
    const input = this.flow.workflowInput.get(context);
    const itemValue = this.database.getItemPrice(input.itemName);
    const orderValue = input.requestedQuantity * itemValue;
    this.paymentProcessor.processPayment(orderValue);
    return gracefulComplete();
  }
}

class UpdateQuantityRecovery implements Step<FailureRecoveryWorkflowInput> {
  public readonly inputCodec = workflowInputCodec;

  public constructor(private readonly database: DatabaseConnection) {}

  public getStepType(): string {
    return "UpdateQuantityRecovery";
  }

  public execute(_context: Context, input: FailureRecoveryWorkflowInput): StepDecision {
    this.database.increaseQuantity(input.itemName, input.requestedQuantity);
    return forceFail("Failed to process transaction");
  }
}

class VoidPaymentRecovery implements Step<number> {
  public readonly inputCodec = quantityInputCodec;

  public constructor(
    private readonly flow: FailureRecoveryFlow,
    private readonly database: DatabaseConnection,
    private readonly paymentProcessor: PaymentProcessor,
  ) {}

  public getStepType(): string {
    return "VoidPaymentRecovery";
  }

  public execute(context: Context, _input: number): StepDecision {
    const workflow = this.flow.workflowInput.get(context);
    const itemValue = this.database.getItemPrice(workflow.itemName);
    const orderValue = workflow.requestedQuantity * itemValue;
    this.paymentProcessor.voidPayment(orderValue);
    return goTo(this.flow.updateQuantityRecoveryStep, workflow);
  }
}

export class FailureRecoveryFlow implements Flow<FailureRecoveryWorkflowInput> {
  public readonly workflowInput = new Attribute(WORKFLOW_INPUT_KEY, workflowInputCodec);

  private readonly database = new DatabaseConnection();
  private readonly paymentProcessor = new PaymentProcessor();
  private readonly updateItemQuantity: UpdateItemQuantity;
  private readonly chargeForItems: ChargeForItems;
  private readonly updateQuantityRecovery: UpdateQuantityRecovery;
  private readonly voidPaymentRecovery: VoidPaymentRecovery;

  public constructor() {
    this.updateQuantityRecovery = new UpdateQuantityRecovery(this.database);
    this.voidPaymentRecovery = new VoidPaymentRecovery(
      this,
      this.database,
      this.paymentProcessor,
    );
    this.updateItemQuantity = new UpdateItemQuantity(this, this.database);
    this.chargeForItems = new ChargeForItems(
      this,
      this.database,
      this.paymentProcessor,
    );
  }

  public get updateQuantityRecoveryStep(): Step<FailureRecoveryWorkflowInput> {
    return this.updateQuantityRecovery;
  }

  public get voidPaymentRecoveryStep(): Step<number> {
    return this.voidPaymentRecovery;
  }

  public get chargeForItemsStep(): Step<number> {
    return this.chargeForItems;
  }

  public getFlowType(): string {
    return "FailureRecoveryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.updateItemQuantity).otherSteps(
      this.chargeForItems,
      this.updateQuantityRecovery,
      this.voidPaymentRecovery,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.workflowInput] };
  }
}

export const failureRecoveryFlow = new FailureRecoveryFlow();
