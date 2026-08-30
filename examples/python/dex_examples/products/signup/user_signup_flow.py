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


class VerifyEmail(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        verify_email: Channel[None],
        status: Attribute[str],
    ) -> None:
        self.service = service
        self.form = form
        self.verify_email = verify_email
        self.status = status

    def wait_for(self, context: Context, input: None) -> Wait:
        self.status.set(context, "waiting_for_verification")
        return Wait.any_of(
            Timer.by_duration(timedelta(seconds=24)),
            self.verify_email.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        signup_form = self.form.get(context)
        if self.verify_email.results(context):
            self.service.send_email(
                signup_form.email,
                "complete onboarding task 1",
                "task 1 is ready",
            )
            return go_to(AccomplishTask1, None)
        self.service.send_email(
            signup_form.email,
            "reminder",
            "please verify your email",
        )
        return go_to(VerifyEmail, None)


class AccomplishTask1(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        status: Attribute[str],
        task_1_completed: Channel[None],
    ) -> None:
        self.service = service
        self.form = form
        self.status = status
        self.task_1_completed = task_1_completed

    def wait_for(self, context: Context, input: None) -> Wait:
        self.status.set(context, "waiting_for_task_1")
        return Wait.any_of(
            Timer.by_duration(timedelta(seconds=24)),
            self.task_1_completed.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        signup_form = self.form.get(context)
        if self.task_1_completed.results(context):
            self.service.send_email(
                signup_form.email,
                "complete onboarding task 2",
                "task 2 is ready",
            )
            return go_to(AccomplishTask2, None)
        self.service.send_email(
            signup_form.email,
            "task 1 reminder",
            "please complete onboarding task 1",
        )
        return go_to(AccomplishTask1, None)


class AccomplishTask2(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        status: Attribute[str],
        task_2_completed: Channel[None],
    ) -> None:
        self.service = service
        self.form = form
        self.status = status
        self.task_2_completed = task_2_completed

    def wait_for(self, context: Context, input: None) -> Wait:
        self.status.set(context, "waiting_for_task_2")
        return Wait.any_of(
            Timer.by_duration(timedelta(seconds=24)),
            self.task_2_completed.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        signup_form = self.form.get(context)
        if self.task_2_completed.results(context):
            self.status.set(context, "completed")
            self.service.send_email(
                signup_form.email,
                "onboarding complete",
                "welcome aboard",
            )
            return graceful_complete("onboarding completed")
        self.service.send_email(
            signup_form.email,
            "task 2 reminder",
            "please complete onboarding task 2",
        )
        return go_to(AccomplishTask2, None)


class Submit(Step[SignupForm]):
    def __init__(
        self,
        service: MyDependencyService,
        form: Attribute[SignupForm],
        status: Attribute[str],
        verify_step: VerifyEmail,
    ) -> None:
        self.service = service
        self.form = form
        self.status = status
        self.verify_step = verify_step

    def execute(self, context: Context, input: SignupForm) -> StepDecision:
        self.form.set(context, input)
        self.service.send_email(input.email, "verify your email", "start your onboarding")
        return go_to(VerifyEmail, None)


class UserOnboardingFlow(Flow[SignupForm]):
    form = Attribute("Form", SignupForm)
    status = Attribute("Status", str)
    verify_email = Channel[None]("VerifyEmail", type(None))
    task_1_completed = Channel[None]("Task1Completed", type(None))
    task_2_completed = Channel[None]("Task2Completed", type(None))

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.verify_step = VerifyEmail(service, self.form, self.verify_email, self.status)
        self.task_1_step = AccomplishTask1(
            service,
            self.form,
            self.status,
            self.task_1_completed,
        )
        self.task_2_step = AccomplishTask2(
            service,
            self.form,
            self.status,
            self.task_2_completed,
        )
        self.submit = Submit(service, self.form, self.status, self.verify_step)

    def get_steps(self) -> StepList[SignupForm]:
        return StepList.start_step(self.submit).other_steps(
            self.verify_step,
            self.task_1_step,
            self.task_2_step,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.form,
            self.status,
            self.verify_email,
            self.task_1_completed,
            self.task_2_completed,
        )

    @rpc
    def verify(self, context: Context) -> RPCResult[str]:
        if self.status.get(context) != "waiting_for_verification":
            return RPCResult("already verified")
        self.verify_email.publish(context, None)
        return RPCResult("verified")

    @rpc
    def accomplish_task_1(self, context: Context) -> RPCResult[str]:
        if self.status.get(context) != "waiting_for_task_1":
            return RPCResult("task 1 is not waiting")
        self.task_1_completed.publish(context, None)
        return RPCResult("task 1 accomplished")

    @rpc
    def accomplish_task_2(self, context: Context) -> RPCResult[str]:
        if self.status.get(context) != "waiting_for_task_2":
            return RPCResult("task 2 is not waiting")
        self.task_2_completed.publish(context, None)
        return RPCResult("task 2 accomplished")
