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
  gracefulComplete,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

/** The input of one credit check child Flow. */
export interface CreditCheckRequest {
  readonly applicantId: string;
  /** Simulates how long the credit bureau takes to answer. */
  readonly reportDelaySeconds: number;
}

/** Waits for the simulated credit bureau to answer. */
class AwaitCreditReport implements Step<CreditCheckRequest> {
  public getStepType(): string {
    return "AwaitCreditReport";
  }

  public waitFor(_context: Context, request: CreditCheckRequest): Wait {
    return Wait.until(Timer.byDuration(request.reportDelaySeconds * 1000));
  }

  public execute(_context: Context, request: CreditCheckRequest): StepDecision {
    return gracefulComplete(`credit report for ${request.applicantId}`);
  }
}

/** The child Flow that LoanApplicationFlow starts. */
export class CreditCheckFlow implements Flow<CreditCheckRequest> {
  public getFlowType(): string {
    return "CreditCheckFlow";
  }

  public getSteps(): StepList<CreditCheckRequest> {
    return StepList.startStep(new AwaitCreditReport());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}

export const creditCheckFlow = new CreditCheckFlow();
