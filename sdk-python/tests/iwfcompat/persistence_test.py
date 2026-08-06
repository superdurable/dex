# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import datetime

from dex import Client, InitialAttribute, StartFlowOptions

from . import iwf_flows


def compile_persistence_reads(client: Client) -> None:
    flow = iwf_flows.BASIC_PERSISTENCE
    options = StartFlowOptions(attributes=(InitialAttribute(flow.initial, "initial"),))
    client.start_flow(flow, "persistence", "input", options)
    data: str = client.get_attribute("persistence", flow.data)
    integer: int = client.get_attribute("persistence", flow.integer)
    datetime_value: datetime = client.get_attribute(
        "persistence",
        flow.datetime,
    )
    del data, integer, datetime_value


def compile_persistence_writes(client: Client) -> None:
    flow = iwf_flows.SET_ATTRIBUTES
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
