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
from typing import Literal

from dex._invocation_context import AttributeAccessKey, InvocationContext
from dex.flow import (
    _AttributeLockScope,
    _RegisteredFlow,
    _RegisteredRPC,
    _RegisteredStep,
)
from dex.runtime_errors import (
    UnlockedAttributeAccess,
    UnlockedAttributeReadModifyWriteError,
)

_LOGGER = logging.getLogger(__name__)

UNLOCKED_READ_MODIFY_WRITE_WARNING = "unlocked Attribute read-modify-write"

# Flow type, method, Step type, RPC name, and Attribute name; a tuple avoids label collisions.
_ReportKey = tuple[str, str, str | None, str | None, str]


class UnlockedAttributeReadModifyWriteCheck:
    """Report Attributes that one invocation read and wrote without a lock.

    One instance belongs to one Worker; its methods are safe across handler threads.
    """

    def __init__(self, fails_invocation: bool) -> None:
        self._fails_invocation = fails_invocation
        self._lock = threading.Lock()
        self._reported_attributes: set[_ReportKey] = set()
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
        scope = (
            step.wait_for_lock_scope
            if method == "wait_for"
            else step.execute_lock_scope
        )
        self._report(flow.name, step.name, method, None, candidates, scope)

    def check_rpc(
        self,
        flow: _RegisteredFlow,
        rpc: _RegisteredRPC,
        context: InvocationContext,
    ) -> None:
        candidates = context._attribute_read_modify_write_candidates()
        if not candidates:
            return
        scope = _AttributeLockScope(
            frozenset(rpc.locks), frozenset(rpc.load_attribute_map_instances)
        )
        self._report(flow.name, None, "rpc", rpc.name, candidates, scope)

    def _report(
        self,
        flow_type: str,
        step_type: str | None,
        method: Literal["wait_for", "execute", "rpc"],
        rpc_name: str | None,
        candidates: list[AttributeAccessKey],
        scope: _AttributeLockScope,
    ) -> None:
        unlocked = [
            UnlockedAttributeAccess(candidate.attribute_name, candidate.instance)
            for candidate in candidates
            if candidate.physical_name not in scope.lock_keys
            # A per-call load may come with a per-call lock that the Worker cannot see.
            and (
                candidate.instance is None
                or f"{candidate.attribute_name}/" in scope.map_load_keys
                or candidate.physical_name in scope.map_load_keys
            )
        ]
        if not unlocked:
            return
        with self._lock:
            self._detection_count += len(unlocked)
            if self._fails_invocation:
                first_reports: list[UnlockedAttributeAccess] = []
            else:
                first_reports = [
                    access
                    for access in unlocked
                    if self._mark_reported(
                        (flow_type, method, step_type, rpc_name, access.attribute_name)
                    )
                ]
        if self._fails_invocation:
            raise UnlockedAttributeReadModifyWriteError(
                flow_type, step_type, method, rpc_name, unlocked
            )
        handler_name = f"rpc.{rpc_name}" if method == "rpc" else f"{step_type}.{method}"
        for access in first_reports:
            try:
                _LOGGER.warning(
                    "%s: a concurrent invocation can overwrite this update; lock "
                    "the Attribute in the handler's registered options "
                    "(flow_type=%s handler=%s attribute=%s instance=%s)",
                    UNLOCKED_READ_MODIFY_WRITE_WARNING,
                    flow_type,
                    handler_name,
                    access.attribute_name,
                    access.instance,
                )
            except Exception:  # noqa: BLE001
                # A failing log handler must not fail a successful invocation.
                pass

    def _mark_reported(self, report_key: _ReportKey) -> bool:
        if report_key in self._reported_attributes:
            return False
        self._reported_attributes.add(report_key)
        return True
