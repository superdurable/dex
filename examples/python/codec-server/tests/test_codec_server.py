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

"""Tests the codec server protocol, the golden vectors, and the security rules."""

from __future__ import annotations

import base64
import gzip
import json
import logging
import os
import tracemalloc
from collections.abc import AsyncIterator, Sequence
from pathlib import Path
from typing import Any

import pytest
from dex import (
    AesGcmPayloadCodec,
    PayloadCompression,
    PayloadContext,
    PayloadEncryptionError,
    PayloadErrorCategory,
)
from dex.dexpb import dex_pb2 as pb
from google.protobuf import json_format

from dex_codec_server.auth import AnyPayloadCodec, CallerForbiddenError, CallerIdentity
from dex_codec_server.config import PayloadSizeLimits
from dex_codec_server.keys import create_aes_gcm_payload_codec
from dex_codec_server.value_json import measure_value_json_bytes, serialize_value_json
from tests.codec_server_fixtures import (
    SDK_CONTEXT,
    TEST_ALLOWED_ORIGIN,
    TEST_BEARER_TOKEN,
    TEST_CONTEXT,
    VECTORS_PATH,
    assert_error_response,
    assert_same_value,
    build_test_server,
    default_test_config,
    envelope_text,
    parse_value_json,
    post_json,
    sample_values,
    sdk_codec_from_key_file,
    serve_test_server,
    write_random_key_file,
)

SENTINEL_SECRET = "SENTINEL-provider-secret-arn:kms:tenant-7"


@pytest.fixture
def key_file_path(tmp_path: Path) -> Path:
    path, _ = write_random_key_file(tmp_path)
    return path


async def test_decode_returns_values_encrypted_by_python_sdk_codec(key_file_path: Path) -> None:
    sdk_codec = sdk_codec_from_key_file(key_file_path)
    values = sample_values()
    envelopes = sdk_codec.encode(SDK_CONTEXT, [value.SerializeToString() for value in values])
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        status, _, body = await post_json(
            client,
            "/decode",
            {"context": TEST_CONTEXT, "envelopes": [envelope_text(e) for e in envelopes]},
        )
    assert status == 200, body
    assert len(body["values"]) == len(values)
    for got_json, want in zip(body["values"], values):
        assert_same_value(parse_value_json(got_json), want)


async def test_python_sdk_codec_decodes_envelopes_from_encode(key_file_path: Path) -> None:
    values = sample_values()
    value_json = [
        {"stringValue": "hello, dex"},
        {"intValue": "-7"},
        {"doubleValue": -0.0},
        {"boolValue": True},
        {"objValue": {"encoding": "json", "payload": "eyJvcmRlciI6IkEtMTAwMSJ9"}},
    ]
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        status, _, body = await post_json(
            client, "/encode", {"context": TEST_CONTEXT, "values": value_json}
        )
    assert status == 200, body
    envelopes = [pb.PayloadEnvelope.FromString(base64.b64decode(t)) for t in body["envelopes"]]
    assert all(envelope.key_id == "k1" for envelope in envelopes)
    plaintexts = sdk_codec_from_key_file(key_file_path).decode(SDK_CONTEXT, envelopes)
    for plaintext, want in zip(plaintexts, values):
        assert_same_value(pb.Value.FromString(plaintext), want)


async def test_empty_flow_type_is_accepted(key_file_path: Path) -> None:
    context_without_flow_type = {"flow_type": "", "flow_id": "order-1001"}
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        status, _, body = await post_json(
            client,
            "/encode",
            {"context": context_without_flow_type, "values": [{"stringValue": "x"}]},
        )
        assert status == 200, body
        status, _, decoded = await post_json(
            client,
            "/decode",
            {"context": {"flow_id": "order-1001"}, "envelopes": body["envelopes"]},
        )
    assert status == 200, decoded
    assert decoded == {"values": [{"stringValue": "x"}]}


