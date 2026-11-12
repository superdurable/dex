# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The local requests a Worker host serves: images, new sessions, and the connector list."""

from __future__ import annotations

import asyncio
import logging
import stat
from collections.abc import Mapping
from dataclasses import asdict
from pathlib import Path
from typing import Any

import httpx2
from dex import AsyncClient, FlowAlreadyStartedError, FlowNotFoundError, StartFlowOptions

from dex_agents import (
    MAX_IMAGE_BYTES,
    MAX_IMAGES_PER_MESSAGE,
    AgentSessionClient,
    AgentSessionFlow,
    ImageUserMessageBuilder,
    ImageValidationError,
    MediaStore,
    detect_image_mime_type,
)
from dex_agents_cli_core.config import ConnectorsConfig
from dex_agents_cli_core.connectors import ConnectorsMcpUnavailableError, read_connector_catalog
from dex_agents_cli_core.terminal_ui.contract import candidate_session_ids
from dex_agents_cli_core.terminal_ui.local_requests import RequestOutcome

_SESSION_START_TIMEOUT_SECONDS = 30.0
_SESSION_START_POLL_SECONDS = 0.1
_CONNECTIONS_TIMEOUT_SECONDS = 10.0
_LOGGER = logging.getLogger(__name__)


def session_flow_capabilities(flow: AgentSessionFlow) -> dict[str, Any]:
    """Return the launch document's ``capabilities`` of the session Flow the Worker serves.

    Images are present when the runner builds user messages with images.
    Every agent session Flow supports steer and cancel; the snapshot's
    ``accepts_turn_control`` still decides each time.
    """
    capabilities: dict[str, Any] = {}
    if isinstance(flow.runner, ImageUserMessageBuilder):
        capabilities["images"] = {
            "max_per_message": MAX_IMAGES_PER_MESSAGE,
            "max_bytes": MAX_IMAGE_BYTES,
        }
    capabilities["plan_mode"] = flow.options.plan_mode_enabled
    capabilities["turn_control"] = True
    return capabilities


