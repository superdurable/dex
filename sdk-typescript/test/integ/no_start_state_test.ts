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

export async function compileNoStartStep(client: Client): Promise<void> {
  const flow = flows.NO_START;
  await client.startFlow(flow, "no-start", undefined);
  const output: number = await client.invokeRPC(flow.invoke, "no-start", "input");
  void output;
}

export async function compileNoStep(client: Client): Promise<void> {
  const flow = flows.NO_STATE;
  await client.startFlow(flow, "no-step", undefined);
  const output: number = await client.invokeRPC(flow.increaseCounter, "no-step");
  await client.stopFlow("no-step");
  void output;
}

export async function compileDeadEnd(client: Client): Promise<void> {
  const flow = flows.DEAD_END;
  await client.startFlow(flow, "dead-end", undefined);
  const size: number = await client.invokeRPC(flow.publishInternal, "dead-end");
  void size;
}
