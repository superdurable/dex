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

import com.fasterxml.jackson.core.JsonProcessingException;
import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.StateMovement;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.InternalChannelCommand;
import io.iworkflow.core.communication.InternalChannelCommandResult;
import io.iworkflow.core.communication.InternalChannelDef;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.gen.models.ChannelRequestStatus;

import java.util.Arrays;
import java.util.List;
import java.util.Optional;

public class DrainInternalChannelsWorkflow implements ObjectWorkflow {
    final static String UPSERT_MONGO_DATA_INTERNAL_CHANNEL = "upsert_mongo_data_internal_channel";
    final static String PROCESS_DATA_STATE_EXECUTION_COUNTER = "process_data_state_execution_counter";

    final ServiceDependency externalService;
    final ServiceDependency mongoCollection;

    /**
     * Constructs a DrainInternalChannelsWorkflow with two instances of ServiceDependency.
     * @param externalService ServiceDependency instance that mocks an external service.
     * @param mongoCollection ServiceDependency instances that mocks a mongo collection.
     */
    public DrainInternalChannelsWorkflow(
            final ServiceDependency externalService,
            final ServiceDependency mongoCollection
    ) {
        this.externalService = externalService;
        this.mongoCollection = mongoCollection;
    }

    /**
     * Returns the list of states that define the workflow.
     *
     * @return a list of {@code StateDef} objects representing the workflow states.
     */
    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new UpsertMongoRecordState(mongoCollection)),
                StateDef.nonStartingState(new ProcessDataState(externalService)),
                StateDef.nonStartingState(new FinalizeState())
        );
    }

    /**
     * Returns the persistence schema for the workflow, which defines the data attributes
     * that are persisted across workflow states. This schema includes an integer attribute
     * to track the execution counter for the ProcessDataState.
     *
     * @return a list of {@code PersistenceFieldDef} objects representing the persistence schema.
     */
    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(Integer.class, PROCESS_DATA_STATE_EXECUTION_COUNTER)
        );
    }

    /**
     * Returns the communication schema for the workflow, which includes one internal channel.
     *
     * @return a list of {@code CommunicationMethodDef} objects representing the communication schema.
     */
    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(MongoDocument.class, UPSERT_MONGO_DATA_INTERNAL_CHANNEL)
        );
    }
}

/**
 * The {@code InitState} class represents the initial state of the workflow.
 */
class InitState implements WorkflowState<String> {

    /**
     * Returns the input type for this state, which is {@code Void}.
     *
     * @return the {@code Class} object representing the input type.
     */
    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    /**
     * Executes the state logic and returns the next state decisions.
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
        persistence.setDataAttribute(DrainInternalChannelsWorkflow.PROCESS_DATA_STATE_EXECUTION_COUNTER, 0);
        //Starting two states so the processes in ProcessDataState doesn't block UpsertMongoRecordState
        return StateDecision.multiNextStates(
                StateMovement.create(UpsertMongoRecordState.class),
                StateMovement.create(ProcessDataState.class, input));
    }
}

/**
 * The {@code UpsertMongoRecordState} class represents a state that upserts a record into a MongoDB collection.
 */
class UpsertMongoRecordState implements WorkflowState<Void> {
    final ServiceDependency mongoCollection;

    /**
     * Constructs an {@code UpsertMongoRecordState} with a {@code ServiceDependency} for MongoDB operations.
     *
     * @param mongoCollection the {@code ServiceDependency} for MongoDB operations.
     */
    public UpsertMongoRecordState(final ServiceDependency mongoCollection) {
        this.mongoCollection = mongoCollection;
    }

    /**
     * Returns the input type for this state, which is {@code Void}.
     *
     * @return the {@code Class} object representing the input type.
     */
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    /**
     * Waits for a command in the UPSERT_MONGO_DATA_INTERNAL_CHANNEL before executing the state logic.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param persistence the persistence layer.
     * @param communication the communication layer.
     * @return a {@code CommandRequest} object representing the command to wait for.
     */
    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                InternalChannelCommand.create(DrainInternalChannelsWorkflow.UPSERT_MONGO_DATA_INTERNAL_CHANNEL)
        );
    }

    /**
     * Executes the state logic and returns the next state decisions.
     * Gets a command from UPSERT_MONGO_DATA_INTERNAL_CHANNEL and mock upserts to a mongo collection.
     * If message is the final command then it gracefully completes (waits for other threads to close),
     * else loops this state.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param commandResults the results of any commands executed.
     * @param persistence the persistence layer.
     * @param communication the communication layer.
     * @return a {@code StateDecision} object representing the next state decisions.
     * @throws IllegalStateException if no document or data was sent.
     */
    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        final Optional<InternalChannelCommandResult> result = commandResults.getAllInternalChannelCommandResult()
                .stream()
                .filter(r -> r.getRequestStatusEnum().equals(ChannelRequestStatus.RECEIVED))
                .findFirst();

        if(result.isEmpty()) {
            throw new IllegalStateException("No document was sent");
        }

        final Optional<Object> data = result.get().getValue();

        if (data.isEmpty()) {
            throw new IllegalStateException("No data was sent");
        }

        final MongoDocument document = (MongoDocument) data.get();
        try {
            mongoCollection.upsert(document);
        } catch (final JsonProcessingException e) {
            throw new RuntimeException(e);
        }

        //This is the crux of this pattern. This state recognizes that the final message has been upserted,
        // allowing the workflow to be closed without any data loss.
        if (document.isFinalCommand()) {
            return StateDecision.gracefulCompleteWorkflow();
        } else {
            return StateDecision.singleNextState(UpsertMongoRecordState.class);
        }
    }
}

/**
 * The {@code FinalizeState} class represents the final state of the workflow.
 */
class FinalizeState implements WorkflowState<Void> {

    /**
     * Returns the input type for this state, which is {@code Void}.
     *
     * @return the {@code Class} object representing the input type.
     */
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    /**
     * Creates the final mongo document update and publishes it to the internal channel, then closes this thread.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param commandResults the results of any commands executed.
     * @param persistence the persistence layer.
     * @param communication the communication layer.
     * @return a {@code StateDecision} object representing the next state decisions.
     */
    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        final MongoDocument document = ImmutableMongoDocument.builder()
                .id("documentId-1")
                .status("FINALIZED")
                .isFinalCommand(true)
                .build();
        communication.publishInternalChannel(DrainInternalChannelsWorkflow.UPSERT_MONGO_DATA_INTERNAL_CHANNEL, document);

        return StateDecision.gracefulCompleteWorkflow();
    }
}
