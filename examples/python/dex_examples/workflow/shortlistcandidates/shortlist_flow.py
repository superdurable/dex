# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import time
from datetime import timedelta

from dex import (
    Attribute,
    AttributeIndex,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    force_complete,
    go_to,
    rpc,
)

from dex_examples.my_dependency_service import MyDependencyService
from dex_examples.workflow.shortlistcandidates.shortlist_input import ShortlistInput
from dex_examples.workflow.shortlistcandidates.workflow_ids import OptInChecker

SA_KEY_EMPLOYER_ID = "SHORTLIST_EmployerId"
SA_KEY_CANDIDATE_ID = "SHORTLIST_CandidateId"
DA_EMAIL_SENT_TIMESTAMP = "SHORTLIST_EmailSentTimestamp"
SIGNAL_REVOKE_SHORTLIST = "SHORTLIST_SIGNAL_RevokeShortlist"


class SendEmail(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        opt_in_checker: OptInChecker,
        employer_id: Attribute[str],
        candidate_id: Attribute[str],
        email_sent_timestamp: Attribute[int],
        revoke_shortlist: Channel[None],
    ) -> None:
        self.service = service
        self.opt_in_checker = opt_in_checker
        self.employer_id = employer_id
        self.candidate_id = candidate_id
        self.email_sent_timestamp = email_sent_timestamp
        self.revoke_shortlist = revoke_shortlist

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(timedelta(minutes=5)),
            self.revoke_shortlist.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        employer = self.employer_id.get(context)
        candidate = self.candidate_id.get(context)

        if self.revoke_shortlist.results(context):
            print(f"Not sending the email to {employer}-{candidate} because of revoking")
            return force_complete()

        if not self.opt_in_checker.is_opted_in(employer):
            print(
                f"Not sending the email to {employer}-{candidate} "
                "because of not opted-in"
            )
            return force_complete()

        self.service.send_email(
            f"{employer}-{candidate}",
            f"Employer {employer} wants to know more about you",
            "Hello xxx, ...",
        )

        self.email_sent_timestamp.set(context, int(time.time() * 1000))
        return force_complete()


class Shortlist(Step[ShortlistInput]):
    def __init__(
        self,
        employer_id: Attribute[str],
        candidate_id: Attribute[str],
        email_sent_timestamp: Attribute[int],
        send_email: SendEmail,
    ) -> None:
        self.employer_id = employer_id
        self.candidate_id = candidate_id
        self.email_sent_timestamp = email_sent_timestamp
        self.send_email = send_email

    def execute(self, context: Context, input: ShortlistInput) -> StepDecision:
        self.employer_id.set(context, input.employer_id)
        self.candidate_id.set(context, input.candidate_id)
        self.email_sent_timestamp.set(context, 0)
        return go_to(self.send_email, None)


class ShortlistFlow(Flow[ShortlistInput]):
    employer_id = Attribute(
        SA_KEY_EMPLOYER_ID,
        str,
        AttributeIndex(IndexType.KEYWORD, "CustomKeywordField"),
    )
    candidate_id = Attribute(SA_KEY_CANDIDATE_ID, str)
    email_sent_timestamp = Attribute(DA_EMAIL_SENT_TIMESTAMP, int)
    revoke_shortlist = Channel[None](SIGNAL_REVOKE_SHORTLIST, type(None))

    def __init__(
        self,
        service: MyDependencyService,
        opt_in_checker: OptInChecker,
    ) -> None:
        self.service = service
        self.opt_in_checker = opt_in_checker
        self.send_email = SendEmail(
            service,
            opt_in_checker,
            self.employer_id,
            self.candidate_id,
            self.email_sent_timestamp,
            self.revoke_shortlist,
        )
        self.shortlist = Shortlist(
            self.employer_id,
            self.candidate_id,
            self.email_sent_timestamp,
            self.send_email,
        )

    def get_steps(self) -> StepList[ShortlistInput]:
        return StepList.start_step(self.shortlist).other_steps(self.send_email)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.employer_id,
            self.candidate_id,
            self.email_sent_timestamp,
            self.revoke_shortlist,
        )

    @rpc
    def get_email_sent_timestamp(self, context: Context) -> RPCResult[int]:
        return RPCResult(self.email_sent_timestamp.get(context))
