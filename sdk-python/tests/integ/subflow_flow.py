# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import (
    Context,
    Flow,
    FlowConfig,
    Step,
    StepDecision,
    StepList,
    SubFlow,
    SubFlowOptions,
    SubFlowReusePolicy,
    Timer,
    Wait,
    graceful_complete,
)


class _SingleSubFlowStep(Step[int]):
    def __init__(
        self,
        target: Flow[int],
        reuse_policy: SubFlowReusePolicy | None = None,
    ) -> None:
        self.target = target
        self.options = SubFlowOptions(
            reuse_policy=reuse_policy
            or SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY
        )

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.until(SubFlow.run(self.target, input, self.options))

    def execute(self, context: Context, input: int) -> StepDecision:
        del input
        result = SubFlow.get_condition_results(context)
        output = "" if not result.completions else str(result.single_output(int))
        return graceful_complete(
            f"{SubFlow.get_flow_id(context)}|{result.status.name}|{output}"
        )


class SingleSubFlowParent(Flow[int]):
    def __init__(
        self,
        target: Flow[int],
        reuse_policy: SubFlowReusePolicy | None = None,
    ) -> None:
        self.start = _SingleSubFlowStep(target, reuse_policy)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)


class _AllSubFlowStep(Step[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.target = target

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.all_of(
            SubFlow.run(self.target, input),
            SubFlow.run(self.target, input + 10),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        del input
        values: list[str] = []
        for index in range(2):
            result = SubFlow.get_condition_results(context, index)
            values.append(
                f"{SubFlow.get_flow_id(context, index)}|"
                f"{result.status.name}|{result.single_output(int)}"
            )
        return graceful_complete(";".join(values))


class AllSubFlowParent(Flow[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.start = _AllSubFlowStep(target)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)


class _AnySubFlowStep(Step[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.target = target

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.any_of(
            Timer.by_duration(timedelta(0)),
            SubFlow.run(self.target, input),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        del input
        result = SubFlow.get_condition_results(context)
        rejected_output = False
        try:
            result.single_output(int)
        except ValueError:
            rejected_output = True
        return graceful_complete(
            f"{SubFlow.get_flow_id(context)}|{result.status.name}|"
            f"{str(result.is_terminal).lower()}|{str(rejected_output).lower()}"
        )


class AnySubFlowParent(Flow[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.start = _AnySubFlowStep(target)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)


class _ContinueAsNewSubFlowStep(Step[int]):
    def __init__(self, completed: Flow[int], delayed: Flow[int]) -> None:
        self.completed = completed
        self.delayed = delayed
        self.options = SubFlowOptions(
            config_override=FlowConfig(continue_as_new_threshold=100)
        )

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.all_of(
            SubFlow.run(self.completed, input, self.options),
            SubFlow.run(self.delayed, 300, self.options),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        del input
        completed = SubFlow.get_condition_results(context)
        delayed = SubFlow.get_condition_results(context, 1)
        return graceful_complete(
            f"{SubFlow.get_flow_id(context)}|{completed.single_output(int)}|"
            f"{SubFlow.get_flow_id(context, 1)}|{delayed.status.name}"
        )


class ContinueAsNewSubFlowParent(Flow[int]):
    def __init__(self, completed: Flow[int], delayed: Flow[int]) -> None:
        self.start = _ContinueAsNewSubFlowStep(completed, delayed)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
