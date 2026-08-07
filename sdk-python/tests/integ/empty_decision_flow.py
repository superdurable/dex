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
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    go_to_multi,
)


class EmptyDecisionStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return go_to_multi()

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=RetryPolicy(maximum_attempts=1))


class EmptyDecisionFlow(Flow[int]):
    start = EmptyDecisionStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
