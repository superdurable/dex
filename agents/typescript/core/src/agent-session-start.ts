// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ActiveStepSearchMode, type Client, type StartFlowOptions } from "@superdurable/dex";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "./agent-framework-runner.js";
import type { AgentSessionFlow } from "./agent-session-flow.js";
import { validateSessionSettings } from "./agent-session-settings.js";
import type { AgentSessionStartInput } from "./agent-session-values.js";

/**
 * The search mode {@link startAgentSession} gives a new session.
 *
 * It indexes the Steps that wait, such as `AwaitToolApproval`, `AwaitQuestionAnswer`, and `AwaitUserMessage`, and
 * leaves out Steps that run straight away, such as `RunModel`. So a session's search entry changes when it enters or
 * leaves a wait, not on every Step.
 *
 * Pass it as `activeStepSearchMode` in `StartFlowOptions.configOverride` to `Client.startFlow`, in a SubFlow's
 * `configOverride`, or to `Client.updateFlowConfig` for a running session.
 *
 * The value is `ActiveStepSearchMode.DEFAULT` because the Dex SDK this core supports has no `WITH_WAIT_FOR` value.
 * It sends the unspecified mode, which the server stores on the Flow and treats as Steps with a wait.
 *
 * @since 0.3.0
 */
export const agentSessionActiveStepSearchMode: ActiveStepSearchMode = ActiveStepSearchMode.DEFAULT;

/**
 * Starts one agent session, searchable by the Steps it waits in.
 *
 * `ActiveStepTypes = "AwaitToolApproval"` then finds the session while a tool call waits for a person. Prefer this to
 * `Client.startFlow`, which leaves the session out of that search unless `configOverride` sets
 * {@link agentSessionActiveStepSearchMode}.
 *
 * Set `configOverride.activeStepSearchMode` to choose another mode. To leave a session out of the search, start it
 * with `Client.startFlow` and no mode: the Dex SDK this core supports cannot send the disabled mode. A session started
 * as a SubFlow inherits its parent's mode unless its `configOverride` sets one.
 *
 * @example
 * ```ts
 * await startAgentSession(client, flow, "session-42", { initialMessage: "Refund order 7" });
 * const page = await client.searchFlows('ActiveStepTypes = "AwaitToolApproval"', 50);
 * ```
 *
 * @param client - An open Client whose Registry contains `flow`.
 * @param flow - The registered agent session Flow.
 * @param sessionId - Flow ID of the new session.
 * @param input - The first user message, the session's settings, and where the caller runs.
 * @param options - Start options, such as a Request ID for an idempotent start. `configOverride` keeps every field
 * it sets.
 * @returns The server-assigned run ID.
 * @throws {TypeError | RangeError} From `validateSessionSettings`, before anything is sent.
 * @throws {FlowAlreadyStartedError} When the Flow ID is already in use.
 * @since 0.3.0
 */
export async function startAgentSession(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  sessionId: string,
  input: Partial<AgentSessionStartInput> = {},
  options: StartFlowOptions = {},
): Promise<string> {
  const sessionSettings = input.sessionSettings ?? null;
  if (sessionSettings !== null) {
    validateSessionSettings(sessionSettings);
  }
  return client.startFlow(
    flow,
    sessionId,
    { initialMessage: input.initialMessage ?? null, sessionSettings, startEnvironment: input.startEnvironment ?? null },
    {
      ...options,
      configOverride: {
        ...options.configOverride,
        activeStepSearchMode: options.configOverride?.activeStepSearchMode ?? agentSessionActiveStepSearchMode,
      },
    },
  );
}
