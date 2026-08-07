// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileBasicInternalChannel(client: Client): Promise<void> {
  await client.startFlow(flows.BASIC_INTERNAL, "basic-internal", 1);
  const output: number = await client.waitForFlow("basic-internal", doubleCodec);
  void output;
}

export async function compileWaitingInternalChannel(client: Client): Promise<void> {
  const flow = flows.WAITING_INTERNAL;
  await client.startFlow(flow, "waiting-internal", 1);
  await client.publish("waiting-internal", flow.channel, 2, 3);
  const output: number = await client.waitForFlow("waiting-internal", doubleCodec);
  void output;
}
