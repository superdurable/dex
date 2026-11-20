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

"""Tests configuration validation, in-memory keys, and graceful shutdown."""

from __future__ import annotations

import asyncio
import base64
import json
import logging
import os
from pathlib import Path

import aiohttp
import pytest
from dex.dexpb import dex_pb2 as pb

from dex_codec_server.auth import AnyPayloadCodec, CallerIdentity
from dex_codec_server.config import CodecServerConfigurationError, load_codec_server_config
from dex_codec_server.keys import KeyFileJsonPayloadEncryptionKeyProvider
from dex_codec_server.server_process import (
    run_codec_server,
    start_codec_server,
    stop_codec_server,
)
from tests.codec_server_fixtures import (
    SDK_CONTEXT,
    TEST_ALLOWED_ORIGIN,
    TEST_BEARER_TOKEN,
    TEST_CONTEXT,
    build_test_server,
    default_test_config,
    envelope_text,
    post_json,
    sdk_codec_from_key_file,
    serve_test_server,
    write_random_key_file,
)


def _base_environment(key_file_path: Path) -> dict[str, str]:
    return {
        "DEX_CODEC_SERVER_KEY_FILE": str(key_file_path),
        "DEX_CODEC_SERVER_ALLOWED_ORIGINS": TEST_ALLOWED_ORIGIN,
        "DEX_CODEC_SERVER_AUTH_MODE": "bearer-token",
        "DEX_CODEC_SERVER_BEARER_TOKEN": TEST_BEARER_TOKEN,
    }


def test_load_config_reads_flags_and_environment(tmp_path: Path) -> None:
    key_file_path, _ = write_random_key_file(tmp_path)
    token_file = tmp_path / "token"
    token_file.write_text(TEST_BEARER_TOKEN + "\n")
    environment = _base_environment(key_file_path)
    environment.pop("DEX_CODEC_SERVER_BEARER_TOKEN")
    config = load_codec_server_config(
        [
            "--listen-address", "127.0.0.1:9090",
            "--bearer-token-file", str(token_file),
            "--allowed-origins", "https://A.example.com, http://localhost:8802",
            "--allow-credentials",
            "--max-items-per-request", "10",
            "--shutdown-timeout", "500ms",
        ],
        environment,
    )
    assert (config.listen_host, config.listen_port) == ("127.0.0.1", 9090)
    assert config.bearer_token == TEST_BEARER_TOKEN
    assert config.allowed_origins == ("https://a.example.com", "http://localhost:8802")
    assert config.allow_credentials is True
    assert config.max_items_per_request == 10
    assert config.shutdown_timeout_seconds == 0.5
    assert config.payload_size_limits.max_encoded_text_bytes == 22_457_004
    assert TEST_BEARER_TOKEN not in repr(config)


