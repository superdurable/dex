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

export async function compileExecuteOnlySteps(client: Client): Promise<void> {
  await client.startFlow(flows.EXECUTE_ONLY, "execute-only", 0);
  const output: number = await client.waitForFlow("execute-only", doubleCodec);
  void output;
}

export async function compileMixedWaitStyles(client: Client): Promise<void> {
  await client.startFlow(flows.MIXED_WAIT, "mixed-wait", 0);
  const output: number = await client.waitForFlow("mixed-wait", doubleCodec);
  void output;
}
