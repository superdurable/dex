# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Context, Flow, Step, StepDecision, StepList, Wait


class StateFailureStep(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("state API failure")


class StateFailureFlow(Flow[int]):
    start = StateFailureStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
