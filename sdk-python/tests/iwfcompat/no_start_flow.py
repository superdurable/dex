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
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    graceful_complete,
    rpc,
)


class TriggeredStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete(1)


class NoStartFlow(Flow[None]):
    def __init__(self) -> None:
        self.triggered = TriggeredStep()

    def get_steps(self) -> StepList[None]:
        return StepList.without_start_step(self.triggered)

    @rpc
    def invoke(self, context: Context, input: str) -> RPCResult[int]:
        del context, input
        return RPCResult(1, (StepMovement.of(self.triggered, None),))
