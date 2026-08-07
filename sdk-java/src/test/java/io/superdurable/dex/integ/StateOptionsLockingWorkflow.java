/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

final class StateOptionsLockingWorkflow implements Flow<Integer> {
    final Attribute<Integer> waitForCount = Attribute.define(
            "step-lock-wait-for-count",
            Integer.class);
    final Attribute<Integer> executeCount = Attribute.define(
            "step-lock-execute-count",
            Integer.class);
    private final Channel<Void> completed = Channel.define("step-lock-completed", Void.class);
    private final StartStep start = new StartStep();
    private final LockedStep locked = new LockedStep();
    private final CompleteStep complete = new CompleteStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start).otherSteps(locked, complete);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(waitForCount, executeCount, completed);
    }

    final class StartStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer parallelism) {
            final StepMovement<?>[] movements = new StepMovement<?>[parallelism + 1];
            for (int index = 0; index < parallelism; index++) {
                movements[index] = StepMovement.of(locked, index);
            }
            movements[parallelism] = StepMovement.of(complete, parallelism);
            return StepDecision.goToMulti(movements);
        }
    }

    final class LockedStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            waitForCount.set(context, increment(waitForCount.get(context)));
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            executeCount.set(context, increment(executeCount.get(context)));
            completed.publish(context, null);
            return StepDecision.deadEnd();
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addWaitForLock(AttributeLock.of(waitForCount))
                    .addExecuteLock(AttributeLock.of(executeCount))
                    .build();
        }
    }

    final class CompleteStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer parallelism) {
            return Wait.allOf(completed.forN(parallelism));
        }

        @Override
        public StepDecision execute(final Context context, final Integer parallelism) {
            if (completed.getConditionResults(context).size() != parallelism) {
                throw new IllegalStateException("not all locked Steps completed");
            }
            return StepDecision.gracefulComplete(
                    waitForCount.get(context) + ":" + executeCount.get(context));
        }
    }

    private static int increment(final Integer value) {
        return value == null ? 1 : value + 1;
    }
}
