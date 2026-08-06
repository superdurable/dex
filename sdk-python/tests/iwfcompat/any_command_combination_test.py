# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import Client, StartFlowOptions

from . import iwf_flows


def compile_state_api_failure(client: Client) -> None:
    options = StartFlowOptions(timeout=timedelta(seconds=10))
    client.start_flow(
        iwf_flows.ANY_COMBINATION_FAIL,
        "any-combination",
        0,
        options,
    )
    result: int = client.wait_for_flow("any-combination", int)
    del result
