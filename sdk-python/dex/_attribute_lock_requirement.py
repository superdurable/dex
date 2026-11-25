# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from collections.abc import Collection
from typing import Literal
from urllib.parse import unquote

from dex._invocation_context import InvocationContext
from dex.attribute import Attribute, AttributeMap, _required_write_lock_key
from dex.channel import Channel, ChannelMap
from dex.flow import _RegisteredFlow, _RegisteredRPC, _RegisteredStep
from dex.runtime_errors import MissingAttributeLockAccess, MissingAttributeLockError


def check_step_required_locks(
    flow: _RegisteredFlow,
    step: _RegisteredStep,
    method: Literal["wait_for", "execute"],
    context: InvocationContext,
) -> None:
    """Raise MissingAttributeLockError when a Step method lacks a declared lock.

    Only the Step's registration-time locks count.
    """
    held_lock_keys = (
        step.wait_for_lock_keys if method == "wait_for" else step.execute_lock_keys
    )
    missing = _missing_lock_accesses(flow, held_lock_keys, context)
    if missing:
        raise MissingAttributeLockError(flow.name, step.name, method, None, missing)


def check_rpc_required_locks(
    flow: _RegisteredFlow,
    rpc: _RegisteredRPC,
    context: InvocationContext,
) -> None:
    """Raise MissingAttributeLockError when an RPC lacks a declared lock.

    Only ``@rpc(lock_attributes=...)`` counts; call-time locks never reach the Worker.
    """
    missing = _missing_lock_accesses(flow, rpc.locks, context)
    if missing:
        raise MissingAttributeLockError(flow.name, None, "rpc", rpc.name, missing)


def _missing_lock_accesses(
    flow: _RegisteredFlow,
    held_lock_keys: Collection[str],
    context: InvocationContext,
) -> list[MissingAttributeLockAccess]:
    missing: list[MissingAttributeLockAccess] = []
    for physical in context.attribute_writes:
        name, instance = _split_physical_name(physical)
        attribute = flow.persistence.get(name)
        if not isinstance(attribute, (Attribute, AttributeMap)):
            continue
        lock_key = _required_write_lock_key(attribute, physical)
        if lock_key is None or lock_key in held_lock_keys:
            continue
        is_own_lock = attribute.writes_require_lock is True
        missing.append(
            MissingAttributeLockAccess(
                kind=(
                    "AttributeMap"
                    if isinstance(attribute, AttributeMap)
                    else "Attribute"
                ),
                name=name,
                instance=instance,
                lock_attribute_name=name if is_own_lock else lock_key,
                lock_instance=instance if is_own_lock else None,
            )
        )
    published: set[str] = set()
    for publication in context.publications:
        physical = publication.channel_name
        if physical in published:
            continue
        published.add(physical)
        name, instance = _split_physical_name(physical)
        channel = flow.persistence.get(name)
        if not isinstance(channel, (Channel, ChannelMap)):
            continue
        lock_attribute = channel.publishes_require_lock
        if lock_attribute is None or lock_attribute.name in held_lock_keys:
            continue
        missing.append(
            MissingAttributeLockAccess(
                kind="ChannelMap" if isinstance(channel, ChannelMap) else "Channel",
                name=name,
                instance=instance,
                lock_attribute_name=lock_attribute.name,
            )
        )
    return missing


def _split_physical_name(physical: str) -> tuple[str, str | None]:
    """Return the definition name and the decoded map instance of a stored key."""
    name, separator, escaped_instance = physical.partition("/")
    if not separator:
        return name, None
    return name, unquote(escaped_instance)
