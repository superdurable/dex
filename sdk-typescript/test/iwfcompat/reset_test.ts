// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { ResetType, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileLockingRpcReapply(client: Client): Promise<void> {
  const flow = flows.RPC_LOCKING;
  await client.startFlow(flow, "reset-locking", undefined);
  await client.invokeRPC(flow.withLocking, "reset-locking");
  await client.invokeRPC(flow.withAttributeMapLock, "reset-locking");
  const runId: string = await client.resetFlow("reset-locking", {
    type: ResetType.BEGINNING,
    reason: "replay locking RPC",
    skipLockingRpcReapply: false,
  });
  void runId;
}

export async function compileSkipRpcAndChannelReapply(client: Client): Promise<void> {
  const runId: string = await client.resetFlow("reset-locking", {
    type: ResetType.STEP_TYPE,
    stepType: "LockWaitStep",
    skipLockingRpcReapply: true,
    skipChannelMessagesReapply: true,
  });
  void runId;
}
