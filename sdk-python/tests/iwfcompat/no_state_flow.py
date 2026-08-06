# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Attribute, Context, Flow, PersistenceSchema, RPCResult, rpc


class NoStateFlow(Flow[None]):
    counter = Attribute("counter", int)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.counter)

    @rpc(lock_attributes=(counter.lock(),))
    def increase_counter(self, context: Context) -> RPCResult[int]:
        next_value = self.counter.get(context) + 1
        self.counter.set(context, next_value)
        return RPCResult(next_value)

    @rpc
    def get_counter(self, context: Context) -> RPCResult[int]:
        return RPCResult(self.counter.get(context))

    @rpc
    def fail(self, context: Context, input: str) -> RPCResult[int]:
        del context
        raise ValueError(input)