async def test_empty_lists_return_empty_lists(key_file_path: Path) -> None:
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        status, headers, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": []}
        )
        assert (status, body) == (200, {"values": []})
        assert headers["Cache-Control"] == "no-store"
        status, _, body = await post_json(
            client, "/encode", {"context": TEST_CONTEXT, "values": []}
        )
        assert (status, body) == (200, {"envelopes": []})


def _load_vectors() -> dict[str, Any]:
    vectors: dict[str, Any] = json.loads(VECTORS_PATH.read_text())
    return vectors


def _vector_envelope_text(vector_case: dict[str, Any]) -> str | None:
    """Return what a caller sends to /decode: the dex/1 payload or the text after the prefix."""
    case_input = vector_case["input"]
    if "text" in case_input:
        text: str = case_input["text"]
        return text.removeprefix("dex-encoded:") if text.startswith("dex-encoded:") else None
    object_value = case_input.get("value", {}).get("objValue")
    if not object_value or object_value.get("encoding") != "dex/1":
        return None
    payload: str = object_value.get("payload", "")
    return payload


def _vector_limits(limits: dict[str, int]) -> PayloadSizeLimits:
    return PayloadSizeLimits(
        max_envelope_bytes=limits["maxEnvelopeBytes"],
        max_encoded_text_bytes=limits["maxEncodedTextBytes"],
        max_data_bytes=limits["maxDataBytes"],
        max_plaintext_bytes=limits["maxPlaintextBytes"],
    )


async def test_decode_golden_vectors(tmp_path: Path) -> None:
    vectors = _load_vectors()
    key_file_path = tmp_path / "vector-keys.json"
    key_file_path.write_text(json.dumps(vectors["keyFile"]))
    key_file_path.chmod(0o600)

    async def decode_one(vector_case: dict[str, Any], text: str) -> tuple[int, Any]:
        limits = _vector_limits(vector_case.get("limits") or vectors["limits"])
        config = default_test_config(key_file_path, payload_size_limits=limits)
        async with serve_test_server(build_test_server(config)) as client:
            status, _, body = await post_json(
                client, "/decode", {"context": TEST_CONTEXT, "envelopes": [text]}
            )
        return status, body

    decoded_cases = 0
    for vector_case in vectors["positive"]:
        text = _vector_envelope_text(vector_case)
        if text is None:
            continue
        status, body = await decode_one(vector_case, text)
        assert status == 200, (vector_case["name"], body)
        want = pb.Value.FromString(base64.b64decode(vector_case["expectedPlaintext"]))
        assert_same_value(parse_value_json(body["values"][0]), want)
        decoded_cases += 1
    for vector_case in vectors["negative"]:
        text = _vector_envelope_text(vector_case)
        # The caller checks the marker and that prefix text holds a string, so the server cannot.
        if (
            text is None
            or vector_case["expectedError"] == "unsupported_marker"
            or vector_case["name"] == "text-plaintext-not-string"
        ):
            continue
        status, body = await decode_one(vector_case, text)
        try:
            assert_error_response(status, body, 422, vector_case["expectedError"], 0)
        except AssertionError as error:
            raise AssertionError(f"{vector_case['name']}: {error}") from None
        decoded_cases += 1
    assert decoded_cases >= 55, f"only {decoded_cases} vector cases ran; the layout may have changed"


async def test_decode_error_names_first_failing_index(tmp_path: Path) -> None:
    vectors = _load_vectors()
    key_file_path = tmp_path / "vector-keys.json"
    key_file_path.write_text(json.dumps(vectors["keyFile"]))
    envelope_by_name = {
        vector_case["name"]: text
        for vector_case in vectors["positive"] + vectors["negative"]
        if (text := _vector_envelope_text(vector_case)) is not None
    }
    envelope_by_name["non-canonical-base64"] = "AAE"
    cases = [
        (["string-ascii", "int-positive", "wrong-tag"], "authentication_failed", 2),
        (["wrong-tag", "non-canonical-base64"], "authentication_failed", 0),
        (["plaintext-null-value", "wrong-tag"], "invalid_plaintext", 0),
        (["string-ascii", "non-canonical-base64", "unknown-key-id"], "malformed", 1),
    ]
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        for names, want_category, want_index in cases:
            status, _, body = await post_json(
                client,
                "/decode",
                {"context": TEST_CONTEXT, "envelopes": [envelope_by_name[n] for n in names]},
            )
            assert_error_response(status, body, 422, want_category, want_index)


