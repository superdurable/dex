// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import sessionFlowGraphTemplate from "./agent-session-flow-graph.json" with { type: "json" };

const actionPermissionPattern = /^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/;

/** What an application chooses for the session Flow Definition Graph of one agent. */
export interface SessionFlowGraphOptions {
  /** The Flow type of the agent, as passed to `createAgentSessionFlow`. */
  readonly flowType: string;
  /** The permission every session Action requires, such as `support-agent.operate`. */
  readonly actionPermission: string;
}

/**
 * Renders the Flow Definition Graph 2.0 file of one agent's session Flow.
 *
 * `dexcli visualize` cannot read the session Flow: the Flow lives in this library, and the application chooses its
 * Flow type. This function fills both choices into the graph that ships with the package. Write the text to a file in
 * the directory that Dex Web reads with `--flow-rendering-dir`. Dex Web then draws the session Steps and offers the
 * `resolve_interrupt`, `answer_question`, `steer_user_message`, and `cancel_turn` Actions. Those Actions have no
 * condition, because each RPC checks current state itself.
 *
 * The Python core renders the same bytes, so a graph from either core serves a session on Workers of either language.
 * Render the file again after you upgrade this package. The `dex-agents-session-graph` command does the same from a
 * build or deployment script.
 *
 * @example
 * ```ts
 * import { writeFileSync } from "node:fs";
 * import { renderSessionFlowGraph } from "@superdurable/dex-agents";
 *
 * writeFileSync(
 *   "definitions/support-agent.json",
 *   renderSessionFlowGraph({ flowType: "SupportAgent", actionPermission: "support-agent.operate" }),
 * );
 * ```
 *
 * @param options - The Flow type and the Action permission.
 * @returns The graph as JSON text that ends with a newline.
 * @throws {TypeError} When `flowType` is blank, or when `actionPermission` is not lowercase letters, digits, dots, and
 *   hyphens that start with a letter.
 * @since 0.3.0
 */
export function renderSessionFlowGraph(options: SessionFlowGraphOptions): string {
  if (options.flowType.trim() === "") {
    throw new TypeError("flowType must not be empty");
  }
  if (!actionPermissionPattern.test(options.actionPermission)) {
    throw new TypeError(`actionPermission ${JSON.stringify(options.actionPermission)} is not a valid permission`);
  }
  const graph = structuredClone(sessionFlowGraphTemplate);
  graph.flow.name = options.flowType;
  for (const action of graph.v2.actions) {
    action.requiredPermission = options.actionPermission;
  }
  return `${JSON.stringify(graph, null, 2)}\n`;
}
