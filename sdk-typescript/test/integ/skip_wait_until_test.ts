// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
