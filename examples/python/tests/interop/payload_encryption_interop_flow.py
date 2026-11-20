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

"""The Step and Attributes of the cross-SDK payload encryption interoperability Flow.

Every SDK defines the same Flow shape under its own Flow type. Each SDK's
Worker serves only its own Flow type, and every SDK's Client starts and reads
all three. Each Flow type lives in its own module, so dexcli visualize can
render it. Keep this package in sync with examples/go/interop/payloadencryption/
and examples/typescript/test/interop/.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass

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
    StepOptions,
)

OWNERS = ("go", "python", "typescript")
"""Every SDK whose Worker takes part in the interop test."""

FAIL_INPUT_PREFIX = "fail:"
"""Makes the Step fail with an error that names the rest of the input."""


@dataclass(frozen=True)
class PayloadInteropNested:
    level: int
    weight: float
    flags: list[bool]
    note: str


@dataclass(frozen=True)
class PayloadInteropDocument:
    """A JSON object with nested fields, arrays, and non-ASCII text."""

    title: str
    tags: list[str]
    nested: PayloadInteropNested


@dataclass(frozen=True)
class PayloadInteropInput:
    text: str
    count: int
    ratio: float
    document: PayloadInteropDocument


SECRET_TEXT = Attribute("payload-interop-secret", str)
STATUS_TEXT = Attribute("payload-interop-status", str, plaintext=True)
DOCUMENT_OBJECT = Attribute("payload-interop-document", PayloadInteropDocument)
COUNT_INTEGER = Attribute("payload-interop-count", int)
RATIO_DOUBLE = Attribute("payload-interop-ratio", float)

EXPECTED_COUNT = -1234567890123
"""The int64 every Client sends; it needs more than 32 bits."""

EXPECTED_RATIO = 3.141592653589793

EXPECTED_DOCUMENT = PayloadInteropDocument(
    title="naïve café ✓ Grüße €",
    tags=["alpha", "beta ✓", ""],
    nested=PayloadInteropNested(level=3, weight=1.25, flags=[True, False], note="ü ß ✓"),
)


def new_payload_interop_input(text: str) -> PayloadInteropInput:
    return PayloadInteropInput(
        text=text, count=EXPECTED_COUNT, ratio=EXPECTED_RATIO, document=EXPECTED_DOCUMENT
    )


def expected_secret_text(owner: str, input: str) -> str:
    return f"{input} | stored by {owner}"


def expected_status_text(owner: str) -> str:
    return f"stored by {owner}"


def expected_output(owner: str, input: str) -> str:
    return f"completed by {owner}: {input}"


def expected_failure_text(owner: str, input: str) -> str:
    """Returns the Step error text for the input after FAIL_INPUT_PREFIX."""
    return f"payload interop failure in {owner}: {input}"


PAYLOAD_INTEROP_STEP_OPTIONS = StepOptions(execute_retry=RetryPolicy(maximum_attempts=1))
"""One attempt, so a failing Step fails the Flow at once."""


def store_payload_interop_values(context: Context, owner: str, input: PayloadInteropInput) -> None:
    """Writes the Owner's Attributes, or raises the Step error for a failing input."""
    if input.text.startswith(FAIL_INPUT_PREFIX):
        raise RuntimeError(
            expected_failure_text(owner, input.text.removeprefix(FAIL_INPUT_PREFIX))
        )
    SECRET_TEXT.set(context, expected_secret_text(owner, input.text))
    STATUS_TEXT.set(context, expected_status_text(owner))
    DOCUMENT_OBJECT.set(context, input.document)
    COUNT_INTEGER.set(context, input.count)
    RATIO_DOUBLE.set(context, input.ratio)


def payload_interop_persistence_schema() -> PersistenceSchema:
    return PersistenceSchema.of(
        SECRET_TEXT, STATUS_TEXT, DOCUMENT_OBJECT, COUNT_INTEGER, RATIO_DOUBLE
    )


def new_client_registry(flows: Mapping[str, Flow[PayloadInteropInput]]) -> Registry:
    return Registry(tuple(flows.values()), allow_async_handlers=True)


def new_file_payload_encryption(key_file_path: str) -> PayloadEncryptionOptions:
    """Reads a key file, such as one from dexcli codec-server keygen."""
    return PayloadEncryptionOptions(
        AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(key_file_path)),
        encrypt_string_values=True,
        encrypt_primitive_values=True,
    )
