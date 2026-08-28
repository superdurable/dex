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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;

final class WorkflowUncompletedEmptyDecisionWorkflow implements Flow<Integer> {
    private final WorkflowUncompletedEmptyDecisionStep start =
            new WorkflowUncompletedEmptyDecisionStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }
}

final class WorkflowUncompletedEmptyDecisionStep implements Step<Integer> {
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StepDecision execute(final Context context, final Integer input) {
        return StepDecision.goToMany();
    }

    @Override
    public StepOptions getStepOptions() {
        return StepOptions.newBuilder()
                .executeRetry(RetryPolicy.newBuilder().maximumAttempts(1).build())
                .build();
    }
}
