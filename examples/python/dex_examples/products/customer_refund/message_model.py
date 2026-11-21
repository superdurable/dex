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

"""A deterministic stand-in for a language model that drafts customer messages."""

from __future__ import annotations


class MockMessageModel:
    """Answers like a model would, without an API key or a network call."""

    def write_message(self, customer: str, outcome: str, amount_usd: float, note: str) -> str:
        if outcome == "refunded":
            return (
                f"Hi {customer}, we are sorry for the service interruption. "
                f"We refunded {amount_usd:,.2f} USD to your card. {note}"
            ).strip()
        return (
            f"Hi {customer}, thank you for reaching out. " f"We cannot refund this charge. {note}"
        ).strip()
