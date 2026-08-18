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
  Timer,
  Wait,
  goTo,
  goToMulti,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { type WorkJobParametersInput } from "./work-job-parameters-input.js";

export const DA_INTERRUPT_SIGNAL = "interruptSignal";

class Init implements Step<void> {
  public constructor(private readonly flow: InterruptibleExecutionFlow) {}

  public getStepType(): string {
    return "Init";
  }

  public execute(_context: Context, _input: void): StepDecision {
    const input: WorkJobParametersInput = { jobUpperBound: 15, progress: 1 };
    return goToMulti(
      StepMovement.of(this.flow.workAExecutionStep, input),
      StepMovement.of(this.flow.workNExecutionStep, input),
    );
  }
}

class WorkAExecution implements Step<WorkJobParametersInput> {
  public constructor(private readonly flow: InterruptibleExecutionFlow) {}

  public getStepType(): string {
    return "WorkAExecution";
  }

  public waitFor(_context: Context, _input: WorkJobParametersInput): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  public execute(context: Context, input: WorkJobParametersInput): StepDecision {
    const signal = this.flow.interruptSignal.get(context);
    if (signal === "cancel") {
      console.log("A: Interrupted!");
      return gracefulComplete();
    }

    if (input.progress > input.jobUpperBound) {
      console.log("Executing WorkAExecution completed");
      return gracefulComplete();
    }

    console.log(
      `[${context.flowId}][${context.stepExecutionId}]: Doing job ${input.progress}`,
    );

    return goTo(this.flow.workAExecutionStep, {
      jobUpperBound: input.jobUpperBound,
      progress: input.progress + 1,
    });
  }
}

class WorkNExecution implements Step<WorkJobParametersInput> {
  public constructor(private readonly flow: InterruptibleExecutionFlow) {}

  public getStepType(): string {
    return "WorkNExecution";
  }

  public waitFor(_context: Context, _input: WorkJobParametersInput): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  public execute(context: Context, input: WorkJobParametersInput): StepDecision {
    const signal = this.flow.interruptSignal.get(context);
    if (signal === "cancel") {
      console.log("N: Interrupted!");
      return gracefulComplete();
    }

    if (input.progress > input.jobUpperBound) {
      console.log("Executing WorkNExecution completed");
      return gracefulComplete();
    }

    console.log(
      `[${context.flowId}][${context.stepExecutionId}]: Processing job ${input.progress}`,
    );

    return goTo(this.flow.workNExecutionStep, {
      jobUpperBound: input.jobUpperBound,
      progress: input.progress + 1,
    });
  }
}

export class InterruptibleExecutionFlow implements Flow<void> {
  public readonly interruptSignal = new Attribute(DA_INTERRUPT_SIGNAL, stringCodec);

  private readonly initStep = new Init(this);
  private readonly workAExecution = new WorkAExecution(this);
  private readonly workNExecution = new WorkNExecution(this);

  public get workAExecutionStep(): Step<WorkJobParametersInput> {
    return this.workAExecution;
  }

  public get workNExecutionStep(): Step<WorkJobParametersInput> {
    return this.workNExecution;
  }

  public getFlowType(): string {
    return "InterruptibleExecutionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.initStep).otherSteps(
      this.workAExecution,
      this.workNExecution,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.interruptSignal] };
  }

  @rpc()
  public interrupt(context: Context): void {
    this.interruptSignal.set(context, "cancel");
  }
}

export const interruptibleExecutionFlow = new InterruptibleExecutionFlow();
