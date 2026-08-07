// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { doubleCodec, type Client } from "../../src/index.js";

import * as flows from "./iwf_flows.js";

export async function compileStateApiFailure(client: Client): Promise<void> {
  await client.startFlow(flows.ANY_COMBINATION_FAIL, "any-combination", 0, {
    timeoutMs: 10_000,
  });
  const result: number = await client.waitForFlow("any-combination", doubleCodec);
  void result;
}
