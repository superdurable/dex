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
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

class RouteDurabilityStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: DurabilityFlow) {}

  public getStepType(): string {
    return "RouteDurabilityStep";
  }

  public execute(_context: Context, mode: string): StepDecision {
    if (mode === "async") {
      return goTo(this.flow.asyncWorkStep, mode);
    }
    return goTo(this.flow.syncWorkStep, mode);
  }
}

class SyncWorkStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: DurabilityFlow) {}

  public getStepType(): string {
    return "SyncWorkStep";
  }

  public getStepOptions(): StepOptions {
    return { executeDurability: "sync" };
  }

  public execute(_context: Context, mode: string): StepDecision {
    return goTo(this.flow.finishStep, `sync:${mode}`);
  }
}

class AsyncWorkStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: DurabilityFlow) {}

  public getStepType(): string {
    return "AsyncWorkStep";
  }

  public getStepOptions(): StepOptions {
    return { executeDurability: "async" };
  }

  public execute(_context: Context, mode: string): StepDecision {
    return goTo(this.flow.finishStep, `async:${mode}`);
  }
}

class FinishDurabilityStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "FinishDurabilityStep";
  }

  public waitFor(_context: Context, _label: string): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  public execute(_context: Context, label: string): StepDecision {
    return gracefulComplete(label);
  }
}

export class DurabilityFlow implements Flow<string> {
  private readonly route = new RouteDurabilityStep(this);
  private readonly syncWork = new SyncWorkStep(this);
  private readonly asyncWork = new AsyncWorkStep(this);
  private readonly finish = new FinishDurabilityStep();

  public get syncWorkStep(): Step<string> {
    return this.syncWork;
  }

  public get asyncWorkStep(): Step<string> {
    return this.asyncWork;
  }

  public get finishStep(): Step<string> {
    return this.finish;
  }

  public getFlowType(): string {
    return "DurabilityFlow";
  }

  public getSteps() {
    return StepList.startStep(this.route).otherSteps(this.syncWork, this.asyncWork, this.finish);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const durabilityFlow = new DurabilityFlow();
