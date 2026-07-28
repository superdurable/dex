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

package io.iworkflow.patterns.workflow.drainchannels.internal;

import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.core.Context;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;

import static io.iworkflow.patterns.workflow.drainchannels.internal.DrainInternalChannelsWorkflow.PROCESS_DATA_STATE_EXECUTION_COUNTER;

/**
 * The {@code ProcessDataState} class represents a state that processes data.
 */
public class ProcessDataState implements WorkflowState<String> {
    final ServiceDependency externalService;

    /**
     * Constructs a {@code ProcessDataState} with a {@code ServiceDependency} for external API operations.
     *
     * @param externalService the {@code ServiceDependency} for external API operations.
     */
    public ProcessDataState(final ServiceDependency externalService) {
        this.externalService = externalService;
    }

    /**
     * Returns the input type for this state, which is {@code String}.
     *
     * @return the {@code Class} object representing the input type.
     */
    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    /**
     * Loops through different status for a single mongo document and publishes them to the internal channel.
     * Once it's done looping it goes to the FinalizedState.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param commandResults the results of any commands executed.
     * @param persistence the persistence layer.
     * @param communication the communication layer.
     * @return a {@code StateDecision} object representing the next state decisions.
     */
    @Override
    public StateDecision execute(final Context context, final String input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        Integer executionCount = persistence.getDataAttribute(PROCESS_DATA_STATE_EXECUTION_COUNTER, Integer.class);
        executionCount++;
        persistence.setDataAttribute(PROCESS_DATA_STATE_EXECUTION_COUNTER, executionCount);

        final String status = switch (executionCount) {
            case 1 -> "RECEIVED";
            case 2 -> "ACCEPTED";
            case 3 -> "PASSED";
            default -> "ERROR";
        };
        final MongoDocument document = ImmutableMongoDocument.builder()
                .id(input)
                .status(status)
                .isFinalCommand(false)
                .build();
        communication.publishInternalChannel(DrainInternalChannelsWorkflow.UPSERT_MONGO_DATA_INTERNAL_CHANNEL, document);

        // Handle data (e.g., job seeker ID) or perform actions (e.g. reporting) in this state that are not desired to obstruct the UpsertMongoRecordState.
        externalService.externalApiCall("external service call to process data (e.g. notify the job seeker)");

        externalService.externalApiCall("a call to send metrics or add a log to logrepo");

        if (executionCount <= 3) {
            return StateDecision.singleNextState(ProcessDataState.class, input);
        } else {
            return StateDecision.singleNextState(FinalizeState.class);
        }
    }
}
