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

"""Starts one refund with the chosen simulation knobs: start FLOW_ID [options]."""

from __future__ import annotations

import argparse
import asyncio
from pathlib import Path

from dex_examples.products.refund_durable_timers.refund_settings import (
    new_presenter_client,
    new_refund_timers_flow,
    open_cache,
)
from dex_examples.products.refund_durable_timers.refund_timers_flow import (
    RefundRequest,
    RefundSimulation,
    SettlementPolling,
    refund_start_options,
)


def new_refund_request(
    settlement_polling: SettlementPolling, simulation: RefundSimulation
) -> RefundRequest:
    """Returns the sample refund request with the given polling choice and knobs."""
    return RefundRequest(
        customer="Harbor Lane Bakery",
        customer_email="billing@harborlane.example",
        order_id="INV-2071",
        amount_cents=186_000,
        settlement_polling=settlement_polling,
        simulation=simulation,
    )


def parse_start_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Start one refund with simulation knobs.")
    parser.add_argument("flow_id", help="a short Flow ID, such as refund1")
    parser.add_argument(
        "--settlement-polling",
        choices=[choice.value for choice in SettlementPolling],
        default=SettlementPolling.TIMER.value,
        help="timer: a durable Timer per check; heartbeat: one long-running Step",
    )
    parser.add_argument(
        "--transient-failures", type=int, default=0, help="payment attempts that fail first"
    )
    parser.add_argument(
        "--rate-limit-retry-after",
        type=int,
        default=0,
        metavar="SECONDS",
        help="rate-limit the next payment attempt once with this Retry-After",
    )
    parser.add_argument(
        "--settle-after-checks", type=int, default=1, help="the check that reports settled"
    )
    parser.add_argument("--never-settle", action="store_true", help="the bank never settles")
    return parser.parse_args()


async def start_refund(arguments: argparse.Namespace) -> None:
    simulation = RefundSimulation(
        transient_payment_failures=arguments.transient_failures,
        rate_limit_retry_after_seconds=arguments.rate_limit_retry_after,
        settle_after_checks=arguments.settle_after_checks,
        bank_never_settles=arguments.never_settle,
    )
    request = new_refund_request(SettlementPolling(arguments.settlement_polling), simulation)
    flow = new_refund_timers_flow()
    cache = open_cache(Path(".dex-blob-cache/refund-durable-timers-client"))
    client = new_presenter_client(flow, cache)
    try:
        await client.start_flow(flow, arguments.flow_id, request, refund_start_options())
        print(f"Started refund {arguments.flow_id}: {request}", flush=True)
    finally:
        await client.close()
        cache.close()


def main() -> None:
    asyncio.run(start_refund(parse_start_arguments()))


if __name__ == "__main__":
    main()
