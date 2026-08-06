# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Context, Flow, Step, StepDecision, StepDef, go_to, graceful_complete


class EmptySecondStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete()


class EmptyFirstStep(Step[None]):
    def __init__(self, second: EmptySecondStep) -> None:
        self.second = second

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to(self.second, None)


class EmptyInputFlow(Flow[None]):
    def __init__(self) -> None:
        self.second = EmptySecondStep()
        self.first = EmptyFirstStep(self.second)

    def get_flow_type(self) -> str:
        return "test-customized-flow-type"

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )
