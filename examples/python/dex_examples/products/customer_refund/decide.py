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

"""Approves or rejects a waiting refund: decide FLOW_ID approve|reject REASON."""

from __future__ import annotations

import argparse
from pathlib import Path

from dex import Client, ClientOptions, Registry

from dex_examples.products.customer_refund.refund_flow import DecisionReason
from dex_examples.products.customer_refund.refund_settings import (
    SERVER_ADDRESS,
    new_refund_flow,
    open_cache,
    payload_encryption,
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Approve or reject a refund.")
    parser.add_argument("flow_id")
    parser.add_argument("decision", choices=("approve", "reject"))
    parser.add_argument("reason")
    arguments = parser.parse_args()

    refund_flow = new_refund_flow()
    cache = open_cache(Path(".dex-blob-cache/customer-refund-client"))
    options = ClientOptions(server_address=SERVER_ADDRESS, payload_encryption=payload_encryption())
    client = Client(Registry((refund_flow,)), cache, options)
    try:
        rpc = (
            refund_flow.approve_refund
            if arguments.decision == "approve"
            else refund_flow.reject_refund
        )
        client.invoke_rpc(rpc, arguments.flow_id, DecisionReason(arguments.reason))
        outcome = "approved" if arguments.decision == "approve" else "rejected"
        print(f"Refund {arguments.flow_id}: {outcome}.", flush=True)
    finally:
        client.close()
        cache.close()


if __name__ == "__main__":
    main()
