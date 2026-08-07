// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

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
