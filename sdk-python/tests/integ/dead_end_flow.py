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
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    dead_end,
    graceful_complete,
    rpc,
)


class DeadEndStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return dead_end()


class DeadEndCompleteStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete()


class DeadEndFlow(Flow[None]):
    RPC_OUTPUT = 100
    idle_signal = Channel[None]("idle-signal", type(None))
    idle_internal = Channel[None]("idle-internal", type(None))

    def __init__(self) -> None:
        self.start = DeadEndStep()
        self.complete = DeadEndCompleteStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start).other_steps(self.complete)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.idle_signal, self.idle_internal)

    @rpc
    def signal_size(self, context: Context) -> RPCResult[int]:
        return RPCResult(self.idle_signal.size(context))

    @rpc
    def publish_internal(self, context: Context) -> RPCResult[int]:
        self.idle_internal.publish(context, None)
        return RPCResult(self.idle_internal.size(context))

    @rpc
    def invoke(self, context: Context, input: str) -> RPCResult[int]:
        del input
        if not context.flow_id or not context.run_id:
            raise RuntimeError("invalid RPC context")
        return RPCResult(
            self.RPC_OUTPUT,
            (StepMovement.of(DeadEndCompleteStep, None),),
        )
