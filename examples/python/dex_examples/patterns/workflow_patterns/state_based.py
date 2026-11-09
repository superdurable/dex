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

"""State-based Workflow Patterns: WCP-16 to WCP-18, WCP-39, and WCP-40."""

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass

from dex import (
    AsyncContext,
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)


@dataclass(frozen=True)
class TaskInterval:
    """When one task ran, in Unix epoch milliseconds."""

    task: str
    started_at_millis: int
    finished_at_millis: int


INTERLEAVED_TASK_SECONDS = 0.2
CRITICAL_SECTION_STEP_SECONDS = 0.1


def current_time_millis() -> int:
    return time.time_ns() // 1_000_000


async def run_timed_task(task: str, duration_seconds: float) -> TaskInterval:
    started_at_millis = current_time_millis()
    await asyncio.sleep(duration_seconds)
    return TaskInterval(task, started_at_millis, current_time_millis())


# WCP-16 Deferred Choice: AnyOf takes whichever response comes first.

OFFER_PRESENTED = Channel("offerPresented", str)
OFFER_ACCEPTED = Channel("offerAccepted", str)
OFFER_DECLINED = Channel("offerDeclined", str)


class PresentOffer(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(OFFER_PRESENTED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(AwaitCustomerResponse, None)


class AwaitCustomerResponse(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(OFFER_ACCEPTED.for_one(), OFFER_DECLINED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        if OFFER_ACCEPTED.results(context):
            return go_to(FulfillOffer, None)
        return go_to(ArchiveOffer, None)


class FulfillOffer(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("FulfillOffer")


class ArchiveOffer(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("ArchiveOffer")


class DeferredChoiceFlow(Flow[None]):
    def __init__(self) -> None:
        self.present_offer_step = PresentOffer()
        self.await_customer_response = AwaitCustomerResponse()
        self.fulfill_offer = FulfillOffer()
        self.archive_offer = ArchiveOffer()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.present_offer_step).other_steps(
            self.await_customer_response, self.fulfill_offer, self.archive_offer
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(OFFER_PRESENTED, OFFER_ACCEPTED, OFFER_DECLINED)

    @rpc
    def present_offer(self, context: Context) -> None:
        OFFER_PRESENTED.publish(context, "presented")

    @rpc
    def accept_offer(self, context: Context) -> None:
        OFFER_ACCEPTED.publish(context, "accepted")

    @rpc
    def decline_offer(self, context: Context) -> None:
        OFFER_DECLINED.publish(context, "declined")


# WCP-17: one shared Execute lock keeps the tasks apart; GoTo orders two of them.

# The lock covers one Execute, not a WaitFor or a task that spans Steps.

ONBOARDING_TASK_LOCK = Attribute("onboardingTaskLock", bool)
ONBOARDING_TASK_FINISHED = Channel("onboardingTaskFinished", TaskInterval)
ONBOARDING_TASK_COUNT = 3
ONBOARDING_TASK_OPTIONS = StepOptions(
    execute_lock_attributes=(ONBOARDING_TASK_LOCK.lock(),)
)


class StartOnboardingTasks(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(PrepareContract, None),
            StepMovement.of(RunCreditCheck, None),
            StepMovement.of(CompleteOnboardingTasks, ONBOARDING_TASK_COUNT),
        )


class PrepareContract(Step[None]):
    def get_step_options(self) -> StepOptions:
        return ONBOARDING_TASK_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        interval = await run_timed_task("PrepareContract", INTERLEAVED_TASK_SECONDS)
        ONBOARDING_TASK_FINISHED.publish(context, interval)
        return go_to(SignContract, None)


class SignContract(Step[None]):
    def get_step_options(self) -> StepOptions:
        return ONBOARDING_TASK_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        interval = await run_timed_task("SignContract", INTERLEAVED_TASK_SECONDS)
        ONBOARDING_TASK_FINISHED.publish(context, interval)
        return dead_end()


class RunCreditCheck(Step[None]):
    def get_step_options(self) -> StepOptions:
        return ONBOARDING_TASK_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        interval = await run_timed_task("RunCreditCheck", INTERLEAVED_TASK_SECONDS)
        ONBOARDING_TASK_FINISHED.publish(context, interval)
        return dead_end()


class CompleteOnboardingTasks(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(ONBOARDING_TASK_FINISHED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(list(ONBOARDING_TASK_FINISHED.results(context)))


class InterleavedParallelRoutingFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_onboarding_tasks = StartOnboardingTasks()
        self.prepare_contract = PrepareContract()
        self.sign_contract = SignContract()
        self.run_credit_check = RunCreditCheck()
        self.complete_onboarding_tasks = CompleteOnboardingTasks()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_onboarding_tasks).other_steps(
            self.prepare_contract,
            self.sign_contract,
            self.run_credit_check,
            self.complete_onboarding_tasks,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(ONBOARDING_TASK_LOCK, ONBOARDING_TASK_FINISHED)


# WCP-18 Milestone: the RPC starts ApplyConfigChange only while the window is open.

IS_CHANGE_WINDOW_OPEN = Attribute("isChangeWindowOpen", bool)
RELEASE_APPROVED = Channel("releaseApproved", str)
RELEASE_FINALIZED = Channel("releaseFinalized", str)


class OpenChangeWindow(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        IS_CHANGE_WINDOW_OPEN.set(context, True)
        return go_to(AwaitReleaseApproval, None)


class AwaitReleaseApproval(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(RELEASE_APPROVED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        IS_CHANGE_WINDOW_OPEN.set(context, False)
        return go_to(AwaitReleaseFinalization, None)


class AwaitReleaseFinalization(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(RELEASE_FINALIZED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("AwaitReleaseFinalization")


class ApplyConfigChange(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class MilestoneFlow(Flow[None]):
    def __init__(self) -> None:
        self.open_change_window = OpenChangeWindow()
        self.await_release_approval = AwaitReleaseApproval()
        self.await_release_finalization = AwaitReleaseFinalization()
        self.apply_config_change = ApplyConfigChange()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.open_change_window).other_steps(
            self.await_release_approval,
            self.await_release_finalization,
            self.apply_config_change,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            IS_CHANGE_WINDOW_OPEN, RELEASE_APPROVED, RELEASE_FINALIZED
        )

    @rpc
    def request_config_change(self, context: Context, input: str) -> RPCResult[bool]:
        if not IS_CHANGE_WINDOW_OPEN.get(context):
            return RPCResult(False)
        return RPCResult(True, next_steps=(StepMovement.of(ApplyConfigChange, input),))

    @rpc
    def approve_release(self, context: Context) -> None:
        RELEASE_APPROVED.publish(context, "approved")

    @rpc
    def finalize_release(self, context: Context) -> None:
        RELEASE_FINALIZED.publish(context, "finalized")


# WCP-39 Critical Section: one Channel message is the token for a two-Step section.

CRITICAL_SECTION_TOKEN = Channel("criticalSectionToken", str)
CRITICAL_SECTION_FINISHED = Channel("criticalSectionFinished", TaskInterval)
CRITICAL_SECTION_COUNT = 2


class StartCriticalSections(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        CRITICAL_SECTION_TOKEN.publish(context, "token")
        return go_to_many(
            StepMovement.of(ReserveStock, None),
            StepMovement.of(RecalculatePrice, None),
            StepMovement.of(FinishCriticalSections, CRITICAL_SECTION_COUNT),
        )


class ReserveStock(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CRITICAL_SECTION_TOKEN.for_one())

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        section_started_at_millis = current_time_millis()
        await asyncio.sleep(CRITICAL_SECTION_STEP_SECONDS)
        return go_to(CommitStock, section_started_at_millis)


class CommitStock(Step[int]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: int
    ) -> StepDecision:
        await asyncio.sleep(CRITICAL_SECTION_STEP_SECONDS)
        CRITICAL_SECTION_FINISHED.publish(
            context, TaskInterval("inventory", input, current_time_millis())
        )
        CRITICAL_SECTION_TOKEN.publish(context, "token")
        return dead_end()


class RecalculatePrice(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CRITICAL_SECTION_TOKEN.for_one())

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        section_started_at_millis = current_time_millis()
        await asyncio.sleep(CRITICAL_SECTION_STEP_SECONDS)
        return go_to(PublishPrice, section_started_at_millis)


class PublishPrice(Step[int]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: int
    ) -> StepDecision:
        await asyncio.sleep(CRITICAL_SECTION_STEP_SECONDS)
        CRITICAL_SECTION_FINISHED.publish(
            context, TaskInterval("pricing", input, current_time_millis())
        )
        CRITICAL_SECTION_TOKEN.publish(context, "token")
        return dead_end()


class FinishCriticalSections(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(CRITICAL_SECTION_FINISHED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(list(CRITICAL_SECTION_FINISHED.results(context)))


class CriticalSectionFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_critical_sections = StartCriticalSections()
        self.reserve_stock = ReserveStock()
        self.commit_stock = CommitStock()
        self.recalculate_price = RecalculatePrice()
        self.publish_price = PublishPrice()
        self.finish_critical_sections = FinishCriticalSections()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_critical_sections).other_steps(
            self.reserve_stock,
            self.commit_stock,
            self.recalculate_price,
            self.publish_price,
            self.finish_critical_sections,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CRITICAL_SECTION_TOKEN, CRITICAL_SECTION_FINISHED)


# WCP-40: each audit runs once, in any order, one at a time.

# The lock covers one Execute, not a WaitFor or a task that spans Steps.

AUDIT_LOCK = Attribute("auditLock", bool)
AUDIT_FINISHED = Channel("auditFinished", TaskInterval)
AUDIT_COUNT = 3
AUDIT_OPTIONS = StepOptions(execute_lock_attributes=(AUDIT_LOCK.lock(),))


class StartAudits(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(AuditPayroll, None),
            StepMovement.of(AuditExpenses, None),
            StepMovement.of(AuditTaxes, None),
            StepMovement.of(CompleteAudits, AUDIT_COUNT),
        )


class AuditPayroll(Step[None]):
    def get_step_options(self) -> StepOptions:
        return AUDIT_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        AUDIT_FINISHED.publish(
            context, await run_timed_task("AuditPayroll", INTERLEAVED_TASK_SECONDS)
        )
        return dead_end()


class AuditExpenses(Step[None]):
    def get_step_options(self) -> StepOptions:
        return AUDIT_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        AUDIT_FINISHED.publish(
            context, await run_timed_task("AuditExpenses", INTERLEAVED_TASK_SECONDS)
        )
        return dead_end()


class AuditTaxes(Step[None]):
    def get_step_options(self) -> StepOptions:
        return AUDIT_OPTIONS

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        AUDIT_FINISHED.publish(
            context, await run_timed_task("AuditTaxes", INTERLEAVED_TASK_SECONDS)
        )
        return dead_end()


class CompleteAudits(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(AUDIT_FINISHED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(list(AUDIT_FINISHED.results(context)))


class InterleavedRoutingFlow(Flow[None]):
    def __init__(self) -> None:
        self.start_audits = StartAudits()
        self.audit_payroll = AuditPayroll()
        self.audit_expenses = AuditExpenses()
        self.audit_taxes = AuditTaxes()
        self.complete_audits = CompleteAudits()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_audits).other_steps(
            self.audit_payroll,
            self.audit_expenses,
            self.audit_taxes,
            self.complete_audits,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(AUDIT_LOCK, AUDIT_FINISHED)
