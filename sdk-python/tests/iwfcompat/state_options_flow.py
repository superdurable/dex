# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import timedelta

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepDef,
    StepDurability,
    StepMovement,
    StepOptions,
    Wait,
    go_to,
    go_to_multi,
    graceful_complete,
)


class OptionsThirdStep(Step[None]):
    def __init__(self, both_value: Attribute[str]) -> None:
        self.both_value = both_value

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete("success")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_lock_attributes=(self.both_value.lock(),),
            execute_lock_attributes=(self.both_value.lock(),),
        )


class OptionsSecondStep(Step[None]):
    def __init__(
        self,
        third: OptionsThirdStep,
        wait_value: Attribute[str],
        execute_value: Attribute[str],
        both_value: Attribute[str],
    ) -> None:
        self.third = third
        self.wait_value = wait_value
        self.execute_value = execute_value
        self.both_value = both_value

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        self.wait_value.set(context, "wait")
        self.both_value.set(context, "wait")
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.execute_value.set(context, "execute")
        self.both_value.set(context, "execute")
        override = StepOptions(execute_method_timeout=timedelta(seconds=2))
        return go_to_multi(StepMovement.of(self.third, None, options=override))

    def get_step_options(self) -> StepOptions:
        retry = RetryPolicy(
            initial_interval=timedelta(milliseconds=10),
            maximum_attempts=3,
        )
        return StepOptions(
            wait_for_method_timeout=timedelta(seconds=1),
            execute_method_timeout=timedelta(seconds=1),
            wait_for_retry=retry,
            execute_retry=retry,
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.ASYNC,
            wait_for_lock_attributes=(self.wait_value.lock(),),
            execute_lock_attributes=(self.execute_value.lock(),),
        )


class OptionsFirstStep(Step[None]):
    def __init__(self, second: OptionsSecondStep) -> None:
        self.second = second

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to(self.second, None)


class StateOptionsFlow(Flow[None]):
    def __init__(self) -> None:
        self.wait_value = Attribute("DA_WAIT_UNTIL", str)
        self.execute_value = Attribute("DA_EXECUTE", str)
        self.both_value = Attribute("DA_BOTH", str)
        self.third = OptionsThirdStep(self.both_value)
        self.second = OptionsSecondStep(
            self.third,
            self.wait_value,
            self.execute_value,
            self.both_value,
        )
        self.first = OptionsFirstStep(self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
            StepDef.non_start_step(self.third),
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            attributes=(self.wait_value, self.execute_value, self.both_value)
        )
