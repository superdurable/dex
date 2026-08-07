// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
