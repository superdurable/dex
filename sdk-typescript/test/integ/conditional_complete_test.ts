// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileSignalChannel(client: Client): Promise<void> {
  const flow = flows.CONDITIONAL_COMPLETE;
  await client.startFlow(flow, "conditional-signal", true);
  await client.publish("conditional-signal", flow.signal, undefined);
  const output: number = await client.waitForFlow("conditional-signal", doubleCodec);
  void output;
}

export async function compileInternalChannel(client: Client): Promise<void> {
  const flow = flows.CONDITIONAL_COMPLETE;
  await client.startFlow(flow, "conditional-internal", false);
  await client.invokeRPC(flow.publishToInternalChannel, "conditional-internal");
}
