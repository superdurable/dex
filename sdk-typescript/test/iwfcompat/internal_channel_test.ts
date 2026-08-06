// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

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
