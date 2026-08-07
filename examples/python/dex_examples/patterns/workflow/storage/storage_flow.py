# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""A singleton flow that acts as storage via per-key AttributeMap entries."""

from __future__ import annotations

from dex import (
    AttributeMap,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    StepList,
    rpc,
)

from dex_examples.patterns.workflow.storage.add_storage_item_request import (
    AddStorageItemRequest,
)

STORAGE_FLOW_ID = "sample-storage-test"


class StorageFlow(Flow[None]):
    DA_STORE = "Store"

    store = AttributeMap(DA_STORE, str)

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.store)

    @rpc
    def add_item(self, context: Context, input: AddStorageItemRequest) -> None:
        self.store.set(context, input.key, input.value)

    @rpc
    def get_item(self, context: Context, input: str) -> RPCResult[str]:
        return RPCResult(self.store.get(context, input))

    @rpc
    def remove_item(self, context: Context, input: str) -> None:
        self.store.delete(context, input)
