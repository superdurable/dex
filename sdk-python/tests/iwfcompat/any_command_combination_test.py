# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
