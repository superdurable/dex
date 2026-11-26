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

"""Approves or rejects a refund that waits for the manager: decide FLOW_ID approve|reject REASON."""

from __future__ import annotations

import argparse
import asyncio
from pathlib import Path

from dex_examples.products.refund_durable_timers.refund_settings import (
    new_presenter_client,
    new_refund_timers_flow,
    open_cache,
)
from dex_examples.products.refund_durable_timers.refund_timers_flow import DecisionReason


async def send_manager_decision(flow_id: str, decision: str, reason: str) -> None:
    flow = new_refund_timers_flow()
    cache = open_cache(Path(".dex-blob-cache/refund-durable-timers-client"))
    client = new_presenter_client(flow, cache)
    try:
        decide_rpc = flow.approve_refund if decision == "approve" else flow.reject_refund
        await client.invoke_rpc(decide_rpc, flow_id, DecisionReason(reason))
        print(f"Refund {flow_id}: {decision} sent.", flush=True)
    finally:
        await client.close()
        cache.close()


def main() -> None:
    parser = argparse.ArgumentParser(description="Approve or reject a refund.")
    parser.add_argument("flow_id")
    parser.add_argument("decision", choices=("approve", "reject"))
    parser.add_argument("reason")
    arguments = parser.parse_args()
    asyncio.run(send_manager_decision(arguments.flow_id, arguments.decision, arguments.reason))


if __name__ == "__main__":
    main()
