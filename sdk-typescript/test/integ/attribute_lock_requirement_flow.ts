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
  AttributeMap,
  Channel,
  ChannelMap,
  ExecuteFailure,
  StepList,
  StepMovement,
  Wait,
  booleanCodec,
  doubleCodec,
  forceComplete,
  goTo,
  goToMany,
  gracefulComplete,
  rpc,
  stringCodec,
  voidCodec,
  type AttributeLock,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

// It needs path escaping, so the tests cover escaped map lock keys.
export const lockReqSpacedInstance = "item a";

export const lockReqCoordination = new Attribute("lock-req-coordination", booleanCodec);
// Chained in both orders, so each option keeps the other's declaration.
export const lockReqTotal = new Attribute("lock-req-total", doubleCodec).writesRequireLock().plaintext();
export const lockReqItems = new AttributeMap("lock-req-items", doubleCodec).plaintext().writesRequireLock();
export const lockReqShared = new AttributeMap("lock-req-shared", doubleCodec)
  .writesRequireAttributeLock(lockReqCoordination);
export const lockReqFree = new Attribute("lock-req-free", doubleCodec);
export const lockReqReplies = new Channel("lock-req-replies", stringCodec)
  .publishesRequireAttributeLock(lockReqCoordination);
export const lockReqEvents = new ChannelMap("lock-req-events", stringCodec)
  .publishesRequireAttributeLock(lockReqCoordination);
export const lockReqFreeChannel = new Channel("lock-req-free-channel", stringCodec);

function lockReqSchema(): PersistenceSchema {
  return {
    attributes: [lockReqTotal, lockReqCoordination, lockReqItems, lockReqShared, lockReqFree],
    channels: [lockReqReplies, lockReqEvents, lockReqFreeChannel],
  };
}

/** Runs one attempt and routes an execute failure to the recovery Step. */
function lockReqStepOptions(locks: readonly AttributeLock[] = []): StepOptions {
  return {
    waitForRetry: { maximumAttempts: 1 },
    executeRetry: { maximumAttempts: 1 },
    executeLockAttributes: locks,
    executeFailure: ExecuteFailure.proceedTo(LockReqRecoveryStep),
  };
}

/** Completes with the failed attempt's error type and detail. */
class LockReqRecoveryStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqRecoveryStep";
  }

  public execute(context: Context): StepDecision {
    const recovery = context.recoveryError;
    if (recovery === undefined) {
      throw new Error("recovery Step has no recovery error");
    }
    return gracefulComplete(`${recovery.errorType}: ${recovery.detail}`);
  }
}

/** Writes a declared Attribute from an async execute without registering its lock. */
class LockReqUnlockedTotalStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqUnlockedTotalStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions();
  }

  public async execute(context: Context): Promise<StepDecision> {
    await Promise.resolve();
    lockReqTotal.set(context, 1);
    return gracefulComplete("written");
  }
}

/** Completes with the waitFor failure, or "written" when waitFor succeeded. */
function lockReqWaitForResult(context: Context): StepDecision {
  if (!context.waitForMethodFailed()) {
    return gracefulComplete("written");
  }
  const recovery = context.recoveryError;
  if (recovery === undefined) {
    return gracefulComplete("waitFor failed without a recovery error");
  }
  return gracefulComplete(`${recovery.errorType}: ${recovery.detail}`);
}

/** Registers the lock for execute only, so its waitFor write fails. */
class LockReqWaitForTotalStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqWaitForTotalStep";
  }

  public getStepOptions(): StepOptions {
    return { ...lockReqStepOptions([lockReqTotal.lock()]), waitForFailure: "proceed" };
  }

  public waitFor(context: Context): Wait {
    lockReqTotal.set(context, 1);
    return Wait.skipImmediately();
  }

  public execute(context: Context): StepDecision {
    return lockReqWaitForResult(context);
  }
}

/** Registers the lock for waitFor, so its waitFor write commits. */
class LockReqLockedWaitForTotalStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqLockedWaitForTotalStep";
  }

  public getStepOptions(): StepOptions {
    return {
      ...lockReqStepOptions(),
      waitForLockAttributes: [lockReqTotal.lock()],
      waitForFailure: "proceed",
    };
  }

  public async waitFor(context: Context): Promise<Wait> {
    await Promise.resolve();
    lockReqTotal.set(context, 1);
    return Wait.skipImmediately();
  }

  public execute(context: Context): StepDecision {
    return lockReqWaitForResult(context);
  }
}

class LockReqLockedTotalStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqLockedTotalStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions([lockReqTotal.lock()]);
  }

  public execute(context: Context): StepDecision {
    lockReqTotal.set(context, 1);
    return gracefulComplete("written");
  }
}

class LockReqLockedItemStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqLockedItemStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions([lockReqItems.lock(lockReqSpacedInstance)]);
  }

  public execute(context: Context): StepDecision {
    lockReqItems.set(context, lockReqSpacedInstance, 1);
    return gracefulComplete("written");
  }
}

