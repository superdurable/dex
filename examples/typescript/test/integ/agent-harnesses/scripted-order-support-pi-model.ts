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

import type { Api, Model } from "@earendil-works/pi-ai";

/** A pi-ai Anthropic Messages model whose requests go to the scripted Messages API at `baseUrl`. */
export function createScriptedOrderSupportPiModel(baseUrl: string): Model<Api> {
  return {
    id: "claude-scripted",
    name: "claude-scripted",
    api: "anthropic-messages",
    provider: "anthropic",
    baseUrl,
    reasoning: false,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 200_000,
    maxTokens: 4_096,
  };
}
