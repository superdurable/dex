/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RecoveryErrorInfo;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;

/** Routes its input to one Step per declared lock requirement case. */
final class AttributeLockRequirementStepWorkflow implements Flow<String> {
    // Needs path escaping, so the cases cover escaped map lock keys.
    static final String SPACED_INSTANCE = "item a";
    static final String OTHER_INSTANCE = "item b";
    static final String WRITTEN = "written";

    static final Attribute<Integer> TOTAL =
            Attribute.define("lock-req-total", Integer.class).writesRequireLock();
    static final Attribute<Boolean> COORDINATION =
            Attribute.define("lock-req-coordination", Boolean.class);
    static final AttributeMap<Integer> ITEMS =
            AttributeMap.define("lock-req-items", Integer.class).writesRequireLock();
    static final AttributeMap<Integer> SHARED =
            AttributeMap.define("lock-req-shared", Integer.class)
                    .writesRequireAttributeLock(COORDINATION);
    static final Attribute<Integer> FREE = Attribute.define("lock-req-free", Integer.class);
    static final Channel<String> REPLIES =
            Channel.define("lock-req-replies", String.class)
                    .publishesRequireAttributeLock(COORDINATION);
    static final ChannelMap<String> EVENTS =
            ChannelMap.define("lock-req-events", String.class)
                    .publishesRequireAttributeLock(COORDINATION);
    static final Channel<String> FREE_CHANNEL =
            Channel.define("lock-req-free-channel", String.class);

    static PersistenceSchema lockRequirementSchema() {
        return PersistenceSchema.of(
                TOTAL, COORDINATION, ITEMS, SHARED, FREE, REPLIES, EVENTS, FREE_CHANNEL);
    }

    /** Runs one attempt and routes an execute failure to the recovery Step. */
    static StepOptions.Builder caseOptions() {
        final RetryPolicy oneAttempt = RetryPolicy.newBuilder()
                .maximumAttempts(1)
                .backoffCoefficient(2.0)
                .build();
        return StepOptions.newBuilder()
                .waitForRetry(oneAttempt)
                .executeRetry(oneAttempt)
                .onExecuteFailureProceedTo(LockReqRecoveryStep.class);
    }

    @Override
    public StepList<String> getSteps() {
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
                new LockReqRecoveryStep());
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return lockRequirementSchema();
    }

    /** Completes with the failed attempt's error type and detail. */
    static String recoveryResult(final Context context) {
        final RecoveryErrorInfo recovery = context.getRecoveryError();
        if (recovery == null) {
            return "no recovery error";
        }
        return recovery.getErrorType() + ": " + recovery.getDetail();
    }

    /** Completes with the waitFor failure, or "written" when waitFor succeeded. */
    static StepDecision waitForResult(final Context context) {
        if (!context.waitForMethodFailed()) {
            return StepDecision.gracefulComplete(WRITTEN);
        }
        return StepDecision.gracefulComplete(recoveryResult(context));
    }

    abstract static class LockReqCaseStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }
    }

    static final class LockReqDispatchStep extends LockReqCaseStep {
        @Override
        public StepDecision execute(final Context context, final String stepCase) {
            switch (stepCase) {
                case "unlocked-total":
                    return StepDecision.goTo(LockReqUnlockedTotalStep.class, stepCase);
                case "movement-lock-total":
                    // A per-movement override does not count, even when it names the lock.
                    return StepDecision.goToMany(StepMovement.of(
                            LockReqUnlockedTotalStep.class,
                            stepCase,
                            caseOptions().addExecuteLock(AttributeLock.of(TOTAL)).build()));
                case "wait-for-total":
                    return StepDecision.goTo(LockReqWaitForTotalStep.class, stepCase);
                case "locked-wait-for-total":
                    return StepDecision.goTo(LockReqLockedWaitForTotalStep.class, stepCase);
                case "locked-total":
                    return StepDecision.goTo(LockReqLockedTotalStep.class, stepCase);
                case "locked-item":
                    return StepDecision.goTo(LockReqLockedItemStep.class, stepCase);
                case "other-item":
                    return StepDecision.goTo(LockReqOtherItemStep.class, stepCase);
                case "locked-shared":
                    return StepDecision.goTo(LockReqLockedSharedStep.class, stepCase);
                case "unlocked-shared":
                    return StepDecision.goTo(LockReqUnlockedSharedStep.class, stepCase);
                case "undeclared":
                    return StepDecision.goTo(LockReqUndeclaredStep.class, stepCase);
                default:
                    return StepDecision.forceFail("unknown lock requirement case " + stepCase);
            }
        }
    }

    static final class LockReqUnlockedTotalStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            TOTAL.set(context, 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    /** Registers the lock for execute only, so its waitFor write fails. */
    static final class LockReqWaitForTotalStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions()
                    .addExecuteLock(AttributeLock.of(TOTAL))
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            TOTAL.set(context, 1);
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return waitForResult(context);
        }
    }

    /** Registers the lock for waitFor, so its waitFor write commits. */
    static final class LockReqLockedWaitForTotalStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions()
                    .addWaitForLock(AttributeLock.of(TOTAL))
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            TOTAL.set(context, 1);
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return waitForResult(context);
        }
    }

    static final class LockReqLockedTotalStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().addExecuteLock(AttributeLock.of(TOTAL)).build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            TOTAL.set(context, 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    static final class LockReqLockedItemStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().addExecuteLock(AttributeLock.of(ITEMS, SPACED_INSTANCE)).build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            ITEMS.set(context, SPACED_INSTANCE, 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    /** Holds one instance's lock and writes another instance from a second thread. */
    static final class LockReqOtherItemStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().addExecuteLock(AttributeLock.of(ITEMS, SPACED_INSTANCE)).build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            ITEMS.set(context, SPACED_INSTANCE, 1);
            final Thread writer = new Thread(() -> ITEMS.set(context, OTHER_INSTANCE, 1));
            writer.start();
            try {
                writer.join();
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(interrupted);
            }
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    static final class LockReqLockedSharedStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().addExecuteLock(AttributeLock.of(COORDINATION)).build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            SHARED.set(context, "x", 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    /** Holds the written instance's own lock, not the declared coordination lock. */
    static final class LockReqUnlockedSharedStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().addExecuteLock(AttributeLock.of(SHARED, "x")).build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            SHARED.set(context, "x", 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    static final class LockReqUndeclaredStep extends LockReqCaseStep {
        @Override
        public StepOptions getStepOptions() {
            return caseOptions().build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            FREE.set(context, 1);
            FREE_CHANNEL.publish(context, "free");
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }

    static final class LockReqRecoveryStep extends LockReqCaseStep {
        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(recoveryResult(context));
        }
    }
}
