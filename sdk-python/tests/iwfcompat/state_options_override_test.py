# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_movement_options_override(client: Client) -> None:
    client.start_flow(
        iwf_flows.STATE_OPTIONS_OVERRIDE,
        "options-override",
        "input",
    )
    output: str = client.wait_for_flow("options-override", str)
    del output
