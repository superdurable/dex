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
  Channel,
  FlowClosingError,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  StepList,
  StopType,
  SubFlow,
  SubFlowReusePolicy,
  Wait,
  forceFail,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Client,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import type { CreditCheckFlow } from "./credit-check-flow.js";

/**
 * The child's own Flow timeout. It stops the child even when the parent
 * closes without stopping it.
 */
export const CREDIT_CHECK_TIMEOUT_MS = 7 * 24 * 60 * 60 * 1000;

const APPLICATION_CANCELED_PREFIX = "application canceled: ";

/** The input of LoanApplicationFlow. */
export interface LoanApplication {
  readonly applicantId: string;
  readonly creditReportDelaySeconds: number;
}

export const stoppedCreditCheckFlowId = new Attribute("stoppedCreditCheckFlowId", stringCodec);
export const applicationCanceled = new Channel("applicationCanceled", stringCodec);
export const underwriterDecided = new Channel("underwriterDecided", stringCodec);

/** Waits for its child Flow or a cancellation, whichever comes first. */
class RunCreditCheck implements Step<LoanApplication> {
  public constructor(
    private readonly getClient: () => Client,
    private readonly creditCheckFlow: CreditCheckFlow,
  ) {}

  public getStepType(): string {
    return "RunCreditCheck";
  }

  public waitFor(_context: Context, application: LoanApplication): Wait {
    // The cancel condition comes first: it wins when both are ready.
    return Wait.anyOf(
      applicationCanceled.forOne(),
      SubFlow.run(
        this.creditCheckFlow,
        {
          applicantId: application.applicantId,
          reportDelaySeconds: application.creditReportDelaySeconds,
        },
        {
          timeoutMs: CREDIT_CHECK_TIMEOUT_MS,
          reusePolicy: SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY,
        },
      ),
    );
  }

  public async execute(context: Context, _application: LoanApplication): Promise<StepDecision> {
    const reasons = applicationCanceled.results(context);
    if (reasons.length > 0) {
      const childFlowId = SubFlow.getFlowId(context);
      await this.stopCreditCheck(childFlowId);
      stoppedCreditCheckFlowId.set(context, childFlowId);
      return forceFail(APPLICATION_CANCELED_PREFIX + reasons[0]);
    }
    const result = SubFlow.getConditionResults(context);
    if (result.status !== "completed") {
      return forceFail(`credit check ended with status ${result.status}`);
    }
    return goTo(AwaitUnderwriterDecision, result.singleOutput(stringCodec));
  }

  /** Cancels the child; a missing or already closed child counts as stopped. */
  private async stopCreditCheck(childFlowId: string): Promise<void> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    try {
      await getClient().stopFlow(childFlowId, {
        type: StopType.CANCEL,
        reason: "loan application canceled",
      });
    } catch (error) {
      if (
        !(error instanceof FlowNotFoundError) &&
        !(error instanceof FlowNotActiveOrNotFoundError) &&
        !(error instanceof FlowClosingError)
      ) {
        throw error;
      }
    }
  }
}

/**
 * Also waits for the cancel Channel, so a cancellation after the credit check
 * still closes the application.
 */
class AwaitUnderwriterDecision implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "AwaitUnderwriterDecision";
  }

  public waitFor(_context: Context, _report: string): Wait {
    return Wait.anyOf(applicationCanceled.forOne(), underwriterDecided.forOne());
  }

  public execute(context: Context, report: string): StepDecision {
    const reasons = applicationCanceled.results(context);
    if (reasons.length > 0) {
      return forceFail(APPLICATION_CANCELED_PREFIX + reasons[0]);
    }
    return gracefulComplete(`${underwriterDecided.results(context)[0]} after ${report}`);
  }
}

/**
 * Runs a credit check in a child Flow. Canceling the application also stops
 * the child Flow.
 */
export class LoanApplicationFlow implements Flow<LoanApplication> {
  private readonly runCreditCheck: RunCreditCheck;

  /**
   * @param getClient - Returns the Client that stops the child; it is called only during execute.
   * @param creditCheckFlow - The registered child Flow.
   */
  public constructor(getClient: () => Client, creditCheckFlow: CreditCheckFlow) {
    this.runCreditCheck = new RunCreditCheck(getClient, creditCheckFlow);
  }

  public getFlowType(): string {
    return "LoanApplicationFlow";
  }

  public getSteps(): StepList<LoanApplication> {
    return StepList.startStep(this.runCreditCheck).otherSteps(new AwaitUnderwriterDecision());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [stoppedCreditCheckFlowId],
      channels: [applicationCanceled, underwriterDecided],
    };
  }

  /** Cancels the application and the credit check it started. */
  @rpc({ inputCodec: stringCodec })
  public cancelApplication(context: Context, reason: string): void {
    applicationCanceled.publish(context, reason);
  }

  /** Records the underwriter's decision. */
  @rpc({ inputCodec: stringCodec })
  public decideApplication(context: Context, decision: string): void {
    underwriterDecided.publish(context, decision);
  }
}
