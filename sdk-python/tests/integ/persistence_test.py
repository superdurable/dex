# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import AttributeMatch, Client, StartFlowOptions, WaitForAttributeOptions

from .basic_persistence_flow import BasicPersistenceFlow
from .set_attributes_flow import SetAttributesFlow


def compile_persistence_reads(client: Client) -> None:
    flow = BasicPersistenceFlow()
    options = (
        StartFlowOptions()
        .with_attribute(flow.initial, "initial")
        .with_attribute(flow.data_map, "one", "initial-map")
    )
    client.start_flow(flow, "persistence", "input", options)
    output: str = client.wait_for_flow("persistence").single_output(str)
    del output


def compile_persistence_writes(client: Client) -> None:
    flow = SetAttributesFlow()
    client.start_flow(flow, "set-attributes", "input")
    client.invoke_rpc(flow.set_data, "set-attributes", "value")
    client.invoke_rpc(flow.set_map_one, "set-attributes", "value")
    client.invoke_rpc(flow.set_indexed, "set-attributes")
    matched_data: str = client.wait_for_attribute_match(
        "set-attributes",
        flow.data,
        AttributeMatch.equal_to("value"),
        WaitForAttributeOptions("wait-set-attributes-data", timedelta(seconds=30)),
    )
    matched_map: str = client.wait_for_attribute_match(
        "set-attributes",
        flow.data_map,
        "one",
        AttributeMatch.equal_to("value"),
        WaitForAttributeOptions("wait-set-attributes-map", timedelta(seconds=30)),
    )
    client.invoke_rpc(flow.complete, "set-attributes")
    output: str = client.wait_for_flow("set-attributes").single_output(str)
    del matched_data, matched_map, output
