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
import {
  parseSessionTriggerBindingFlag,
  readSessionTriggerBindingsFile,
  resolveSessionTriggerBinding,
  type SessionConnectorTriggerBinding,
} from "./agent-session-trigger-bindings.js";

const usage =
  "usage: dex-agents-session-graph --flow-type TYPE --permission PERMISSION [--out FILE]\n" +
  "         [--trigger-binding CONNECTION=PACKAGE@VERSION:TRIGGER[#FACTORY]:BINDING ...] [--trigger-bindings FILE]\n";

function main(): number {
  let values: {
    "flow-type"?: string | undefined;
    permission?: string | undefined;
    out?: string | undefined;
    "trigger-binding"?: string[] | undefined;
    "trigger-bindings"?: string | undefined;
  };
  try {
    ({ values } = parseArgs({
      options: {
        "flow-type": { type: "string" },
        permission: { type: "string" },
        out: { type: "string" },
        "trigger-binding": { type: "string", multiple: true },
        "trigger-bindings": { type: "string" },
      },
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
    const connectorTriggerBindings: SessionConnectorTriggerBinding[] = [];
    for (const flag of values["trigger-binding"] ?? []) {
      connectorTriggerBindings.push(resolveSessionTriggerBinding(parseSessionTriggerBindingFlag(flag)));
    }
    const bindingsFile = values["trigger-bindings"];
    for (const spec of bindingsFile === undefined ? [] : readSessionTriggerBindingsFile(bindingsFile)) {
      connectorTriggerBindings.push(resolveSessionTriggerBinding(spec));
    }
    graph = renderSessionFlowGraph({ flowType, actionPermission, connectorTriggerBindings });
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
