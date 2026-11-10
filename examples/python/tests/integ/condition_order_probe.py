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

"""Shared definitions of the condition order probe Flows.

The probe Flows hold their first wait behind a gate, so both competing
messages are pending before the AnyOf registers.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from dex import Channel

PROBE_GATE_OPENED = Channel("probeGateOpened", type(None))


@dataclass(frozen=True)
class ConditionOrderOutcome:
    """Which condition won and how many messages the other left pending."""

    winner: str
    loser_pending_count: int = field(metadata={"dex_json_name": "loserPendingCount"})