async def test_cors_rejects_unlisted_origin(key_file_path: Path) -> None:
    attacker_origin = "https://attacker.example.com"
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:

        async def preflight(origin: str) -> Any:
            return await client.options(
                "/decode",
                headers={
                    "Origin": origin,
                    "Access-Control-Request-Method": "POST",
                    "Access-Control-Request-Headers": "authorization, content-type",
                },
            )

        allowed = await preflight(TEST_ALLOWED_ORIGIN)
        assert allowed.status == 204
        assert allowed.headers["Access-Control-Allow-Origin"] == TEST_ALLOWED_ORIGIN
        assert "Authorization" in allowed.headers["Access-Control-Allow-Headers"]
        assert "Access-Control-Allow-Credentials" not in allowed.headers

        rejected = await preflight(attacker_origin)
        assert rejected.status == 403
        assert "Access-Control-Allow-Origin" not in rejected.headers

        body = {"context": TEST_CONTEXT, "envelopes": []}
        status, headers, response_body = await post_json(
            client, "/decode", body, {"Origin": attacker_origin}
        )
        assert_error_response(status, response_body, 403, "forbidden")
        assert "Access-Control-Allow-Origin" not in headers

        status, headers, response_body = await post_json(
            client, "/decode", body, {"Origin": TEST_ALLOWED_ORIGIN.upper()}
        )
        assert status == 200, response_body
        assert headers["Access-Control-Allow-Origin"] == TEST_ALLOWED_ORIGIN.upper()


async def test_cors_allow_credentials_header(key_file_path: Path) -> None:
    config = default_test_config(key_file_path, allow_credentials=True)
    async with serve_test_server(build_test_server(config)) as client:
        status, headers, _ = await post_json(
            client,
            "/decode",
            {"context": TEST_CONTEXT, "envelopes": []},
            {"Origin": TEST_ALLOWED_ORIGIN},
        )
    assert status == 200
    assert headers["Access-Control-Allow-Credentials"] == "true"


async def test_bearer_token_failures_return_401(key_file_path: Path) -> None:
    body = {"context": TEST_CONTEXT, "values": []}
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        for authorization in (
            "",
            "Bearer not-the-configured-token-0123456789",
            f"Basic {TEST_BEARER_TOKEN}",
            f"Bearer {TEST_BEARER_TOKEN}x",
        ):
            status, _, response_body = await post_json(
                client, "/encode", body, {"Authorization": authorization}
            )
            assert_error_response(status, response_body, 401, "unauthenticated")


async def test_trusted_proxy_header_authentication(
    key_file_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level(logging.INFO)
    config = default_test_config(
        key_file_path,
        auth_mode="trusted-proxy-header",
        bearer_token="",
        trusted_identity_header="X-Forwarded-Email",
    )
    body = {"context": TEST_CONTEXT, "values": []}
    async with serve_test_server(build_test_server(config)) as client:
        status, _, response_body = await post_json(client, "/encode", body, {"Authorization": ""})
        assert_error_response(status, response_body, 401, "unauthenticated")
        status, _, response_body = await post_json(
            client,
            "/encode",
            body,
            {"Authorization": "", "X-Forwarded-Email": "ada@example.com"},
        )
    assert status == 200, response_body
    access_lines = [r for r in caplog.records if r.getMessage() == "codec request"]
    assert getattr(access_lines[-1], "log_fields")["caller"] == "ada@example.com"


class _ForbiddingPayloadCodecSelector:
    async def select_payload_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        raise CallerForbiddenError()


async def test_codec_selector_forbidden_returns_403(key_file_path: Path) -> None:
    server = build_test_server(
        default_test_config(key_file_path), _ForbiddingPayloadCodecSelector()
    )
    async with serve_test_server(server) as client:
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": []}
        )
    assert_error_response(status, body, 403, "forbidden")


