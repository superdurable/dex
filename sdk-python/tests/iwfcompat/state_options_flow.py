# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    go_to,
    graceful_complete,
)


class OptionsThirdStep(Step[None]):
    def __init__(self, both_value: Attribute[str]) -> None:
        self.both_value = both_value

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        if self.both_value.get(context) != "both":
            raise RuntimeError("shared attribute was not loaded in execute")
        return graceful_complete("success")

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        if self.both_value.get(context) != "both":
            raise RuntimeError("shared attribute was not loaded in wait_for")
        return Wait.skip_immediately()

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
        if self.wait_value.get(context) != "wait_until":
            raise RuntimeError("wait_for attribute was not loaded")
        if self.execute_value.get(context) != "execute":
            raise RuntimeError("execute attribute was not loaded in wait_for")
        if self.both_value.get(context) != "both":
            raise RuntimeError("shared attribute was not loaded in wait_for")
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        if self.execute_value.get(context) != "execute":
            raise RuntimeError("execute attribute was not loaded")
        if self.wait_value.get(context) != "wait_until":
            raise RuntimeError("wait_for attribute was not loaded in execute")
        if self.both_value.get(context) != "both":
            raise RuntimeError("shared attribute was not loaded in execute")
        return go_to(self.third, None)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_lock_attributes=(self.wait_value.lock(),),
            execute_lock_attributes=(self.execute_value.lock(),),
        )


class OptionsFirstStep(Step[None]):
    def __init__(
        self,
        second: OptionsSecondStep,
        wait_value: Attribute[str],
        execute_value: Attribute[str],
        both_value: Attribute[str],
    ) -> None:
        self.second = second
        self.wait_value = wait_value
        self.execute_value = execute_value
        self.both_value = both_value

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.execute_value.set(context, "execute")
        self.wait_value.set(context, "wait_until")
        self.both_value.set(context, "both")
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
        self.first = OptionsFirstStep(
            self.second,
            self.wait_value,
            self.execute_value,
            self.both_value,
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.first).other_steps(self.second, self.third)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.wait_value,
            self.execute_value,
            self.both_value,
        )
