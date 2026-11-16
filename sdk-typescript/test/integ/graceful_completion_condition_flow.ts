// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  Channel,
  GracefulCompletion,
  StepList,
  Wait,
  deadEnd,
  goTo,
  jsonCodec,
  rpc,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
} from "../../src/index.js";

/** One execute call of the message waiter: what it consumed and whether graceful completed. */
export interface GracefulWaiterExecution {
  readonly messages: readonly string[];
  readonly isGracefulCompleted: boolean;
}

export const gracefulConditionMessages = new Channel("graceful-condition-messages", stringCodec);
export const gracefulConditionGate = new Channel("graceful-condition-gate", voidCodec);
export const gracefulWaiterExecutions = new Attribute(
  "graceful-waiter-executions",
  jsonCodec<GracefulWaiterExecution[]>(),
);

class GracefulConditionGateStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly waiter: GracefulMessageWaiterStep) {}

  public getStepType(): string {
    return "GracefulConditionGateStep";
  }

  public waitFor(): Wait {
    return Wait.until(gracefulConditionGate.forOne());
  }

  public execute(): StepDecision {
    return goTo(this.waiter, undefined);
  }
}

class GracefulMessageWaiterStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: GracefulCompletionConditionFlow) {}

  public getStepType(): string {
    return "GracefulMessageWaiterStep";
  }

  public waitFor(): Wait {
    this.flow.markWaiterWaitForCalled();
    return Wait.anyOf(gracefulConditionMessages.forOne(), GracefulCompletion.requested("graceful"));
  }

  public execute(context: Context): StepDecision {
    const messages = gracefulConditionMessages.results(context);
    const isGracefulCompleted = context.isGracefulCompletionConditionCompleted();
    const executions = gracefulWaiterExecutions.get(context) ?? [];
    gracefulWaiterExecutions.set(context, [...executions, { messages, isGracefulCompleted }]);
    if (messages.length > 0) {
      return goTo(this, undefined);
    }
    if (isGracefulCompleted) {
      return deadEnd();
    }
    return goTo(this, undefined);
  }
}

export class GracefulCompletionConditionFlow implements Flow<void> {
  public readonly waiterWaitForCalled: Promise<void>;
  private resolveWaiterWaitForCalled!: () => void;
  private readonly waiter = new GracefulMessageWaiterStep(this);
  private readonly gate = new GracefulConditionGateStep(this.waiter);

  public constructor() {
    this.waiterWaitForCalled = new Promise((resolve) => { this.resolveWaiterWaitForCalled = resolve; });
  }

  public getFlowType(): string {
    return "GracefulCompletionConditionFlow";
  }

  public getSteps() {
    return StepList.startStep(this.gate).otherSteps(this.waiter);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [gracefulWaiterExecutions],
      channels: [gracefulConditionMessages, gracefulConditionGate],
    };
  }

  public markWaiterWaitForCalled(): void {
    this.resolveWaiterWaitForCalled();
  }

  @rpc({ inputCodec: stringCodec })
  public publishMessage(context: Context, message: string): void {
    gracefulConditionMessages.publish(context, message);
  }

  @rpc()
  public openGate(context: Context): void {
    gracefulConditionGate.publish(context, undefined);
  }
}
