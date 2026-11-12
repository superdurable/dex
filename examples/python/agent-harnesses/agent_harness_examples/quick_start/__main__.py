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

"""Runs one turn of a quick start agent session from the command line.

    python -m agent_harness_examples.quick_start openai-agents "Cancel order 1002"
    python -m agent_harness_examples.quick_start openai-agents --session-id ID

The second form resumes a session, for example after you stopped this program
while a tool call waited for your approval.
"""

from __future__ import annotations

import argparse
import asyncio
import uuid

from dex_agents import AgentInterrupt

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.session import (
    FRAMEWORKS,
    QuickStartApp,
    build_quick_start_agent,
    run_turn,
)


async def ask_for_approval(interrupt: AgentInterrupt) -> bool:
    prompt = f"{interrupt.tool_name} wants to run with {interrupt.arguments}. Approve? [y/N] "
    answer = await asyncio.to_thread(input, prompt)
    return answer.strip().lower() in ("y", "yes")


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("framework", choices=FRAMEWORKS)
    parser.add_argument("message", nargs="?", help="the user message to send")
    parser.add_argument("--session-id", help="resume this session instead of starting one")
    arguments = parser.parse_args()
    if arguments.message is None and arguments.session_id is None:
        parser.error("pass a message, or --session-id to resume a session")

    config = AgentHarnessExamplesConfig.from_env()
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    app = QuickStartApp(build_quick_start_agent(arguments.framework, order_desk, config), config)
    try:
        await app.start_worker()
        session_id = arguments.session_id
        if session_id is None:
            session_id = f"{arguments.framework}-quick-start-{uuid.uuid4().hex[:8]}"
            await app.sessions.start_session(session_id)
        print(f"Session {session_id}", flush=True)
        replies = await run_turn(app.sessions, session_id, arguments.message, ask_for_approval)
        for reply in replies:
            print(f"Agent: {reply}", flush=True)
    finally:
        await app.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\nStopped. The session waits in Dex; run again with --session-id to resume.")
