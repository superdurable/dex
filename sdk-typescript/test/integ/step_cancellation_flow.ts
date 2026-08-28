// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  Attribute,
  ExecuteFailure,
  StepList,
  StepMovement,
  Timer,
  Wait,
  deadEnd,
  forceFail,
  goTo,
  goToMany,
  gracefulComplete,
  stringCodec,
  voidCodec,
  withCancelingSiblingSteps,
  withCancelingSteps,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export type CancellationScenario =
  | "heartbeat-execute"
  | "heartbeat-wait-for"
  | "local-execute"
  | "local-timeout-fallback"
  | "no-heartbeat"
  | "global-selector"
  | "sibling-selector";

export const cancellationScenarios: readonly CancellationScenario[] = [
  "heartbeat-execute",
  "heartbeat-wait-for",
  "local-execute",
  "local-timeout-fallback",
  "no-heartbeat",
  "global-selector",
  "sibling-selector",
];

class CancellationStart implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationStart";
  }

  public execute(_context: Context, _input: string): StepDecision {
    if (this.flow.scenario === "heartbeat-wait-for") {
      return goToMany(
        StepMovement.of(CancellationBlockingWaitFor, undefined),
        StepMovement.of(CancellationWinner, undefined),
      );
    }
    if (this.flow.scenario === "global-selector" || this.flow.scenario === "sibling-selector") {
      return goToMany(
        StepMovement.of(CancellationFirstParent, undefined),
        StepMovement.of(CancellationSecondParent, undefined),
      );
    }
    return goToMany(
      StepMovement.of(CancellationBlockingExecute, undefined),
      StepMovement.of(CancellationWinner, undefined),
    );
  }
}

class CancellationBlockingExecute implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationBlockingExecute";
  }

  public getStepOptions(): StepOptions {
    return {
      executeMethodTimeoutMs: 15_000,
      ...(
        this.flow.scenario === "heartbeat-execute" ||
          this.flow.scenario === "local-timeout-fallback"
          ? { heartbeatTimeoutMs: 2_000 }
          : {}
      ),
      executeDurability:
        this.flow.scenario === "local-execute" ||
          this.flow.scenario === "local-timeout-fallback"
          ? "async"
          : "sync",
      executeFailure: ExecuteFailure.proceedTo(CancellationRecovery),
    };
  }

  public async execute(context: Context, _input: void): Promise<StepDecision> {
    this.flow.blockingInvocations += 1;
    this.flow.markBlockingStarted();
    try {
      if (this.flow.scenario === "no-heartbeat") {
        await delay(7_000);
      } else {
        await delay(10_000, context.cancellationSignal);
      }
    } catch (failure) {
      if (!context.cancellationSignal.aborted) {
        throw failure;
      }
      this.flow.handlerCanceled = true;
      this.flow.contextReportedCancellation = context.cancellationSignal.aborted;
      this.flow.markCancellationObserved();
    }
    this.flow.lateWrite.set(context, "late");
    this.flow.markLateHandlerReturned();
    return goTo(CancellationRecovery, undefined);
  }
}

class CancellationBlockingWaitFor implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationBlockingWaitFor";
  }

  public getStepOptions(): StepOptions {
    return {
      waitForMethodTimeoutMs: 15_000,
      heartbeatTimeoutMs: 2_000,
      waitForFailure: "proceed",
      waitForDurability: "sync",
    };
  }

  public async waitFor(context: Context, _input: void): Promise<Wait> {
    this.flow.blockingInvocations += 1;
    this.flow.markBlockingStarted();
    try {
      await delay(10_000, context.cancellationSignal);
    } catch (failure) {
      if (!context.cancellationSignal.aborted) {
        throw failure;
      }
      this.flow.handlerCanceled = true;
      this.flow.contextReportedCancellation = context.cancellationSignal.aborted;
      this.flow.markCancellationObserved();
    }
    return Wait.skipImmediately();
  }

  public execute(_context: Context, _input: void): StepDecision {
    this.flow.recoveryRan = true;
    return forceFail("canceled waitFor execution continued");
  }
}

class CancellationWinner implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationWinner";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return this.flow.scenario === "local-execute"
      ? Wait.skipImmediately()
      : Wait.until(Timer.byDuration(3_000));
  }

  public async execute(_context: Context, _input: void): Promise<StepDecision> {
    if (this.flow.scenario === "local-execute") {
      await this.flow.blockingStarted;
      await delay(1_000);
    }
    const selected = this.flow.scenario === "heartbeat-wait-for"
      ? CancellationBlockingWaitFor
      : CancellationBlockingExecute;
    return withCancelingSteps(goTo(CancellationFinal, this.flow.scenario), selected);
  }
}

class CancellationRecovery implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationRecovery";
  }

  public execute(_context: Context, _input: void): StepDecision {
    this.flow.recoveryRan = true;
    return forceFail("canceled execution reached recovery");
  }
}

