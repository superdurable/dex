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
  StepList,
  StepMovement,
  Timer,
  Wait,
  deadEnd,
  goTo,
  goToMulti,
  gracefulComplete,
  jsonCodec,
  stringCodec,
  withCancelingSteps,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

interface Quote {
  carrier: string;
  price: number;
}

const quoteCodec = jsonCodec<Quote>();

class RouteStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: StepDecisionFlow) {}

  public getStepType(): string {
    return "RouteStep";
  }

  public execute(_context: Context, mode: string): StepDecision {
    if (mode === "graceful") {
      return gracefulComplete("done");
    }
    if (mode === "dead-end") {
      return goToMulti(
        StepMovement.of(this.flow.branchWorkerStep, "left"),
        StepMovement.of(this.flow.branchWorkerStep, "right"),
      );
    }
    const quote: Quote = { carrier: "winner", price: 9 };
    return goToMulti(
      StepMovement.of(this.flow.carrierAStep, { carrier: "A", price: 10 }),
      StepMovement.of(this.flow.carrierBStep, { carrier: "B", price: 12 }),
      StepMovement.of(this.flow.winnerStep, quote),
    );
  }
}

class BranchWorkerStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "BranchWorkerStep";
  }

  public execute(_context: Context, _input: string): StepDecision {
    return deadEnd();
  }
}

class CarrierAStep implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "CarrierAStep";
  }

  public waitFor(_context: Context, _quote: Quote): Wait {
    return Wait.until(Timer.byDuration(2_000));
  }

  public execute(_context: Context, _quote: Quote): StepDecision {
    return deadEnd();
  }
}

class CarrierBStep implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "CarrierBStep";
  }

  public waitFor(_context: Context, _quote: Quote): Wait {
    return Wait.until(Timer.byDuration(2_000));
  }

  public execute(_context: Context, _quote: Quote): StepDecision {
    return deadEnd();
  }
}

class WinnerStep implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public constructor(private readonly flow: StepDecisionFlow) {}

  public getStepType(): string {
    return "WinnerStep";
  }

  public execute(_context: Context, quote: Quote): StepDecision {
    return withCancelingSteps(
      goTo(this.flow.recordQuoteStep, quote),
      this.flow.carrierAStep,
      this.flow.carrierBStep,
    );
  }
}

class RecordQuoteStep implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "RecordQuoteStep";
  }

  public execute(_context: Context, quote: Quote): StepDecision {
    return gracefulComplete(quote);
  }
}

export class StepDecisionFlow implements Flow<string> {
  private readonly route = new RouteStep(this);
  private readonly branchWorker = new BranchWorkerStep();
  private readonly carrierA = new CarrierAStep();
  private readonly carrierB = new CarrierBStep();
  private readonly winner = new WinnerStep(this);
  private readonly recordQuote = new RecordQuoteStep();

  public get branchWorkerStep(): Step<string> {
    return this.branchWorker;
  }

  public get carrierAStep(): Step<Quote> {
    return this.carrierA;
  }

  public get carrierBStep(): Step<Quote> {
    return this.carrierB;
  }

  public get winnerStep(): Step<Quote> {
    return this.winner;
  }

  public get recordQuoteStep(): Step<Quote> {
    return this.recordQuote;
  }

  public getFlowType(): string {
    return "StepDecisionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.route).otherSteps(
      this.carrierA,
      this.carrierB,
      this.winner,
      this.recordQuote,
      this.branchWorker,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const stepDecisionFlow = new StepDecisionFlow();
