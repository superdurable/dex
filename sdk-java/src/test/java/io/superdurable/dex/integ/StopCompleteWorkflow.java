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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

final class StopCompleteWorkflow implements Flow<Integer> {
    final CountDownLatch executeStarted = new CountDownLatch(1);
    final CountDownLatch releaseExecute = new CountDownLatch(1);
    private final StopCompleteBlockingStep start = new StopCompleteBlockingStep(this);

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }
}

final class StopCompleteBlockingStep implements Step<Integer> {
    private final StopCompleteWorkflow flow;

    StopCompleteBlockingStep(final StopCompleteWorkflow flow) {
        this.flow = flow;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StepDecision execute(final Context context, final Integer input) {
        flow.executeStarted.countDown();
        try {
            flow.releaseExecute.await(20, TimeUnit.SECONDS);
        } catch (InterruptedException exception) {
            Thread.currentThread().interrupt();
        }
        return StepDecision.deadEnd();
    }
}
