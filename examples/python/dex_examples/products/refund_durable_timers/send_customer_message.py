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

"""Sends a customer message: send_customer_message FLOW_ID TEXT [--documents-complete]."""

from __future__ import annotations

import argparse
import asyncio
from pathlib import Path

from dex_examples.products.refund_durable_timers.refund_settings import (
    new_presenter_client,
    new_refund_timers_flow,
    open_cache,
)
from dex_examples.products.refund_durable_timers.refund_timers_flow import CustomerMessage


async def send_message(flow_id: str, message: CustomerMessage) -> None:
    flow = new_refund_timers_flow()
    cache = open_cache(Path(".dex-blob-cache/refund-durable-timers-client"))
    client = new_presenter_client(flow, cache)
    try:
        await client.invoke_rpc(flow.send_customer_message, flow_id, message)
        print(f"Refund {flow_id}: customer message sent.", flush=True)
    finally:
        await client.close()
        cache.close()


def main() -> None:
    parser = argparse.ArgumentParser(description="Send a customer message to a refund.")
    parser.add_argument("flow_id")
    parser.add_argument("text")
    parser.add_argument(
        "--documents-complete",
        action="store_true",
        help="the message completes the documents, so the refund moves to the manager",
    )
    arguments = parser.parse_args()
    message = CustomerMessage(arguments.text, documents_complete=arguments.documents_complete)
    asyncio.run(send_message(arguments.flow_id, message))


if __name__ == "__main__":
    main()