class _LeakingPayloadEncryptionKeyProvider:
    def current_key_id(self, context: PayloadContext) -> str:
        raise RuntimeError("current key lookup failed: " + SENTINEL_SECRET)

    def key(self, context: PayloadContext, key_id: str) -> bytes:
        raise RuntimeError("key lookup failed: " + SENTINEL_SECRET)


class _LeakingPayloadCodec:
    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        raise RuntimeError("codec failed: " + SENTINEL_SECRET)

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        raise RuntimeError("codec failed: " + SENTINEL_SECRET)


class _LeakingAsyncPayloadCodec:
    async def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        raise RuntimeError("async codec failed: " + SENTINEL_SECRET)

    async def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        raise RuntimeError("async codec failed: " + SENTINEL_SECRET)


class _CategorizedLeakingPayloadCodec:
    """Raises a typed decode error whose detail and key ID both hold the secret."""

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        raise PayloadEncryptionError(
            "encode", PayloadErrorCategory.UNKNOWN_KEY, SENTINEL_SECRET, "SENTINEL-key-id"
        )

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        raise PayloadEncryptionError(
            "decode",
            PayloadErrorCategory.AUTHENTICATION_FAILED,
            SENTINEL_SECRET,
            "SENTINEL-key-id",
        )


class _CodecFailedPayloadCodec:
    """Raises ``codec_failed``, which names no envelope, with the secret in its detail."""

    def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        raise PayloadEncryptionError("encode", PayloadErrorCategory.CODEC_FAILED, SENTINEL_SECRET)

    def decode(
        self, context: PayloadContext, envelopes: Sequence[pb.PayloadEnvelope]
    ) -> Sequence[bytes]:
        raise PayloadEncryptionError("decode", PayloadErrorCategory.CODEC_FAILED, SENTINEL_SECRET)


class _PlaintextLimitRecordingPayloadCodec:
    """Wraps the SDK codec and records the plaintext limit each decode receives."""

    def __init__(self, inner: AesGcmPayloadCodec) -> None:
        self.received_limits: list[int | None] = []
        self._inner = inner

    async def encode(
        self, context: PayloadContext, plaintexts: Sequence[bytes]
    ) -> Sequence[pb.PayloadEnvelope]:
        return self._inner.encode(context, plaintexts)

    async def decode(
        self,
        context: PayloadContext,
        envelopes: Sequence[pb.PayloadEnvelope],
        *,
        max_plaintext_bytes: int | None = None,
    ) -> Sequence[bytes]:
        self.received_limits.append(max_plaintext_bytes)
        return self._inner.decode(context, envelopes, max_plaintext_bytes=max_plaintext_bytes)


async def test_decode_passes_the_plaintext_limit_to_codecs_that_declare_it(
    key_file_path: Path,
) -> None:
    sdk_codec = sdk_codec_from_key_file(key_file_path)
    recording_codec = _PlaintextLimitRecordingPayloadCodec(sdk_codec)
    [envelope] = sdk_codec.encode(SDK_CONTEXT, [pb.Value(string_value="x" * 64).SerializeToString()])
    config = default_test_config(
        key_file_path,
        payload_size_limits=PayloadSizeLimits.from_envelope_and_plaintext_limits(16_842_752, 32),
    )
    server = build_test_server(config, _StaticPayloadCodecSelector(recording_codec))
    async with serve_test_server(server) as client:
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": [envelope_text(envelope)]}
        )
    assert_error_response(status, body, 422, "limit_exceeded", 0)
    assert recording_codec.received_limits == [32]


class _StaticPayloadCodecSelector:
    def __init__(self, codec: AnyPayloadCodec | None, error: Exception | None = None) -> None:
        self._codec = codec
        self._error = error

    async def select_payload_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        if self._error is not None:
            raise self._error
        assert self._codec is not None
        return self._codec


