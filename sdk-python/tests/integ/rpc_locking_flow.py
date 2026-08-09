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
    AttributeMap,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)


class LockCompleteStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete("lock complete")


class LockWaitStep(Step[None]):
    def __init__(self, channel: Channel[None], second: LockCompleteStep) -> None:
        self.channel = channel
        self.second = second

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(self.channel.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to(self.second, None)


class RpcLockingFlow(Flow[None]):
    channel = Channel[None]("rpc-channel", type(None))
    data = Attribute("rpc-lock-data", str)
    counter = Attribute(
        "rpc-lock-counter",
        int,
        AttributeIndex(IndexType.INT),
    )
    items = AttributeMap("rpc-lock-items", str)

    def __init__(self) -> None:
        self.second = LockCompleteStep()
        self.first = LockWaitStep(self.channel, self.second)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.first).other_steps(self.second)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.data, self.counter, self.items, self.channel)

    @rpc(lock_attributes=(data.lock(), counter.lock()))
    def with_locking(self, context: Context) -> None:
        self.data.set(context, "locked")
        self.counter.set(context, 1)
        self.channel.publish(context, None)

    @rpc(lock_attributes=(items.lock("order-1"),))
    def with_attribute_map_lock(self, context: Context) -> None:
        self.items.set(context, "order-1", "locked")

    @rpc
    def without_locking(self, context: Context) -> None:
        self.channel.publish(context, None)
