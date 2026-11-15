# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dex import Flow, FlowErrorType, FlowStatus, Registry, SubFlow
from dex._invocation_context import InvocationContext, InvocationMethod
from dex._value_mapper import ValueMapper
from dex.dexpb import dex_pb2 as pb


class ParentFlow(Flow[None]):
    pass


def _execute_context(*sub_flow_results: pb.FlowResult) -> InvocationContext:
    registry = Registry((ParentFlow(),))
    return InvocationContext(
        InvocationMethod.EXECUTE,
        registry._flow_by_type("ParentFlow"),
        pb.Context(),
        ValueMapper(registry.codec_registry),
        (),
        condition_results=pb.ConditionResults(sub_flow_results=sub_flow_results),
    )


def test_sub_flow_result_maps_abandoned_and_unrecognized_error_types() -> None:
    context = _execute_context(
        pb.FlowResult(
            flow_status=pb.FLOW_STATUS_FAILED,
            error_type=pb.FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED,
            error_message="abandoned by a Time Travel",
        ),
        pb.FlowResult(flow_status=pb.FLOW_STATUS_FAILED, error_type=99),  # type: ignore[arg-type]
    )

    abandoned = SubFlow.get_condition_results(context, 0)
    assert abandoned.status is FlowStatus.FAILED
    assert abandoned.error_type is FlowErrorType.SUB_FLOW_ABANDONED
    assert abandoned.error_message == "abandoned by a Time Travel"

    unrecognized = SubFlow.get_condition_results(context, 1)
    assert unrecognized.status is FlowStatus.FAILED
    assert unrecognized.error_type is None
