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

from .execute_only_flow import ExecuteOnlyFlow
from .mixed_wait_flow import MixedWaitFlow


def compile_execute_only_steps(client: Client) -> None:
    client.start_flow(ExecuteOnlyFlow(), "execute-only", 0)
    output: int = client.wait_for_flow("execute-only").single_output(int)
    del output


def compile_mixed_wait_styles(client: Client) -> None:
    client.start_flow(MixedWaitFlow(), "mixed-wait", 0)
    output: int = client.wait_for_flow("mixed-wait").single_output(int)
    del output
