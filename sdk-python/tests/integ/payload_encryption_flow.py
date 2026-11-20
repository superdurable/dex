# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""Flows that send a secret through every Value path for payload encryption tests."""

from __future__ import annotations

from collections.abc import Generator
from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Action,
    AsyncContext,
    Attribute,
    AttributeIndex,
    AttributeMap,
    AttributeMatch,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    StepOutput,
    Stream,
    SubFlow,
    Wait,
    force_fail,
    go_to,
    graceful_complete,
    heartbeat,
    rpc,
    when_attribute_matches,
)

# Every secret starts with SECRET so history can be searched for leaks.
SECRET_NOTE = "SECRET-note-4242"
SECRET_LARGE = "SECRET-large-" + "x" * 400
SECRET_HEARTBEAT = "SECRET-heartbeat"
SECRET_STREAM = "SECRET-stream"
SECRET_LOCAL = "SECRET-local"
SECRET_EVENT = "SECRET-event"
SECRET_APPROVAL = "SECRET-approval"
SECRET_RETRY_DETAIL = "SECRET-retry-detail"
SECRET_RECOVERY_DETAIL = "SECRET-recovery-detail"
SECRET_CHILD_FAILURE = "SECRET-child-failure"
SECRET_RPC_FAILURE = "SECRET-rpc-failure"
SECRET_MAP_NOTE = "SECRET-map-note"
AWAITING_APPROVAL = "awaiting-approval"
SEARCH_TAG_KEY = "CustomKeywordField"
APPROVE_PERMISSION = "order.approve"


@dataclass
class EncryptedOrder:
    card_number: str
    search_tag: str


@dataclass
class EncryptedOrderSnapshot:
    note: str
    count: int
    status: str
    large: str


@dataclass
class EncryptedOrderSummary:
    approval: str
    child_output: str
    child_error: str
    child_flow_id: str
    recovery_detail: str
    card_number: str


class ChildEchoStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        if input == "fail":
            return force_fail(SECRET_CHILD_FAILURE)
        return graceful_complete(f"child:{input}:{context.flow_id}")


class PayloadEncryptionChildFlow(Flow[str]):
    def __init__(self) -> None:
        self.echo = ChildEchoStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.echo)


class RecordOrderStep(Step[EncryptedOrder]):
    def __init__(self, flow: PayloadEncryptionOrderFlow) -> None:
        self.flow = flow

    def execute(
        self, context: Context, input: EncryptedOrder
    ) -> Generator[StepOutput, None, StepDecision]:
        if context.attempt == 1:
            yield heartbeat(SECRET_HEARTBEAT)
            raise RuntimeError(SECRET_RETRY_DETAIL)
        if context.get_last_heartbeat_value(str) != SECRET_HEARTBEAT:
            raise AssertionError("heartbeat value did not round trip")
        flow = self.flow
        yield flow.progress.write(context, SECRET_STREAM)
        flow.note.set(context, SECRET_NOTE)
        flow.count.set(context, 42)
        flow.status.set(context, AWAITING_APPROVAL)
        flow.large.set(context, SECRET_LARGE)
        flow.map_notes.set(context, "first", SECRET_MAP_NOTE)
        flow.search_tag.set(context, input.search_tag)
        context.record_event("order-recorded", SECRET_EVENT)
        return go_to(AwaitApprovalStep, input)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=3),
                maximum_attempts=3,
            )
        )


class AsyncRecordOrderStep(RecordOrderStep):
    """The AsyncWorker form: heartbeats and Stream writes go out while it runs."""

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: EncryptedOrder
    ) -> StepDecision:
        if context.attempt == 1:
            await context.heartbeat(SECRET_HEARTBEAT)
            raise RuntimeError(SECRET_RETRY_DETAIL)
        if context.get_last_heartbeat_value(str) != SECRET_HEARTBEAT:
            raise AssertionError("heartbeat value did not round trip")
        flow = self.flow
        flow.progress.write(context, SECRET_STREAM)
        flow.note.set(context, SECRET_NOTE)
        flow.count.set(context, 42)
        flow.status.set(context, AWAITING_APPROVAL)
        flow.large.set(context, SECRET_LARGE)
        flow.map_notes.set(context, "first", SECRET_MAP_NOTE)
        flow.search_tag.set(context, input.search_tag)
        context.record_event("order-recorded", SECRET_EVENT)
        return go_to(AwaitApprovalStep, input)


class AwaitApprovalStep(Step[EncryptedOrder]):
    def __init__(self, flow: PayloadEncryptionOrderFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: EncryptedOrder) -> Wait:
        context.set_step_execution_local("note", SECRET_LOCAL)
        return Wait.until(self.flow.approvals.for_one())

    def execute(self, context: Context, input: EncryptedOrder) -> StepDecision:
        if context.get_step_execution_local("note", str) != SECRET_LOCAL:
            raise AssertionError("step execution local did not round trip")
        approval = self.flow.approvals.results(context)[0]
        return go_to(RunChildrenStep, f"{approval}|{input.card_number}")


