# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Attribute, Context, Flow, PersistenceSchema, RPCResult, rpc


class NoStateFlow(Flow[None]):
    counter = Attribute("counter", int)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(attributes=(self.counter,))

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
