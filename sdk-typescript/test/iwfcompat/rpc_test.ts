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

export async function compileLocking(client: Client): Promise<void> {
  const flow = flows.NO_STATE;
  await client.startFlow(flow, "rpc-lock", undefined);
  const first: number = await client.invokeRPC(flow.increaseCounter, "rpc-lock");
  const second: number = await client.invokeRPC(flow.getCounter, "rpc-lock");
  void first;
  void second;
}

export async function compileFunctionsAndProcedures(client: Client): Promise<void> {
  const flow = flows.RPC;
  await client.startFlow(flow, "rpc", 0);
  await client.invokeRPC(flow.noPersistence, "rpc");
  const one: number = await client.invokeRPC(flow.functionOne, "rpc", "input");
  const zero: number = await client.invokeRPC(flow.functionZero, "rpc");
  await client.invokeRPC(flow.procedureOne, "rpc", "input");
  await client.invokeRPC(flow.procedureZero, "rpc");
  const readOnly: number = await client.invokeRPC(flow.readOnly, "rpc", "input");
  await client.invokeRPC(flow.setData, "rpc", "value");
  const data: string = await client.invokeRPC(flow.getData, "rpc");
  await client.invokeRPC(flow.setKeyword, "rpc", "value");
  const keyword: string = await client.invokeRPC(flow.getKeyword, "rpc");
  void one;
  void zero;
  void readOnly;
  void data;
  void keyword;
}

export async function compileRpcErrorAndChannelSize(client: Client): Promise<void> {
  const ignored: number = await client.invokeRPC(
    flows.NO_STATE.fail,
    "rpc-error",
    "error",
  );
  const published: number = await client.invokeRPC(
    flows.DEAD_END.publishInternal,
    "channel-size",
  );
  const size: number = await client.invokeRPC(
    flows.DEAD_END.signalSize,
    "channel-size",
  );
  void ignored;
  void published;
  void size;
}
