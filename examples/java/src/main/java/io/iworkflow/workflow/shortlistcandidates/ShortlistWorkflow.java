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

import io.iworkflow.core.*;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.*;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.core.persistence.SearchAttributeDef;
import io.iworkflow.gen.models.ChannelRequestStatus;
import io.iworkflow.gen.models.SearchAttributeValueType;
import io.iworkflow.workflow.MyDependencyService;
import io.iworkflow.workflow.shortlistcandidates.model.ShortlistInput;
import io.iworkflow.workflow.shortlistcandidates.utils.WorkflowUtil;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

@Component
public class ShortlistWorkflow implements ObjectWorkflow {

    @Autowired
    private MyDependencyService myService;

    @Autowired
    private Client client;

    public static final String SA_KEY_EMPLOYER_ID = "SHORTLIST_EmployerId";
    public static final String SA_KEY_CANDIDATE_ID = "SHORTLIST_CandidateId";
    public static final String DA_EMAIL_SENT_TIMESTAMP = "SHORTLIST_EmailSentTimestamp";
    public static final String SIGNAL_REVOKE_SHORTLIST = "SHORTLIST_SIGNAL_RevokeShortlist";

    @Override
    public List<StateDef> getWorkflowStates() {
        // The business logic is as follows:
        // After an employer shortlists a candidate, we will email the candidate after 5 minutes.
        // If the employer revokes the shortlist within the 5-minute timeframe, we will not send the email.
        return Arrays.asList(
                StateDef.startingState(new ShortlistState()),
                StateDef.nonStartingState(new SendEmailState(myService, client))
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_EMPLOYER_ID),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_CANDIDATE_ID),

                DataAttributeDef.create(Long.class, DA_EMAIL_SENT_TIMESTAMP)
        );
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, SIGNAL_REVOKE_SHORTLIST)
        );
    }

    @RPC
    public Long getEmailSentTimestamp(final Context context, final Persistence persistence, final Communication communication) {
        return persistence.getDataAttribute(DA_EMAIL_SENT_TIMESTAMP, Long.class);
    }
}

class ShortlistState implements WorkflowState<ShortlistInput> {
    @Override
    public Class<ShortlistInput> getInputType() {
        return ShortlistInput.class;
    }

    @Override
    public StateDecision execute(final Context context, final ShortlistInput input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        persistence.setSearchAttributeKeyword(ShortlistWorkflow.SA_KEY_EMPLOYER_ID, input.getEmployerId());
        persistence.setSearchAttributeKeyword(ShortlistWorkflow.SA_KEY_CANDIDATE_ID, input.getCandidateId());

        persistence.setDataAttribute(ShortlistWorkflow.DA_EMAIL_SENT_TIMESTAMP, 0L);

        return StateDecision.singleNextState(SendEmailState.class);
    }
}

class SendEmailState implements WorkflowState<Void> {
    private MyDependencyService myService;
    private Client client;

    public SendEmailState(final MyDependencyService myService, final Client client) {
        this.myService = myService;
        this.client = client;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofMinutes(5)),
                SignalCommand.create(ShortlistWorkflow.SIGNAL_REVOKE_SHORTLIST)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        final String employerId = persistence.getSearchAttributeKeyword(ShortlistWorkflow.SA_KEY_EMPLOYER_ID);
        final String candidateId = persistence.getSearchAttributeKeyword(ShortlistWorkflow.SA_KEY_CANDIDATE_ID);

        // If the remove_shortlist signal is received, force complete the workflow to prevent sending the email
        final SignalCommandResult revokeShortlistSignalResult = commandResults.getAllSignalCommandResults().get(0);
        if (revokeShortlistSignalResult.getSignalRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {
            System.out.printf("Not sending the email to %s-%s because of revoking%n", employerId, candidateId);
            return StateDecision.forceCompleteWorkflow();
        }

        // Check whether the employer is still in the opt-in status
        final Boolean optedIn = WorkflowUtil.isOptedIn(client, employerId);
        if (!optedIn) {
            System.out.printf("Not sending the email to %s-%s because of not opted-in%n", employerId, candidateId);
            return StateDecision.forceCompleteWorkflow();
        }

        myService.sendEmail(
                employerId + "-" + candidateId,
                String.format("Employer %s wants to know more about you", employerId),
                "Hello xxx, ..."
        );

        persistence.setDataAttribute(ShortlistWorkflow.DA_EMAIL_SENT_TIMESTAMP, System.currentTimeMillis());

        return StateDecision.forceCompleteWorkflow();
    }
}