async def test_provider_codec_and_selector_error_text_never_reaches_responses_or_logs(
    key_file_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level(logging.DEBUG)
    sdk_codec = sdk_codec_from_key_file(key_file_path)
    [envelope] = sdk_codec.encode(SDK_CONTEXT, [pb.Value(string_value="x").SerializeToString()])
    decode_body = {"context": TEST_CONTEXT, "envelopes": [envelope_text(envelope)]}
    encode_body = {"context": TEST_CONTEXT, "values": [{"stringValue": "x"}]}
    leaking_aes_codec = create_aes_gcm_payload_codec(
        _LeakingPayloadEncryptionKeyProvider(), 1 << 20
    )
    cases: list[tuple[str, _StaticPayloadCodecSelector, str, dict[str, Any], int, str, int | None]] = [
        ("provider refuses key on decode", _StaticPayloadCodecSelector(leaking_aes_codec),
         "/decode", decode_body, 422, "unknown_key", 0),
        ("provider fails on encode", _StaticPayloadCodecSelector(leaking_aes_codec),
         "/encode", encode_body, 500, "internal", None),
        ("custom codec fails on decode", _StaticPayloadCodecSelector(_LeakingPayloadCodec()),
         "/decode", decode_body, 500, "internal", None),
        ("custom codec fails on encode", _StaticPayloadCodecSelector(_LeakingPayloadCodec()),
         "/encode", encode_body, 500, "internal", None),
        ("async codec fails on decode", _StaticPayloadCodecSelector(_LeakingAsyncPayloadCodec()),
         "/decode", decode_body, 500, "internal", None),
        ("async codec fails on encode", _StaticPayloadCodecSelector(_LeakingAsyncPayloadCodec()),
         "/encode", encode_body, 500, "internal", None),
        ("typed codec error with secret key id",
         _StaticPayloadCodecSelector(_CategorizedLeakingPayloadCodec()),
         "/decode", decode_body, 422, "authentication_failed", 0),
        ("typed codec error on encode",
         _StaticPayloadCodecSelector(_CategorizedLeakingPayloadCodec()),
         "/encode", encode_body, 500, "internal", None),
        ("codec_failed on decode", _StaticPayloadCodecSelector(_CodecFailedPayloadCodec()),
         "/decode", decode_body, 500, "internal", None),
        ("codec_failed on encode", _StaticPayloadCodecSelector(_CodecFailedPayloadCodec()),
         "/encode", encode_body, 500, "internal", None),
        ("selector fails",
         _StaticPayloadCodecSelector(None, RuntimeError("tenant lookup failed: " + SENTINEL_SECRET)),
         "/decode", decode_body, 500, "internal", None),
    ]
    for name, selector, path, body, want_status, want_category, want_index in cases:
        caplog.clear()
        server = build_test_server(default_test_config(key_file_path), selector)
        async with serve_test_server(server) as client:
            status, _, response_body = await post_json(client, path, body)
        assert_error_response(status, response_body, want_status, want_category, want_index)
        assert "SENTINEL" not in json.dumps(response_body), name
        assert "SENTINEL" not in caplog.text, (name, caplog.text)
        for record in caplog.records:
            assert "SENTINEL" not in json.dumps(getattr(record, "log_fields", {})), name
        if want_status == 422:
            assert "'k1'" in response_body["error"]["message"], name


async def test_decoded_bytes_budget_bounds_compressible_envelopes(key_file_path: Path) -> None:
    config = default_test_config(key_file_path, max_decoded_bytes_per_request=4 << 20)
    gzip_codec = sdk_codec_from_key_file(key_file_path, compression=PayloadCompression.GZIP)
    # Each 1 MiB string compresses to about 1 KiB, so five fit easily in the 1 MiB body limit.
    plaintext = pb.Value(string_value="a" * (1 << 20)).SerializeToString()
    texts = [envelope_text(e) for e in gzip_codec.encode(SDK_CONTEXT, [plaintext] * 5)]
    async with serve_test_server(build_test_server(config)) as client:
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": texts[:3]}
        )
        assert status == 200
        assert len(body["values"]) == 3
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": texts}
        )
        assert_error_response(status, body, 413, "limit_exceeded")
        # A failing entry before the budget runs out still reports its own index.
        with_bad_entry = [texts[0], "AAE", texts[1], texts[2], texts[3]]
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": with_bad_entry}
        )
        assert_error_response(status, body, 422, "malformed", 1)


