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
  booleanCodec,
  StepMovement,
  gracefulComplete,
  int64Codec,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { FakeAccountArchive } from "./account-archive.js";

export interface ContactDetails {
  readonly displayName: string;
  readonly email: string;
}

export interface CreditAdjustment {
  readonly amount: number;
}

const MAX_CREDIT_BALANCE = BigInt(Number.MAX_SAFE_INTEGER);

export function customerAccountFlowId(accountId: string): string {
  return `customer-account-${accountId}`;
}

export function validateContactDetails(contact: ContactDetails): void {
  if (contact.displayName.trim().length === 0) {
    throw new Error("display name is required");
  }
  if (!contact.email.includes("@")) {
    throw new Error("email must contain @");
  }
}

export function validateCreditAmount(amount: unknown): number {
  if (typeof amount !== "number" || !Number.isSafeInteger(amount)) {
    throw new Error("amount must be a safe integer");
  }
  if (amount === 0) {
    throw new Error("amount must not be zero");
  }
  return amount;
}

const creditBalance = new Attribute("credit_balance", int64Codec);
const retired = new Attribute("retired", booleanCodec);

// dex:group group-id:account-retirement group-label:"Account retirement"
// dex:explanation text:"Archive the retired account, then complete its Flow."
export class CloseAccountStep implements Step<void> {
  public constructor(private readonly archive: FakeAccountArchive) {}

  public getStepType(): string {
    return "CloseAccountStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { initialIntervalMs: 1_000, maximumIntervalMs: 1_000 },
    };
  }

  public execute(context: Context, _input: void): StepDecision {
    this.archive.archiveAccount(context.flowId, context.stepExecutionId);
    return gracefulComplete();
  }
}

export class CustomerAccountFlow implements Flow<void> {
  public readonly displayName = new Attribute("display_name", stringCodec);
  public readonly email = new Attribute("email", stringCodec);
  public readonly creditBalance = creditBalance;
  public readonly retired = retired;
  private readonly closeAccount: CloseAccountStep;

  public constructor(archive: FakeAccountArchive) {
    this.closeAccount = new CloseAccountStep(archive);
  }

  public getFlowType(): string {
    return "CustomerAccountFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty<void>().otherSteps(this.closeAccount);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.displayName, this.email, this.creditBalance, this.retired],
    };
  }

  @rpc({
    name: "UpdateContactDetails",
    inputCodec: jsonCodec<ContactDetails>(),
    lockAttributes: [creditBalance.lock()],
  })
  public updateContactDetails(context: Context, contact: ContactDetails): void {
    this.requireOpenAccount(context);
    validateContactDetails(contact);
    this.displayName.set(context, contact.displayName);
    this.email.set(context, contact.email);
  }

  @rpc({
    name: "AdjustCreditBalance",
    inputCodec: jsonCodec<CreditAdjustment>(),
    outputCodec: int64Codec,
    lockAttributes: [creditBalance.lock()],
  })
  public adjustCreditBalance(
    context: Context,
    adjustment: CreditAdjustment,
  ): RPCResult<bigint> {
    this.requireOpenAccount(context);
    const amount = validateCreditAmount(adjustment.amount);
    const balance = this.creditBalance.get(context) + BigInt(amount);
    if (balance < 0n) {
      throw new Error("credit balance cannot go below zero");
    }
    if (balance > MAX_CREDIT_BALANCE) {
      throw new Error("credit balance cannot exceed the largest safe integer");
    }
    this.creditBalance.set(context, balance);
    return { output: balance };
  }

  @rpc({
    name: "RetireAccount",
    outputCodec: voidCodec,
    lockAttributes: [creditBalance.lock()],
  })
  public retireAccount(context: Context): RPCResult<void> {
    if (this.retired.get(context)) {
      return { output: undefined };
    }
    if (this.creditBalance.get(context) !== 0n) {
      throw new Error("spend or refund the credit balance before retiring");
    }
    this.retired.set(context, true);
    return {
      output: undefined,
      nextSteps: [StepMovement.of(CloseAccountStep, undefined)],
    };
  }

  private requireOpenAccount(context: Context): void {
    if (this.retired.get(context)) {
      throw new Error("account is retired");
    }
  }
}

export const accountArchive = new FakeAccountArchive();
export const customerAccountFlow = new CustomerAccountFlow(accountArchive);
