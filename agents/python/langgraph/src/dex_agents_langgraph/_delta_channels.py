# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turn-local stand-ins for LangGraph ``DeltaChannel`` state channels."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any, Self

from langgraph._internal._typing import MISSING
from langgraph.channels.base import BaseChannel
from langgraph.channels.delta import DeltaChannel


# A DeltaChannel checkpoint holds only a marker, and the stored run state keeps one checkpoint.
class CheckpointedDeltaChannel(BaseChannel[Any, Any, Any]):
    """Apply a ``DeltaChannel``'s reducer, but store the whole value in every checkpoint."""

    __slots__ = ("delta",)

    def __init__(self, delta: DeltaChannel[Any]) -> None:
        super().__init__(delta.typ, delta.key)
        self.delta = delta

    @property
    def ValueType(self) -> Any:
        return self.delta.ValueType

    @property
    def UpdateType(self) -> Any:
        return self.delta.UpdateType

    def copy(self) -> Self:
        return self.__class__(self.delta.copy())

    def checkpoint(self) -> Any:
        return self.delta.get() if self.delta.is_available() else MISSING

    def from_checkpoint(self, checkpoint: Any) -> Self:
        # DeltaChannel reads a plain value as a full value, as for migrated threads.
        return self.__class__(self.delta.from_checkpoint(checkpoint))

    def update(self, values: Sequence[Any]) -> bool:
        return self.delta.update(values)

    def get(self) -> Any:
        return self.delta.get()

    def is_available(self) -> bool:
        return self.delta.is_available()


def checkpointed_delta_channels(
    channels: Mapping[str, Any],
) -> dict[str, Any] | None:
    """Return the channels with each ``DeltaChannel`` replaced, or ``None`` when there is none."""
    if not any(isinstance(channel, DeltaChannel) for channel in channels.values()):
        return None
    return {
        name: CheckpointedDeltaChannel(channel) if isinstance(channel, DeltaChannel) else channel
        for name, channel in channels.items()
    }
