# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client, TimeTravelOptions, TimeTravelType

from .rpc_locking_flow import RpcLockingFlow


def compile_locking_rpc_reapply(client: Client) -> None:
    flow = RpcLockingFlow()
    client.start_flow(flow, "reset-locking", None)
    client.invoke_rpc(flow.with_locking, "reset-locking")
    client.invoke_rpc(flow.with_attribute_map_lock, "reset-locking")
    options = TimeTravelOptions(
        type=TimeTravelType.BEGINNING,
        reason="replay locking RPC",
        skip_writes_reapply=False,
    )
    run_id: str = client.time_travel("reset-locking", options)
    del run_id


def compile_skip_writes_reapply(client: Client) -> None:
    options = TimeTravelOptions(
        type=TimeTravelType.STEP_TYPE,
        step_type="LockWaitStep",
        skip_writes_reapply=True,
    )
    run_id: str = client.time_travel("reset-locking", options)
    del run_id
