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

from .rpc_flow import RpcFlow


def compile_memo_replacement(client: Client) -> None:
    flow = RpcFlow()
    client.start_flow(flow, "rpc-cache", 0)
    client.invoke_rpc(flow.set_data, "rpc-cache", "value")
    data: str = client.invoke_rpc(flow.get_data, "rpc-cache")
    client.invoke_rpc(flow.set_keyword, "rpc-cache", "keyword")
    keyword: str = client.invoke_rpc(flow.get_keyword, "rpc-cache")
    result: int = client.invoke_rpc(flow.function_one, "rpc-cache", "input")
    del data, keyword, result
