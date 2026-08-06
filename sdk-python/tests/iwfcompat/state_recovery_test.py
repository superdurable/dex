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
