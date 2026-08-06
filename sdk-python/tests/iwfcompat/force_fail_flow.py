# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Context, Flow, Step, StepDecision, StepList, force_fail


class ForceFailStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return force_fail("a failing message")


class ForceFailFlow(Flow[int]):
    start = ForceFailStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
