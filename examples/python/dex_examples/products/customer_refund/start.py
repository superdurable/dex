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

"""Starts one CustomerRefundFlow: start FLOW_ID [normal|fail-then-succeed|deny|small]."""

from __future__ import annotations

import argparse
from dataclasses import replace
from pathlib import Path

from dex import Client, ClientOptions, Registry

from dex_examples.products.customer_refund.refund_flow import RefundRequest
from dex_examples.products.customer_refund.refund_settings import (
    SERVER_ADDRESS,
    new_refund_flow,
    open_cache,
    payload_encryption,
)

NORTHSTAR_RENEWAL = RefundRequest(
    customer="Northstar Studio",
    customer_email="billing@northstar.example",
    order_id="INV-1249",
    reason="Our workspace was down for 31 hours after the renewal.",
)

SCENARIOS = {
    "normal": NORTHSTAR_RENEWAL,
    "fail-then-succeed": replace(NORTHSTAR_RENEWAL, payment_timeouts_to_simulate=2),
    "deny": replace(NORTHSTAR_RENEWAL, order_id="INV-0388"),
    "small": replace(NORTHSTAR_RENEWAL, order_id="INV-0049"),
}


def main() -> None:
    parser = argparse.ArgumentParser(description="Start one customer refund.")
    parser.add_argument("flow_id", help="a short Flow ID, such as refund1")
    parser.add_argument("scenario", nargs="?", default="normal", choices=SCENARIOS)
    arguments = parser.parse_args()

    refund_flow = new_refund_flow()
    cache = open_cache(Path(".dex-blob-cache/customer-refund-client"))
    options = ClientOptions(server_address=SERVER_ADDRESS, payload_encryption=payload_encryption())
    client = Client(Registry((refund_flow,)), cache, options)
    try:
        client.start_flow(refund_flow, arguments.flow_id, SCENARIOS[arguments.scenario])
        print(f"Started refund {arguments.flow_id} ({arguments.scenario}).", flush=True)
    finally:
        client.close()
        cache.close()


if __name__ == "__main__":
    main()
