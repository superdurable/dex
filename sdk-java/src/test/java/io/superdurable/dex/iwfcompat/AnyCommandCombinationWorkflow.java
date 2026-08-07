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

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

final class AnyCommandCombinationWorkflow implements Flow<Integer> {
    private final Channel<Integer> first = Channel.define("test-signal-1", Integer.class);
    private final Channel<Integer> second = Channel.define("test-signal-2", Integer.class);
    private final Channel<Integer> third = Channel.define("test-signal-3", Integer.class);
    private final AnyCommandCombinationStep start =
            new AnyCommandCombinationStep(first, second, third);

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(first, second, third);
    }
}

final class AnyCommandCombinationStep implements Step<Integer> {
    private final Channel<Integer> first;
    private final Channel<Integer> second;
    private final Channel<Integer> third;

    AnyCommandCombinationStep(
            final Channel<Integer> first,
            final Channel<Integer> second,
            final Channel<Integer> third) {
        this.first = first;
        this.second = second;
        this.third = third;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public Wait waitFor(final Context context, final Integer input) {
        throw new IllegalArgumentException(
                "Found unknown condition ID in the combination list");
    }

    @Override
    public StepDecision execute(final Context context, final Integer input) {
        return StepDecision.gracefulComplete(input);
    }

    @Override
    public StepOptions getStepOptions() {
        return StepOptions.newBuilder()
                .waitForRetry(RetryPolicy.newBuilder().maximumAttempts(1).build())
                .build();
    }
}