@pytest.mark.parametrize(
    ("overrides", "arguments", "message"),
    [
        ({"DEX_CODEC_SERVER_AUTH_MODE": ""}, [], "never runs without authentication"),
        ({"DEX_CODEC_SERVER_AUTH_MODE": "none"}, [], "never runs without authentication"),
        ({"DEX_CODEC_SERVER_BEARER_TOKEN": "short"}, [], "at least 32 bytes"),
        ({"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "*"}, [], "wildcards are not allowed"),
        ({"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "https://*.example.com"}, [], "wildcards"),
        ({"DEX_CODEC_SERVER_ALLOWED_ORIGINS": ""}, [], "allowed-origins"),
        ({"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "https://a.example.com/path"}, [], "must look like"),
        ({"DEX_CODEC_SERVER_ALLOWED_ORIGINS": "ftp://a.example.com"}, [], "must look like"),
        ({"DEX_CODEC_SERVER_KEYS_JSON": "{}"}, [], "exactly one key source"),
        ({"DEX_CODEC_SERVER_KEY_FILE": ""}, [], "exactly one key source"),
        ({}, ["--max-request-body-bytes", "0"], "must be positive"),
        ({}, ["--max-items-per-request", "-1"], "must be positive"),
        ({}, ["--max-decoded-bytes-per-request", "0"], "must be positive"),
        ({}, ["--max-envelope-bytes", "0"], "must be positive"),
        ({}, ["--max-plaintext-bytes", "abc"], "not an integer"),
        ({}, ["--shutdown-timeout", "0s"], "must be positive"),
        ({}, ["--shutdown-timeout", "soon"], "must look like"),
        ({}, ["--listen-address", "8080"], "--listen-address"),
        ({}, ["--no-such-flag"], "unrecognized"),
        ({"DEX_CODEC_SERVER_AUTH_MODE": "trusted-proxy-header"}, [], "trusted-identity-header"),
    ],
)
def test_load_config_rejects_unsafe_settings(
    tmp_path: Path, overrides: dict[str, str], arguments: list[str], message: str
) -> None:
    key_file_path, _ = write_random_key_file(tmp_path)
    environment = {**_base_environment(key_file_path), **overrides}
    with pytest.raises(CodecServerConfigurationError, match=message):
        load_codec_server_config(arguments, environment)


async def test_keys_json_from_environment(tmp_path: Path) -> None:
    key_file_path, encoded_key = write_random_key_file(tmp_path)
    environment = _base_environment(key_file_path)
    environment.pop("DEX_CODEC_SERVER_KEY_FILE")
    environment["DEX_CODEC_SERVER_KEYS_JSON"] = key_file_path.read_text()
    config = load_codec_server_config([], environment)
    [envelope] = sdk_codec_from_key_file(key_file_path).encode(
        SDK_CONTEXT, [pb.Value(string_value="from the environment").SerializeToString()]
    )
    async with serve_test_server(build_test_server(config)) as client:
        status, _, body = await post_json(
            client, "/decode", {"context": TEST_CONTEXT, "envelopes": [envelope_text(envelope)]}
        )
    assert status == 200, body
    assert body == {"values": [{"stringValue": "from the environment"}]}
    assert encoded_key not in repr(config)


@pytest.mark.parametrize(
    ("document", "message"),
    [
        ("not json", "not valid key file JSON"),
        ('{"current_key_id": "k1", "keys": {}}', "non-empty keys"),
        ('{"current_key_id": "k9", "keys": {"k1": "%s"}}', "current_key_id"),
        ('{"current_key_id": "k 1", "keys": {"k 1": "%s"}}', "invalid key id"),
        ('{"current_key_id": "k1", "keys": {"k1": "%s"}}', "32 bytes"),
    ],
)
def test_keys_json_rejects_invalid_documents_without_key_bytes(
    document: str, message: str
) -> None:
    key = base64.b64encode(os.urandom(32)).decode("ascii")
    short_key = base64.b64encode(os.urandom(16)).decode("ascii")
    filled = document % (short_key if message == "32 bytes" else key) if "%s" in document else document
    with pytest.raises(CodecServerConfigurationError, match=message) as raised:
        KeyFileJsonPayloadEncryptionKeyProvider(filled)
    assert key not in str(raised.value) and short_key not in str(raised.value)


def test_startup_errors_exit_with_status_one(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level(logging.INFO)
    environment = _base_environment(tmp_path / "missing-keys.json")
    assert run_codec_server([], environment) == 1
    environment["DEX_CODEC_SERVER_AUTH_MODE"] = ""
    assert run_codec_server([], environment) == 1
    assert TEST_BEARER_TOKEN not in caplog.text


class _GatedPayloadCodecSelector:
    """Holds each request until the test opens the gate, to keep it in flight."""

    def __init__(self, codec: AnyPayloadCodec) -> None:
        self.request_arrived = asyncio.Event()
        self.gate = asyncio.Event()
        self._codec = codec

    async def select_payload_codec(self, caller: CallerIdentity) -> AnyPayloadCodec:
        self.request_arrived.set()
        await self.gate.wait()
        return self._codec


async def test_graceful_shutdown_finishes_in_flight_requests(tmp_path: Path) -> None:
    key_file_path, _ = write_random_key_file(tmp_path)
    config = default_test_config(
        key_file_path, listen_host="127.0.0.1", listen_port=0, shutdown_timeout_seconds=10.0
    )
    logger = logging.getLogger("dex_codec_server")
    selector = _GatedPayloadCodecSelector(
        sdk_codec_from_key_file(key_file_path)
    )
    runner = await start_codec_server(build_test_server(config, selector), config, logger)
    host, port = runner.addresses[0][:2]
    base_url = f"http://{host}:{port}"
    async with aiohttp.ClientSession() as session:
        async with session.get(f"{base_url}/healthz") as health:
            assert health.status == 200
        in_flight = asyncio.create_task(
            session.post(
                f"{base_url}/encode",
                data=json.dumps({"context": TEST_CONTEXT, "values": [{"stringValue": "x"}]}),
                headers={
                    "Content-Type": "application/json",
                    "Authorization": f"Bearer {TEST_BEARER_TOKEN}",
                },
            )
        )
        await asyncio.wait_for(selector.request_arrived.wait(), timeout=10)
        shutdown = asyncio.create_task(stop_codec_server(runner, config, logger))
        await asyncio.sleep(0.2)
        assert not shutdown.done()
        selector.gate.set()
        response = await asyncio.wait_for(in_flight, timeout=10)
        assert response.status == 200
        assert len((await response.json())["envelopes"]) == 1
        await asyncio.wait_for(shutdown, timeout=10)
        with pytest.raises(aiohttp.ClientConnectionError):
            await session.get(f"{base_url}/healthz")
