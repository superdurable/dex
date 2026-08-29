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

import {
  serviceDependency,
  type ServiceDependency,
} from "../shared/service-dependency.js";

class PollingStep implements Step<void> {
  public constructor(
    private readonly flow: BackoffPollingFlow,
    private readonly service: ServiceDependency,
  ) {}

  public getStepType(): string { return "PollingStep"; }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        backoffCoefficient: 2,
        maximumAttempts: 5,
        totalDurationMs: 3_600_000,
        initialIntervalMs: 1000,
        maximumIntervalMs: 60_000,
      },
    };
  }

  public execute(_context: Context, _input: void): StepDecision {
    const result = this.service.attemptExternalApiCall("Poll for BackoffPollingFlow");
    return gracefulComplete(result);
  }
}

export class BackoffPollingFlow implements Flow<void> {
  private readonly pollingStep: PollingStep;

  public constructor(service: ServiceDependency = serviceDependency) {
    this.pollingStep = new PollingStep(this, service);
  }

  public getFlowType(): string {
    return "BackoffPollingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.pollingStep);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const backoffPollingFlow = new BackoffPollingFlow();
