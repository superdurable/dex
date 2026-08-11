# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone

import pytest

from dex import (
    Attribute,
    FlowNotActiveError,
    FlowNotFoundError,
    LongPollTimeoutError,
    StartFlowOptions,
)

from .basic_persistence_flow import BasicPersistenceFlow
from .environment import DexDevTestEnvironment
from .set_attributes_flow import SetAttributesFlow
from .shared import ModelInput, unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_persistence_reads_and_step_execution_local() -> None:
    flow = BasicPersistenceFlow()
    options = (
        StartFlowOptions()
        .with_attribute(flow.initial, "initial")
        .with_attribute(flow.data_map, "one", "initial")
    )
    with DexDevTestEnvironment(flow) as environment:
        with pytest.raises(FlowNotFoundError):
            environment.client.get_attribute(unique_id("missing"), flow.data)
        flow_id = unique_id("persistence")
        environment.client.start_flow(flow, flow_id, "input", options)
        assert environment.client.wait_for_flow(flow_id, str, WAIT_TIMEOUT) == "input"
        assert environment.client.get_attribute(flow_id, flow.data) == "input"
        assert environment.client.get_attribute(flow_id, flow.initial) == "initial"
        assert environment.client.get_attribute(flow_id, flow.data_map, "one") is None
        assert environment.client.get_attribute(flow_id, flow.keyword) == "input"
        assert environment.client.get_attribute(flow_id, flow.integer) == 1
        assert environment.client.get_attribute(flow_id, flow.datetime) == datetime(
            2023, 4, 17, 21, 17, 49, tzinfo=timezone.utc
        )
        assert environment.client.get_attribute(flow_id, flow.model).value == 0
        with pytest.raises(FlowNotActiveError):
            environment.client.set_attribute(flow_id, flow.data, "closed")


def test_set_indexed_attributes() -> None:
    flow = SetAttributesFlow()
    keywords = ("keyword-1", "keyword-2")
    timestamp = datetime(
        2024,
        11,
        13,
        0,
        0,
        1,
        731455,
        tzinfo=timezone.utc,
    )
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("set-indexed-attributes")
        environment.client.start_flow(flow, flow_id, "start")
        environment.client.set_attribute(flow_id, flow.keyword, "keyword-1")
        environment.client.set_attribute(flow_id, flow.text, "text-1")
        environment.client.set_attribute(flow_id, flow.decimal, 1.0)
        environment.client.set_attribute(flow_id, flow.integer, 1)
        environment.client.set_attribute(flow_id, flow.bool, True)
        environment.client.set_attribute(flow_id, flow.keywords, keywords)
        environment.client.set_attribute(flow_id, flow.datetime, timestamp)
        environment.client.publish(flow_id, flow.proceed, None)
        assert (
            environment.client.wait_for_flow(flow_id, str, WAIT_TIMEOUT)
            == "test-result"
        )
        assert environment.client.get_attribute(flow_id, flow.keyword) == "keyword-1"
        assert environment.client.get_attribute(flow_id, flow.text) == "text-1"
        assert environment.client.get_attribute(flow_id, flow.decimal) == 1.0
        assert environment.client.get_attribute(flow_id, flow.integer) == 1
        assert environment.client.get_attribute(flow_id, flow.bool) is True
        assert environment.client.get_attribute(flow_id, flow.keywords) == keywords
        assert environment.client.get_attribute(flow_id, flow.datetime) == timestamp


def test_set_data_attributes() -> None:
    flow = SetAttributesFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("set-data-attributes")
        environment.client.start_flow(flow, flow_id, "start")
        with pytest.raises(LongPollTimeoutError):
            environment.client.wait_for_attribute_equal(
                flow_id, flow.data, "never", timedelta(seconds=1)
            )
        with ThreadPoolExecutor(max_workers=1) as executor:
            waiting = executor.submit(
                environment.client.wait_for_attribute_equal,
                flow_id,
                flow.data,
                "query-start",
                WAIT_TIMEOUT,
            )
            environment.client.set_attribute(flow_id, flow.data, "query-start")
            waiting.result(timeout=WAIT_TIMEOUT.total_seconds())
        with ThreadPoolExecutor(max_workers=1) as executor:
            waiting = executor.submit(
                environment.client.wait_for_attribute_map_equal,
                flow_id,
                flow.data_map,
                "one",
                "mapped-value",
                WAIT_TIMEOUT,
            )
            environment.client.set_attribute(
                flow_id, flow.data_map, "one", "mapped-value"
            )
            waiting.result(timeout=WAIT_TIMEOUT.total_seconds())
        with pytest.raises(ValueError, match="only scalar"):
            environment.client.wait_for_attribute_equal(
                flow_id, flow.model, ModelInput(value=8), WAIT_TIMEOUT
            )
        with pytest.raises(ValueError, match="only scalar"):
            environment.client.wait_for_attribute_equal(
                flow_id, Attribute("bytes", bytes), b"value", WAIT_TIMEOUT
            )
        with pytest.raises(ValueError, match="only scalar"):
            environment.client.wait_for_attribute_equal(
                flow_id, Attribute("null", type(None)), None, WAIT_TIMEOUT
            )
        environment.client.set_attribute(flow_id, flow.model, ModelInput(value=7))
        environment.client.publish(flow_id, flow.proceed, None)
        assert (
            environment.client.wait_for_flow(flow_id, str, WAIT_TIMEOUT)
            == "test-result"
        )
        assert environment.client.get_attribute(flow_id, flow.data) == "query-start"
        assert environment.client.get_attribute(flow_id, flow.data_map, "one") == (
            "mapped-value"
        )
        assert environment.client.get_attribute(flow_id, flow.model).value == 7
