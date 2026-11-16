# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dex._utils import require_name
from dex._value_mapper import ValueMapper
from dex.dexpb import dex_pb2 as pb
from dex.flow_options import StopFlowOptions, StopType

_STOP_TYPES = {
    StopType.CANCEL: pb.STOP_TYPE_CANCEL,
    StopType.TERMINATE: pb.STOP_TYPE_TERMINATE,
    StopType.FAIL: pb.STOP_TYPE_FAIL,
    StopType.GRACEFUL_COMPLETE: pb.STOP_TYPE_GRACEFUL_COMPLETE,
    StopType.FORCE_COMPLETE: pb.STOP_TYPE_FORCE_COMPLETE,
}

_COMPLETE_STOP_TYPES = frozenset({StopType.GRACEFUL_COMPLETE, StopType.FORCE_COMPLETE})


def map_stop_flow_request(
    flow_id: str, options: StopFlowOptions, values: ValueMapper
) -> pb.StopFlowRequest:
    request = pb.StopFlowRequest(
        flow_id=require_name(flow_id),
        reason=options.reason or "",
        stop_type=_STOP_TYPES[options.type],
    )
    if options.output is None:
        return request
    if options.type not in _COMPLETE_STOP_TYPES:
        raise ValueError(
            "StopFlowOptions.output requires StopType.GRACEFUL_COMPLETE or "
            "StopType.FORCE_COMPLETE"
        )
    request.output.CopyFrom(values.encode_dynamic(options.output))
    return request
