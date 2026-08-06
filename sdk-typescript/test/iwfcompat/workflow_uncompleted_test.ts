// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { StopType, doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileWaitAndFlowTimeouts(client: Client): Promise<void> {
  await client.startFlow(flows.SIGNAL, "wait-timeout", 0, { timeoutMs: 1_000 });
  const output: number = await client.waitForFlow(
    "wait-timeout",
    doubleCodec,
    1,
  );
  void output;
}

export async function compileCancellationTerminationAndFailure(
  client: Client,
): Promise<void> {
  await client.stopFlow("cancel");
  await client.stopFlow("terminate", {
    type: StopType.TERMINATE,
    reason: "terminated",
  });
  await client.stopFlow("fail", {
    type: StopType.FAIL,
    reason: "failed by API",
  });
}

export async function compileWorkerFailureModes(client: Client): Promise<void> {
  await client.startFlow(flows.FORCE_FAIL, "force-fail", 0);
  await client.startFlow(flows.STATE_FAILURE, "state-failure", 0);
  await client.startFlow(flows.STATE_TIMEOUT, "state-timeout", 0);
  await client.startFlow(flows.EMPTY_DECISION, "empty-decision", 0);
}
