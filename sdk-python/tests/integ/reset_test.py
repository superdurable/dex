# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client, ResetFlowOptions, ResetType

from .rpc_locking_flow import RpcLockingFlow


def compile_locking_rpc_reapply(client: Client) -> None:
    flow = RpcLockingFlow()
    client.start_flow(flow, "reset-locking", None)
    client.invoke_rpc(flow.with_locking, "reset-locking")
    client.invoke_rpc(flow.with_attribute_map_lock, "reset-locking")
    options = ResetFlowOptions(
        type=ResetType.BEGINNING,
        reason="replay locking RPC",
        skip_writes_reapply=False,
    )
    run_id: str = client.reset_flow("reset-locking", options)
    del run_id


def compile_skip_writes_reapply(client: Client) -> None:
    options = ResetFlowOptions(
        type=ResetType.STEP_TYPE,
        step_type="LockWaitStep",
        skip_writes_reapply=True,
    )
    run_id: str = client.reset_flow("reset-locking", options)
    del run_id
