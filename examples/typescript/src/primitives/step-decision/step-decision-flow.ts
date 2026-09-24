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
  goToMany,
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

class Route implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: StepDecisionFlow) {}

  public getStepType(): string {
    return "Route";
  }

  public execute(_context: Context, mode: string): StepDecision {
    if (mode === "graceful") {
      return gracefulComplete("done");
    }
    if (mode === "dead-end") {
      return goToMany(
        StepMovement.of(BranchWorker, "left"),
        StepMovement.of(BranchWorker, "right"),
      );
    }
    const quote: Quote = { carrier: "winner", price: 9 };
    return goToMany(
      StepMovement.of(CarrierA, { carrier: "A", price: 10 }),
      StepMovement.of(CarrierB, { carrier: "B", price: 12 }),
      StepMovement.of(Winner, quote),
    );
  }
}

class BranchWorker implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "BranchWorker";
  }

  public execute(_context: Context, _input: string): StepDecision {
    return deadEnd();
  }
}

class CarrierA implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "CarrierA";
  }

  public waitFor(_context: Context, _quote: Quote): Wait {
    return Wait.until(Timer.byDuration(2_000));
  }

  public execute(_context: Context, _quote: Quote): StepDecision {
    return deadEnd();
  }
}

class CarrierB implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "CarrierB";
  }

  public waitFor(_context: Context, _quote: Quote): Wait {
    return Wait.until(Timer.byDuration(2_000));
  }

  public execute(_context: Context, _quote: Quote): StepDecision {
    return deadEnd();
  }
}

class Winner implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public constructor(private readonly flow: StepDecisionFlow) {}

  public getStepType(): string {
    return "Winner";
  }

  public execute(_context: Context, quote: Quote): StepDecision {
    return withCancelingSteps(
      goTo(RecordQuote, quote),
      CarrierA,
      CarrierB,
    );
  }
}

class RecordQuote implements Step<Quote> {
  public readonly inputCodec = quoteCodec;

  public getStepType(): string {
    return "RecordQuote";
  }

  public execute(_context: Context, quote: Quote): StepDecision {
    return gracefulComplete(quote);
  }
}

export class StepDecisionFlow implements Flow<string> {
  private readonly route = new Route(this);
  private readonly branchWorker = new BranchWorker();
  private readonly carrierA = new CarrierA();
  private readonly carrierB = new CarrierB();
  private readonly winner = new Winner(this);
  private readonly recordQuote = new RecordQuote();

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
