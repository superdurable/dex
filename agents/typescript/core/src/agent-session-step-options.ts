// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ExecuteFailure, StepRef, type AttributeMapLoad, type RetryPolicy, type StepOptions } from "@superdurable/dex";

import type { AgentSessionPersistence } from "./agent-session-persistence.js";
import { agentSessionStepTypes } from "./agent-session-step-types.js";

export const minuteMs = 60_000;
// CompactContext makes one model call, so it shares RunModel's timeouts and retries.
export const modelCallStepOptions = {
  executeMethodTimeoutMs: 15 * minuteMs,
  heartbeatTimeoutMs: 2 * minuteMs,
  executeRetry: { maximumAttempts: 3, totalDurationMs: 30 * minuteMs },
  executeFailure: ExecuteFailure.proceedTo(new StepRef(agentSessionStepTypes.recordTurnFailure)),
} as const satisfies StepOptions;
// Recovery Steps must eventually run; cap their backoff so a returning Worker picks them up quickly.
export const recoveryRetry: RetryPolicy = { maximumIntervalMs: 5_000 };
// An ASYNC FlowConfig could replay a committed result after a Server failover, re-running a write.
export const syncDurability = { waitForDurability: "sync", executeDurability: "sync" } as const;

/**
 * The archive chunks a Step reads on top of the whole `AgentMessages` window: exact instances, or every chunk
 * when the model input covers them all.
 */
export interface MessageChunkLoads {
  readonly isWholeMap: boolean;
  readonly instances: readonly AttributeMapLoad[];
}

/** Returns the options of a Step that reads model input and stores messages, with its chunk loads. */
export function messageStepOptions(
  persistence: AgentSessionPersistence,
  stepType: string,
  chunkLoads: MessageChunkLoads,
): StepOptions {
  const loads = {
    executeLoadAttributeMaps: chunkLoads.isWholeMap
      ? [persistence.agentMessages, persistence.agentToolCalls, persistence.agentMessageChunks]
      : [persistence.agentMessages, persistence.agentToolCalls],
    ...(chunkLoads.instances.length === 0 ? {} : { executeLoadAttributeMapInstances: chunkLoads.instances }),
  };
  switch (stepType) {
    case agentSessionStepTypes.runModel:
      return { ...syncDurability, ...modelCallStepOptions, ...loads };
    case agentSessionStepTypes.applyTurnControl:
      return { ...syncDurability, executeRetry: recoveryRetry, ...loads };
    default:
      throw new TypeError(`Step type ${stepType} does not read the model input`);
  }
}
