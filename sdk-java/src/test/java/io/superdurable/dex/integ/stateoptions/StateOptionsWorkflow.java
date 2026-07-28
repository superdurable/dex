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

package io.superdurable.dex.integ.stateoptions;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.WorkflowStateOptions;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.gen.models.PersistenceLoadingPolicy;
import io.superdurable.dex.gen.models.PersistenceLoadingType;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

@Component
public class StateOptionsWorkflow implements ObjectWorkflow {
    public static final String DA_WAIT_UNTIL = "DA_WAIT_UNTIL";
    public static final String DA_EXECUTE = "DA_EXECUTE";
    public static final String DA_BOTH = "DA_BOTH";

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DA_WAIT_UNTIL),
                DataAttributeDef.create(String.class, DA_EXECUTE),
                DataAttributeDef.create(String.class, DA_BOTH)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new StateOptionsWorkflowState1()),
                StateDef.nonStartingState(new StateOptionsWorkflowState2()),
                StateDef.nonStartingState(new StateOptionsWorkflowState3())
        );
    }
}

class StateOptionsWorkflowState1 implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(StateOptionsWorkflow.DA_EXECUTE, "execute");
        persistence.setDataAttribute(StateOptionsWorkflow.DA_WAIT_UNTIL, "wait_until");
        persistence.setDataAttribute(StateOptionsWorkflow.DA_BOTH, "both");

        return StateDecision.singleNextState(StateOptionsWorkflowState2.class);
    }
}

class StateOptionsWorkflowState2 implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        final String daWaitUntil = persistence.getDataAttribute(StateOptionsWorkflow.DA_WAIT_UNTIL, String.class);

        if (!daWaitUntil.equals("wait_until")) {
            throw new RuntimeException("Expected DA_WAIT_UNTIL to be 'wait_until', got " + daWaitUntil);
        }

        final String daExecute = persistence.getDataAttribute(StateOptionsWorkflow.DA_EXECUTE, String.class);
        if (daExecute != null) {
            throw new RuntimeException("Expected DA_EXECUTE to be null, got " + daExecute);
        }

        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final String daExecute = persistence.getDataAttribute(StateOptionsWorkflow.DA_EXECUTE, String.class);

        if (!daExecute.equals("execute")) {
            throw new RuntimeException("Expected DA_EXECUTE to be 'execute', got " + daExecute);
        }

        final String daWaitUntil = persistence.getDataAttribute(StateOptionsWorkflow.DA_WAIT_UNTIL, String.class);
        if (daWaitUntil != null) {
            throw new RuntimeException("Expected DA_WAIT_UNTIL to be null, got " + daWaitUntil);
        }

        return StateDecision.singleNextState(StateOptionsWorkflowState3.class);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setWaitUntilApiDataAttributesLoadingPolicy(
                        new PersistenceLoadingPolicy()
                                .persistenceLoadingType(PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK)
                                .partialLoadingKeys(Collections.singletonList(StateOptionsWorkflow.DA_WAIT_UNTIL))
                )
                .setExecuteApiDataAttributesLoadingPolicy(
                        new PersistenceLoadingPolicy()
                                .persistenceLoadingType(PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK)
                                .partialLoadingKeys(Collections.singletonList(StateOptionsWorkflow.DA_EXECUTE)));
    }
}

class StateOptionsWorkflowState3 implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, Persistence persistence, final Communication communication) {
        final String str = persistence.getDataAttribute(StateOptionsWorkflow.DA_BOTH, String.class);

        if (!str.equals("both")) {
            throw new RuntimeException("Expected DA_BOTH to be 'both', got " + str);
        }

        return CommandRequest.empty;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final String str = persistence.getDataAttribute(StateOptionsWorkflow.DA_BOTH, String.class);

        if (!str.equals("both")) {
            throw new RuntimeException("Expected DA_BOTH to be 'both', got " + str);
        }

        return StateDecision.gracefulCompleteWorkflow("success");
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setDataAttributesLoadingPolicy(
                        new PersistenceLoadingPolicy()
                                .persistenceLoadingType(PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK)
                                .partialLoadingKeys(Collections.singletonList(StateOptionsWorkflow.DA_BOTH))
                );
    }
}