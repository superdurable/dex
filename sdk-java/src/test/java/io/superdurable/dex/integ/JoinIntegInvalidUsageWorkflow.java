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

import io.superdurable.dex.Condition;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

import java.util.Collections;
import java.util.function.Function;

/** Misuses a Join in one of several ways so the failing Step method fails the Flow. */
class JoinIntegInvalidUsageWorkflow implements Flow<JoinIntegInvalidUsageWorkflow.Misuse> {
    static final int BRANCH_COUNT = 2;

    enum Misuse {
        FORK_TWICE,
        FOR_BRANCHES_ZERO,
        FOR_BRANCHES_ABOVE_BRANCH_COUNT,
        FOR_REMAINING_BRANCHES_NEGATIVE,
        FOR_REMAINING_BRANCHES_ABOVE_BRANCH_COUNT
    }

    final Join<String> misusedResults = Join.define("misusedResult", String.class);
    private final JoinIntegInvalidUsageStep misuse = new JoinIntegInvalidUsageStep();

    @Override
    public StepList<Misuse> getSteps() {
        return StepList.startStep(misuse);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(misusedResults);
    }

    final class JoinIntegInvalidUsageStep implements Step<Misuse> {
        @Override
        public Class<Misuse> getInputType() {
            return Misuse.class;
        }

        @Override
        public Wait waitFor(final Context context, final Misuse input) {
            final JoinRound round = new JoinRound("Misused-1", BRANCH_COUNT);
            final Condition condition;
            switch (input) {
                case FOR_BRANCHES_ZERO:
                    condition = misusedResults.forBranches(round, 0);
                    break;
                case FOR_BRANCHES_ABOVE_BRANCH_COUNT:
                    condition = misusedResults.forBranches(round, BRANCH_COUNT + 1);
                    break;
                case FOR_REMAINING_BRANCHES_NEGATIVE:
                    condition = misusedResults.forRemainingBranches(round, -1);
                    break;
                case FOR_REMAINING_BRANCHES_ABOVE_BRANCH_COUNT:
                    condition = misusedResults.forRemainingBranches(round, BRANCH_COUNT + 1);
                    break;
                default:
                    return Wait.skipImmediately();
            }
            return Wait.until(condition);
        }

        @Override
        public StepDecision execute(final Context context, final Misuse input) {
            final Function<JoinRound, StepMovement<?>> joinBuilder =
                    round -> StepMovement.of(JoinIntegInvalidUsageStep.class, input);
            misusedResults.fork(
                    context,
                    Collections.<Function<JoinRound, StepMovement<?>>>emptyList(),
                    joinBuilder);
            misusedResults.fork(
                    context,
                    Collections.<Function<JoinRound, StepMovement<?>>>emptyList(),
                    joinBuilder);
            return StepDecision.gracefulComplete();
        }

        @Override
        public StepOptions getStepOptions() {
            final RetryPolicy singleAttempt = RetryPolicy.newBuilder().maximumAttempts(1).build();
            return StepOptions.newBuilder()
                    .waitForRetry(singleAttempt)
                    .executeRetry(singleAttempt)
                    .build();
        }
    }
}
