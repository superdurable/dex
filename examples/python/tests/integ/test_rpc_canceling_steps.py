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

from typing import Callable

import pytest
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.primitives.rpc_canceling_steps.report_generation_flow import (
    REPORT_PROGRESS,
    REPORT_STOPPED,
)
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient, FlowStatus

pytestmark = pytest.mark.integ


async def test_stop_report_cancels_running_generation(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("rpc-canceling-steps")
    await client.start_flow(app.report_generation, flow_id, "q3", start_options())
    first_section = await client.read_stream(flow_id, REPORT_PROGRESS, "", WAIT_TIMEOUT)
    assert first_section.value == 1

    assert await client.invoke_rpc(app.report_generation.stop_report, flow_id, None)

    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED
    assert result.single_output(str) == REPORT_STOPPED
