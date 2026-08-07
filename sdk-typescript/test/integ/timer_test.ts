// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { StepExecutionId, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileTimerAndStepWait(client: Client): Promise<void> {
  await client.startFlow(flows.TIMER, "timer", 1);
  await client.waitForStepCompletion("timer", StepExecutionId.of("TimerStep"), 10_000);
  await client.waitForFlow("timer");
}
