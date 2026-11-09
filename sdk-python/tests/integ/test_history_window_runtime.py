# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from typing import cast

import grpc
import pytest

from dex import DexServiceError, FlowConfig, StartFlowOptions
from dex.dexpb import dex_pb2 as pb

from .environment import DexDevTestEnvironment
from .flow_service_client import flow_service_client
from .no_state_flow import NoStateFlow
from .shared import unique_id


def _read_history_window_events(flow_id: str, run_id: str) -> int:
    response = flow_service_client().GetFlowState(
        pb.GetFlowStateRequest(flow_id=flow_id, run_id=run_id)
    )
    config = cast(pb.FlowConfig, response.flow_config)
    assert config.HasField("history_window_events")
    return config.history_window_events


def test_history_window_events_start_and_update() -> None:
    flow = NoStateFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("history-window")
        run_id = client.start_flow(
            flow,
            flow_id,
            None,
            StartFlowOptions(config_override=FlowConfig(history_window_events=500)),
        )
        assert _read_history_window_events(flow_id, run_id) == 500

        client.update_flow_config(
            flow_id,
            FlowConfig(history_window_events=2000),
        )
        assert _read_history_window_events(flow_id, run_id) == 2000

        client.update_flow_config(flow_id, FlowConfig(history_window_events=3000))
        assert _read_history_window_events(flow_id, run_id) == 3000

        with pytest.raises(DexServiceError) as rejected_update:
            client.update_flow_config(flow_id, FlowConfig(history_window_events=0))
        assert rejected_update.value.code == grpc.StatusCode.INVALID_ARGUMENT
        assert _read_history_window_events(flow_id, run_id) == 3000

        client.stop_flow(flow_id)


@pytest.mark.parametrize("history_window_events", [0, -1, 100_001])
def test_history_window_events_rejects_out_of_range_start(
    history_window_events: int,
) -> None:
    flow = NoStateFlow()
    with DexDevTestEnvironment(flow) as environment:
        with pytest.raises(DexServiceError) as rejected_start:
            environment.client.start_flow(
                flow,
                unique_id("history-window-invalid"),
                None,
                StartFlowOptions(
                    config_override=FlowConfig(
                        history_window_events=history_window_events
                    )
                ),
            )
        assert rejected_start.value.code == grpc.StatusCode.INVALID_ARGUMENT