def test_value_json_length_is_exact_and_matches_protobuf_json() -> None:
    every_byte_text = "".join(chr(code) for code in range(0x200)) + '"\\/ \U0001f30d'
    values = [
        pb.Value(string_value=""),
        pb.Value(string_value=every_byte_text),
        pb.Value(string_value="\x01" * 1000),
        pb.Value(string_value="Grüße, 世界 🌍"),
        # Longer than one measuring slice.
        pb.Value(string_value='é😀"\\x\x01' * 300_000),
        pb.Value(int_value=-(2**63)),
        pb.Value(int_value=2**63 - 1),
        pb.Value(double_value=-0.0),
        pb.Value(double_value=float("nan")),
        pb.Value(double_value=float("inf")),
        pb.Value(double_value=float("-inf")),
        pb.Value(double_value=5e-324),
        pb.Value(double_value=1.7976931348623157e308),
        pb.Value(double_value=0.1),
        pb.Value(bool_value=False),
        pb.Value(obj_value=pb.EncodedObject()),
        pb.Value(obj_value=pb.EncodedObject(encoding="raw\x00\x7f")),
        pb.Value(obj_value=pb.EncodedObject(payload=b"\x00\x01\x02\xfe\xff")),
        pb.Value(obj_value=pb.EncodedObject(encoding="json", payload=b'{"a":1}' * 101)),
    ]
    for value in values:
        text = serialize_value_json(value)
        assert measure_value_json_bytes(value) == len(text), value
        assert text.isascii()
        assert_same_value(parse_value_json(json.loads(text)), value)
        if value.WhichOneof("kind") != "double_value":
            assert json.loads(text) == json.loads(json_format.MessageToJson(value)), value


async def test_decoded_budget_is_checked_before_escaped_json_is_built(
    key_file_path: Path,
) -> None:
    gzip_codec = sdk_codec_from_key_file(key_file_path, compression=PayloadCompression.GZIP)
    # Each control character becomes six JSON bytes (\u0001), so 8 MiB would need 48 MiB of JSON.
    control_text = pb.Value(string_value="\x01" * (8 << 20))
    exact_json_bytes = measure_value_json_bytes(control_text)
    [control_envelope] = gzip_codec.encode(SDK_CONTEXT, [control_text.SerializeToString()])
    small = pb.Value(string_value="\x01" * 1000)
    small_json_bytes = measure_value_json_bytes(small)
    small_texts = [envelope_text(e) for e in gzip_codec.encode(SDK_CONTEXT, [small.SerializeToString()] * 3)]

    over_budget = default_test_config(key_file_path, max_decoded_bytes_per_request=1 << 20)
    async with serve_test_server(build_test_server(over_budget)) as client:
        tracemalloc.start()
        try:
            status, _, body = await post_json(
                client,
                "/decode",
                {"context": TEST_CONTEXT, "envelopes": [envelope_text(control_envelope)]},
            )
            _, peak_bytes = tracemalloc.get_traced_memory()
        finally:
            tracemalloc.stop()
        assert_error_response(status, body, 413, "limit_exceeded")
        assert peak_bytes < exact_json_bytes, f"peak {peak_bytes} reached the full JSON size"

    for budget, want_status in ((exact_json_bytes, 200), (exact_json_bytes - 1, 413)):
        config = default_test_config(key_file_path, max_decoded_bytes_per_request=budget)
        async with serve_test_server(build_test_server(config)) as client:
            status, _, body = await post_json(
                client,
                "/decode",
                {"context": TEST_CONTEXT, "envelopes": [envelope_text(control_envelope)]},
            )
        assert status == want_status, budget
        if want_status == 200:
            assert body["values"][0] == {"stringValue": control_text.string_value}

    for budget, want_status in ((3 * small_json_bytes, 200), (3 * small_json_bytes - 1, 413)):
        config = default_test_config(key_file_path, max_decoded_bytes_per_request=budget)
        async with serve_test_server(build_test_server(config)) as client:
            status, _, body = await post_json(
                client, "/decode", {"context": TEST_CONTEXT, "envelopes": small_texts}
            )
        assert status == want_status, budget


