// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import { StepExecutionId, TimerId, doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileSignalsAndTimerSkip(client: Client): Promise<void> {
  const flow = flows.SIGNAL;
  await client.startFlow(flow, "signal", 0);
  await client.invokeRPC(flow.publishFirst, "signal", 1);
  await client.invokeRPC(flow.publishSecond, "signal", 2);
  await client.invokeRPC(flow.publishThird, "signal");
  await client.invokeRPC(flow.publishMapped, "signal", 5);
  await client.skipTimer(
    "signal",
    StepExecutionId.of("SignalCombinationStep"),
    TimerId.byConditionId("test-timer-id"),
  );
  const output: number = await client.waitForFlow("signal").then((result) => result.singleOutput(doubleCodec));
  void output;
}
