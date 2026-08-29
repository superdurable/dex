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

import { StepList, goTo, gracefulComplete, stringCodec, type Context, type Flow, type PersistenceSchema, type Step, type StepDecision } from "@superdurable/dex";

class IterationStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string { return "IterationStep"; }
  public execute(_context: Context, pageToken: string): StepDecision {
    const nextPageToken = pageToken === "" ? "page-2" : pageToken === "page-2" ? "page-3" : "";
    return nextPageToken === "" ? gracefulComplete() : goTo(IterationStep, nextPageToken);
  }
}

export class IterationFlow implements Flow<string> {
  private readonly iterationStep = new IterationStep();
  public getFlowType(): string { return "IterationFlow"; }
  public getSteps() { return StepList.startStep(this.iterationStep); }
  public getPersistenceSchema(): PersistenceSchema { return {}; }
}
export const iterationFlow = new IterationFlow();
