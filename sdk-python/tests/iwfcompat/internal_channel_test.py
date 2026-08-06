# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client

from . import iwf_flows


def compile_basic_internal_channel(client: Client) -> None:
    client.start_flow(iwf_flows.BASIC_INTERNAL, "basic-internal", 1)
    output: int = client.wait_for_flow("basic-internal", int)
    del output


def compile_waiting_internal_channel(client: Client) -> None:
    flow = iwf_flows.WAITING_INTERNAL
    client.start_flow(flow, "waiting-internal", 1)
    client.publish("waiting-internal", flow.channel, 2, 3)
    output: int = client.wait_for_flow("waiting-internal", int)
    del output
