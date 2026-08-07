// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import { type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileMemoReplacement(client: Client): Promise<void> {
  const flow = flows.RPC;
  await client.startFlow(flow, "rpc-cache", 0);
  await client.invokeRPC(flow.setData, "rpc-cache", "value");
  const data: string | undefined = await client.invokeRPC(flow.getData, "rpc-cache");
  await client.invokeRPC(flow.setKeyword, "rpc-cache", "keyword");
  const keyword: string | undefined = await client.invokeRPC(flow.getKeyword, "rpc-cache");
  const result: number = await client.invokeRPC(
    flow.functionOne,
    "rpc-cache",
    "input",
  );
  void data;
  void keyword;
  void result;
}
