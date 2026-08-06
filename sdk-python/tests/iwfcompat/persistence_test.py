# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import datetime

from dex import Client, StartFlowOptions

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
    data: str = client.get_attribute("persistence", flow.data)
    integer: int = client.get_attribute("persistence", flow.integer)
    datetime_value: datetime = client.get_attribute(
        "persistence",
        flow.datetime,
    )
    del data, integer, datetime_value


def compile_persistence_writes(client: Client) -> None:
    flow = SetAttributesFlow()
    client.start_flow(flow, "set-attributes", "input")
    client.set_attribute("set-attributes", flow.data, "value")
    client.set_attribute("set-attributes", flow.data_map, "one", "value")
    client.set_attribute("set-attributes", flow.keyword, "keyword")
    client.set_attribute("set-attributes", flow.decimal, 1.5)
    client.set_attribute("set-attributes", flow.integer, 1)
    client.set_attribute("set-attributes", flow.bool, True)
    client.set_attribute("set-attributes", flow.keywords, ("one", "two"))
    output: str = client.wait_for_flow("set-attributes", str)
    del output
