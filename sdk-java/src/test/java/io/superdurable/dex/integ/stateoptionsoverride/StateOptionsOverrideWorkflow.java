/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
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