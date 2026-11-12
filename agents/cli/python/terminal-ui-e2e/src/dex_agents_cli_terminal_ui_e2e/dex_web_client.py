# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A second client of a command's session: the Dex Web routes a browser calls."""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

SESSION_ACTION_PERMISSION = "dex-agents-cli.operate"
"""The permission every session Action of a command's session graph requires."""

_REQUEST_TIMEOUT_SECONDS = 30.0


@dataclass(frozen=True)
class DexWebResponse:
    """One Dex Web answer: the HTTP status and the decoded JSON body."""

    status: int
    body: Mapping[str, Any]


class DexWebSessionClient:
    """Calls the Dex Web routes that the Agent session tab calls for one session."""

    def __init__(self, web_port: int, flow_id: str) -> None:
        """Bind the client to one session on the Dex Web of ``web_port``."""
        self._base_url = f"http://127.0.0.1:{web_port}"
        self.flow_id = flow_id

    def flow_type(self) -> str:
        """Return the session's Flow type from its Flow summary."""
        summary = self._get("/api/flows/summary", {"flowId": self.flow_id})
        return str(summary.body["flowType"])

    def definition_revision(self, flow_type: str) -> str:
        """Return the definition revision that Dex Web checks before each Action."""
        definitions = self._get("/api/flow-definitions/by-type", {"flowType": flow_type})
        return str(definitions.body["v2"]["definitionRevision"])

    def send_message(self, text: str, permissions: Sequence[str]) -> DexWebResponse:
        """Send one message as the composer's Send does, Working as ``permissions``."""
        return self._post_session_route(
            "/api/v2/agent-sessions/messages", {"text": text}, permissions
        )

    def resolve_interrupt(
        self, interrupt_id: str, is_approved: bool, permissions: Sequence[str]
    ) -> DexWebResponse:
        """Approve or reject one waiting call, as the approval card does."""
        flow_type = self.flow_type()
        return self._post(
            "/api/v2/actions",
            {
                "flowType": flow_type,
                "flowId": self.flow_id,
                "rpcName": "resolve_interrupt",
                "workQueuePermissions": list(permissions),
                "input": {"interrupt_id": interrupt_id, "approved": is_approved},
                "attributeSnapshot": {},
            },
            self.definition_revision(flow_type),
        )

    def wait_for_pending_interrupt(self, timeout_seconds: float = 90.0) -> Mapping[str, Any]:
        """Return the first pending interrupt of the session, once there is one.

        Raises:
            AssertionError: If none appears before the timeout.
        """
        deadline = time.monotonic() + timeout_seconds
        while time.monotonic() < deadline:
            interrupts = self._pending_interrupts()
            if interrupts:
                return interrupts[0]
            time.sleep(0.5)
        raise AssertionError(f"no pending interrupt in {self.flow_id} after {timeout_seconds}s")

    def _pending_interrupts(self) -> list[Mapping[str, Any]]:
        summary = self._get("/api/flows/summary", {"flowId": self.flow_id})
        state = self._get(
            "/api/flows/state", {"flowId": self.flow_id, "runId": str(summary.body["runId"])}
        )
        for attribute in state.body.get("attributes") or []:
            if attribute.get("key") == "PendingInterrupts":
                value = self._load_blob(attribute.get("value"))
                return list(value) if isinstance(value, list) else []
        return []

    def _load_blob(self, value: Any) -> Any:
        """Return ``value``, read through ``/api/blobs/load`` when the state holds a reference."""
        if not isinstance(value, dict) or "__dexBlobReference" not in value:
            return value
        reference = value["__dexBlobReference"]
        loaded = self._post_json("/api/blobs/load", {"flowId": self.flow_id, "values": [reference]})
        return loaded.body.get("values", {}).get(f"{reference['kind']}:{reference['id']}")

    def _post_session_route(
        self, route: str, fields: Mapping[str, Any], permissions: Sequence[str]
    ) -> DexWebResponse:
        flow_type = self.flow_type()
        body = {
            "flowType": flow_type,
            "flowId": self.flow_id,
            "workQueuePermissions": list(permissions),
            **fields,
        }
        return self._post(route, body, self.definition_revision(flow_type))

    def _get(self, path: str, query: Mapping[str, str]) -> DexWebResponse:
        url = f"{self._base_url}{path}?{urllib.parse.urlencode(query)}"
        return self._send(urllib.request.Request(url))

    def _post(self, path: str, body: Mapping[str, Any], revision: str) -> DexWebResponse:
        return self._post_json(path, body, {"X-Dex-Flow-Definition-Revision": revision})

    def _post_json(
        self, path: str, body: Mapping[str, Any], headers: Mapping[str, str] | None = None
    ) -> DexWebResponse:
        request = urllib.request.Request(
            f"{self._base_url}{path}",
            data=json.dumps(body).encode(),
            headers={"Content-Type": "application/json", **(headers or {})},
            method="POST",
        )
        return self._send(request)

    def _send(self, request: urllib.request.Request) -> DexWebResponse:
        try:
            with urllib.request.urlopen(request, timeout=_REQUEST_TIMEOUT_SECONDS) as response:
                return DexWebResponse(response.status, _decode_body(response.read()))
        except urllib.error.HTTPError as error:
            return DexWebResponse(error.code, _decode_body(error.read()))


def _decode_body(raw: bytes) -> Mapping[str, Any]:
    """Decode a JSON object body; keep any other body as text, so its status still counts."""
    try:
        decoded = json.loads(raw or b"{}")
    except ValueError:
        return {"text": raw.decode(errors="replace")}
    return decoded if isinstance(decoded, dict) else {"value": decoded}
