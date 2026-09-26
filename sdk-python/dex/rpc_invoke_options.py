# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass

from dex.attribute import AttributeLock, AttributeMapLoad
from dex.channel import ChannelMapLoad


@dataclass(frozen=True)
class RPCInvokeOptions:
    """Add runtime-selected map instances to one RPC invocation.

    Dex unions these selections with the RPC decorator's fixed locks and state loads,
    then sorts and deduplicates their physical names. Locking and loading are
    independent. A read-modify-write handler must include the same AttributeMap
    instance in both collections.

    Attributes:
        lock_attribute_map_instances: AttributeMap instance locks for this call.
        load_attribute_map_instances: Exact AttributeMap instance snapshots.
        load_channel_map_instances: Exact ChannelMap pending-message snapshots.

    Examples:
        >>> options = RPCInvokeOptions(
        ...     lock_attribute_map_instances=(profiles.lock(partition),),
        ...     load_attribute_map_instances=(profiles.load(partition),),
        ... )
        >>> client.invoke_rpc(directory.upsert_customer_profile, flow_id, profile,
        ...                   options=options)
    """

    lock_attribute_map_instances: tuple[AttributeLock, ...] = ()
    load_attribute_map_instances: tuple[AttributeMapLoad, ...] = ()
    load_channel_map_instances: tuple[ChannelMapLoad, ...] = ()
