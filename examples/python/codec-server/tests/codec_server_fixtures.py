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

"""Builds codec servers and requests for the tests."""

from __future__ import annotations

import base64
import json
import logging
import math
import os
import struct
from collections.abc import AsyncIterator, Mapping
from contextlib import asynccontextmanager
from dataclasses import replace
from pathlib import Path
from typing import Any

from aiohttp.test_utils import TestClient, TestServer
from dex import AesGcmPayloadCodec, FilePayloadEncryptionKeyProvider, PayloadContext
from dex.dexpb import dex_pb2 as pb
from google.protobuf import json_format

from dex_codec_server.auth import (
    CallerPayloadCodecSelector,
    SingleTenantPayloadCodecSelector,
    create_request_authenticator,
)
from dex_codec_server.codec_server import PayloadCodecHttpServer
from dex_codec_server.config import CodecServerConfig, load_codec_server_config
from dex_codec_server.cors import CorsOriginAllowList
from dex_codec_server.keys import (
    create_aes_gcm_payload_codec,
    create_payload_encryption_key_provider,
)

TEST_ALLOWED_ORIGIN = "https://dex-web.example.com"
TEST_BEARER_TOKEN = "test-bearer-token-0123456789abcdef0123456789"
TEST_CONTEXT = {"flow_type": "OrderFlow", "flow_id": "order-1001"}
SDK_CONTEXT = PayloadContext(flow_type="OrderFlow", flow_id="order-1001")
VECTORS_PATH = (
    Path(__file__).resolve().parents[4]
    / "protos"
    / "conformance"
    / "payload-encryption"
    / "vectors.json"
)
TEST_LOGGER = logging.getLogger("dex_codec_server.test")


def write_random_key_file(directory: Path) -> tuple[Path, str]:
    """Write a key file with one random key and return its path and the encoded key."""
    encoded_key = base64.b64encode(os.urandom(32)).decode("ascii")
    path = directory / "payload-keys.json"
    path.write_text(json.dumps({"current_key_id": "k1", "keys": {"k1": encoded_key}}))
    path.chmod(0o600)
    return path, encoded_key


def default_test_config(key_file_path: Path, **overrides: Any) -> CodecServerConfig:
    """Return a bearer-token config for ``key_file_path`` with ``overrides`` applied."""
    environment = {
        "DEX_CODEC_SERVER_KEY_FILE": str(key_file_path),
        "DEX_CODEC_SERVER_ALLOWED_ORIGINS": TEST_ALLOWED_ORIGIN,
        "DEX_CODEC_SERVER_AUTH_MODE": "bearer-token",
        "DEX_CODEC_SERVER_BEARER_TOKEN": TEST_BEARER_TOKEN,
        "DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES": str(1 << 20),
    }
    return replace(load_codec_server_config([], environment), **overrides)


def build_test_server(
    config: CodecServerConfig, codec_selector: CallerPayloadCodecSelector | None = None
) -> PayloadCodecHttpServer:
    """Build the server as production does, optionally with another codec selector."""
    if codec_selector is None:
        codec = create_aes_gcm_payload_codec(
            create_payload_encryption_key_provider(config),
            config.payload_size_limits.max_plaintext_bytes,
        )
        codec_selector = SingleTenantPayloadCodecSelector(codec)
    return PayloadCodecHttpServer(
        authenticator=create_request_authenticator(config),
        codec_selector=codec_selector,
        cors_origins=CorsOriginAllowList(config.allowed_origins, config.allow_credentials),
        payload_size_limits=config.payload_size_limits,
        max_request_body_bytes=config.max_request_body_bytes,
        max_items_per_request=config.max_items_per_request,
        max_decoded_bytes_per_request=config.max_decoded_bytes_per_request,
        logger=logging.getLogger("dex_codec_server"),
    )


@asynccontextmanager
async def serve_test_server(server: PayloadCodecHttpServer) -> AsyncIterator[TestClient[Any, Any]]:
    """Serve ``server`` on a local port and yield an HTTP client for it."""
    client: TestClient[Any, Any] = TestClient(TestServer(server.create_application()))
    await client.start_server()
    try:
        yield client
    finally:
        await client.close()


async def post_json(
    client: TestClient[Any, Any],
    path: str,
    body: Any,
    headers: Mapping[str, str] | None = None,
) -> tuple[int, Mapping[str, str], Any]:
    """POST ``body`` as JSON with the test bearer token unless ``headers`` override it."""
    request_headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {TEST_BEARER_TOKEN}",
    }
    request_headers.update(headers or {})
    sent_headers = {name: value for name, value in request_headers.items() if value}
    data = body if isinstance(body, (bytes, str)) else json.dumps(body)
    response = await client.post(path, data=data, headers=sent_headers)
    content = await response.read()
    try:
        parsed = json.loads(content)
    except ValueError:
        parsed = content.decode(errors="replace")
    return response.status, response.headers, parsed


def assert_error_response(
    status: int,
    body: Any,
    want_status: int,
    want_category: str,
    want_index: int | None = None,
) -> None:
    """Check the spec error body ``{"error": {"category", "message", "index"}}``."""
    assert status == want_status, body
    assert isinstance(body, dict) and set(body) == {"error"}, body
    error = body["error"]
    assert error["category"] == want_category, body
    assert isinstance(error["message"], str) and error["message"], body
    if want_index is None:
        assert "index" not in error, body
    else:
        assert error.get("index") == want_index, body


def sdk_codec_from_key_file(key_file_path: Path, **options: Any) -> AesGcmPayloadCodec:
    """Return the Python SDK codec that a Worker with this key file uses."""
    return AesGcmPayloadCodec(FilePayloadEncryptionKeyProvider(key_file_path), **options)


def envelope_text(envelope: pb.PayloadEnvelope) -> str:
    """Return the base64 text that a caller sends to ``/decode``."""
    return base64.b64encode(envelope.SerializeToString()).decode("ascii")


def sample_values() -> list[pb.Value]:
    """Return one Value of each encryptable kind."""
    return [
        pb.Value(string_value="hello, dex"),
        pb.Value(int_value=-7),
        pb.Value(double_value=-0.0),
        pb.Value(bool_value=True),
        pb.Value(obj_value=pb.EncodedObject(encoding="json", payload=b'{"order":"A-1001"}')),
    ]


def parse_value_json(value_json: Any) -> pb.Value:
    """Parse one protocol Value JSON entry."""
    value = pb.Value()
    json_format.ParseDict(value_json, value)
    return value


def assert_same_value(got: pb.Value, want: pb.Value) -> None:
    """Compare Values exactly; doubles compare by bits, and any NaN equals any NaN."""
    if want.WhichOneof("kind") == "double_value" and got.WhichOneof("kind") == "double_value":
        if math.isnan(want.double_value):
            assert math.isnan(got.double_value)
            return
        assert struct.pack("<d", got.double_value) == struct.pack("<d", want.double_value)
        return
    assert got.SerializeToString() == want.SerializeToString(), (got, want)
