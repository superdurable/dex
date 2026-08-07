// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { StepExecutionId, TimerId, doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileSignalsAndTimerSkip(client: Client): Promise<void> {
  const flow = flows.SIGNAL;
  await client.startFlow(flow, "signal", 0);
  await client.publish("signal", flow.first, 1);
  await client.publish("signal", flow.second, 2);
  await client.publish("signal", flow.third, undefined);
  await client.publish("signal", flow.signalMap, "one", 5);
  await client.skipTimer(
    "signal",
    StepExecutionId.of("SignalCombinationStep"),
    TimerId.byConditionId("test-timer-id"),
  );
  const output: number = await client.waitForFlow("signal", doubleCodec);
  void output;
}
