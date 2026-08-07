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

import { Registry } from "@superdurable/dex";

import { cronScheduleFlow } from "./workflow/cron/cron-schedule-flow.js";
import { drainInternalChannelsFlow } from "./workflow/drainchannels/internal/drain-internal-channels-flow.js";
import { drainSignalChannelsFlow } from "./workflow/drainchannels/signal/drain-signal-channels-flow.js";
import { interruptibleExecutionFlow } from "./workflow/interruptible/interruptible-execution-flow.js";
import { manualInterventionFlow } from "./workflow/intervention/manual-intervention-flow.js";
import { parallelStatesWithAwaitFlow } from "./workflow/parallel/parallel-states-with-await-flow.js";
import { simpleParallelStatesFlow } from "./workflow/parallel/simple-parallel-states-flow.js";
import { parentFlowV2 } from "./workflow/parentchild/parent-flow-v2.js";
import { backoffPollingFlow } from "./workflow/polling/backoff-polling-flow.js";
import { simplePollingFlow } from "./workflow/polling/simple-polling-flow.js";
import { failureRecoveryFlow } from "./workflow/recovery/failure-recovery-flow.js";
import { reminderFlow } from "./workflow/reminders/reminder-flow.js";
import { resettableTimerFlow } from "./workflow/resettabletimer/resettable-timer-flow.js";
import { childFlow } from "./workflow/scalableparallel/child-flow.js";
import { parentFlow } from "./workflow/scalableparallel/parent-flow.js";
import { requestReceiverFlow } from "./workflow/scalableparallel/request-receiver-flow.js";
import { storageFlow } from "./workflow/storage/storage-flow.js";
import { flowGracefulTimeout } from "./workflow/timeout/flow-graceful-timeout.js";
import { waitForStateCompletionFlow } from "./workflow/waitforstatecompletion/wait-for-state-completion-flow.js";

export function createDesignPatternRegistry(): Registry {
  return new Registry([
    cronScheduleFlow,
    drainInternalChannelsFlow,
    drainSignalChannelsFlow,
    interruptibleExecutionFlow,
    manualInterventionFlow,
    simpleParallelStatesFlow,
    parallelStatesWithAwaitFlow,
    parentFlowV2,
    simplePollingFlow,
    backoffPollingFlow,
    failureRecoveryFlow,
    reminderFlow,
    resettableTimerFlow,
    childFlow,
    parentFlow,
    requestReceiverFlow,
    storageFlow,
    flowGracefulTimeout,
    waitForStateCompletionFlow,
  ]);
}
