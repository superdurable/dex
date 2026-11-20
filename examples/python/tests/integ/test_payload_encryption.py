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

from __future__ import annotations

import base64
import json
import os
import secrets
from collections.abc import Callable
from pathlib import Path

import pytest
from dex import (
    AsyncClient,
    BlobCacheConfig,
    ClientOptions,
    Registry,
    ValueMappingError,
    open_blob_cache,
)

from dex_examples.config import start_options
from dex_examples.production.payload_encryption import (
    TICKET_MESSAGE,
    TICKET_STATUS,
    SupportTicketFlow,
    start_encrypted_support_ticket,
    stop_encrypted_support_ticket,
)
from tests.integ.conftest import WAIT_TIMEOUT, server_address

pytestmark = pytest.mark.integ


def write_payload_key_file(directory: Path) -> str:
    """Writes a key file in the dexcli codec-server keygen format."""
    path = directory / "payload-keys.json"
    key = base64.b64encode(secrets.token_bytes(32)).decode("ascii")
    path.write_text(json.dumps({"current_key_id": "k1", "keys": {"k1": key}}))
    os.chmod(path, 0o600)
    return str(path)


async def test_payload_encryption_stores_ticket_message_encrypted(
    new_flow_id: Callable[[str], str],
    tmp_path: Path,
) -> None:
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path / "encryption-cache"), 64 << 20))
    key_file_path = write_payload_key_file(tmp_path)
    flow, worker, client, worker_task = await start_encrypted_support_ticket(
        cache, server_address(), key_file_path
    )
    client_without_codec = AsyncClient(
        Registry((SupportTicketFlow(),), allow_async_handlers=True),
        cache,
        ClientOptions(server_address=server_address()),
    )
    try:
        flow_id = new_flow_id("support-ticket")
        await client.start_flow(flow, flow_id, "My card was charged twice.", start_options())
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(str) == "received"
        message = await client.invoke_rpc(flow.get_ticket_message, flow_id)
        assert message == "My card was charged twice."

        status = await client_without_codec.get_attributes(flow_id, TICKET_STATUS)
        assert status[TICKET_STATUS] == "received"
        with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
            await client_without_codec.get_attributes(flow_id, TICKET_MESSAGE)
    finally:
        await client_without_codec.close()
        await stop_encrypted_support_ticket(worker, client, worker_task)
        cache.close()
