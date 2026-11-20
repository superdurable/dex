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

"""The Flow that the cross-SDK payload encryption interoperability test runs.

Every SDK defines the same Flow shape under its own Flow type. Each SDK's
Worker serves only its own Flow type, and every SDK's Client starts and reads
all three. Keep this file in sync with
examples/go/interop/payloadencryption/payload_interop_flow.go and
examples/typescript/test/interop/payload-encryption-interop-flow.ts.
"""

from __future__ import annotations

from dex import (
    AesGcmPayloadCodec,
    Attribute,
    Context,
    FilePayloadEncryptionKeyProvider,
    Flow,
    PayloadEncryptionOptions,
    PersistenceSchema,
    Registry,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    graceful_complete,
)

OWNERS = ("go", "python", "typescript")
"""Every SDK whose Worker takes part in the interop test."""

FAIL_INPUT_PREFIX = "fail:"
"""Makes the Step fail with an error that names the rest of the input."""

SECRET_TEXT = Attribute("payload-interop-secret", str)
STATUS_TEXT = Attribute("payload-interop-status", str, plaintext=True)

_FLOW_TYPES = {
    "go": "GoPayloadInteropFlow",
    "python": "PythonPayloadInteropFlow",
    "typescript": "TypeScriptPayloadInteropFlow",
}


def expected_secret_text(owner: str, input: str) -> str:
    return f"{input} | stored by {owner}"


def expected_status_text(owner: str) -> str:
    return f"stored by {owner}"


def expected_output(owner: str, input: str) -> str:
    return f"completed by {owner}: {input}"


def expected_failure_text(owner: str, input: str) -> str:
    """Returns the Step error text for the input after FAIL_INPUT_PREFIX."""
    return f"payload interop failure in {owner}: {input}"


class StorePayloadInteropSecretStep(Step[str]):
    def __init__(self, owner: str) -> None:
        self._owner = owner

    def get_step_type(self) -> str:
        return "StorePayloadInteropSecretStep"

    def get_step_options(self) -> StepOptions:
        # One attempt, so a failing Step fails the Flow at once.
        return StepOptions(execute_retry=RetryPolicy(maximum_attempts=1))

    def execute(self, context: Context, input: str) -> StepDecision:
        if input.startswith(FAIL_INPUT_PREFIX):
            raise RuntimeError(
                expected_failure_text(self._owner, input.removeprefix(FAIL_INPUT_PREFIX))
            )
        SECRET_TEXT.set(context, expected_secret_text(self._owner, input))
        STATUS_TEXT.set(context, expected_status_text(self._owner))
        return graceful_complete(expected_output(self._owner, input))


class PayloadInteropFlow(Flow[str]):
    def __init__(self, owner: str) -> None:
        self.owner = owner
        self.store_payload_interop_secret_step = StorePayloadInteropSecretStep(owner)

    def get_flow_type(self) -> str:
        return _FLOW_TYPES[self.owner]

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.store_payload_interop_secret_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(SECRET_TEXT, STATUS_TEXT)


def new_client_flows() -> dict[str, PayloadInteropFlow]:
    """Returns every Owner's Flow, so one Client can start them all."""
    return {owner: PayloadInteropFlow(owner) for owner in OWNERS}


def new_client_registry(flows: dict[str, PayloadInteropFlow]) -> Registry:
    return Registry(tuple(flows.values()), allow_async_handlers=True)


def new_file_payload_encryption(key_file_path: str) -> PayloadEncryptionOptions:
    """Reads a key file, such as one from dexcli codec-server keygen."""
    return PayloadEncryptionOptions(
        AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(key_file_path))
    )
