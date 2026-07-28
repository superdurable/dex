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

package io.superdurable.dex.workflow.microservices;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.RPC;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.SignalChannelDef;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.gen.models.TimerStatus;
import io.superdurable.dex.workflow.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.workflow.microservices.OrchestrationWorkflow.DA_DATA1;
import static io.superdurable.dex.workflow.microservices.OrchestrationWorkflow.READY_SIGNAL;

@Component

public class OrchestrationWorkflow implements ObjectWorkflow {

    public static final String DA_DATA1 = "SomeData";
    public static final String READY_SIGNAL = "Ready";

    private MyDependencyService myService;

    public OrchestrationWorkflow(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new State1(myService)),
                StateDef.nonStartingState(new State2(myService)),
                StateDef.nonStartingState(new State3(myService)),
                StateDef.nonStartingState(new State4(myService))
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DA_DATA1)
        );
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, READY_SIGNAL)
        );
    }

    // NOTE: this is to demonstrate how you can read/write workflow persistence in RPC
    @RPC
    public String swap(Context context, String newData, Persistence persistence, Communication communication) {
        String oldData = persistence.getDataAttribute(DA_DATA1, String.class);
        persistence.setDataAttribute(DA_DATA1, newData);
        return oldData;
    }
}

class State1 implements WorkflowState<String> {

    private MyDependencyService myService;

    public State1(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public StateDecision execute(final Context context, final String input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(DA_DATA1, input);
        System.out.println("call API1 with backoff retry in this method..");
        this.myService.callAPI1(input);
        return StateDecision.multiNextStates(State2.class, State3.class);
    }
}

class State2 implements WorkflowState<Void> {

    private MyDependencyService myService;

    public State2(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API2 with backoff retry in this method..");
        this.myService.callAPI2(someData);
        return StateDecision.deadEnd();
    }
}

class State3 implements WorkflowState<Void> {

    private MyDependencyService myService;

    public State3(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofHours(24)),
                SignalCommand.create(READY_SIGNAL)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        if (commandResults.getAllTimerCommandResults().get(0).getTimerStatus() == TimerStatus.FIRED) {
            return StateDecision.singleNextState(State4.class);
        }

        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API3 with backoff retry in this method..");
        this.myService.callAPI3(someData);
        return StateDecision.gracefulCompleteWorkflow();
    }
}

class State4 implements WorkflowState<Void> {

    private MyDependencyService myService;

    public State4(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        String someData = persistence.getDataAttribute(DA_DATA1, String.class);
        System.out.println("call API4 with backoff retry in this method..");
        this.myService.callAPI4(someData);
        return StateDecision.gracefulCompleteWorkflow();
    }
}