class HostedChatSession:
    """Serve the terminal UI's local requests from the Worker host's media store and Client."""

    def __init__(
        self,
        *,
        harness_name: str,
        client: AsyncClient,
        sessions: AgentSessionClient,
        media_store: MediaStore,
        start_environment: Mapping[str, str],
        connectors: ConnectorsConfig,
        connectors_mcp_url: str | None,
        web_url: str | None,
    ) -> None:
        """Serve requests for one harness's session Flow type.

        Args:
            harness_name: The prefix of session IDs.
            client: The Dex Client, to describe Flows.
            sessions: The session client of the Flow the Worker serves.
            media_store: The store ``put_image`` writes to.
            start_environment: The ``start_environment`` of a new session.
            connectors: The configuration file's ``connectors`` section.
            connectors_mcp_url: The connectors MCP endpoint, or ``None`` when off.
            web_url: Dex Web's URL, for its connections, or ``None``.
        """
        self._harness_name = harness_name
        self._client = client
        self._sessions = sessions
        self._media_store = media_store
        self._start_environment = dict(start_environment)
        self._connectors = connectors
        self._connectors_mcp_url = connectors_mcp_url
        self._web_url = web_url

    async def put_image(self, params: Mapping[str, Any], idempotency_key: str) -> RequestOutcome:
        """Store one image file by its SHA-256 and return its reference and base name."""
        path_text = params.get("path")
        if not isinstance(path_text, str) or not Path(path_text).is_absolute():
            return RequestOutcome.failed(
                "invalid_params",
                "put_image needs the absolute path of a file.",
                is_remembered=False,
            )
        path = Path(path_text)
        data = _read_image_file(path)
        if isinstance(data, RequestOutcome):
            return data
        try:
            reference = await self._media_store.put_image(data)
        except Exception:  # Each store raises its own upload errors, such as S3's.
            _LOGGER.exception("put_image could not store an image")
            return RequestOutcome.failed(
                "store_failed", f"{path.name} could not be stored; see the log."
            )
        return RequestOutcome.succeeded({"image": asdict(reference), "name": path.name})

    async def start_session(
        self, params: Mapping[str, Any], idempotency_key: str
    ) -> RequestOutcome:
        """Start the session the key names, or find the one it already started.

        Every start passes the key as the start request's ID, so the Dex
        server returns the run this key started and refuses a Flow ID another
        request holds. The long candidate is checked first, so a resend never
        starts the short one while the long one already holds this key.
        """
        short_id, long_id = candidate_session_ids(self._harness_name, idempotency_key)
        try:
            is_long_taken = await self._flow_exists(long_id)
            if is_long_taken and await self._start_with_key(long_id, idempotency_key):
                return await self._wait_for_session(long_id)
            if await self._start_with_key(short_id, idempotency_key):
                return await self._wait_for_session(short_id)
            if not is_long_taken and await self._start_with_key(long_id, idempotency_key):
                return await self._wait_for_session(long_id)
        except Exception:  # Any start failure is reported the same way.
            _LOGGER.exception("start_session could not start %s", short_id)
            return RequestOutcome.failed(
                "start_failed", f"Session {short_id} could not start; see the log."
            )
        return RequestOutcome.failed(
            "id_conflict", "Other requests already started both session IDs this key derives."
        )

    async def _flow_exists(self, session_id: str) -> bool:
        try:
            await self._client.describe_flow(session_id)
        except FlowNotFoundError:
            return False
        return True

    async def _start_with_key(self, session_id: str, idempotency_key: str) -> bool:
        """Start a session with the key as its request ID; ``False`` when another key holds it."""
        options = StartFlowOptions(request_id=idempotency_key, ignore_already_started=True)
        try:
            await self._sessions.start_session(
                session_id, options=options, start_environment=dict(self._start_environment)
            )
        except FlowAlreadyStartedError:
            _LOGGER.info("another request holds session %s", session_id)
            return False
        return True

    async def list_connectors(
        self, params: Mapping[str, Any], idempotency_key: str
    ) -> RequestOutcome:
        """Return the connector tools and, when Dex Web answers, its connections."""
        url = self._connectors_mcp_url
        if url is None:
            return RequestOutcome.failed(
                "mcp_unavailable",
                "Connector tools are off: connectors.mcp is auto, which needs dex.server: auto.",
            )
        try:
            catalog = await read_connector_catalog(url)
        except ConnectorsMcpUnavailableError as error:
            return RequestOutcome.failed(
                "mcp_unavailable", f"The connectors MCP server is unavailable: {error}."
            )
        tools = []
        for tool in catalog:
            identity = tool.identity
            is_enabled = self._connectors.enabled is None or (
                identity is not None and identity.is_enabled_by(self._connectors)
            )
            tools.append(
                {
                    "name": tool.name,
                    "connector": identity.connector if identity else None,
                    "connection": identity.connection if identity else None,
                    "operation": identity.operation if identity else None,
                    "is_enabled": is_enabled,
                }
            )
        return RequestOutcome.succeeded(
            {"mcp_url": url, "tools": tools, "connections": await self._read_connections()}
        )

    async def _wait_for_session(self, session_id: str) -> RequestOutcome:
        loop = asyncio.get_running_loop()
        deadline = loop.time() + _SESSION_START_TIMEOUT_SECONDS
        while (await self._sessions.get_agent_snapshot(session_id)).state is None:
            if loop.time() > deadline:
                return RequestOutcome.failed(
                    "start_failed", f"Session {session_id} did not start; is the Worker running?"
                )
            await asyncio.sleep(_SESSION_START_POLL_SECONDS)
        return RequestOutcome.succeeded({"session_id": session_id})

    async def _read_connections(self) -> list[dict[str, Any]] | None:
        if self._web_url is None:
            return None
        try:
            async with httpx2.AsyncClient(timeout=_CONNECTIONS_TIMEOUT_SECONDS) as http:
                response = await http.get(f"{self._web_url}/api/v2/connector-connections")
            listed = response.json().get("connections", []) if response.is_success else None
        except (httpx2.HTTPError, ValueError, AttributeError) as error:
            _LOGGER.warning("cannot list the Dex Web connections: %s", error)
            return None
        if not isinstance(listed, list):
            return None
        return [
            {
                "connector": connection.get("connectorId"),
                "connection": connection.get("connectionName"),
                "status": connection.get("status"),
            }
            for connection in listed
            if isinstance(connection, dict)
        ]


def _read_image_file(path: Path) -> bytes | RequestOutcome:
    """Return an image file's bytes, or the error that refuses it, before storing anything."""
    try:
        status = path.stat()
    except OSError:
        return RequestOutcome.failed("not_found", f"{path.name} does not exist.")
    if not stat.S_ISREG(status.st_mode):
        return RequestOutcome.failed("not_an_image", f"{path.name} is not a regular file.")
    if status.st_size > MAX_IMAGE_BYTES:
        return _too_large(path.name, status.st_size)
    try:
        with path.open("rb") as file:
            data = file.read(MAX_IMAGE_BYTES + 1)
    except OSError:
        return RequestOutcome.failed("not_found", f"{path.name} cannot be read.")
    if len(data) > MAX_IMAGE_BYTES:
        return _too_large(path.name, len(data))
    try:
        detect_image_mime_type(data)
    except ImageValidationError:
        return RequestOutcome.failed(
            "not_an_image", f"{path.name} is not a PNG, JPEG, GIF, or WebP image."
        )
    return data


def _too_large(name: str, size: int) -> RequestOutcome:
    return RequestOutcome.failed(
        "too_large", f"{name} has {size:,} bytes; an image may have at most {MAX_IMAGE_BYTES:,}."
    )
