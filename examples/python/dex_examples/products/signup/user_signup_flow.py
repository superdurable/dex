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

from datetime import timedelta

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.signup.signup_form import SignupForm


class Verify(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        verify_channel: Channel[None],
    ) -> None:
        self.service = service
        self.form = form
        self.verify_channel = verify_channel

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(timedelta(seconds=24)),
            self.verify_channel.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        signup_form = self.form.get(context)
        if self.verify_channel.results(context):
            self.service.send_email(signup_form.email, "welcome", "welcome to Indeed!")
            return graceful_complete("done")
        self.service.send_email(
            signup_form.email,
            "reminder",
            "please verify your email",
        )
        return go_to(Verify, None)


class Submit(Step[SignupForm]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        status: Attribute[str],
        verify_step: Verify,
    ) -> None:
        self.service = service
        self.form = form
        self.status = status
        self.verify_step = verify_step

    def execute(self, context: Context, input: SignupForm) -> StepDecision:
        self.form.set(context, input)
        self.status.set(context, "waiting")
        self.service.send_email(input.email, "please verify the signup", "content")
        return go_to(Verify, None)


class UserSignupFlow(Flow[SignupForm]):
    form = Attribute("Form", SignupForm)
    status = Attribute("Status", str)
    verify_channel = Channel[None]("Verify", type(None))

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.verify_step = Verify(service, self.form, self.verify_channel)
        self.submit = Submit(service, self.form, self.status, self.verify_step)

    def get_steps(self) -> StepList[SignupForm]:
        return StepList.start_step(self.submit).other_steps(self.verify_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.form, self.status, self.verify_channel)

    @rpc
    def verify(self, context: Context) -> RPCResult[str]:
        if self.status.get(context) == "verified":
            return RPCResult("already verified")
        self.status.set(context, "verified")
        self.verify_channel.publish(context, None)
        return RPCResult("done")
