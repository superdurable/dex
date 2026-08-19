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

import { cronScheduleFlow } from "./patterns/cron/cron-schedule-flow.js";
import { drainInternalChannelsFlow } from "./patterns/drain-channels/internal/drain-internal-channels-flow.js";
import { drainSignalChannelsFlow } from "./patterns/drain-channels/signal/drain-signal-channels-flow.js";
import { userProfileFlow } from "./patterns/entity-store/user-profile-flow.js";
import { interruptibleExecutionFlow } from "./patterns/interruptible/interruptible-execution-flow.js";
import { manualInterventionFlow } from "./patterns/intervention/manual-intervention-flow.js";
import { parallelStatesWithAwaitFlow } from "./patterns/parallel/parallel-states-with-await-flow.js";
import { simpleParallelStatesFlow } from "./patterns/parallel/simple-parallel-states-flow.js";
import { parentFlowV2 } from "./patterns/parent-child/parent-flow-v2.js";
import { backoffPollingFlow } from "./patterns/polling/backoff-polling-flow.js";
import { simplePollingFlow } from "./patterns/polling/simple-polling-flow.js";
import { failureRecoveryFlow } from "./patterns/recovery/failure-recovery-flow.js";
import { reminderFlow } from "./patterns/reminders/reminder-flow.js";
import { resettableTimerFlow } from "./patterns/resettable-timer/resettable-timer-flow.js";
import { childFlow } from "./patterns/scalable-parallel/child-flow.js";
import { parentFlow } from "./patterns/scalable-parallel/parent-flow.js";
import { requestReceiverFlow } from "./patterns/scalable-parallel/request-receiver-flow.js";
import { flowGracefulTimeout } from "./patterns/timeout/flow-graceful-timeout.js";
import { waitForStateCompletionFlow } from "./patterns/wait-for-state-completion/wait-for-state-completion-flow.js";
import { attributeFlow } from "./primitives/attribute/attribute-flow.js";
import { channelFlow } from "./primitives/channel/channel-flow.js";
import { clientApisFlow } from "./primitives/client-apis/client-apis-flow.js";
import { rpcFlow } from "./primitives/rpc/rpc-flow.js";
import { retryFlow } from "./primitives/step/retry-flow.js";
import { stepFlow } from "./primitives/step/step-flow.js";
import { subFlowChildFlow, subFlowParentFlow } from "./primitives/subflow/subflow-flow.js";
import { timerFlow } from "./primitives/timer/timer-flow.js";
import { engagementFlow } from "./products/engagement/engagement-flow.js";
import { jobPostFlow } from "./products/job-post/job-post-flow.js";
import { orchestrationFlow } from "./products/microservices/orchestration-flow.js";
import { moneyTransferFlow } from "./products/money-transfer/money-transfer-flow.js";
import { OrderProcessingFlow } from "./products/order-processing/order-processing-flow.js";
import { pollingFlow } from "./products/polling/polling-flow.js";
import { employerOptInFlow } from "./products/shortlist-candidates/employer-opt-in-flow.js";
import { shortlistFlow } from "./products/shortlist-candidates/shortlist-flow.js";
import { userSignupFlow } from "./products/signup/user-signup-flow.js";
import { subscriptionFlow } from "./products/subscription/subscription-flow.js";
import { MyDependencyService } from "./shared/my-dependency-service.js";

const orderProcessingFlow = new OrderProcessingFlow(new MyDependencyService());

export const allExampleFlows: readonly Flow<any>[] = [
  moneyTransferFlow,
  orderProcessingFlow,
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
  stepFlow,
  retryFlow,
  attributeFlow,
  channelFlow,
  timerFlow,
  rpcFlow,
  subFlowChildFlow,
  subFlowParentFlow,
  clientApisFlow,
];

export function createExampleRegistry(): Registry {
  return new Registry(allExampleFlows);
}

export {
  moneyTransferFlow,
  orderProcessingFlow,
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
  stepFlow,
  retryFlow,
  attributeFlow,
  channelFlow,
  timerFlow,
  rpcFlow,
  subFlowChildFlow,
  subFlowParentFlow,
  clientApisFlow,
};
