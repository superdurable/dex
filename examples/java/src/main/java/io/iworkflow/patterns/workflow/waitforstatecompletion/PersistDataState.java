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

package io.iworkflow.patterns.workflow.waitforstatecompletion;

import com.fasterxml.jackson.core.JsonProcessingException;
import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.core.Context;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;

public class PersistDataState implements WorkflowState<JobSeekerData> {
    final ServiceDependency mongoCollection;

    public PersistDataState(final ServiceDependency mongoCollection) {
        this.mongoCollection = mongoCollection;
    }

    @Override
    public Class<JobSeekerData> getInputType() {
        return JobSeekerData.class;
    }

    @Override
    public StateDecision execute(final Context context, final JobSeekerData input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        try {
            mongoCollection.upsert(input);
        } catch (final JsonProcessingException e) {
            throw new RuntimeException(e);
        }
        persistence.setDataAttribute(WaitForStateCompletionWorkflow.JOB_SEEKER_DATA, input);
        return StateDecision.singleNextState(UpdateExternalSystemState.class, input);
    }
}