async def test_data_size_check_follows_the_spec_check_order(key_file_path: Path) -> None:
    limits = PayloadSizeLimits.from_envelope_and_plaintext_limits(16_842_752, 67_108_864)
    config = default_test_config(
        key_file_path,
        payload_size_limits=PayloadSizeLimits(
            max_envelope_bytes=limits.max_envelope_bytes,
            max_encoded_text_bytes=limits.max_encoded_text_bytes,
            max_data_bytes=64,
            max_plaintext_bytes=limits.max_plaintext_bytes,
        ),
    )
    oversized_data = os.urandom(65)

    def oversized_envelope(**fields: Any) -> str:
        envelope_fields: dict[str, Any] = {
            "cipher": pb.PAYLOAD_CIPHER_AES256_GCM,
            "key_id": "k1",
            "compression": pb.PAYLOAD_COMPRESSION_NONE,
            "data": oversized_data,
        }
        envelope_fields.update(fields)
        return envelope_text(pb.PayloadEnvelope(**envelope_fields))

    cases = [
        ("unknown cipher", oversized_envelope(cipher=7), "unsupported_cipher"),
        ("custom cipher", oversized_envelope(cipher=pb.PAYLOAD_CIPHER_CUSTOM), "unsupported_cipher"),
        ("metadata", oversized_envelope(metadata={"a": b"b"}), "malformed"),
        ("unknown compression", oversized_envelope(compression=9), "unsupported_compression"),
        ("unknown key", oversized_envelope(key_id="k9"), "unknown_key"),
        ("only oversized", oversized_envelope(), "limit_exceeded"),
    ]
    async with serve_test_server(build_test_server(config)) as client:
        for name, text, want_category in cases:
            status, _, body = await post_json(
                client, "/decode", {"context": TEST_CONTEXT, "envelopes": [text]}
            )
            try:
                assert_error_response(status, body, 422, want_category, 0)
            except AssertionError as error:
                raise AssertionError(f"{name}: {error}") from None


async def test_request_limits_return_413(key_file_path: Path) -> None:
    config = default_test_config(
        key_file_path, max_request_body_bytes=1024, max_items_per_request=3
    )
    async with serve_test_server(build_test_server(config)) as client:
        oversized = {"context": TEST_CONTEXT, "values": [{"stringValue": "x" * 2048}]}
        status, _, body = await post_json(client, "/encode", oversized)
        assert_error_response(status, body, 413, "limit_exceeded")

        too_many = {"context": TEST_CONTEXT, "values": [{"stringValue": "x"}] * 4}
        status, _, body = await post_json(client, "/encode", too_many)
        assert_error_response(status, body, 413, "limit_exceeded")

        async def chunked_body() -> AsyncIterator[bytes]:
            yield b'{"context": {"flow_id": "order-1001"}, "values": ['
            for _ in range(64):
                yield b'{"stringValue": "' + b"x" * 64 + b'"},'
            yield b'{"stringValue": "x"}]}'

        response = await client.post(
            "/encode",
            data=chunked_body(),
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {TEST_BEARER_TOKEN}",
            },
        )
        assert response.request_info.headers.get("Transfer-Encoding") == "chunked"
        assert_error_response(response.status, await response.json(), 413, "limit_exceeded")


