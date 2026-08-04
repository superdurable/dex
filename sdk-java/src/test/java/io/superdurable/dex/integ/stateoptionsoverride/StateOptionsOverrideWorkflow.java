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

package io.superdurable.dex.integ.stateoptionsoverride;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.RetryPolicy;
import io.superdurable.dex.gen.models.WaitUntilApiFailurePolicy;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class StateOptionsOverrideWorkflow implements ObjectWorkflow {

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new StateOptionsOverrideWorkflowState1()),
                StateDef.nonStartingState(new StateOptionsOverrideWorkflowState2())
        );
    }
}

class StateOptionsOverrideWorkflowState1 implements WorkflowState<String> {
    private String output = "";

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, String input, Persistence persistence, Communication communication) {
        output = input + "_state1_start";
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(Context context, String input, CommandResults commandResults, Persistence persistence, Communication communication) {
        output = output + "_state1_decide";
        return StateDecision.singleNextState(
                StateOptionsOverrideWorkflowState2.class, output,
                new WorkflowStateOptions()
                        .setWaitUntilApiRetryPolicy(new RetryPolicy().maximumAttempts(2))
                        .setProceedToExecuteWhenWaitUntilRetryExhausted(true)
        );
    }
}

class StateOptionsOverrideWorkflowState2 implements WorkflowState<String> {
    private String output = "";

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, String input, Persistence persistence, Communication communication) {
        output = input + "_state2_start";
        throw new RuntimeException("");
    }

    @Override
    public StateDecision execute(Context context, String input, CommandResults commandResults, Persistence persistence, Communication communication) {
        output = output + "_state2_decide";
        return StateDecision.gracefulCompleteWorkflow(output);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setWaitUntilApiRetryPolicy(new RetryPolicy().maximumAttempts(1))
                .setProceedToExecuteWhenWaitUntilRetryExhausted(false);
    }
}