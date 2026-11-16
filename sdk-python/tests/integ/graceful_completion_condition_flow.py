# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    dead_end,
    go_to,
    graceful_completion_requested,
    rpc,
)

GATE_INPUT = "gate"
DIRECT_INPUT = "direct"


class GracefulConditionGateStep(Step[str]):
    def __init__(self, flow: GracefulCompletionConditionFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: str) -> Wait:
        if input == DIRECT_INPUT:
            return Wait.skip_immediately()
        return Wait.until(self.flow.gate.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to(self.flow.message_or_stop, None)


class GracefulConditionMessageOrStopStep(Step[None]):
    def __init__(self, flow: GracefulCompletionConditionFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        is_supported = context.supports_graceful_completion_condition()
        self.flow.support_observations.append(is_supported)
        if not is_supported:
            return Wait.until(self.flow.messages.for_one())
        return Wait.any_of(
            self.flow.messages.for_one(),
            graceful_completion_requested(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        messages = self.flow.messages.results(context)
        is_graceful_completed = context.is_graceful_completion_condition_completed()
        self.flow.observations.append((tuple(messages), is_graceful_completed))
        if messages:
            return go_to(self.flow.message_or_stop, None)
        if is_graceful_completed:
            return dead_end()
        return go_to(self.flow.message_or_stop, None)


class GracefulCompletionConditionFlow(Flow[str]):
    messages = Channel("graceful-condition-messages", str)
    gate = Channel("graceful-condition-gate", str)

    def __init__(self) -> None:
        self.observations: list[tuple[tuple[str, ...], bool]] = []
        self.support_observations: list[bool] = []
        self.gate_step = GracefulConditionGateStep(self)
        self.message_or_stop = GracefulConditionMessageOrStopStep(self)

    def get_flow_type(self) -> str:
        # Other SDK suites may share the test Server; a unique type keeps their Workers apart.
        return "PythonGracefulCompletionConditionFlow"

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.gate_step).other_steps(self.message_or_stop)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.messages, self.gate)

    @rpc
    def send_message(self, context: Context, message: str) -> None:
        self.messages.publish(context, message)

    @rpc
    def open_gate(self, context: Context) -> None:
        self.gate.publish(context, "open")
