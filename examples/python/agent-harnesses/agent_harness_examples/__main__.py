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

"""Runs every order support agent behind one Quart app and one AsyncWorker."""

from __future__ import annotations

import asyncio

from agent_harness_examples.app import AgentHarnessExamplesApp
from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.http_app import create_app
from agent_harness_examples.slack_connector_tools import open_slack_project_connection


async def main() -> None:
    config = AgentHarnessExamplesConfig.from_env()
    slack_setup = await open_slack_project_connection()
    try:
        app_state = AgentHarnessExamplesApp(config, slack_setup.connection if slack_setup else None)
        try:
            await app_state.start_worker()
            host, _, port = config.http_address.rpartition(":")
            print(f"Dex agent harness examples listening on http://{config.http_address}")
            await create_app(app_state).run_task(host=host or "127.0.0.1", port=int(port))
        finally:
            await app_state.close()
    finally:
        if slack_setup is not None:
            await slack_setup.aclose()


if __name__ == "__main__":
    asyncio.run(main())
