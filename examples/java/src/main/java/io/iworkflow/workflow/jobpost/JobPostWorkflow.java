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

package io.iworkflow.workflow.jobpost;

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
import io.iworkflow.core.persistence.PersistenceOptions;
import io.iworkflow.core.persistence.SearchAttributeDef;
import io.iworkflow.gen.models.RetryPolicy;
import io.iworkflow.gen.models.SearchAttributeValueType;
import io.iworkflow.core.WorkflowStateOptions;
import io.iworkflow.workflow.MyDependencyService;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;
import java.util.Optional;

@Component
public class JobPostWorkflow implements ObjectWorkflow {

    @Autowired
    private MyDependencyService service;

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(

                SearchAttributeDef.create(SearchAttributeValueType.TEXT, SA_KEY_JOB_DESCRIPTION),
                SearchAttributeDef.create(SearchAttributeValueType.TEXT, SA_KEY_TITLE),
                SearchAttributeDef.create(SearchAttributeValueType.INT, SA_KEY_LAST_UPDATE_TIMESTAMP),

                DataAttributeDef.create(String.class, DA_KEY_NOTES));
    }

    @Override
    public PersistenceOptions getPersistenceOptions() {
        return PersistenceOptions.builder().enableCaching(true).build();
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.nonStartingState(new ExternalUpdateState(service)));
    }

    @RPC
    public JobInfo get(Context context, Persistence persistence, Communication communication) {
        String title = persistence.getSearchAttributeText(SA_KEY_TITLE);
        String description = persistence.getSearchAttributeText(SA_KEY_JOB_DESCRIPTION);
        String notes = persistence.getDataAttribute(DA_KEY_NOTES, String.class);

        return ImmutableJobInfo.builder()
                .title(title)
                .description(description)
                .notes(Optional.ofNullable(notes))
                .build();
    }

    @RPC(bypassCachingForStrongConsistency = true)
    public JobInfo getWithStrongConsistency(Context context, Persistence persistence, Communication communication) {
        return this.get(context, persistence, communication);
    }

    @RPC
    public void update(Context context, JobInfo input, Persistence persistence, Communication communication) {
        persistence.setSearchAttributeText(SA_KEY_TITLE, input.getTitle());
        persistence.setSearchAttributeText(SA_KEY_JOB_DESCRIPTION, input.getDescription());

        persistence.setSearchAttributeInt64(SA_KEY_LAST_UPDATE_TIMESTAMP, System.currentTimeMillis());

        if (input.getNotes().isPresent()) {
            persistence.setDataAttribute(DA_KEY_NOTES, input.getNotes().get());
        }
        communication.triggerStateMovements(
                StateMovement.create(ExternalUpdateState.class));
    }

    public static final String SA_KEY_JOB_DESCRIPTION = "JobDescription";
    public static final String SA_KEY_TITLE = "Title";
    public static final String SA_KEY_LAST_UPDATE_TIMESTAMP = "LastUpdateTimeMillis";

    public static final String DA_KEY_NOTES = "Notes";
}

class ExternalUpdateState implements WorkflowState<Void> {

    private MyDependencyService service;

    ExternalUpdateState(MyDependencyService service) {
        this.service = service;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults,
            final Persistence persistence, final Communication communication) {
        service.updateExternalSystem("this is an update to external service");
        return StateDecision.deadEnd();
    }

    /**
     * By default, all state execution will retry infinitely (until workflow
     * timeout).
     * This may not work for some dependency as we may want to retry for only a
     * certain times
     */
    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setExecuteApiRetryPolicy(
                        new RetryPolicy()
                                .backoffCoefficient(2f)
                                .maximumAttempts(100)
                                .maximumAttemptsDurationSeconds(3600)
                                .initialIntervalSeconds(3)
                                .maximumIntervalSeconds(60));
    }
}