# Copyright (c) 2026 Super Durable, Inc.
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

"""Termination Workflow Patterns: WCP-11 (implicit) and WCP-43 (explicit)."""

from __future__ import annotations

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    force_complete,
    go_to_many,
    graceful_complete,
    rpc,
)

# WCP-11 Implicit Termination: the Flow closes after its last active Step.

PROFILE_COMPLETED = Channel("profileCompleted", str)


class StartOnboarding(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(CreateAccount, None),
            StepMovement.of(SendWelcomeEmail, None),
            StepMovement.of(AwaitProfileCompletion, None),
        )


class CreateAccount(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("CreateAccount")


class SendWelcomeEmail(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("SendWelcomeEmail")


class AwaitProfileCompletion(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(PROFILE_COMPLETED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("AwaitProfileCompletion")


class ImplicitTerminationFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_onboarding = StartOnboarding()
        self.create_account = CreateAccount()
        self.send_welcome_email = SendWelcomeEmail()
        self.await_profile_completion = AwaitProfileCompletion()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_onboarding).other_steps(
            self.create_account, self.send_welcome_email, self.await_profile_completion
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(PROFILE_COMPLETED)

    @rpc
    def complete_profile(self, context: Context) -> None:
        PROFILE_COMPLETED.publish(context, "completed")


# WCP-43 Explicit Termination: ForceComplete closes the Flow and cancels the rest.

DIAGNOSTICS_COLLECTED = Channel("diagnosticsCollected", str)
ON_CALL_ACKNOWLEDGED = Channel("onCallAcknowledged", str)
INCIDENT_RESOLVED = Channel("incidentResolved", str)


class StartIncident(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(CollectDiagnostics, None),
            StepMovement.of(NotifyOnCall, None),
            StepMovement.of(ResolveIncident, None),
        )


class CollectDiagnostics(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(DIAGNOSTICS_COLLECTED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("CollectDiagnostics")


class NotifyOnCall(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(ON_CALL_ACKNOWLEDGED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("NotifyOnCall")


class ResolveIncident(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(INCIDENT_RESOLVED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return force_complete("ResolveIncident")


class ExplicitTerminationFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_incident = StartIncident()
        self.collect_diagnostics = CollectDiagnostics()
        self.notify_on_call = NotifyOnCall()
        self.resolve_incident_step = ResolveIncident()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_incident).other_steps(
            self.collect_diagnostics, self.notify_on_call, self.resolve_incident_step
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            DIAGNOSTICS_COLLECTED, ON_CALL_ACKNOWLEDGED, INCIDENT_RESOLVED
        )

    @rpc
    def resolve_incident(self, context: Context) -> None:
        INCIDENT_RESOLVED.publish(context, "resolved")
