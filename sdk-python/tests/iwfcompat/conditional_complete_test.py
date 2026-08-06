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

from .conditional_complete_flow import ConditionalCompleteFlow


def compile_signal_channel(client: Client) -> None:
    flow = ConditionalCompleteFlow()
    client.start_flow(flow, "conditional-signal", True)
    client.publish("conditional-signal", flow.signal, None)
    output: int = client.wait_for_flow("conditional-signal", int)
    del output


def compile_internal_channel(client: Client) -> None:
    flow = ConditionalCompleteFlow()
    client.start_flow(flow, "conditional-internal", False)
    client.invoke_rpc(
        flow.publish_to_internal_channel,
        "conditional-internal",
    )
