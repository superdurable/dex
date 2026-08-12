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
from tests.integ.conftest import WAIT_TIMEOUT

from dex import AsyncClient

pytestmark = pytest.mark.integ

POLLING_COMPLETION_THRESHOLD = 2


async def test_polling_completes_after_all_three_tasks(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("polling")
    await client.start_flow(
        app.polling,
        flow_id,
        POLLING_COMPLETION_THRESHOLD,
        start_options(),
    )

    # Task C completes on its own once Poll reaches the threshold.
    await client.publish(flow_id, app.polling.task_a_completed, None)
    await client.publish(flow_id, app.polling.task_b_completed, None)

    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "all tasks completed"