class RunChildrenStep(Step[str]):
    def __init__(self, child: PayloadEncryptionChildFlow) -> None:
        self.child = child

    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.all_of(
            SubFlow.run(self.child, input), SubFlow.run(self.child, "fail")
        )

    def execute(self, context: Context, input: str) -> StepDecision:
        succeeded = SubFlow.get_condition_results(context, 0)
        failed = SubFlow.get_condition_results(context, 1)
        approval, card_number = input.split("|")
        summary = EncryptedOrderSummary(
            approval=approval,
            child_output=succeeded.single_output(str),
            child_error=failed.error_message or "",
            child_flow_id=SubFlow.get_flow_id(context, 0),
            recovery_detail="",
            card_number=card_number,
        )
        return go_to(FailOnceStep, summary)


class FailOnceStep(Step[EncryptedOrderSummary]):
    def execute(self, context: Context, input: EncryptedOrderSummary) -> StepDecision:
        raise RuntimeError(SECRET_RECOVERY_DETAIL)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1)
        ).on_execute_failure_proceed_to(RecoverOrderStep)


class RecoverOrderStep(Step[EncryptedOrderSummary]):
    def execute(self, context: Context, input: EncryptedOrderSummary) -> StepDecision:
        recovery = context.recovery_error
        input.recovery_detail = recovery.detail if recovery is not None else ""
        return graceful_complete(input)


class PayloadEncryptionOrderFlow(Flow[EncryptedOrder]):
    def __init__(
        self, child: PayloadEncryptionChildFlow, is_async: bool = False
    ) -> None:
        self.note = Attribute("payload-encryption-note", str)
        self.count = Attribute("payload-encryption-count", int)
        self.status = Attribute("payload-encryption-status", str, plaintext=True)
        self.large = Attribute("payload-encryption-large", str)
        self.map_notes = AttributeMap("payload-encryption-map-notes", str)
        self.search_tag = Attribute(
            SEARCH_TAG_KEY, str, AttributeIndex(IndexType.KEYWORD)
        )
        self.approvals = Channel("payload-encryption-approvals", str)
        self.progress = Stream("payload-encryption-progress", str, 1 << 20)
        self.record = AsyncRecordOrderStep(self) if is_async else RecordOrderStep(self)
        self.await_approval = AwaitApprovalStep(self)
        self.run_children = RunChildrenStep(child)
        self.fail_once = FailOnceStep()
        self.recover = RecoverOrderStep()

    def get_steps(self) -> StepList[EncryptedOrder]:
        return StepList.start_step(self.record).other_steps(
            self.await_approval, self.run_children, self.fail_once, self.recover
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.note,
            self.count,
            self.status,
            self.large,
            self.map_notes,
            self.search_tag,
            self.approvals,
            self.progress,
        )

    @rpc(
        action=Action(
            "Approve order",
            required_permission=APPROVE_PERMISSION,
            condition=when_attribute_matches(
                Attribute("payload-encryption-status", str, plaintext=True),
                AttributeMatch.equal_to(AWAITING_APPROVAL),
            ),
        )
    )
    def approve(self, context: Context, note: str) -> None:
        self.approvals.publish(context, note)

    @rpc
    def get_snapshot(self, context: Context) -> RPCResult[EncryptedOrderSnapshot]:
        return RPCResult(
            EncryptedOrderSnapshot(
                note=self.note.get(context),
                count=self.count.get(context),
                status=self.status.get(context),
                large=self.large.get(context),
            )
        )

    @rpc
    def fail_with_secret(self, context: Context) -> None:
        raise ValueError(SECRET_RPC_FAILURE)


class PlaintextOrderStep(Step[str]):
    def __init__(self, flow: PlaintextOrderFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: str) -> StepDecision:
        self.flow.note.set(context, input)
        return graceful_complete(input)


class PlaintextOrderFlow(Flow[str]):
    def __init__(self) -> None:
        self.note = Attribute("payload-encryption-plain-note", str)
        self.start = PlaintextOrderStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.note)


class EncryptedConditionStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(None)


class EncryptedActionConditionFlow(Flow[None]):
    """An Action condition on an encrypted Attribute, which Workers must reject."""

    def __init__(self) -> None:
        self.state = Attribute("payload-encryption-encrypted-state", str)
        self.start = EncryptedConditionStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.state)

    @rpc(
        action=Action(
            "Close",
            required_permission="order.close",
            condition=when_attribute_matches(
                Attribute("payload-encryption-encrypted-state", str),
                AttributeMatch.equal_to("open"),
            ),
        )
    )
    def close(self, context: Context) -> None:
        return None


class AwaitForeverStep(Step[None]):
    def __init__(self, never: Channel[str]) -> None:
        self.never = never

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.never.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(None)


class ReplayCounterFlow(Flow[None]):
    """Counts RPC executions, to show that a replayed request ID runs once."""

    def __init__(self) -> None:
        self.visits = Attribute("payload-encryption-visits", int)
        self.never = Channel("payload-encryption-never", str)
        self.start = AwaitForeverStep(self.never)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.visits, self.never)

    @rpc
    def record_visit(self, context: Context, note: str) -> RPCResult[int]:
        visits = self.visits.get(context) + 1
        self.visits.set(context, visits)
        return RPCResult(visits)
