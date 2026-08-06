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
    AttributeIndex,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepDef,
    StepMovement,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)


class RpcSecondStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class RpcFirstStep(Step[int]):
    def __init__(self, internal: Channel[None], second: RpcSecondStep) -> None:
        self.internal = internal
        self.second = second

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_of(self.internal.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return go_to(self.second, 0)


class RpcFlow(Flow[int]):
    internal = Channel[None]("rpc-internal", type(None))
    data = Attribute("rpc-data", str)
    keyword = Attribute(
        "rpc-keyword",
        str,
        AttributeIndex(IndexType.KEYWORD),
    )

    def __init__(self) -> None:
        self.second = RpcSecondStep()
        self.first = RpcFirstStep(self.internal, self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            attributes=(self.data, self.keyword),
            channels=(self.internal,),
        )

    @rpc
    def no_persistence(self, context: Context) -> None:
        self.internal.publish(context, None)

    @rpc
    def function_one(self, context: Context, input: str) -> RPCResult[int]:
        self.data.set(context, input)
        self.keyword.set(context, input)
        return RPCResult(1, (StepMovement.of(self.second, 0),))

    @rpc
    def function_zero(self, context: Context) -> RPCResult[int]:
        del context
        return RPCResult(1, (StepMovement.of(self.second, 0),))

    @rpc
    def procedure_one(self, context: Context, input: str) -> None:
        self.data.set(context, input)

    @rpc
    def procedure_zero(self, context: Context) -> None:
        self.internal.publish(context, None)

    @rpc
    def read_only(self, context: Context, input: str) -> RPCResult[int]:
        del context
        return RPCResult(len(input))

    @rpc
    def set_data(self, context: Context, input: str) -> None:
        self.data.set(context, input)

    @rpc
    def get_data(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.data.get(context))

    @rpc
    def set_keyword(self, context: Context, input: str) -> None:
        self.keyword.set(context, input)

    @rpc
    def get_keyword(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.keyword.get(context))
