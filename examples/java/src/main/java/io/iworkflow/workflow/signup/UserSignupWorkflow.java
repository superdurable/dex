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

package io.iworkflow.workflow.signup;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.InternalChannelCommand;
import io.iworkflow.core.communication.InternalChannelDef;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.gen.models.ChannelRequestStatus;
import io.iworkflow.workflow.MyDependencyService;
import io.iworkflow.workflow.microservices.SignupForm;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

import static io.iworkflow.workflow.signup.UserSignupWorkflow.DA_FORM;
import static io.iworkflow.workflow.signup.UserSignupWorkflow.DA_Status;
import static io.iworkflow.workflow.signup.UserSignupWorkflow.VERIFY_CHANNEL;

@Component

public class UserSignupWorkflow implements ObjectWorkflow {

    public static final String DA_FORM = "Form";

    public static final String DA_Status = "Status";
    public static final String VERIFY_CHANNEL = "Verify";

    private MyDependencyService myService;

    public UserSignupWorkflow(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new SubmitState(myService)),
                StateDef.nonStartingState(new VerifyState(myService))
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(SignupForm.class, DA_FORM),
                DataAttributeDef.create(String.class, DA_Status)
        );
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Void.class, VERIFY_CHANNEL)
        );
    }

    // Atomically read/write/send message in RPC
    @RPC
    public String verify(Context context, Persistence persistence, Communication communication) {
        String status = persistence.getDataAttribute(DA_Status, String.class);
        if (status == "verified") {
            return "already verified";
        }
        persistence.setDataAttribute(DA_Status, "verified");
        communication.publishInternalChannel(VERIFY_CHANNEL, null);
        return "done";
    }
}

class SubmitState implements WorkflowState<SignupForm> {

    private MyDependencyService myService;

    public SubmitState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<SignupForm> getInputType() {
        return SignupForm.class;
    }

    @Override
    public StateDecision execute(final Context context, final SignupForm input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(UserSignupWorkflow.DA_FORM, input);
        persistence.setDataAttribute(DA_Status, "waiting");
        this.myService.sendEmail(input.getEmail(), "please verify the signup", "content");
        return StateDecision.singleNextState(VerifyState.class);
    }
}

class VerifyState implements WorkflowState<Void> {

    private MyDependencyService myService;

    public VerifyState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofSeconds(24)), // use seconds for demo
                InternalChannelCommand.create(VERIFY_CHANNEL)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        SignupForm form = persistence.getDataAttribute(DA_FORM, SignupForm.class);

        if (commandResults.getAllInternalChannelCommandResult().get(0).getRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {
            myService.sendEmail(form.getEmail(), "welcome", "welcome to Indeed!");
            return StateDecision.gracefulCompleteWorkflow("done");
        }

        myService.sendEmail(form.getEmail(), "reminder", "please verify your email");
        return StateDecision.singleNextState(VerifyState.class);
    }
}
