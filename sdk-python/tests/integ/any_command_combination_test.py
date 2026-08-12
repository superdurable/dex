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

from .any_combination_fail_flow import AnyCombinationFailFlow


def compile_state_api_failure(client: Client) -> None:
    options = StartFlowOptions(timeout=timedelta(seconds=10))
    client.start_flow(
        AnyCombinationFailFlow(),
        "any-combination",
        0,
        options,
    )
    result: int = client.wait_for_flow("any-combination").single_output(int)
    del result