async def test_body_is_not_read_before_authentication(key_file_path: Path) -> None:
    config = default_test_config(key_file_path, max_request_body_bytes=1024)
    async with serve_test_server(build_test_server(config)) as client:
        status, _, body = await post_json(
            client, "/encode", b"x" * 4096, {"Authorization": ""}
        )
    assert_error_response(status, body, 401, "unauthenticated")


async def test_protocol_error_bodies(key_file_path: Path) -> None:
    cases: list[tuple[str, str, Any, dict[str, str], int, str, int | None]] = [
        ("not JSON", "/decode", b"{", {}, 400, "malformed", None),
        ("missing context", "/decode", {"envelopes": []}, {}, 400, "malformed", None),
        ("empty flow id", "/decode", {"context": {"flow_type": "OrderFlow", "flow_id": ""},
                                      "envelopes": []}, {}, 400, "malformed", None),
        ("non-string flow type", "/decode", {"context": {"flow_type": 7, "flow_id": "f"},
                                             "envelopes": []}, {}, 400, "malformed", None),
        ("missing envelopes", "/decode", {"context": TEST_CONTEXT}, {}, 400, "malformed", None),
        ("non-string envelope", "/decode", {"context": TEST_CONTEXT, "envelopes": [7]}, {},
         400, "malformed", None),
        ("wrong content type", "/decode", {"context": TEST_CONTEXT, "envelopes": []},
         {"Content-Type": "text/plain"}, 400, "malformed", None),
        ("compressed body", "/decode",
         gzip.compress(json.dumps({"context": TEST_CONTEXT, "envelopes": []}).encode()),
         {"Content-Encoding": "gzip"}, 400, "malformed", None),
        ("value not Value JSON", "/encode",
         {"context": TEST_CONTEXT, "values": [{"stringValue": "a"}, {"noSuchField": 1}]}, {},
         422, "malformed", 1),
        ("value not an object", "/encode", {"context": TEST_CONTEXT, "values": ["text"]}, {},
         422, "malformed", 0),
        ("null value", "/encode", {"context": TEST_CONTEXT, "values": [{"nullValue": None}]},
         {}, 422, "invalid_plaintext", 0),
        ("reserved encoding", "/encode",
         {"context": TEST_CONTEXT,
          "values": [{"objValue": {"encoding": "dex/1", "payload": "AA=="}}]},
         {}, 422, "invalid_plaintext", 0),
    ]
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        for name, path, body, headers, want_status, want_category, want_index in cases:
            status, _, response_body = await post_json(client, path, body, headers)
            try:
                assert_error_response(
                    status, response_body, want_status, want_category, want_index
                )
            except AssertionError as error:
                raise AssertionError(f"{name}: {error}") from None
        response = await client.get("/decode")
        assert_error_response(response.status, await response.json(), 400, "malformed")


async def test_health_check_needs_no_credentials(key_file_path: Path) -> None:
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        response = await client.get("/healthz")
        assert response.status == 200
        assert await response.json() == {"status": "ok"}


async def test_logs_contain_no_payloads_keys_or_tokens(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level(logging.DEBUG)
    key_file_path, encoded_key = write_random_key_file(tmp_path)
    secret_text = "SENTINEL-plaintext-card-4111"
    async with serve_test_server(build_test_server(default_test_config(key_file_path))) as client:
        status, _, encoded = await post_json(
            client, "/encode", {"context": TEST_CONTEXT, "values": [{"stringValue": secret_text}]}
        )
        assert status == 200
        status, _, decoded = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": encoded["envelopes"]}
        )
        assert decoded == {"values": [{"stringValue": secret_text}]}
        await post_json(
            client,
            "/decode",
            {"context": TEST_CONTEXT, "envelopes": encoded["envelopes"]},
            {"Authorization": "Bearer SENTINEL-wrong-token-0123456789"},
        )
    logged = caplog.text + "".join(
        json.dumps(getattr(record, "log_fields", {})) for record in caplog.records
    )
    assert "codec request" in logged
    for secret in (secret_text, encoded_key, TEST_BEARER_TOKEN, "SENTINEL-wrong-token",
                   encoded["envelopes"][0]):
        assert secret not in logged
