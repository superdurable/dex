// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileWaitAndExecuteRecovery(client: Client): Promise<void> {
  await client.startFlow(flows.STATE_RECOVERY, "state-recovery", 1);
  const output: number = await client.waitForFlow("state-recovery", doubleCodec);
  void output;
}

export async function compileExecuteOnlyRecovery(client: Client): Promise<void> {
  await client.startFlow(
    flows.STATE_RECOVERY_NO_WAIT,
    "state-recovery-no-wait",
    1,
  );
  const output: number = await client.waitForFlow(
    "state-recovery-no-wait",
    doubleCodec,
  );
  void output;
}
