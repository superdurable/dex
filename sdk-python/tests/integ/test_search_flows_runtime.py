# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import time
from datetime import timedelta
from uuid import uuid4

import pytest

from dex import Client, FlowStatus
from dex.flow_info import SearchFlowEntry

from .environment import DexDevTestEnvironment
from .search_flows_flow import KEYWORD_KEY, SearchFlowsFlow
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_search_flows_finds_indexed_flow() -> None:
    flow = SearchFlowsFlow()
    with DexDevTestEnvironment(flow) as environment:
        keyword_value = f"sf-{uuid4().hex}"
        flow_id = unique_id("search-flows")
        environment.client.start_flow(flow, flow_id, keyword_value)
        assert (
            environment.client.wait_for_flow(flow_id, str, WAIT_TIMEOUT)
            == keyword_value
        )
        query = f"{KEYWORD_KEY} = '{keyword_value}'"
        entry = _poll_for_flow(environment.client, query, flow_id)
        assert entry.flow_id == flow_id
        assert entry.run_id
        assert entry.status == FlowStatus.COMPLETED
        assert entry.started_at is not None
        assert entry.attributes[KEYWORD_KEY].data == keyword_value


def test_search_flows_rejects_negative_page_size() -> None:
    flow = SearchFlowsFlow()
    with DexDevTestEnvironment(flow) as environment:
        with pytest.raises(ValueError):
            environment.client.search_flows("CustomKeywordField = 'x'", -1)


def _poll_for_flow(client: Client, query: str, flow_id: str) -> SearchFlowEntry:
    deadline = time.monotonic() + 30
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            page = client.search_flows(query, 100)
            for entry in page.flows:
                if entry.flow_id == flow_id:
                    return entry
        except Exception as error:  # noqa: BLE001
            last_error = error
        time.sleep(0.2)
    raise AssertionError(f"flow {flow_id} not found via search_flows") from last_error
