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

package io.superdurable.dex.patterns.workflow.waitforstatecompletion;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.patterns.services.ServiceDependency;
import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.persistence.Persistence;

public class UpdateExternalSystemState implements WorkflowState<JobSeekerData> {
    final ServiceDependency serviceDependency;
    final ObjectMapper objectMapper;

    public UpdateExternalSystemState(final ServiceDependency serviceDependency, final ObjectMapper objectMapper) {
        this.serviceDependency = serviceDependency;
        this.objectMapper = objectMapper;
    }

    @Override
    public Class<JobSeekerData> getInputType() {
        return JobSeekerData.class;
    }

    @Override
    public StateDecision execute(final Context context, final JobSeekerData input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        try {
            serviceDependency.updateExternalSystem(objectMapper.writeValueAsString(input));
        } catch (final JsonProcessingException e) {
            throw new RuntimeException(e);
        }
        return StateDecision.gracefulCompleteWorkflow();
    }
}
