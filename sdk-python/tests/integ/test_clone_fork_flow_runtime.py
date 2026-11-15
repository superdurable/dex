# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import time
from datetime import timedelta

from dex import (
    CloneForkFlowOptions,
    FlowStatus,
    StopFlowOptions,
    StopType,
    SubFlowResetPolicy,
    TimeTravelOptions,
    TimeTravelType,
)
from dex.dexpb import dex_pb2 as pb

from .clone_fork_flow import CloneForkSessionFlow
from .environment import DexDevTestEnvironment
from .flow_service_client import flow_service_client
from .shared import unique_id


def test_clone_fork_flow_copies_running_source_at_current_and_event_id() -> None:
    flow = CloneForkSessionFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        source_id = unique_id("clone-fork-source")
        source_run_id = client.start_flow(flow, source_id, "source")
        wait_event_id = await_wait_for_completed_event_id(source_id, source_run_id)

        current_id = unique_id("clone-fork-current")
        current = client.clone_fork_flow(
            flow,
            source_id,
            current_id,
            CloneForkFlowOptions(reason="branch at the current point").with_attribute(
                flow.label, "current"
            ),
        )
        assert current.run_id
        assert current.source_run_id == source_run_id
        assert current.source_run_attempt == 1
        assert current.source_event_id >= wait_event_id

        event_id = unique_id("clone-fork-event")
        by_event = client.clone_fork_flow(
            flow,
            source_id,
            event_id,
            CloneForkFlowOptions(
                type=TimeTravelType.EVENT_ID,
                event_id=wait_event_id,
                source_run_id=source_run_id,
            ),
        )
        assert by_event.source_run_id == source_run_id
        assert by_event.source_event_id == wait_event_id

        client.invoke_rpc(flow.release_session, current_id, "a")
        client.invoke_rpc(flow.release_session, event_id, "b")
        assert (
            client.wait_for_flow(current_id, timedelta(seconds=30)).single_output(str)
            == "current|a"
        )
        assert (
            client.wait_for_flow(event_id, timedelta(seconds=30)).single_output(str)
            == "source|b"
        )

        source = client.describe_flow(source_id)
        assert source.status is FlowStatus.RUNNING
        assert source.run_id == source_run_id
        client.stop_flow(source_id, StopFlowOptions(type=StopType.TERMINATE))


def test_time_travel_by_event_id_returns_to_the_waiting_point() -> None:
    flow = CloneForkSessionFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("time-travel-event-id")
        run_id = client.start_flow(flow, flow_id, "first")
        wait_event_id = await_wait_for_completed_event_id(flow_id, run_id)
        client.invoke_rpc(flow.release_session, flow_id, "a")
        client.wait_for_flow(flow_id, timedelta(seconds=30))

        client.time_travel(
            flow_id,
            TimeTravelOptions(
                type=TimeTravelType.EVENT_ID,
                event_id=wait_event_id,
                skip_writes_reapply=True,
                sub_flow_reset_policy=SubFlowResetPolicy.ATTACH_ELSE_REJECT,
                reason="answer again",
            ),
        )
        client.invoke_rpc(flow.release_session, flow_id, "b")
        assert (
            client.wait_for_flow(flow_id, timedelta(seconds=30)).single_output(str)
            == "first|b"
        )


def await_wait_for_completed_event_id(flow_id: str, run_id: str) -> int:
    service = flow_service_client()
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        response = service.GetHistoryEvents(
            pb.GetHistoryEventsRequest(
                flow_id=flow_id, run_id=run_id, estimate_page_size=100
            )
        )
        for event in response.events:
            if event.HasField("step_wait_for_completed"):
                return int(event.event_id)
        time.sleep(0.02)
    raise AssertionError(f"WaitFor of {flow_id} did not complete")
