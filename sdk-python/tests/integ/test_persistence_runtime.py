# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta

import pytest

from dex import (
    Attribute,
    AttributeMatch,
    StartFlowOptions,
    WaitForAttributeOptions,
    WaitHandlerTimeoutError,
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
        flow_id = unique_id("persistence")
        environment.client.start_flow(flow, flow_id, "input", options)
        assert (
            environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str)
            == "input"
        )


def test_set_indexed_attributes() -> None:
    flow = SetAttributesFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("set-indexed-attributes")
        environment.client.start_flow(flow, flow_id, "start")
        environment.client.invoke_rpc(flow.set_indexed, flow_id)
        environment.client.invoke_rpc(flow.complete, flow_id)
        assert (
            environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str)
            == "test-result"
        )


def test_set_data_attributes() -> None:
    flow = SetAttributesFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("set-data-attributes")
        environment.client.start_flow(flow, flow_id, "start")
        with pytest.raises(WaitHandlerTimeoutError):
            environment.client.wait_for_attribute_match(
                flow_id,
                flow.data,
                AttributeMatch.equal_to("never"),
                WaitForAttributeOptions(f"{flow_id}-never", timedelta(seconds=1)),
            )
        with ThreadPoolExecutor(max_workers=1) as executor:
            waiting = executor.submit(
                environment.client.wait_for_attribute_match,
                flow_id,
                flow.data,
                AttributeMatch.equal_to("query-start"),
                WaitForAttributeOptions(f"{flow_id}-data", WAIT_TIMEOUT),
            )
            environment.client.invoke_rpc(flow.set_data, flow_id, "query-start")
            assert waiting.result(timeout=WAIT_TIMEOUT.total_seconds()) == "query-start"
        with ThreadPoolExecutor(max_workers=1) as executor:
            waiting = executor.submit(
                environment.client.wait_for_attribute_match,
                flow_id,
                flow.data_map,
                "one",
                AttributeMatch.equal_to("mapped-value"),
                WaitForAttributeOptions(f"{flow_id}-map", WAIT_TIMEOUT),
            )
            environment.client.invoke_rpc(flow.set_map_one, flow_id, "mapped-value")
            assert (
                waiting.result(timeout=WAIT_TIMEOUT.total_seconds()) == "mapped-value"
            )
        environment.client.invoke_rpc(flow.set_integer, flow_id, 3)
        assert (
            environment.client.wait_for_attribute_match(
                flow_id,
                flow.integer,
                AttributeMatch.greater_than(0),
                WaitForAttributeOptions(f"{flow_id}-integer", WAIT_TIMEOUT),
            )
            == 3
        )
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            environment.client.wait_for_attribute_match(
                flow_id,
                flow.model,
                AttributeMatch.equal_to(ModelInput(value=8)),
                WaitForAttributeOptions(f"{flow_id}-model", WAIT_TIMEOUT),
            )
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            environment.client.wait_for_attribute_match(
                flow_id,
                Attribute("bytes", bytes),
                AttributeMatch.equal_to(b"value"),
                WaitForAttributeOptions(f"{flow_id}-bytes", WAIT_TIMEOUT),
            )
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            environment.client.wait_for_attribute_match(
                flow_id,
                Attribute("null", type(None)),
                AttributeMatch.equal_to(None),
                WaitForAttributeOptions(f"{flow_id}-null", WAIT_TIMEOUT),
            )
        environment.client.invoke_rpc(flow.set_model, flow_id, ModelInput(value=7))
        environment.client.invoke_rpc(flow.complete, flow_id)
        assert (
            environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(str)
            == "test-result"
        )
