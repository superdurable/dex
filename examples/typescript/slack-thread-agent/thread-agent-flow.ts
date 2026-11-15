/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// The agent session Flow that each Slack thread drives: one session per thread.

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import {
  ScriptedModelClient,
  createBuiltinAgentSessionFlow,
  type BuiltinLoopAgentRunner,
  type ModelClient,
} from "@superdurable/dex-agents/builtin-loop";

export type ThreadAgentFlow = AgentSessionFlow<BuiltinLoopAgentRunner>;

export const echoReplyPrefix = "You said: ";

/** Builds the session Flow on the built-in agent loop. Pass a real ModelClient in production. */
export function createThreadAgentFlow(model: ModelClient = createEchoModelClient()): ThreadAgentFlow {
  return createBuiltinAgentSessionFlow(
    {
      name: "thread-agent",
      instructions: "You answer the people in one Slack thread.",
      model,
    },
    "SlackThreadAgent",
  );
}

/** A scripted model that echoes the latest user message, so the example runs without a model key. */
export function createEchoModelClient(): ScriptedModelClient {
  return new ScriptedModelClient((request) => ({
    text: echoReplyPrefix + (ScriptedModelClient.userTexts(request).at(-1) ?? ""),
  }));
}
