// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileMemoReplacement(client: Client): Promise<void> {
  const flow = flows.RPC_MEMO_REPLACEMENT;
  await client.startFlow(flow, "rpc-cache", 0);
  await client.invokeRPC(flow.setData, "rpc-cache", "value");
  const data: string = await client.invokeRPC(flow.getData, "rpc-cache");
  await client.invokeRPC(flow.setKeyword, "rpc-cache", "keyword");
  const keyword: string = await client.invokeRPC(flow.getKeyword, "rpc-cache");
  const result: number = await client.invokeRPC(
    flow.functionOne,
    "rpc-cache",
    "input",
  );
  void data;
  void keyword;
  void result;
}
