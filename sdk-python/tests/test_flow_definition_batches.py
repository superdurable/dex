# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from datetime import datetime, timezone
from typing import cast

from dex import FlowDefinitionUploadOutcome
from dex._flow_definition_upload import (
    MAX_FLOW_DEFINITION_UPLOAD_CONTENT_BYTES,
    PreparedFlowDefinition,
    flow_definition_batches,
    upload_results_from_response,
)
from dex.dexpb import dex_pb2 as pb

MIB = 1024 * 1024


def definitions_with_sizes(*sizes: int) -> list[PreparedFlowDefinition]:
    return [
        PreparedFlowDefinition(
            flow_type=f"Flow{index}",
            content=b"x" * size,
            generated_at=datetime(2026, 11, 1, tzinfo=timezone.utc),
            origin=f"test {index}",
        )
        for index, size in enumerate(sizes)
    ]


def batch_sizes(definitions: list[PreparedFlowDefinition]) -> list[int]:
    return [len(batch) for batch in flow_definition_batches(definitions)]


def test_empty_input_has_no_batches() -> None:
    assert flow_definition_batches([]) == []


def test_count_limit_splits_at_256_items() -> None:
    assert batch_sizes(definitions_with_sizes(*([1] * 600))) == [256, 256, 88]


def test_content_limit_splits_before_exceeding_16_mib() -> None:
    assert batch_sizes(definitions_with_sizes(6 * MIB, 6 * MIB, 6 * MIB)) == [2, 1]
    exact = MAX_FLOW_DEFINITION_UPLOAD_CONTENT_BYTES - 1
    assert batch_sizes(definitions_with_sizes(exact, 1, 1)) == [2, 1]


def test_oversized_document_goes_alone() -> None:
    assert batch_sizes(definitions_with_sizes(1, 20 * MIB, 1)) == [1, 1, 1]


def test_batches_keep_order() -> None:
    definitions = definitions_with_sizes(10 * MIB, 10 * MIB, 1)
    flattened = [
        definition
        for batch in flow_definition_batches(definitions)
        for definition in batch
    ]
    assert flattened == definitions


def test_unknown_outcome_is_unspecified() -> None:
    response = pb.UploadFlowDefinitionsResponse(
        results=[
            pb.FlowDefinitionUploadResult(
                flow_type="A",
                outcome=pb.FLOW_DEFINITION_UPLOAD_OUTCOME_UNSPECIFIED,
            ),
            pb.FlowDefinitionUploadResult(
                flow_type="B",
                outcome=cast(pb.FlowDefinitionUploadOutcome, 99),
            ),
        ]
    )
    assert [result.outcome for result in upload_results_from_response(response)] == [
        FlowDefinitionUploadOutcome.UNSPECIFIED,
        FlowDefinitionUploadOutcome.UNSPECIFIED,
    ]
