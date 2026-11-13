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

Pass --approve to approve each pending call without asking, as a coding agent
does. Without a terminal and without --approve, the program leaves the call
pending in Dex and exits, which is the same as stopping it at the prompt.
"""

from __future__ import annotations

import argparse
import asyncio
import sys
import uuid

from dex_agents import AgentInterrupt

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.frameworks import FRAMEWORKS, build_quick_start_agent
from agent_harness_examples.quick_start.session import QuickStartApp, run_turn


class ApprovalLeftPending(Exception):
    """No one can answer the prompt, so the call keeps waiting in Dex."""

    def __init__(self, interrupt: AgentInterrupt) -> None:
        super().__init__(interrupt.tool_name)
        self.interrupt = interrupt


async def ask_for_approval(interrupt: AgentInterrupt) -> bool:
    if not sys.stdin.isatty():
        raise ApprovalLeftPending(interrupt)
    prompt = f"{interrupt.tool_name} wants to run with {interrupt.arguments}. Approve? [y/N] "
    answer = await asyncio.to_thread(input, prompt)
    return answer.strip().lower() in ("y", "yes")


async def approve_without_asking(interrupt: AgentInterrupt) -> bool:
    print(f"Approved {interrupt.tool_name} with {interrupt.arguments}.", flush=True)
    return True


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("framework", choices=FRAMEWORKS)
    parser.add_argument("message", nargs="?", help="the user message to send")
    parser.add_argument("--session-id", help="resume this session instead of starting one")
    parser.add_argument("--approve", action="store_true", help="approve each pending call without asking")
    arguments = parser.parse_args()
    if arguments.message is None and arguments.session_id is None:
        parser.error("pass a message, or --session-id to resume a session")

    config = AgentHarnessExamplesConfig.from_env()
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    session_flow = build_quick_start_agent(arguments.framework, order_desk, config)
    app = QuickStartApp(session_flow, config.server_address, config.blob_cache_dir)
    try:
        await app.start_worker()
        session_id = arguments.session_id
        if session_id is None:
            session_id = f"{arguments.framework}-quick-start-{uuid.uuid4().hex[:8]}"
            await app.sessions.start_session(session_id)
        print(f"Session {session_id}", flush=True)
        decide_approval = approve_without_asking if arguments.approve else ask_for_approval
        try:
            replies = await run_turn(app.sessions, session_id, arguments.message, decide_approval)
        except ApprovalLeftPending as pending:
            print(
                f"{pending.interrupt.tool_name} waits for approval. "
                f"Run again with --session-id {session_id} --approve to approve it.",
                flush=True,
            )
            return
        for reply in replies:
            print(f"Agent: {reply}", flush=True)
    finally:
        await app.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\nStopped. The session waits in Dex; run again with --session-id to resume.")
