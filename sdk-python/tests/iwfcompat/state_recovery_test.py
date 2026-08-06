# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_wait_and_execute_recovery(client: Client) -> None:
    client.start_flow(iwf_flows.STATE_RECOVERY, "state-recovery", 1)
    output: int = client.wait_for_flow("state-recovery", int)
    del output


def compile_execute_only_recovery(client: Client) -> None:
    client.start_flow(
        iwf_flows.STATE_RECOVERY_NO_WAIT,
        "state-recovery-no-wait",
        1,
    )
    output: int = client.wait_for_flow("state-recovery-no-wait", int)
    del output
