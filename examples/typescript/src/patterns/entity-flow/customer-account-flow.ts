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
} from "@superdurable/dex";

export interface ContactDetails {
  readonly displayName: string;
  readonly email: string;
}

export interface CreditAdjustment {
  readonly amount: number;
}

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

const creditBalance = new Attribute("credit_balance", int64Codec);

// dex:group group-id:account-retirement group-label:"Account retirement"
// dex:explanation text:"Complete the account Flow after the retire request passes its checks."
export class CloseAccountStep implements Step<void> {
  public getStepType(): string {
    return "CloseAccountStep";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete();
  }
}

export class CustomerAccountFlow implements Flow<void> {
  public readonly displayName = new Attribute("display_name", stringCodec);
  public readonly email = new Attribute("email", stringCodec);
  public readonly creditBalance = creditBalance;
  private readonly closeAccount = new CloseAccountStep();

  public getFlowType(): string {
    return "CustomerAccountFlow";
  }

  public getSteps(): StepList<void> {
    return StepList.empty<void>().otherSteps(this.closeAccount);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.displayName, this.email, this.creditBalance] };
  }

  @rpc({ name: "UpdateContactDetails", inputCodec: jsonCodec<ContactDetails>() })
  public updateContactDetails(context: Context, contact: ContactDetails): void {
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
    if (adjustment.amount === 0) {
      throw new Error("amount must not be zero");
    }
    const balance = this.creditBalance.get(context) + BigInt(adjustment.amount);
    if (balance < 0n) {
      throw new Error("credit balance cannot go below zero");
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
    if (this.creditBalance.get(context) !== 0n) {
      throw new Error("spend or refund the credit balance before retiring");
    }
    return {
      output: undefined,
      nextSteps: [StepMovement.of(CloseAccountStep, undefined)],
    };
  }
}

export const customerAccountFlow = new CustomerAccountFlow();
