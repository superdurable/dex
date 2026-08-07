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
  ExecuteFailure,
  StepList,
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

import { DAY_MS, HOUR_MS } from "../../../config/env.js";
import {
  myDependencyService,
  type MyDependencyService,
} from "../../my-dependency-service.js";
import {
  transferRequestCodec,
  type TransferRequest,
} from "./transfer-request.js";

const inputCodec = jsonCodec<TransferRequest>(transferRequestCodec);

export class MoneyTransferFlow implements Flow<TransferRequest> {
  private readonly checkBalance = new CheckBalance(this);
  private readonly createDebitMemo = new CreateDebitMemo(this);
  private readonly debit = new Debit(this);
  private readonly createCreditMemo = new CreateCreditMemo(this);
  private readonly credit = new Credit(this);
  private readonly compensate = new Compensate(this);

  public constructor(public readonly service: MyDependencyService = myDependencyService) {}

  public getFlowType(): string {
    return "MoneyTransferFlow";
  }

  public getSteps() {
    return StepList.startStep(this.checkBalance).otherSteps(
      this.createDebitMemo,
      this.debit,
      this.createCreditMemo,
      this.credit,
      this.compensate,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }

  public compensatedStepOptions(totalDurationMs: number): StepOptions {
    return {
      executeRetry: { totalDurationMs },
      executeFailure: ExecuteFailure.proceedTo(this.compensate, {
        executeRetry: { totalDurationMs: DAY_MS },
      }),
    };
  }

  public get checkBalanceStep(): Step<TransferRequest> {
    return this.checkBalance;
  }

  public get createDebitMemoStep(): Step<TransferRequest> {
    return this.createDebitMemo;
  }

  public get debitStep(): Step<TransferRequest> {
    return this.debit;
  }

  public get createCreditMemoStep(): Step<TransferRequest> {
    return this.createCreditMemo;
  }

  public get creditStep(): Step<TransferRequest> {
    return this.credit;
  }

  public get compensateStep(): Step<TransferRequest> {
    return this.compensate;
  }
}

class CheckBalance implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "CheckBalance";
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    if (!this.flow.service.checkBalance(request.fromAccount, request.amount)) {
      return forceFail("insufficient funds");
    }
    return goTo(this.flow.createDebitMemoStep, request);
  }
}

class CreateDebitMemo implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "CreateDebitMemo";
  }

  public getStepOptions(): StepOptions {
    return this.flow.compensatedStepOptions(HOUR_MS);
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    this.flow.service.createDebitMemo(request.fromAccount, request.amount, request.notes);
    return goTo(this.flow.debitStep, request);
  }
}

class Debit implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "Debit";
  }

  public getStepOptions(): StepOptions {
    return this.flow.compensatedStepOptions(HOUR_MS);
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    this.flow.service.debit(request.fromAccount, request.amount);
    return goTo(this.flow.createCreditMemoStep, request);
  }
}

class CreateCreditMemo implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "CreateCreditMemo";
  }

  public getStepOptions(): StepOptions {
    return this.flow.compensatedStepOptions(HOUR_MS);
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    this.flow.service.createCreditMemo(request.toAccount, request.amount, request.notes);
    return goTo(this.flow.creditStep, request);
  }
}

class Credit implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "Credit";
  }

  public getStepOptions(): StepOptions {
    return this.flow.compensatedStepOptions(HOUR_MS);
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    this.flow.service.credit(request.toAccount, request.amount);
    return gracefulComplete(
      `transfer is done from ${request.fromAccount} to ${request.toAccount} for amount ${request.amount}`,
    );
  }
}

class Compensate implements Step<TransferRequest> {
  public readonly inputCodec = inputCodec;

  public constructor(private readonly flow: MoneyTransferFlow) {}

  public getStepType(): string {
    return "Compensate";
  }

  public getStepOptions(): StepOptions {
    return { executeRetry: { totalDurationMs: DAY_MS } };
  }

  public execute(_context: Context, request: TransferRequest): StepDecision {
    this.flow.service.undoCredit(request.toAccount, request.amount);
    this.flow.service.undoCreateCreditMemo(request.toAccount, request.amount, request.notes);
    this.flow.service.undoCreateDebitMemo(request.fromAccount, request.amount, request.notes);
    this.flow.service.undoDebit(request.fromAccount, request.amount);
    return forceFail(
      `transfer has failed from ${request.fromAccount} to ${request.toAccount} for amount ${request.amount}`,
    );
  }
}

export const moneyTransferFlow = new MoneyTransferFlow();
