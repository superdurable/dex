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

from .state_recovery_flow import StateRecoveryFlow
from .state_recovery_no_wait_flow import StateRecoveryNoWaitFlow


def compile_wait_and_execute_recovery(client: Client) -> None:
    client.start_flow(StateRecoveryFlow(), "state-recovery", 1)
    output: int = client.wait_for_flow("state-recovery").single_output(int)
    del output


def compile_execute_only_recovery(client: Client) -> None:
    client.start_flow(
        StateRecoveryNoWaitFlow(),
        "state-recovery-no-wait",
        1,
    )
    output: int = client.wait_for_flow("state-recovery-no-wait").single_output(int)
    del output
