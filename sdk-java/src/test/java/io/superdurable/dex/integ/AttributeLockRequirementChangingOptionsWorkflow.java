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

import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.TOTAL;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.WRITTEN;

/** Its start Step registers the required lock only on the first getStepOptions() call. */
class AttributeLockRequirementChangingOptionsWorkflow implements Flow<String> {
    final AtomicInteger optionsReads = new AtomicInteger();
    final CountDownLatch executeEntered = new CountDownLatch(1);
    final CountDownLatch releaseExecute = new CountDownLatch(1);

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(new LockReqChangingOptionsStep());
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return AttributeLockRequirementStepWorkflow.lockRequirementSchema();
    }

    @RPC(lockAttributes = {"lock-req-total"})
    public void setTotalLocked(final Context context, final Integer value) {
        TOTAL.set(context, value);
    }

    final class LockReqChangingOptionsStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepOptions getStepOptions() {
            final StepOptions.Builder options = StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder().maximumAttempts(1).build());
            if (optionsReads.incrementAndGet() == 1) {
                options.addExecuteLock(AttributeLock.of(TOTAL));
            }
            return options.build();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            executeEntered.countDown();
            try {
                if (!releaseExecute.await(60, TimeUnit.SECONDS)) {
                    throw new IllegalStateException("the test did not release execute");
                }
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(interrupted);
            }
            TOTAL.set(context, 1);
            return StepDecision.gracefulComplete(WRITTEN);
        }
    }
}
