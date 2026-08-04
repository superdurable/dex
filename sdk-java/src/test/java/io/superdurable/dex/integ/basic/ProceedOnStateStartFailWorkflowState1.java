/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ.basic;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.RetryPolicy;

public class ProceedOnStateStartFailWorkflowState1 implements WorkflowState<String> {
    private String output = "";

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, String input, Persistence persistence, Communication communication) {
        output = input + "_state1_start";
        throw new RuntimeException("Start failed");
    }

    @Override
    public StateDecision execute(Context context, String input, CommandResults commandResults, Persistence persistence, Communication communication) {
        if (!context.getAttempt().isPresent()) {
            throw new RuntimeException("attempt must be greater than zero");
        }
        if (!context.getFirstAttemptTimestampSeconds().isPresent()) {
            throw new RuntimeException("firstAttemptTimestampSeconds must be greater than zero");
        }

        output = output + "_state1_decide";
        return StateDecision.singleNextState(ProceedOnStateStartFailWorkflowState2.class, output);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToExecuteWhenWaitUntilRetryExhausted(true)
                .setWaitUntilApiRetryPolicy(new RetryPolicy().maximumAttempts(2));
    }
}
