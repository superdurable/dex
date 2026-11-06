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

DEFAULT_GRPC_MAX_MESSAGE_BYTES = 32 << 20
"""The gRPC receive limit of a Client or Worker whose ``grpc_max_message_bytes`` is zero.

The limit applies to every message the SDK receives: Client responses such as RPC
results and state reads, responses to the Worker's FlowService calls such as LoadBlobs,
and the Step and RPC invocations that Dex sends to a Worker. The default is 32 MiB,
which matches the Dex server's default ``api.grpcMaxMessageBytes``. With the default,
the SDK keeps gRPC's unlimited send limit, so the Dex server's receive limit bounds what
the SDK sends. A positive ``grpc_max_message_bytes`` limits both directions.

A message carries one or more application values plus an envelope of metadata. Keep the
limit above the largest application value by an explicit margin. The default leaves
16 MiB of headroom for values up to 16 MiB. When an application needs larger values,
raise both ``grpc_max_message_bytes`` and the server's ``api.grpcMaxMessageBytes``,
because each hop applies the smaller limit of its ends.

Example:
    >>> options = ClientOptions(grpc_max_message_bytes=2 * DEFAULT_GRPC_MAX_MESSAGE_BYTES)

.. versionadded:: 1.38.0
"""


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


def is_grpc_message_size_error(error: grpc.RpcError) -> bool:
    """Report a gRPC size rejection, which carries no Dex service error details."""
    if cast(grpc.Call, error).code() is not grpc.StatusCode.RESOURCE_EXHAUSTED:
        return False
    try:
        status = rpc_status.from_call(cast(grpc.Call, error))
    except ValueError:
        return False
    return status is None or not status.details
