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

package io.superdurable.dex.products.shortlistcandidates;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class ShortlistFlow implements Flow<ShortlistInput> {
    public static final String SA_KEY_EMPLOYER_ID = "SHORTLIST_EmployerId";
    public static final String SA_KEY_CANDIDATE_ID = "SHORTLIST_CandidateId";
    public static final String DA_EMAIL_SENT_TIMESTAMP = "SHORTLIST_EmailSentTimestamp";
    public static final String SIGNAL_REVOKE_SHORTLIST = "SHORTLIST_SIGNAL_RevokeShortlist";

    public final Attribute<String> employerId = Attribute.define(
            SA_KEY_EMPLOYER_ID,
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD, "CustomKeywordField"));
    public final Attribute<String> candidateId = Attribute.define(
            SA_KEY_CANDIDATE_ID,
            String.class);
    public final Attribute<Long> emailSentTimestamp =
            Attribute.define(DA_EMAIL_SENT_TIMESTAMP, Long.class);
    public final Channel<Void> revokeShortlist =
            Channel.define(SIGNAL_REVOKE_SHORTLIST, Void.class);

    private final MyDependencyService myService;
    private final ObjectProvider<Client> clientProvider;
    private final EmployerOptInFlow employerOptInFlow;

    private final Shortlist shortlist = new Shortlist();
    private final SendEmail sendEmail = new SendEmail();

    public ShortlistFlow(
            final MyDependencyService myService,
            final ObjectProvider<Client> clientProvider,
            final EmployerOptInFlow employerOptInFlow) {
        this.myService = myService;
        this.clientProvider = clientProvider;
        this.employerOptInFlow = employerOptInFlow;
    }

    private Client client() {
        return clientProvider.getObject();
    }

    @Override
    public StepList<ShortlistInput> getSteps() {
        return StepList.startStep(shortlist).otherSteps(sendEmail);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                employerId,
                candidateId,
                emailSentTimestamp,
                revokeShortlist);
    }

    @RPC
    public RPCResult<Long> getEmailSentTimestamp(final Context context) {
        return RPCResult.of(emailSentTimestamp.get(context));
    }

    final class Shortlist implements Step<ShortlistInput> {
        @Override
        public Class<ShortlistInput> getInputType() {
            return ShortlistInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final ShortlistInput input) {
            employerId.set(context, input.employerId);
            candidateId.set(context, input.candidateId);
            emailSentTimestamp.set(context, 0L);
            return StepDecision.goTo(sendEmail, null);
        }
    }

    final class SendEmail implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofMinutes(5)),
                    revokeShortlist.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String employer = employerId.get(context);
            final String candidate = candidateId.get(context);

            if (!revokeShortlist.getConditionResults(context).isEmpty()) {
                System.out.printf(
                        "Not sending the email to %s-%s because of revoking%n",
                        employer,
                        candidate);
                return StepDecision.forceComplete();
            }

            if (!WorkflowIds.isOptedIn(client(), employerOptInFlow, employer)) {
                System.out.printf(
                        "Not sending the email to %s-%s because of not opted-in%n",
                        employer,
                        candidate);
                return StepDecision.forceComplete();
            }

            myService.sendEmail(
                    employer + "-" + candidate,
                    String.format("Employer %s wants to know more about you", employer),
                    "Hello xxx, ...");

            emailSentTimestamp.set(context, System.currentTimeMillis());
            return StepDecision.forceComplete();
        }
    }
}
