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

import { Registry, type Flow } from "@superdurable/dex";

import { cronScheduleFlow } from "./patterns/workflow/cron/cron-schedule-flow.js";
import { drainInternalChannelsFlow } from "./patterns/workflow/drainchannels/internal/drain-internal-channels-flow.js";
import { drainSignalChannelsFlow } from "./patterns/workflow/drainchannels/signal/drain-signal-channels-flow.js";
import { interruptibleExecutionFlow } from "./patterns/workflow/interruptible/interruptible-execution-flow.js";
import { manualInterventionFlow } from "./patterns/workflow/intervention/manual-intervention-flow.js";
import { parallelStatesWithAwaitFlow } from "./patterns/workflow/parallel/parallel-states-with-await-flow.js";
import { simpleParallelStatesFlow } from "./patterns/workflow/parallel/simple-parallel-states-flow.js";
import { parentFlowV2 } from "./patterns/workflow/parentchild/parent-flow-v2.js";
import { backoffPollingFlow } from "./patterns/workflow/polling/backoff-polling-flow.js";
import { simplePollingFlow } from "./patterns/workflow/polling/simple-polling-flow.js";
import { failureRecoveryFlow } from "./patterns/workflow/recovery/failure-recovery-flow.js";
import { reminderFlow } from "./patterns/workflow/reminders/reminder-flow.js";
import { resettableTimerFlow } from "./patterns/workflow/resettabletimer/resettable-timer-flow.js";
import { childFlow } from "./patterns/workflow/scalableparallel/child-flow.js";
import { parentFlow } from "./patterns/workflow/scalableparallel/parent-flow.js";
import { requestReceiverFlow } from "./patterns/workflow/scalableparallel/request-receiver-flow.js";
import { userProfileFlow } from "./patterns/workflow/entitystore/user-profile-flow.js";
import { flowGracefulTimeout } from "./patterns/workflow/timeout/flow-graceful-timeout.js";
import { waitForStateCompletionFlow } from "./patterns/workflow/waitforstatecompletion/wait-for-state-completion-flow.js";
import { engagementFlow } from "./workflow/engagement/engagement-flow.js";
import { jobPostFlow } from "./workflow/jobpost/job-post-flow.js";
import { orchestrationFlow } from "./workflow/microservices/orchestration-flow.js";
import { moneyTransferFlow } from "./workflow/money/transfer/money-transfer-flow.js";
import { pollingFlow } from "./workflow/polling/polling-flow.js";
import { employerOptInFlow } from "./workflow/shortlistcandidates/employer-opt-in-flow.js";
import { shortlistFlow } from "./workflow/shortlistcandidates/shortlist-flow.js";
import { userSignupFlow } from "./workflow/signup/user-signup-flow.js";
import { subscriptionFlow } from "./workflow/subscription/subscription-flow.js";

export const allExampleFlows: readonly Flow<any>[] = [
  moneyTransferFlow,
  orchestrationFlow,
  engagementFlow,
  subscriptionFlow,
  pollingFlow,
  userSignupFlow,
  jobPostFlow,
  employerOptInFlow,
  shortlistFlow,
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
  userProfileFlow,
  flowGracefulTimeout,
  waitForStateCompletionFlow,
];

export function createExampleRegistry(): Registry {
  return new Registry(allExampleFlows);
}

export {
  moneyTransferFlow,
  orchestrationFlow,
  engagementFlow,
  subscriptionFlow,
  pollingFlow,
  userSignupFlow,
  jobPostFlow,
  employerOptInFlow,
  shortlistFlow,
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
  userProfileFlow,
  flowGracefulTimeout,
  waitForStateCompletionFlow,
};
