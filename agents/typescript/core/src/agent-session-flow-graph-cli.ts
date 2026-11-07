#!/usr/bin/env node
// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { writeFileSync } from "node:fs";
import { parseArgs } from "node:util";

import { renderSessionFlowGraph } from "./agent-session-flow-graph.js";

const usage = "usage: dex-agents-session-graph --flow-type TYPE --permission PERMISSION [--out FILE]\n";

function main(): number {
  let values: { "flow-type"?: string | undefined; permission?: string | undefined; out?: string | undefined };
  try {
    ({ values } = parseArgs({
      options: { "flow-type": { type: "string" }, permission: { type: "string" }, out: { type: "string" } },
    }));
  } catch (error) {
    process.stderr.write(`${(error as Error).message}\n${usage}`);
    return 2;
  }
  const flowType = values["flow-type"];
  const actionPermission = values.permission;
  if (flowType === undefined || actionPermission === undefined) {
    process.stderr.write(usage);
    return 2;
  }
  let graph: string;
  try {
    graph = renderSessionFlowGraph({ flowType, actionPermission });
  } catch (error) {
    process.stderr.write(`${(error as Error).message}\n`);
    return 2;
  }
  if (values.out === undefined) {
    process.stdout.write(graph);
  } else {
    writeFileSync(values.out, graph);
  }
  return 0;
}

process.exitCode = main();
