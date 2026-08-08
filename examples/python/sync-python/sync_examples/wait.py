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

import time
from collections.abc import Callable
from datetime import timedelta
from typing import Any

from dex import Attribute, Client, DexException, ErrorSubStatus, FlowStatus

WAIT_TIMEOUT = timedelta(seconds=45)
POLL_INTERVAL_SECONDS = 0.5


def wait_until(
    description: str,
    predicate: Callable[[], Any],
    timeout: timedelta = WAIT_TIMEOUT,
) -> Any:
    deadline = time.monotonic() + timeout.total_seconds()
    while True:
        value = predicate()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise AssertionError(f"timed out waiting for {description}")
        time.sleep(POLL_INTERVAL_SECONDS)


def wait_for_attribute(
    client: Client,
    flow_id: str,
    attribute: Attribute[Any],
    timeout: timedelta = WAIT_TIMEOUT,
) -> Any:
    return wait_until(
        f"attribute {attribute.name} on {flow_id}",
        lambda: attribute_or_none(client, flow_id, attribute),
        timeout,
    )


def attribute_or_none(
    client: Client, flow_id: str, attribute: Attribute[Any]
) -> Any:
    try:
        return client.get_attribute(flow_id, attribute)
    except DexException as error:
        if error.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
            return None
        raise


def flow_status_or_none(client: Client, flow_id: str) -> FlowStatus | None:
    try:
        return client.describe_flow(flow_id).status
    except DexException as error:
        if error.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
            return None
        raise
