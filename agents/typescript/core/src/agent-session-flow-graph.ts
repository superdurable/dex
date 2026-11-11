// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionDocument } from "@superdurable/dex";

import sessionFlowGraphTemplate from "./agent-session-flow-graph.json" with { type: "json" };

/**
 * When the session graph template last changed: the `generatedAt` of every {@link sessionFlowDefinitionDocument}.
 *
 * The Dex Server replaces a stored definition only when the uploaded one has a later `generatedAt`. So this time
 * changes, to a later one, in the same change as the template, and stays the same otherwise. The conformance suite's
 * `generate.py` refuses a template change that keeps it. The Python core uses the same time.
 */
export const sessionFlowGraphGeneratedAt = "2026-11-10T07:43:04.000Z";

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

/**
 * Returns the session graph of one agent as a document for the Worker to upload.
 *
 * Pass it in `WorkerOptions.flowDefinitions`. The Worker uploads it to the Dex Server when it starts, and Dex Web draws
 * the agent's sessions and offers their Actions without `--flow-rendering-dir`. The upload needs server-zd 0.1.26 or
 * later. With an older server the Worker logs a warning and starts anyway; render the file with
 * {@link renderSessionFlowGraph} for such a server instead.
 *
 * The content is `renderSessionFlowGraph(options)`. Its `generatedAt` is the time the graph template last changed, not
 * the current time. Restarting a Worker therefore writes nothing, and upgrading this package replaces the graph of an
 * older release. Workers of either core upload the same document. The server keeps a stored graph whose `generatedAt`
 * is the same or later, so a new `actionPermission` alone does not replace it. To change only the permission, upload
 * the document once with `Client.uploadFlowDefinitions(documents, true)`.
 *
 * @example
 * ```ts
 * import { Registry, Worker } from "@superdurable/dex";
 * import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
 * import { sessionFlowDefinitionDocument } from "@superdurable/dex-agents";
 *
 * const flow = createBuiltinAgentSessionFlow(definition, "SupportAgent");
 * const worker = new Worker(new Registry([flow]), blobCache, {
 *   flowDefinitions: [
 *     sessionFlowDefinitionDocument({ flowType: "SupportAgent", actionPermission: "support-agent.operate" }),
 *   ],
 * });
 * await worker.start();
 * ```
 *
 * @param options - The Flow type and the Action permission. The Worker uploads the document only when it registers
 *   the Flow type.
 * @returns A new document with the Flow type, the rendered graph, and a new `Date` of the template's time.
 * @throws {TypeError} When `flowType` is blank, or when `actionPermission` is not lowercase letters, digits, dots, and
 *   hyphens that start with a letter.
 * @since 0.5.0
 */
export function sessionFlowDefinitionDocument(options: SessionFlowGraphOptions): FlowDefinitionDocument {
  return {
    flowType: options.flowType,
    content: renderSessionFlowGraph(options),
    generatedAt: new Date(sessionFlowGraphGeneratedAt),
  };
}
