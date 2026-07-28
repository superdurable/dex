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

package io.superdurable.dex.patterns.workflow.parallel;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.BaseCommand;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.communication.InternalChannelDef;
import io.superdurable.dex.core.persistence.Persistence;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

public class ParallelStatesWithAwaitWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;

    public static final String NOTIFY_CHANNEL = "test_notify_channel";

    public ParallelStatesWithAwaitWorkflow() {
        this.stateDefs = List.of(
                StateDef.startingState(new StartingState()),
                StateDef.nonStartingState(new NotifyUser()),
                StateDef.nonStartingState(new AwaitAllUsersNotified())
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(String.class, NOTIFY_CHANNEL)
        );
    }
}

/**
 * Input is the number of users to notify
 */
class StartingState implements WorkflowState<Integer> {
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer countOfJobSeekers,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        final List<StateMovement> stateMovements = new ArrayList<>();

        // The await state must also be part of the parallel running states
        stateMovements.add(
                // Second parameter here is the number of channels to wait for
                StateMovement.create(AwaitAllUsersNotified.class, countOfJobSeekers)
        );

        // Simulate 50 jobseeker records (can be changed in the Controller input)
        for (int i = 1; i <= countOfJobSeekers; i++) {
            stateMovements.add(
                    StateMovement.create(
                            NotifyUser.class,
                            new JobSeeker(String.valueOf(i), "jobseeker@indeed.com", "0987654321")
                    )

            );
        }

        // Run states concurrently
        return StateDecision.multiNextStates(stateMovements);
    }
}

class NotifyUser implements WorkflowState<JobSeeker> {
    @Override
    public Class<JobSeeker> getInputType() {
        return JobSeeker.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            JobSeeker jobSeeker,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        // Sleep for a random time between 0 and 5 seconds
        // to simulate the time it takes to send a notification
        try {
            Thread.sleep((long) (Math.random() * 5000));
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }

        final String message = "[FAKE] Notifying user of something: " + jobSeeker.id();

        System.out.println(message);
        persistence.recordEvent("notification", message);

        communication.publishInternalChannel(ParallelStatesWithAwaitWorkflow.NOTIFY_CHANNEL, "I sent something");

        return StateDecision.deadEnd();
    }
}

class AwaitAllUsersNotified implements WorkflowState<Integer> {
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, Integer countOfJobSeekers, Persistence persistence, Communication communication) {
        ArrayList<BaseCommand> commands = new ArrayList<>();

        for (int i = 1; i <= countOfJobSeekers; i++) {
            commands.add(InternalChannelCommand.create(ParallelStatesWithAwaitWorkflow.NOTIFY_CHANNEL));
        }

        return CommandRequest.forAllCommandCompleted(commands.toArray(new BaseCommand[0]));
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer countOfJobSeekers,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        final String message = String.format("[FAKE] Sent all %s notifications", countOfJobSeekers);
        persistence.recordEvent("sent-notifications", message);
        return StateDecision.gracefulCompleteWorkflow();
    }
}