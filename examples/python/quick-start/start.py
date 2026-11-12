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

"""Starts one OrderFlow. Pass an order ID, or let the command make one."""

from __future__ import annotations

import sys
import uuid
from pathlib import Path

from dex import Client, ClientOptions, Registry

from quick_start.config import SERVER_ADDRESS, open_cache
from quick_start.order_flow import OrderFlow
from quick_start.shop_api import ShopApi


def main() -> None:
    order_id = sys.argv[1] if len(sys.argv) > 1 else f"order-{uuid.uuid4().hex[:8]}"
    order_flow = OrderFlow(ShopApi())
    registry = Registry((order_flow,))
    cache = open_cache(Path(".dex-blob-cache/client"))
    client = Client(registry, cache, ClientOptions(server_address=SERVER_ADDRESS))
    try:
        client.start_flow(order_flow, order_id, order_id)
        print(f"Started Flow {order_id}. Ship runs in 60 seconds.", flush=True)
    finally:
        client.close()
        cache.close()


if __name__ == "__main__":
    main()