/** Holds one instance's lock and writes another instance. */
class LockReqOtherItemStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqOtherItemStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions([lockReqItems.lock(lockReqSpacedInstance)]);
  }

  public execute(context: Context): StepDecision {
    lockReqItems.set(context, lockReqSpacedInstance, 1);
    lockReqItems.set(context, "item b", 1);
    return gracefulComplete("written");
  }
}

class LockReqLockedSharedStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqLockedSharedStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions([lockReqCoordination.lock()]);
  }

  public execute(context: Context): StepDecision {
    lockReqShared.set(context, "x", 1);
    return gracefulComplete("written");
  }
}

/** Holds the written instance's own lock, not the declared coordination lock. */
class LockReqUnlockedSharedStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqUnlockedSharedStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions([lockReqShared.lock("x")]);
  }

  public execute(context: Context): StepDecision {
    lockReqShared.set(context, "x", 1);
    return gracefulComplete("written");
  }
}

class LockReqUndeclaredStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "LockReqUndeclaredStep";
  }

  public getStepOptions(): StepOptions {
    return lockReqStepOptions();
  }

  public execute(context: Context): StepDecision {
    lockReqFree.set(context, 1);
    lockReqFreeChannel.publish(context, "free");
    return gracefulComplete("written");
  }
}

/** Routes its input to one Step per case. */
class LockReqDispatchStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "LockReqDispatchStep";
  }

  public execute(_context: Context, stepCase: string): StepDecision {
    switch (stepCase) {
      case "unlocked-total":
        return goTo(LockReqUnlockedTotalStep, undefined);
      case "movement-lock-total":
        // A per-movement override does not count, even when it names the required lock.
        return goToMany(StepMovement.of(
          LockReqUnlockedTotalStep,
          undefined,
          lockReqStepOptions([lockReqTotal.lock()]),
        ));
      case "wait-for-total":
        return goTo(LockReqWaitForTotalStep, undefined);
      case "locked-wait-for-total":
        return goTo(LockReqLockedWaitForTotalStep, undefined);
      case "locked-total":
        return goTo(LockReqLockedTotalStep, undefined);
      case "locked-item":
        return goTo(LockReqLockedItemStep, undefined);
      case "other-item":
        return goTo(LockReqOtherItemStep, undefined);
      case "locked-shared":
        return goTo(LockReqLockedSharedStep, undefined);
      case "unlocked-shared":
        return goTo(LockReqUnlockedSharedStep, undefined);
      case "undeclared":
        return goTo(LockReqUndeclaredStep, undefined);
      default:
        throw new Error(`unknown lock requirement case ${stepCase}`);
    }
  }
}

export class AttributeLockRequirementStepFlow implements Flow<string> {
  public getFlowType(): string {
    return "AttributeLockRequirementStepFlow";
  }

  public getSteps() {
    return StepList.startStep(new LockReqDispatchStep()).otherSteps(
      new LockReqUnlockedTotalStep(),
      new LockReqWaitForTotalStep(),
      new LockReqLockedWaitForTotalStep(),
      new LockReqLockedTotalStep(),
      new LockReqLockedItemStep(),
      new LockReqOtherItemStep(),
      new LockReqLockedSharedStep(),
      new LockReqUnlockedSharedStep(),
      new LockReqUndeclaredStep(),
      new LockReqRecoveryStep(),
    );
  }

  public getPersistenceSchema(): PersistenceSchema {
    return lockReqSchema();
  }
}

/** Has no Steps, so it stays running for RPC cases. */
export class AttributeLockRequirementRPCFlow implements Flow {
  public getFlowType(): string {
    return "AttributeLockRequirementRPCFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return lockReqSchema();
  }

  // It also writes an undeclared Attribute, which the failure must discard too.
  @rpc({ inputCodec: stringCodec })
  public publishReplyUnlocked(context: Context, reply: string): void {
    lockReqFree.set(context, 1);
    lockReqReplies.publish(context, reply);
  }

  @rpc({ inputCodec: stringCodec, lockAttributes: [lockReqCoordination.lock()] })
  public publishReplyLocked(context: Context, reply: string): void {
    lockReqReplies.publish(context, reply);
  }

  @rpc({ inputCodec: stringCodec })
  public async setItem(context: Context, instance: string): Promise<void> {
    await Promise.resolve();
    lockReqItems.set(context, instance, 1);
  }

  @rpc({ inputCodec: stringCodec, lockAttributes: [lockReqItems.lock(lockReqSpacedInstance)] })
  public async setItemLocked(context: Context, instance: string): Promise<void> {
    await Promise.resolve();
    lockReqItems.set(context, instance, 1);
  }

  // It also writes an undeclared Attribute, which the failure must discard too.
  @rpc({ inputCodec: stringCodec })
  public publishEventUnlocked(context: Context, instance: string): void {
    lockReqFree.set(context, 1);
    lockReqEvents.publish(context, instance, "event");
  }

  @rpc({ inputCodec: stringCodec, lockAttributes: [lockReqCoordination.lock()] })
  public publishEventLocked(context: Context, instance: string): void {
    lockReqEvents.publish(context, instance, "event");
  }

