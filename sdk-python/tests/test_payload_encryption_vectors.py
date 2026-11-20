# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Decode the cross-SDK dex/1 golden vectors with the real codec."""

from __future__ import annotations

import base64
import json
import math
from pathlib import Path
from typing import Any

import pytest
from google.protobuf import json_format

from dex import (
    AesGcmPayloadCodec,
    FilePayloadEncryptionKeyProvider,
    PayloadContext,
    PayloadEncryptionError,
    PayloadEncryptionOptions,
)
from dex._payload_message_codec import PayloadMessageCodec
from dex.dexpb import dex_pb2 as pb

VECTORS_PATH = (
    Path(__file__).resolve().parents[2]
    / "protos"
    / "conformance"
    / "payload-encryption"
    / "vectors.json"
)
VECTORS: dict[str, Any] = json.loads(VECTORS_PATH.read_text())
CONTEXT = PayloadContext("VectorFlow", "vector-flow")


def message_codec(tmp_path: Path, case: dict[str, Any]) -> PayloadMessageCodec:
    limits = case.get("limits", VECTORS["limits"])
    key_file = tmp_path / "keys.json"
    key_file.write_text(json.dumps(VECTORS["keyFile"]))
    key_file.chmod(0o600)
    codec = AesGcmPayloadCodec(
        FilePayloadEncryptionKeyProvider(key_file),
        max_decompressed_bytes=limits["maxPlaintextBytes"],
    )
    codec._max_data_bytes = limits["maxDataBytes"]
    return PayloadMessageCodec(
        PayloadEncryptionOptions(
            codec,
            max_envelope_bytes=limits["maxEnvelopeBytes"],
            max_plaintext_bytes=limits["maxPlaintextBytes"],
        ),
        max_encoded_text_bytes=limits["maxEncodedTextBytes"],
    )


def decode_input(payloads: PayloadMessageCodec, case: dict[str, Any]) -> Any:
    case_input = case["input"]
    if "text" in case_input:
        return payloads.decode_text(case_input["text"], CONTEXT)
    value = json_format.ParseDict(case_input["value"], pb.Value())
    return payloads.decode_values([value], CONTEXT)[0]


def case_id(case: dict[str, Any]) -> str:
    return str(case["name"])


@pytest.mark.parametrize("case", VECTORS["positive"], ids=case_id)
def test_positive_vector_decodes_to_expected_value(
    tmp_path: Path, case: dict[str, Any]
) -> None:
    decoded = decode_input(message_codec(tmp_path, case), case)
    expected_plaintext = base64.b64decode(case["expectedPlaintext"])
    if "text" in case["input"]:
        assert decoded == case["expectedText"]
        assert pb.Value(string_value=decoded).SerializeToString() == expected_plaintext
        return
    assert decoded.SerializeToString() == expected_plaintext
    # Python's json module reads -0 as the integer 0, so the plaintext holds the sign.
    expected_json = json_format.ParseDict(case["expectedValue"], pb.Value())
    expected = pb.Value.FromString(expected_plaintext)
    assert decoded.WhichOneof("kind") == expected_json.WhichOneof("kind")
    if decoded.WhichOneof("kind") == "double_value":
        if math.isnan(expected_json.double_value):
            assert math.isnan(decoded.double_value)
            return
        assert decoded.double_value == expected_json.double_value
        assert math.copysign(1.0, decoded.double_value) == math.copysign(
            1.0, expected.double_value
        )
        return
    assert decoded == expected_json


@pytest.mark.parametrize("case", VECTORS["passthrough"], ids=case_id)
def test_passthrough_vector_is_unchanged(tmp_path: Path, case: dict[str, Any]) -> None:
    decoded = decode_input(message_codec(tmp_path, case), case)
    if "text" in case["input"]:
        assert decoded == case["input"]["text"]
    else:
        assert decoded == json_format.ParseDict(case["input"]["value"], pb.Value())


@pytest.mark.parametrize("case", VECTORS["negative"], ids=case_id)
def test_negative_vector_fails_with_expected_category(
    tmp_path: Path, case: dict[str, Any]
) -> None:
    with pytest.raises(PayloadEncryptionError) as raised:
        decode_input(message_codec(tmp_path, case), case)
    assert raised.value.category.value == case["expectedError"]
    assert "AAEC" not in str(raised.value)
