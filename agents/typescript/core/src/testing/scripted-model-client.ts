// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


// The scripted model client lives in the stepwise entry point; this path keeps the 0.1.0 module.
export {
  ScriptedModelClient,
  scriptedModelApi,
  scriptedModelProvider,
  type ScriptedModelReply,
  type ScriptedModelResponder,
  type ScriptedToolCall,
} from "../stepwise/scripted-model-client.js";
