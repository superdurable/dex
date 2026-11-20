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

"""The Python Client's definition of the Flow the Go Worker serves."""

from __future__ import annotations

from dex import Context, Flow, PersistenceSchema, Step, StepDecision, StepList, StepOptions, graceful_complete

from tests.interop.payload_encryption_interop_flow import (
    PAYLOAD_INTEROP_STEP_OPTIONS,
    PayloadInteropInput,
    expected_output,
    payload_interop_persistence_schema,
    store_payload_interop_values,
)


class StoreGoPayloadInteropSecretStep(Step[PayloadInteropInput]):
    def get_step_type(self) -> str:
        return "StorePayloadInteropSecretStep"

    def get_step_options(self) -> StepOptions:
        return PAYLOAD_INTEROP_STEP_OPTIONS

    def execute(self, context: Context, input: PayloadInteropInput) -> StepDecision:
        store_payload_interop_values(context, "go", input)
        return graceful_complete(expected_output("go", input.text))


class GoPayloadInteropFlow(Flow[PayloadInteropInput]):
    def __init__(self) -> None:
        self.store_go_payload_interop_secret_step = StoreGoPayloadInteropSecretStep()

    def get_steps(self) -> StepList[PayloadInteropInput]:
        return StepList.start_step(self.store_go_payload_interop_secret_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return payload_interop_persistence_schema()
