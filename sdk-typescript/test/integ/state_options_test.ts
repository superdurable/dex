// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { stringCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileTimeoutRetryDurabilityAndLocks(
  client: Client,
): Promise<void> {
  await client.startFlow(flows.STATE_OPTIONS, "state-options", undefined);
  const output: string = await client.waitForFlow("state-options", stringCodec);
  void output;
}
