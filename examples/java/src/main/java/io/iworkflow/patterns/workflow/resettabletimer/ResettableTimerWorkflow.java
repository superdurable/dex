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

package io.iworkflow.patterns.workflow.resettabletimer;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.command.TimerCommandResult;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.InternalChannelCommand;
import io.iworkflow.core.communication.InternalChannelDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.gen.models.TimerStatus;

import java.time.Duration;
import java.util.List;

import static io.iworkflow.patterns.workflow.resettabletimer.ResettableTimerWorkflow.RESET_TIMER_CHANNEL;

/**
 * A workflow that starts a timer which expiry will trigger an action. Timer will be reset when a message is received.
 * <p>This workflow is designed to start an action when timer fires and gracefully complete after.</p>
 *
 * <h2>Usage</h2>
 * <p>To start the Resettable Timer Workflow, use the following REST endpoint:</p>
 * <pre>
 * GET /design-pattern/resettabletimer/start?workflowId={workflowId}
 * </pre>
 * <p>This endpoint initiates the workflow with the specified {@code workflowId}.</p>
 *
 * <p>To reset the timer, use the following REST endpoint:</p>
 * <pre>
 * GET /design-pattern/resettabletimer/reset?workflowId={workflowId}
 * </pre>
 * <p>This endpoint posts message to the internal channel and resets the timer.</p>
 *
 * <h2>Workflow States</h2>
 * <ul>
 *   <li><b>ResettableTimerState</b>: The initial state where the timer expiration is awaited.</li>
 *   <li><b>TimerExpiredState</b>: The final state after the timer expiration.</li>
 * </ul>
 *
 * <p>This workflow serves as a template for implementing timer mechanisms with a reset capability.</p>
 */
public class ResettableTimerWorkflow implements ObjectWorkflow {

    public static final String RESET_TIMER_CHANNEL = "RESET_TIMER_CHANNEL";

    private final List<StateDef> stateDefs;

    public ResettableTimerWorkflow() {
        this.stateDefs = List.of(
                StateDef.startingState(new ResettableTimerState()),
                StateDef.nonStartingState(new TimerExpiredState())
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return List.of(
                InternalChannelDef.create(String.class, RESET_TIMER_CHANNEL)
        );
    }

    @RPC
    public void sendResetMessage(Context context, Persistence persistence, Communication communication) {
        communication.publishInternalChannel(RESET_TIMER_CHANNEL, "reset");
    }
}

/**
 * Represents the initial state in the workflow that will await either timer firing or message that resets the timer
 * <p>This state can be executed multiple times {@code ResettableTimerState} and with each execution a new timer will started.</p>
 * <p>When the timer fires, the workflow will move to the {@code TimerExpiredState}.</p>
 */
class ResettableTimerState implements WorkflowState<Void> {

    // This is the timer duration that should be adjusted per use case
    public static final Duration TIMER_DURATION = Duration.ofMinutes(5);

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public CommandRequest waitUntil(Context context, Void input, Persistence persistence, Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(TIMER_DURATION),
                InternalChannelCommand.create(ResettableTimerWorkflow.RESET_TIMER_CHANNEL)
        );
    }

    @Override
    public StateDecision execute(Context context,
            Void input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        TimerCommandResult timer = commandResults.getAllTimerCommandResults().get(0);
        if (timer.getTimerStatus() == TimerStatus.FIRED) {
            // timer fired -> move to the end state
            return StateDecision.singleNextState(TimerExpiredState.class);
        }
        // the only other reason to execute is receiving a message in RESET_TIMER_CHANNEL; then loop back to the initial state
        return StateDecision.singleNextState(ResettableTimerState.class);
    }
}


/**
 * Represents the final state of the workflow after the timer has expired
 * <p>This state executes right away and leads to the workflow graceful completion.</p>
 * <p>Before the completion, any required action could be taken (ex. sending an email).</p>
 */
class TimerExpiredState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public StateDecision execute(Context context,
            Void input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        // do something like sending an email; in this case we just print message here
        System.out.println("Timer fired; this is where we would send an email");
        // complete workflow
        return StateDecision.gracefulCompleteWorkflow();
    }
}