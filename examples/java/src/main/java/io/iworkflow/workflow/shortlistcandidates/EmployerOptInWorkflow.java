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

package io.iworkflow.workflow.shortlistcandidates;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.StateMovement;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.core.persistence.SearchAttributeDef;
import io.iworkflow.gen.models.SearchAttributeValueType;
import io.iworkflow.workflow.shortlistcandidates.model.EmployerOptInInput;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class EmployerOptInWorkflow implements ObjectWorkflow {
    public static final String SA_KEY_EMPLOYER_ID = "EMPLOYER_OPT_IN_EmployerId";
    public static final String DA_KEY_OPT_IN_STATUS = "EMPLOYER_OPT_IN_Status"; // true or false

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new EmployerOptInState()),
                StateDef.nonStartingState(new EmployerOptOutState())
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_EMPLOYER_ID),

                DataAttributeDef.create(Boolean.class, DA_KEY_OPT_IN_STATUS)
        );
    }

    @RPC
    public Boolean isOptedIn(final Context context, final Persistence persistence, final Communication communication) {
        return persistence.getDataAttribute(DA_KEY_OPT_IN_STATUS, Boolean.class);
    }

    @RPC
    public void optOut(final Context context, final Persistence persistence, final Communication communication) {
        communication.triggerStateMovements(
                StateMovement.create(EmployerOptOutState.class)
        );
    }
}

class EmployerOptInState implements WorkflowState<EmployerOptInInput> {
    @Override
    public Class<EmployerOptInInput> getInputType() {
        return EmployerOptInInput.class;
    }

    @Override
    public StateDecision execute(final Context context, final EmployerOptInInput input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        persistence.setSearchAttributeKeyword(EmployerOptInWorkflow.SA_KEY_EMPLOYER_ID, input.getEmployerId());

        persistence.setDataAttribute(EmployerOptInWorkflow.DA_KEY_OPT_IN_STATUS, true);

        // The whole workflow continues running when the timeout is set to 0
        return StateDecision.deadEnd();
    }
}

class EmployerOptOutState implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        // Update the data to correctly reflect the opt-in status
        persistence.setDataAttribute(EmployerOptInWorkflow.DA_KEY_OPT_IN_STATUS, false);

        // Complete the whole workflow
        return StateDecision.forceCompleteWorkflow();
    }
}
