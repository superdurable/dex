# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import logging
import threading
from typing import Any, Literal, Sequence

from dex._invocation_context import InvocationContext
from dex.attribute import AttributeLock, AttributeMap, AttributeMapLoad
from dex.flow import Registry, _RegisteredFlow, _RegisteredRPC, _RegisteredStep
from dex.runtime_errors import (
    UnlockedAttributeAccess,
    UnlockedAttributeReadModifyWriteError,
)

_LOGGER = logging.getLogger(__name__)

UNLOCKED_READ_MODIFY_WRITE_WARNING = "unlocked Attribute read-modify-write"


class UnlockedAttributeReadModifyWriteCheck:
    """Report Attributes that one invocation read and wrote without a lock.

    One instance belongs to one Worker; its methods are safe across handler threads.
    """

    def __init__(self, fails_invocation: bool) -> None:
        self._fails_invocation = fails_invocation
        self._lock = threading.Lock()
        # Deduplicates warnings by Flow type, handler, and Attribute name.
        self._reported_attributes: set[tuple[str, str, str]] = set()
        self._detection_count = 0

    @property
    def detection_count(self) -> int:
        with self._lock:
            return self._detection_count

    def check_step(
        self,
        flow: _RegisteredFlow,
        step: _RegisteredStep,
        method: Literal["wait_for", "execute"],
        context: InvocationContext,
    ) -> None:
        candidates = context._attribute_read_modify_write_candidates()
        if not candidates:
            return
        options = step.step.get_step_options()
        locks: Sequence[AttributeLock] = ()
        map_loads: Sequence[AttributeMap[Any]] = ()
        instance_loads: Sequence[AttributeMapLoad] = ()
        if options is not None and method == "wait_for":
            locks = options.wait_for_lock_attributes
            map_loads = options.wait_for_load_attribute_maps
            instance_loads = options.wait_for_load_attribute_map_instances
        elif options is not None:
            locks = options.execute_lock_attributes
            map_loads = options.execute_load_attribute_maps
            instance_loads = options.execute_load_attribute_map_instances
        self._report(
            flow.name,
            step.name,
            method,
            None,
            context,
            frozenset(Registry._physical_lock(lock) for lock in locks),
            frozenset(
                [f"{attribute_map.name}/" for attribute_map in map_loads]
                + [load.physical_name for load in instance_loads]
            ),
        )

    def check_rpc(
        self,
        flow: _RegisteredFlow,
        rpc: _RegisteredRPC,
        context: InvocationContext,
    ) -> None:
        if not context._attribute_read_modify_write_candidates():
            return
        self._report(
            flow.name,
            None,
            "rpc",
            rpc.name,
            context,
            frozenset(rpc.locks),
            frozenset(rpc.load_attribute_map_instances),
        )

    def _report(
        self,
        flow_type: str,
        step_type: str | None,
        method: Literal["wait_for", "execute", "rpc"],
        rpc_name: str | None,
        context: InvocationContext,
        locked_keys: frozenset[str],
        registered_map_loads: frozenset[str],
    ) -> None:
        unlocked = [
            UnlockedAttributeAccess(candidate.attribute_name, candidate.instance)
            for candidate in context._attribute_read_modify_write_candidates()
            if candidate.physical_name not in locked_keys
            # A per-call load may come with a per-call lock that the Worker cannot see.
            and (
                candidate.instance is None
                or f"{candidate.attribute_name}/" in registered_map_loads
                or candidate.physical_name in registered_map_loads
            )
        ]
        if not unlocked:
            return
        handler_name = f"rpc.{rpc_name}" if method == "rpc" else f"{step_type}.{method}"
        with self._lock:
            self._detection_count += len(unlocked)
            first_reports = [
                access
                for access in unlocked
                if self._mark_reported(flow_type, handler_name, access.attribute_name)
            ]
        if self._fails_invocation:
            raise UnlockedAttributeReadModifyWriteError(
                flow_type, step_type, method, rpc_name, unlocked
            )
        for access in first_reports:
            _LOGGER.warning(
                "%s: another Step or RPC can overwrite this update; lock the "
                "Attribute or call declare_single_writer "
                "(flow_type=%s handler=%s attribute=%s instance=%s)",
                UNLOCKED_READ_MODIFY_WRITE_WARNING,
                flow_type,
                handler_name,
                access.attribute_name,
                access.instance,
            )

    def _mark_reported(
        self, flow_type: str, handler_name: str, attribute_name: str
    ) -> bool:
        report_key = (flow_type, handler_name, attribute_name)
        if report_key in self._reported_attributes:
            return False
        self._reported_attributes.add(report_key)
        return True