class CancellationFinal implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "TypeScriptCancellationFinal";
  }

  public waitFor(_context: Context, _input: string): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

class CancellationFirstParent implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationFirstParent";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goToMany(
      StepMovement.of(CancellationSelectorWinner, undefined),
      StepMovement.of(CancellationSelectorWaiting, "first"),
    );
  }
}

class CancellationSecondParent implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationSecondParent";
  }

  public execute(_context: Context, _input: void): StepDecision {
    return goTo(CancellationSelectorWaiting, "second");
  }
}

class CancellationSelectorWinner implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationSelectorWinner";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(Timer.byDuration(1_000));
  }

  public async execute(_context: Context, _input: void): Promise<StepDecision> {
    await this.flow.selectorWaitsRegistered;
    const decision = goTo(CancellationFinal, this.flow.scenario);
    return this.flow.scenario === "global-selector"
      ? withCancelingSteps(decision, CancellationSelectorWaiting)
      : withCancelingSiblingSteps(decision, CancellationSelectorWaiting);
  }
}

class CancellationSelectorWaiting implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: StepCancellationFlow) {}

  public getStepType(): string {
    return "TypeScriptCancellationSelectorWaiting";
  }

  public waitFor(_context: Context, input: string): Wait {
    this.flow.markSelectorWaiting();
    const milliseconds = input === "first" || this.flow.scenario === "global-selector"
      ? 30_000
      : 2_000;
    return Wait.until(Timer.byDuration(milliseconds));
  }

  public execute(_context: Context, input: string): StepDecision {
    if (input === "first") {
      this.flow.firstSelectorExecuted = true;
    } else {
      this.flow.secondSelectorExecuted = true;
    }
    return deadEnd();
  }
}

export class StepCancellationFlow implements Flow<string> {
  public readonly lateWrite = new Attribute("typescript-cancellation-late-write", stringCodec);
  public readonly recovery = new CancellationRecovery(this);
  public readonly blockingExecute = new CancellationBlockingExecute(this);
  public readonly blockingWaitFor = new CancellationBlockingWaitFor(this);
  public readonly winner = new CancellationWinner(this);
  public readonly final = new CancellationFinal();
  public readonly firstParent = new CancellationFirstParent(this);
  public readonly secondParent = new CancellationSecondParent(this);
  public readonly selectorWinner = new CancellationSelectorWinner(this);
  public readonly selectorWaiting = new CancellationSelectorWaiting(this);
  public handlerCanceled = false;
  public contextReportedCancellation = false;
  public recoveryRan = false;
  public firstSelectorExecuted = false;
  public secondSelectorExecuted = false;
  public blockingInvocations = 0;
  public readonly blockingStarted: Promise<void>;
  public readonly cancellationObserved: Promise<void>;
  public readonly lateHandlerReturned: Promise<void>;
  public readonly selectorWaitsRegistered: Promise<void>;

  private readonly start = new CancellationStart(this);
  private resolveBlockingStarted!: () => void;
  private resolveCancellationObserved!: () => void;
  private resolveLateHandlerReturned!: () => void;
  private resolveSelectorWaitsRegistered!: () => void;
  private selectorWaitCount = 0;

  public constructor(public readonly scenario: CancellationScenario) {
    this.blockingStarted = new Promise((resolve) => { this.resolveBlockingStarted = resolve; });
    this.cancellationObserved = new Promise((resolve) => { this.resolveCancellationObserved = resolve; });
    this.lateHandlerReturned = new Promise((resolve) => { this.resolveLateHandlerReturned = resolve; });
    this.selectorWaitsRegistered = new Promise((resolve) => {
      this.resolveSelectorWaitsRegistered = resolve;
    });
  }

  public getFlowType(): string {
    return `TypeScriptStepCancellation-${this.scenario}`;
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(
      this.blockingExecute,
      this.blockingWaitFor,
      this.winner,
      this.recovery,
      this.final,
      this.firstParent,
      this.secondParent,
      this.selectorWinner,
      this.selectorWaiting,
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [this.lateWrite] };
  }

  public markBlockingStarted(): void {
    this.resolveBlockingStarted();
  }

  public markCancellationObserved(): void {
    this.resolveCancellationObserved();
  }

  public markLateHandlerReturned(): void {
    this.resolveLateHandlerReturned();
  }

  public markSelectorWaiting(): void {
    this.selectorWaitCount += 1;
    if (this.selectorWaitCount === 2) {
      this.resolveSelectorWaitsRegistered();
    }
  }
}

function delay(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const complete = (): void => {
      signal?.removeEventListener("abort", abort);
      resolve();
    };
    const timer = setTimeout(complete, milliseconds);
    const abort = (): void => {
      clearTimeout(timer);
      reject(signal?.reason);
    };
    if (signal === undefined) {
      return;
    }
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
  });
}
