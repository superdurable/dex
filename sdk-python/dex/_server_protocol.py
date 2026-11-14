# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from importlib.metadata import PackageNotFoundError, version
from time import monotonic
from typing import Iterable

import grpc

from dex.dexpb import dex_pb2 as pb

MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION = 4
MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION = 5
HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION = 3
NAMED_TASK_QUEUE_PROTOCOL_VERSION = 5
LAST_SERVER_ZD_RELEASE_BEFORE_MINIMUM_PROTOCOL = "v0.1.16"


def sdk_version() -> str:
    try:
        return version("dex-python-sdk")
    except PackageNotFoundError:
        return "dev"


def negotiate_server_protocol(server_info: pb.ServerInfo, artifact_version: str) -> int:
    server_minimum = server_info.minimum_supported_protocol_version
    server_current = server_info.current_protocol_version
    if (
        MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        or MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        or MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION
        > MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION
    ):
        raise _compatibility_error(
            artifact_version,
            server_info.server_version,
            server_minimum,
            server_current,
            "Python SDK protocol interval is invalid",
        )
    if server_minimum == 0 or server_current == 0 or server_minimum > server_current:
        raise _compatibility_error(
            artifact_version,
            server_info.server_version,
            server_minimum,
            server_current,
            "Server protocol interval is invalid",
        )
    negotiated = min(server_current, MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION)
    if (
        negotiated < server_minimum
        or negotiated < MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION
    ):
        raise _compatibility_error(
            artifact_version,
            server_info.server_version,
            server_minimum,
            server_current,
            "protocol intervals do not overlap",
        )
    return negotiated


class NegotiatedServerProtocol:
    def __init__(self) -> None:
        self._version = 0

    @property
    def version(self) -> int:
        return self._version

    def record_version(self, version: int) -> None:
        self._version = version

    def require_hard_delete_attribute_support(
        self,
        writes: Iterable[pb.AttributeWrite],
    ) -> None:
        attribute_names = [
            write.key for write in writes if write.is_hard_delete_on_null
        ]
        if (
            not attribute_names
            or self._version >= HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION
        ):
            return
        raise RuntimeError(
            "hard-delete Attribute writes require Dex Server protocol version "
            f"{HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION} or later, but the negotiated "
            f"protocol version is {self._version}: {', '.join(attribute_names)}"
        )

    def require_named_task_queue_support(self, task_queue: str) -> None:
        if not task_queue or self._version >= NAMED_TASK_QUEUE_PROTOCOL_VERSION:
            return
        raise RuntimeError(
            f"task queue {task_queue!r} requires Dex Server protocol version "
            f"{NAMED_TASK_QUEUE_PROTOCOL_VERSION} or later, but the negotiated "
            f"protocol version is {self._version}; upgrade the Server or use "
            "the default task queue"
        )


def server_info_request_error(
    artifact_version: str, failure: grpc.RpcError
) -> RuntimeError:
    return RuntimeError(
        f'Python SDK version "{artifact_version}" cannot request Dex Server '
        f"information: GetServerInfo failed: {failure}"
    )


class ServerProtocolNegotiationTimeoutError(Exception):
    """Raised when a caller's deadline expires while waiting for negotiation."""


def remaining_negotiation_seconds(deadline: float | None) -> float | None:
    """Return seconds left before a ``time.monotonic`` deadline.

    Returns:
        None when ``deadline`` is None, meaning no limit. Otherwise the seconds
        left, which are zero or negative once the deadline has passed.
    """
    return None if deadline is None else deadline - monotonic()


def _compatibility_error(
    artifact_version: str,
    server_version: str,
    server_minimum: int,
    server_current: int,
    reason: str,
) -> RuntimeError:
    displayed_server_version = server_version or "unknown"
    reported_range = (
        f"The Server reports protocol versions {server_minimum}-{server_current} "
        f"(server version {displayed_server_version})."
    )
    if 0 < server_minimum <= server_current < MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION:
        return RuntimeError(
            "This SDK requires a Dex Server with protocol version "
            f"{MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION} or later "
            "(server-zd newer than "
            f"{LAST_SERVER_ZD_RELEASE_BEFORE_MINIMUM_PROTOCOL}). "
            f"{reported_range} Upgrade the Server before upgrading the SDK."
        )
    if MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION < server_minimum <= server_current:
        return RuntimeError(
            f'Python SDK version "{artifact_version}" supports Dex Server protocol '
            f"versions {MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}-"
            f"{MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}, but the Server requires "
            f"a newer protocol. {reported_range} Upgrade the SDK."
        )
    return RuntimeError(
        f'Python SDK version "{artifact_version}" protocol '
        f"[{MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION},"
        f"{MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}] cannot use "
        f'Server version "{displayed_server_version}" protocol '
        f"[{server_minimum},{server_current}]: {reason}"
    )
