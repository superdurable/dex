# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_memo_replacement(client: Client) -> None:
    flow = iwf_flows.RPC_MEMO_REPLACEMENT
    client.start_flow(flow, "rpc-cache", 0)
    client.invoke_rpc(flow.set_data, "rpc-cache", "value")
    data: str = client.invoke_rpc(flow.get_data, "rpc-cache")
    client.invoke_rpc(flow.set_keyword, "rpc-cache", "keyword")
    keyword: str = client.invoke_rpc(flow.get_keyword, "rpc-cache")
    result: int = client.invoke_rpc(flow.function_one, "rpc-cache", "input")
    del data, keyword, result
