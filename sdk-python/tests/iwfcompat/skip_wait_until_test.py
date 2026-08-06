# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_execute_only_steps(client: Client) -> None:
    client.start_flow(iwf_flows.EXECUTE_ONLY, "execute-only", 0)
    output: int = client.wait_for_flow("execute-only", int)
    del output


def compile_mixed_wait_styles(client: Client) -> None:
    client.start_flow(iwf_flows.MIXED_WAIT, "mixed-wait", 0)
    output: int = client.wait_for_flow("mixed-wait", int)
    del output
