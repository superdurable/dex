# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import TYPE_CHECKING, Any, Generic, TypeVar

from dex._utils import require_name

if TYPE_CHECKING:
    from dex.context import Context

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class Stream(Generic[ValueT]):
    """Define a typed best-effort resumable Stream owned by one Flow type.

    Attributes:
        name: Logical name unique within the Flow persistence schema.
        value_type: Python type encoded for every message.
        stream_capacity_bytes: Positive approximate budget shared by all Flow instances.
    """

    name: str
    value_type: type[ValueT]
    stream_capacity_bytes: int

    def __post_init__(self) -> None:
        """Validate the Stream definition after dataclass construction.

        Raises:
            ValueError: If the name is empty or the byte budget is not positive.
            TypeError: If the byte budget is not an integer.
        """
        require_name(self.name)
        if not isinstance(self.stream_capacity_bytes, int):
            raise TypeError("Stream stream_capacity_bytes must be an integer")
        if self.stream_capacity_bytes <= 0:
            raise ValueError("Stream stream_capacity_bytes must be positive")

    def write(self, context: Context, value: ValueT) -> Any:
        """Append one message immediately from the current Step execution.

        Synchronous handlers call this method directly. Async handlers must await its
        result. One Step execution may write once per Stream, and RPC Contexts reject it.

        Args:
            context: Current synchronous or asynchronous Step Context.
            value: Typed message to append.

        Returns:
            ``None`` for a synchronous Worker or an awaitable for an AsyncWorker.
        """
        return context._write_stream(self, value)


@dataclass(frozen=True)
class StreamMessage(Generic[ValueT]):
    """Describe one retained Stream message returned by a Client.

    Attributes:
        value: Decoded application message.
        resume_token: Opaque token for the next read.
        created_time: Server-assigned UTC creation time.
        idempotency_key: Client key or Step-generated runID#stepExecutionID key.
    """

    value: ValueT
    resume_token: str
    created_time: datetime
    idempotency_key: str
