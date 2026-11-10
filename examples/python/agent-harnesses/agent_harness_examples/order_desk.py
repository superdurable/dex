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

"""The order system that every order support agent reads and writes.

Each agent has the same two tools: look_up_order reads an order, and
cancel_order changes it. A real application calls its order service here.
"""

from __future__ import annotations

from dataclasses import dataclass

ORDER_SUPPORT_INSTRUCTIONS = (
    "You are an order support agent. Look up an order before you answer a "
    "question about it. Cancel an order only when the customer asks, and say "
    "whether the cancellation succeeded."
)


@dataclass
class Order:
    order_id: str
    items: str
    status: str


class OrderDesk:
    """An in-memory order system that records every cancellation it performs."""

    def __init__(self) -> None:
        self._orders = {
            "1001": Order("1001", "2 notebooks", "shipped"),
            "1002": Order("1002", "1 desk lamp", "processing"),
        }
        self.cancelled_order_ids: list[str] = []

    def describe_order(self, order_id: str) -> str:
        order = self._orders.get(order_id)
        if order is None:
            return f"Order {order_id} does not exist."
        return f"Order {order.order_id}: {order.items}, {order.status}."

    def cancel_order(self, order_id: str, reason: str) -> str:
        order = self._orders.get(order_id)
        if order is None:
            return f"Order {order_id} does not exist."
        if order.status != "processing":
            return f"Order {order_id} is {order.status} and cannot be cancelled."
        order.status = "cancelled"
        self.cancelled_order_ids.append(order_id)
        return f"Order {order_id} is cancelled: {reason}."
