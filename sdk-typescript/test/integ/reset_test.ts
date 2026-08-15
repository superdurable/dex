// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import { TimeTravelType, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileLockingRpcReapply(client: Client): Promise<void> {
  const flow = flows.RPC_LOCKING;
  await client.startFlow(flow, "reset-locking", undefined);
  await client.invokeRPC(flow.withLocking, "reset-locking");
  await client.invokeRPC(flow.withAttributeMapLock, "reset-locking");
  const runId: string = await client.timeTravel("reset-locking", {
    type: TimeTravelType.BEGINNING,
    reason: "replay locking RPC",
    skipWritesReapply: false,
  });
  void runId;
}

export async function compileSkipWritesReapply(client: Client): Promise<void> {
  const runId: string = await client.timeTravel("reset-locking", {
    type: TimeTravelType.STEP_TYPE,
    stepType: "LockWaitStep",
    skipWritesReapply: true,
  });
  void runId;
}
