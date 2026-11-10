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

"""The local demo model's replies, shared by every framework's demo model.

The demo model needs no provider key. It reads the turn's user message and the
tool results the turn already has, so a retried model call gets the same reply:

* "cancel order <id>" calls cancel_order.
* Any other text with "order <id>" calls look_up_order.
* After the tool results arrive, it answers with them.
"""

from __future__ import annotations

import re
from collections.abc import Sequence
from dataclasses import dataclass

DEMO_MODEL = "mock/order-support"
DEMO_MODEL_PREFIX = "mock/"
DEMO_REPLY_PREFIX = "Local demo response: "
DEMO_CANCEL_REASON = "requested by the customer"
DEMO_USAGE_TEXT = (
    DEMO_REPLY_PREFIX + "ask me to look up or cancel an order, such as 'Look up order 1001'."
)
_CANCEL_ORDER_PATTERN = re.compile(r"\bcancel order (\w+)", re.IGNORECASE)
_ORDER_PATTERN = re.compile(r"\border (\w+)", re.IGNORECASE)


@dataclass(frozen=True)
class DemoToolRequest:
    tool_name: str
    arguments: dict[str, str]


def is_demo_model(model: str) -> bool:
    return model.startswith(DEMO_MODEL_PREFIX)


def plan_demo_reply(user_text: str, tool_outputs: Sequence[str]) -> DemoToolRequest | str:
    """Returns the tool call to request, or the answer text once the turn has tool results."""
    if tool_outputs:
        return DEMO_REPLY_PREFIX + " ".join(tool_outputs)
    if cancel_match := _CANCEL_ORDER_PATTERN.search(user_text):
        arguments = {"order_id": cancel_match.group(1), "reason": DEMO_CANCEL_REASON}
        return DemoToolRequest("cancel_order", arguments)
    if order_match := _ORDER_PATTERN.search(user_text):
        return DemoToolRequest("look_up_order", {"order_id": order_match.group(1)})
    return DEMO_USAGE_TEXT
