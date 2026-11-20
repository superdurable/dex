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

"""The Python Client decrypts what each SDK's Worker encrypts, and each Worker
decrypts the Python Client's input."""

from __future__ import annotations

import os
from collections.abc import AsyncIterator
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from uuid import uuid4

import pytest
import pytest_asyncio
from dex import (
    AsyncClient,
    BlobCacheConfig,
    ClientOptions,
    FlowStatus,
    StartFlowOptions,
    ValueMappingError,
    open_blob_cache,
)

from tests.interop.payload_encryption_interop_flow import (
    COUNT_INTEGER,
    DOCUMENT_OBJECT,
    EXPECTED_COUNT,
    EXPECTED_DOCUMENT,
    EXPECTED_RATIO,
    FAIL_INPUT_PREFIX,
    OWNERS,
    RATIO_DOUBLE,
    SECRET_TEXT,
    STATUS_TEXT,
    PayloadInteropFlow,
    expected_failure_text,
    expected_output,
    expected_secret_text,
    expected_status_text,
    new_client_flows,
    new_client_registry,
    new_file_payload_encryption,
    new_payload_interop_input,
)

WAIT_TIMEOUT = timedelta(seconds=45)


@dataclass(frozen=True)
class InteropClients:
    flows: dict[str, PayloadInteropFlow]
    client: AsyncClient
    client_without_codec: AsyncClient


@pytest_asyncio.fixture
async def interop_clients(tmp_path: Path) -> AsyncIterator[InteropClients]:
    server_address = os.environ["DEX_FLOW_SERVICE_ADDRESS"]
    encryption = new_file_payload_encryption(os.environ["DEX_PAYLOAD_INTEROP_KEY_FILE"])
    cache = open_blob_cache(BlobCacheConfig(str(tmp_path / "cache"), 64 << 20))
    flows = new_client_flows()
    client = AsyncClient(
        new_client_registry(flows),
        cache,
        ClientOptions(server_address=server_address, payload_encryption=encryption),
    )
    client_without_codec = AsyncClient(
        new_client_registry(flows), cache, ClientOptions(server_address=server_address)
    )
    try:
        yield InteropClients(flows, client, client_without_codec)
    finally:
        await client_without_codec.close()
        await client.close()
        cache.close()


def new_flow_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex}"


@pytest.mark.parametrize("owner", OWNERS)
async def test_python_client_reads_worker_payload(
    interop_clients: InteropClients, owner: str
) -> None:
    flow = interop_clients.flows[owner]
    client = interop_clients.client
    input = f"Python Client input for {owner}: naïve café ✓"
    flow_id = new_flow_id(f"payload-interop-{owner}")
    await client.start_flow(flow, flow_id, new_payload_interop_input(input), StartFlowOptions())
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.COMPLETED, result.error_message
    assert result.single_output(str) == expected_output(owner, input)
    stored = await client.get_attributes(
        flow_id, SECRET_TEXT, DOCUMENT_OBJECT, COUNT_INTEGER, RATIO_DOUBLE
    )
    assert stored[SECRET_TEXT] == expected_secret_text(owner, input)
    assert stored[DOCUMENT_OBJECT] == EXPECTED_DOCUMENT
    assert stored[COUNT_INTEGER] == EXPECTED_COUNT
    assert type(stored[COUNT_INTEGER]) is int
    assert stored[RATIO_DOUBLE] == EXPECTED_RATIO
    assert type(stored[RATIO_DOUBLE]) is float

    status = await interop_clients.client_without_codec.get_attributes(flow_id, STATUS_TEXT)
    assert status[STATUS_TEXT] == expected_status_text(owner)
    with pytest.raises(ValueMappingError, match="configure a PayloadCodec"):
        await interop_clients.client_without_codec.get_attributes(flow_id, SECRET_TEXT)


@pytest.mark.parametrize("owner", OWNERS)
async def test_python_client_reads_worker_error_text(
    interop_clients: InteropClients, owner: str
) -> None:
    flow = interop_clients.flows[owner]
    client = interop_clients.client
    failure_input = "Python Client failure input ✓"
    flow_id = new_flow_id(f"payload-interop-failure-{owner}")
    await client.start_flow(
        flow, flow_id, new_payload_interop_input(FAIL_INPUT_PREFIX + failure_input), StartFlowOptions()
    )
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status == FlowStatus.FAILED
    assert result.error_message is not None
    assert expected_failure_text(owner, failure_input) in result.error_message
