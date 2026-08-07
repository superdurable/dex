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

from dex import Context, Flow, Step, StepDecision, StepList, StepOptions


class StateTimeoutStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("timeout simulation")

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_method_timeout=timedelta(milliseconds=1))


class StateTimeoutFlow(Flow[int]):
    start = StateTimeoutStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