  // Each missing lock appears in the error, writes first.
  @rpc()
  public publishAndWriteUnlocked(context: Context): void {
    lockReqReplies.publish(context, "hi");
    lockReqTotal.set(context, 2);
  }

  @rpc({ inputCodec: stringCodec, outputCodec: doubleCodec })
  public countEvents(context: Context, instance: string): RPCResult<number> {
    return { output: lockReqEvents.size(context, instance) };
  }

  @rpc({ outputCodec: doubleCodec })
  public countReplies(context: Context): RPCResult<number> {
    return { output: lockReqReplies.size(context) };
  }

  @rpc({ inputCodec: doubleCodec, lockAttributes: [lockReqTotal.lock()] })
  public setTotalLocked(context: Context, value: number): void {
    lockReqTotal.set(context, value);
  }

  @rpc()
  public deleteTotalUnlocked(context: Context): void {
    lockReqTotal.delete(context);
  }
}

/** Writes a declared Attribute from its unchecked timeout handler. */
export class AttributeLockRequirementTimeoutFlow implements Flow {
  public getFlowType(): string {
    return "AttributeLockRequirementTimeoutFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return lockReqSchema();
  }

  public handleTimeout(context: Context): StepDecision {
    lockReqTotal.set(context, 5);
    return forceComplete("expired");
  }
}

interface Deferred {
  readonly promise: Promise<void>;
  readonly resolve: () => void;
}

function deferred(): Deferred {
  let resolve: () => void = () => undefined;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

/** Lets a test hold a Step inside its locked execute while it invokes a conflicting RPC. */
export const lockSnapshotGate = { entered: deferred(), release: deferred() };

export function resetLockSnapshotGate(): void {
  lockSnapshotGate.entered = deferred();
  lockSnapshotGate.release = deferred();
}

/** Registers the RPC with this array; the test empties it after registration. */
export const lockSnapshotRPCLocks: AttributeLock[] = [lockReqTotal.lock()];

/** Names the required lock only in its first getStepOptions call, which Registry makes. */
class LockSnapshotHoldStep implements Step<void> {
  public readonly inputCodec = voidCodec;
  public getStepOptionsCalls = 0;

  public constructor(private readonly stepType: string) {}

  public getStepType(): string {
    return this.stepType;
  }

  public getStepOptions(): StepOptions {
    this.getStepOptionsCalls += 1;
    return this.getStepOptionsCalls === 1 ? { executeLockAttributes: [lockReqTotal.lock()] } : {};
  }

  public async execute(context: Context): Promise<StepDecision> {
    lockSnapshotGate.entered.resolve();
    await lockSnapshotGate.release.promise;
    lockReqTotal.set(context, 1);
    return gracefulComplete("written");
  }
}

/** Starts at the holding Step, so the Client sends its default options. */
export class LockSnapshotStartFlow implements Flow {
  public readonly hold = new LockSnapshotHoldStep("LockSnapshotStartHoldStep");

  public getFlowType(): string {
    return "LockSnapshotStartFlow";
  }

  public getSteps() {
    return StepList.startStep(this.hold);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return lockReqSchema();
  }

  // A bounded timeout makes a missing lock fail the test instead of waiting on the held Step.
  @rpc({ inputCodec: doubleCodec, lockAttributes: [lockReqTotal.lock()], timeoutMs: 10_000 })
  public setTotalLocked(context: Context, value: number): void {
    lockReqTotal.set(context, value);
  }

  @rpc({ inputCodec: doubleCodec, lockAttributes: lockSnapshotRPCLocks, timeoutMs: 10_000 })
  public setTotalWithRegisteredArray(context: Context, value: number): void {
    lockReqTotal.set(context, value);
  }
}

class LockSnapshotDispatchStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly hold: LockSnapshotHoldStep) {}

  public getStepType(): string {
    return "LockSnapshotDispatchStep";
  }

  public execute(): StepDecision {
    return goTo(this.hold, undefined);
  }
}

/** Moves to the holding Step, so the Worker sends its default options. */
export class LockSnapshotMovementFlow implements Flow {
  public readonly hold = new LockSnapshotHoldStep("LockSnapshotMovedHoldStep");

  public getFlowType(): string {
    return "LockSnapshotMovementFlow";
  }

  public getSteps() {
    return StepList.startStep(new LockSnapshotDispatchStep(this.hold)).otherSteps(this.hold);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return lockReqSchema();
  }

  @rpc({ inputCodec: doubleCodec, lockAttributes: [lockReqTotal.lock()], timeoutMs: 10_000 })
  public setMovedTotalLocked(context: Context, value: number): void {
    lockReqTotal.set(context, value);
  }
}

/** Registers only the given schema, for registration failures. */
export class AttributeLockRequirementInvalidFlow implements Flow {
  public constructor(private readonly schema: PersistenceSchema) {}

  public getFlowType(): string {
    return "AttributeLockRequirementInvalidFlow";
  }

  public getSteps() {
    return StepList.empty();
  }

  public getPersistenceSchema(): PersistenceSchema {
    return this.schema;
  }
}
