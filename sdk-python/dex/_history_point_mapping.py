# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from collections.abc import Iterable
from datetime import datetime
from uuid import uuid4

from dex.dexpb import dex_pb2 as pb
from dex.flow_options import (
    CloneForkFlowOptions,
    CloneForkFlowResult,
    IdReusePolicy,
    SubFlowResetPolicy,
    TimeTravelOptions,
    TimeTravelStepMethod,
    TimeTravelType,
    _validate_history_point,
)

_RESET_TYPES = {
    TimeTravelType.BEGINNING: pb.FLOW_RESET_TYPE_BEGINNING,
    TimeTravelType.HISTORY_EVENT_TIME: pb.FLOW_RESET_TYPE_HISTORY_EVENT_TIME,
    TimeTravelType.STEP_TYPE: pb.FLOW_RESET_TYPE_STEP_TYPE,
    TimeTravelType.STEP_EXECUTION_ID: pb.FLOW_RESET_TYPE_STEP_EXECUTION_ID,
    TimeTravelType.EVENT_ID: pb.FLOW_RESET_TYPE_EVENT_ID,
}

_CLONE_FORK_POINT_TYPES = {
    TimeTravelType.CURRENT: pb.FLOW_CLONE_FORK_POINT_TYPE_CURRENT,
    TimeTravelType.BEGINNING: pb.FLOW_CLONE_FORK_POINT_TYPE_BEGINNING,
    TimeTravelType.HISTORY_EVENT_TIME: pb.FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME,
    TimeTravelType.STEP_TYPE: pb.FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE,
    TimeTravelType.STEP_EXECUTION_ID: pb.FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID,
    TimeTravelType.EVENT_ID: pb.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID,
}

_STEP_METHODS = {
    TimeTravelStepMethod.WAIT_FOR: pb.FLOW_RESET_STEP_METHOD_WAIT_FOR,
    TimeTravelStepMethod.EXECUTE: pb.FLOW_RESET_STEP_METHOD_EXECUTE,
}

_SUB_FLOW_RESET_POLICIES = {
    SubFlowResetPolicy.DEFAULT: pb.SUB_FLOW_RESET_POLICY_UNSPECIFIED,
    SubFlowResetPolicy.ALWAYS_RESTART: pb.SUB_FLOW_RESET_POLICY_ALWAYS_RESTART,
    SubFlowResetPolicy.ALWAYS_FAIL: pb.SUB_FLOW_RESET_POLICY_ALWAYS_FAIL,
    SubFlowResetPolicy.ALWAYS_REJECT: pb.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT,
    SubFlowResetPolicy.ATTACH_ELSE_RESTART: pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART,
    SubFlowResetPolicy.ATTACH_ELSE_FAIL: pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
    SubFlowResetPolicy.ATTACH_ELSE_REJECT: pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT,
}

_ID_REUSE_POLICIES = {
    IdReusePolicy.DEFAULT: pb.ID_REUSE_POLICY_UNSPECIFIED,
    IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED: (
        pb.ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY
    ),
    IdReusePolicy.ALLOW_IF_NOT_RUNNING: pb.ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING,
    IdReusePolicy.ALLOW_TERMINATE_IF_RUNNING: (
        pb.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
    ),
    IdReusePolicy.DISALLOW: pb.ID_REUSE_POLICY_DISALLOW_REUSE,
}


def map_time_travel_request(
    flow_id: str, options: TimeTravelOptions
) -> pb.ResetFlowRequest:
    """Map time travel options; only the event ID selector is validated, as before."""
    if options.type is TimeTravelType.CURRENT:
        raise ValueError("time travel type CURRENT is valid only for clone_fork_flow")
    if options.type is TimeTravelType.EVENT_ID or options.event_id is not None:
        _validate_history_point(
            "time travel",
            options.type,
            options.history_event_time,
            options.step_type,
            options.step_execution_id,
            options.step_method,
            options.event_id,
        )
    request = pb.ResetFlowRequest(
        flow_id=flow_id,
        reset_type=_RESET_TYPES[options.type],
        reason=options.reason or "",
        skip_writes_reapply=options.skip_writes_reapply,
        sub_flow_reset_policy=_SUB_FLOW_RESET_POLICIES[options.sub_flow_reset_policy],
    )
    if options.history_event_time is not None:
        request.history_event_time = options.history_event_time.isoformat()
    if options.step_type is not None:
        request.step_type = options.step_type
    if options.step_execution_id is not None:
        request.step_execution_id = options.step_execution_id
    if options.step_method is not None:
        request.step_method = _STEP_METHODS[options.step_method]
    if options.event_id is not None:
        request.event_id = options.event_id
    return request


def map_clone_fork_flow_request(
    source_flow_id: str,
    flow_id: str,
    options: CloneForkFlowOptions,
    flow_config_override: pb.FlowConfig | None,
    attributes: Iterable[pb.AttributeWrite],
    timeout_seconds: int,
) -> pb.CloneForkFlowRequest:
    request = pb.CloneForkFlowRequest(
        source_flow_id=source_flow_id,
        source_run_id=options.source_run_id,
        flow_id=flow_id,
        request_id=options.request_id or str(uuid4()),
        id_reuse_policy=_ID_REUSE_POLICIES[options.id_reuse_policy],
        flow_timeout_seconds=timeout_seconds,
        reason=options.reason or "",
        point_type=_CLONE_FORK_POINT_TYPES[options.type],
        history_event_time=_format_history_event_time(options.history_event_time),
        step_type=options.step_type or "",
        step_execution_id=options.step_execution_id or "",
        event_id=options.event_id or 0,
        run_attempt=options.source_run_attempt,
        sub_flow_reset_policy=_SUB_FLOW_RESET_POLICIES[options.sub_flow_reset_policy],
    )
    if options.ignore_already_started:
        request.flow_already_started_options.ignore_already_started_error = True
    if options.step_method is not None:
        request.step_method = _STEP_METHODS[options.step_method]
    if flow_config_override is not None:
        request.flow_config_override.CopyFrom(flow_config_override)
    request.attributes.extend(attributes)
    return request


def clone_fork_flow_result_from_proto(
    response: pb.CloneForkFlowResponse,
) -> CloneForkFlowResult:
    if not response.run_id:
        raise RuntimeError("CloneForkFlow response has no run ID")
    return CloneForkFlowResult(
        run_id=response.run_id,
        source_run_id=response.source_run_id,
        source_run_attempt=response.source_run_attempt,
        source_event_id=response.source_event_id,
    )


def _format_history_event_time(history_event_time: datetime | None) -> str:
    return "" if history_event_time is None else history_event_time.isoformat()
