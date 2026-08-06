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
