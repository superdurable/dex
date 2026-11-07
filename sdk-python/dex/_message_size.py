# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from typing import cast

import grpc
from grpc_status import rpc_status

#: The gRPC receive limit of a Client or Worker whose ``grpc_max_message_bytes`` is zero.
#:
#: The limit applies to every message the SDK receives: Client responses such as RPC
#: results and state reads, responses to the Worker's FlowService calls such as LoadBlobs,
#: and the Step and RPC invocations that Dex sends to a Worker. The default is 32 MiB,
#: which matches the Dex server's default ``api.grpcMaxMessageBytes``. With the default,
#: the SDK keeps gRPC's unlimited send limit, so the Dex server's receive limit bounds what
#: the SDK sends. A positive ``grpc_max_message_bytes`` limits both directions.
#:
#: A message carries one or more application values plus an envelope of metadata. Keep the
#: limit above the largest application value by an explicit margin. The default leaves
#: 16 MiB of headroom for values up to 16 MiB. When an application needs larger values,
#: raise both ``grpc_max_message_bytes`` and the server's ``api.grpcMaxMessageBytes``,
#: because each hop applies the smaller limit of its ends.
#:
#: Example:
#:     >>> options = ClientOptions(grpc_max_message_bytes=2 * DEFAULT_GRPC_MAX_MESSAGE_BYTES)
#:
#: .. versionadded:: 1.38.0
DEFAULT_GRPC_MAX_MESSAGE_BYTES = 32 << 20


def validate_grpc_max_message_bytes(configured: int) -> None:
    if configured < 0:
        raise ValueError(
            f"grpc_max_message_bytes must not be negative, got {configured}"
        )


def grpc_message_size_channel_options(configured: int) -> tuple[tuple[str, int], ...]:
    if configured == 0:
        return (("grpc.max_receive_message_length", DEFAULT_GRPC_MAX_MESSAGE_BYTES),)
    return (
        ("grpc.max_receive_message_length", configured),
        ("grpc.max_send_message_length", configured),
    )


_GRPC_SIZE_STATUS_CODES = frozenset(
    (grpc.StatusCode.RESOURCE_EXHAUSTED, grpc.StatusCode.OUT_OF_RANGE)
)

# Lowercase diagnostics of gRPC message size limits; transports reuse the codes otherwise.
_GRPC_SIZE_DIAGNOSTICS = (
    "message larger than max",
    "message after decompression larger than max",
    "message too large",
    "message with a size larger than",
    "decompresses to a size larger than",
    "message length too large",
    "exceeded while decompressing message",
    "cannot return body with more than 4gb of data",
)


def is_grpc_message_size_error(error: grpc.RpcError) -> bool:
    """Report a gRPC size rejection, which carries no Dex service error details.

    The receiver's limit yields RESOURCE_EXHAUSTED, and a tonic server's send limit
    yields OUT_OF_RANGE. The message must also name a size limit, because HTTP/2 flow
    control failures reuse RESOURCE_EXHAUSTED without details.
    """
    call = cast(grpc.Call, error)
    if call.code() not in _GRPC_SIZE_STATUS_CODES:
        return False
    message = (call.details() or "").lower()
    if not any(diagnostic in message for diagnostic in _GRPC_SIZE_DIAGNOSTICS):
        return False
    try:
        status = rpc_status.from_call(call)
    except ValueError:
        return False
    return status is None or not status.details
