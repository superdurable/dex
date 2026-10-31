# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The framework run state of a turn that waits for its tool call Steps."""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from google.adk.events import Event

from dex_agents_google_adk._history import adk_event_from_json_object, adk_event_to_json_object

SUSPENDED_ADK_TURN_FORMAT_VERSION = 1


@dataclass(frozen=True, kw_only=True)
class SuspendedAdkTurn:
    """Hold what resumes one paused ADK invocation.

    Attributes:
        invocation_id: ADK invocation ID of the turn.
        user_event_id: ID of the user event ADK appended for the turn. The
            session already stores the message, so the completed turn omits it.
        events: Every event ADK appended in the turn so far, from that user
            event on.
        confirmation_request_ids: ``adk_request_confirmation`` call ID by the
            tool call ID it waits on, for the latest model response's calls.
    """

    invocation_id: str
    user_event_id: str
    events: Sequence[Event]
    confirmation_request_ids: Mapping[str, str] = field(default_factory=dict)

    def to_framework_run_state(self) -> str:
        return json.dumps(
            {
                "version": SUSPENDED_ADK_TURN_FORMAT_VERSION,
                "invocation_id": self.invocation_id,
                "user_event_id": self.user_event_id,
                "events": [adk_event_to_json_object(event) for event in self.events],
                "confirmation_request_ids": dict(self.confirmation_request_ids),
            },
            ensure_ascii=False,
        )

    @classmethod
    def from_framework_run_state(cls, framework_run_state: str) -> SuspendedAdkTurn:
        decoded: dict[str, Any] = json.loads(framework_run_state)
        version = decoded.get("version")
        if version != SUSPENDED_ADK_TURN_FORMAT_VERSION:
            raise ValueError(f"unsupported ADK framework run state version {version!r}")
        return cls(
            invocation_id=str(decoded["invocation_id"]),
            user_event_id=str(decoded["user_event_id"]),
            events=[adk_event_from_json_object(event) for event in decoded["events"]],
            confirmation_request_ids={
                str(tool_call_id): str(request_id)
                for tool_call_id, request_id in decoded["confirmation_request_ids"].items()
            },
        )
