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
  Channel,
  StepList,
  Wait,
  doubleCodec,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

const approval = new Channel("Approval", stringCodec);

class NoteWaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "NoteWaitStep";
  }

  public waitFor(context: Context, input: number): Wait {
    context.setStepExecutionLocal("note", `approval:${input}`, stringCodec);
    return Wait.until(approval.forOne());
  }

  public execute(context: Context, _input: number): StepDecision {
    const note = context.getStepExecutionLocal("note", stringCodec) ?? "";
    return gracefulComplete(note);
  }
}

export class StepExecutionLocalFlow implements Flow<number> {
  private readonly noteWait = new NoteWaitStep();

  public getFlowType(): string {
    return "StepExecutionLocalFlow";
  }

  public getSteps() {
    return StepList.startStep(this.noteWait);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { channels: [approval] };
  }
}

export const stepExecutionLocalFlow = new StepExecutionLocalFlow();
