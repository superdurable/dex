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

import { getClient } from "./client-holder.js";
import { creditCheckFlow } from "./patterns/cancel-case/credit-check-flow.js";
import { LoanApplicationFlow } from "./patterns/cancel-case/loan-application-flow.js";
import { contractEditFlow } from "./patterns/critical-section/contract-edit-flow.js";
import { cronScheduleFlow } from "./patterns/cron/cron-schedule-flow.js";
import { offerResponseFlow } from "./patterns/deferred-choice/offer-response-flow.js";
import { drainInternalChannelFlow } from "./patterns/drain-channels/internal/drain-internal-channels-flow.js";
import { drainingExternalChannelFlow } from "./patterns/drain-channels/external-publishing/draining-channel-flow.js";
import { userProfileFlow } from "./patterns/entity-store/user-profile-flow.js";
import { customerDirectoryFlow } from "./patterns/hash-partitioned-attribute-map/customer-directory-flow.js";
import { interruptibleFlow } from "./patterns/interruptible/interruptible-execution-flow.js";
import { manualRecoveryFlow } from "./patterns/intervention/manual-recovery-flow.js";
import {
  awaitParallelStepsFlow,
  dynamicParallelStepsFlow,
  firstWinParallelStepsFlow,
  staticParallelStepsFlow,
} from "./patterns/parallel/parallel-step-flows.js";
import {
  advancedLongLiveParentFlow,
  advancedShortLiveParentFlow,
  basicParentFlow,
  exampleSubFlow as parallelExampleSubFlow,
  submitRequestFlow,
  waitForHalfParentFlow,
} from "./patterns/parallel-subflows/parallel-subflows.js";
import { FakeJobService } from "./patterns/polling/fake-job-service.js";
import { PollingFlow } from "./patterns/polling/polling-flow.js";
import { failureRecoveryFlow } from "./patterns/recovery/failure-recovery-flow.js";
import { reminderFlow } from "./patterns/reminders/reminder-flow.js";
import { chunkedSubscriberFlow } from "./patterns/sequentially-chunked-attribute-map/chunked-subscriber-flow.js";
import { inactivenessTrackerFlow } from "./patterns/inactiveness-tracker-timer/inactiveness-tracker-flow.js";
import { flowGracefulTimeout } from "./patterns/timeout/flow-graceful-timeout.js";
import { waitForStepCompletionFlow } from "./patterns/wait-for-step-completion/wait-for-step-completion-flow.js";
import { workflowPatternFlows } from "./patterns/workflow-patterns/workflow-pattern-flows.js";
import { attributeFlow } from "./primitives/attribute/attribute-flow.js";
import { channelFlow } from "./primitives/channel/channel-flow.js";
import { clientApisFlow } from "./primitives/client-apis/client-apis-flow.js";
import { exampleFlow } from "./primitives/flow/example-flow.js";
import { customRetryFlow } from "./primitives/custom-retry/custom-retry-flow.js";
import { durabilityFlow } from "./primitives/durability/durability-flow.js";
import { heartbeatFlow } from "./primitives/heartbeat/heartbeat-flow.js";
import { optionsOverrideFlow } from "./primitives/options-override/options-override-flow.js";
import { proceedOnWaitFailureFlow } from "./primitives/proceed-on-wait-failure/proceed-on-wait-failure-flow.js";
import { stepExecutionLocalFlow } from "./primitives/step-execution-local/step-execution-local-flow.js";
import { rpcFlow } from "./primitives/rpc/rpc-flow.js";
import { reportGenerationFlow } from "./primitives/rpc-canceling-steps/report-generation-flow.js";
import { retryFlow } from "./primitives/step/retry-flow.js";
import { stepFlow } from "./primitives/step/step-flow.js";
import { stepDecisionFlow } from "./primitives/step-decision/step-decision-flow.js";
import { streamFlow } from "./primitives/stream/stream-flow.js";
import { subFlowChildFlow, subFlowParentFlow } from "./primitives/subflow/subflow-flow.js";
import { timerFlow } from "./primitives/timer/timer-flow.js";
import { waitTypesFlow } from "./primitives/wait-types/wait-types-flow.js";
import { loadAiAgentConfig } from "./products/ai-agent/ai-agent-config.js";
import { createAiAgentFlow } from "./products/ai-agent/ai-agent-flow.js";
import {
  buildClaudeCliEnvironment,
  createClaudeAgentSdkOrderSupportFlow,
  hasAnthropicApiKey,
  loadClaudeAgentSdkOrderSupportModel,
} from "./products/agent-harnesses/claude-agent-sdk/claude-agent-sdk-order-support-flow.js";
import {
  createMastraOrderSupportFlow,
  loadMastraOrderSupportModel,
} from "./products/agent-harnesses/mastra/mastra-order-support-flow.js";
import {
  createOpenAIAgentsOrderSupportFlow,
  loadOpenAIAgentsOrderSupportModel,
} from "./products/agent-harnesses/openai-agents/openai-agents-order-support-flow.js";
import { OrderDesk } from "./products/agent-harnesses/order-support/order-desk.js";
import { createPiOrderSupportFlow, loadPiOrderSupportModel } from "./products/agent-harnesses/pi/pi-order-support-flow.js";
import {
  createVercelAiOrderSupportFlow,
  loadVercelAiOrderSupportModel,
} from "./products/agent-harnesses/vercel-ai/vercel-ai-order-support-flow.js";
import { engagementFlow } from "./products/engagement/engagement-flow.js";
import { dealDSLFlow } from "./products/deal-dsl/deal-dsl-flow.js";
import { jobPostingFlow } from "./products/job-post/job-post-flow.js";
import { orchestrationFlow } from "./products/microservices/orchestration-flow.js";
import { moneyTransferFlow } from "./products/money-transfer/money-transfer-flow.js";
import { OrderProcessingFlow } from "./products/order-processing/order-processing-flow.js";
import { userOnboardingFlow } from "./products/signup/user-signup-flow.js";
import { subscriptionFlow } from "./products/subscription/subscription-flow.js";
import { MyDependencyService } from "./shared/my-dependency-service.js";
import { openSlackConnectionFromProject } from "./shared/slack-connector-tools.js";

const orderProcessingFlow = new OrderProcessingFlow(new MyDependencyService());
const pollingJobs = new FakeJobService();
const pollingFlow = new PollingFlow(pollingJobs);
const loanApplicationFlow = new LoanApplicationFlow(getClient, creditCheckFlow);
// One Slack connection for the agent examples; without it they offer no Slack tools.
const slack = await openSlackConnectionFromProject();
const aiAgentConfig = loadAiAgentConfig();
const aiAgentFlow = createAiAgentFlow(aiAgentConfig, slack);
// One order desk for the five agent harness examples; each harness registers its own session Flow type.
const orderDesk = new OrderDesk();
// Without the key the Claude Code CLI could fall back to a developer's own login, so skip the harness.
const claudeAgentSdkOrderSupportFlow = hasAnthropicApiKey()
  ? createClaudeAgentSdkOrderSupportFlow(orderDesk, loadClaudeAgentSdkOrderSupportModel(), buildClaudeCliEnvironment(), slack)
  : undefined;
if (claudeAgentSdkOrderSupportFlow === undefined) {
  console.warn("ANTHROPIC_API_KEY is not set, so the Claude Agent SDK order support example is not registered");
}
const openAIAgentsOrderSupportFlow = createOpenAIAgentsOrderSupportFlow(orderDesk, loadOpenAIAgentsOrderSupportModel(), slack);
const vercelAiOrderSupportFlow = createVercelAiOrderSupportFlow(orderDesk, loadVercelAiOrderSupportModel(), slack);
const mastraOrderSupportFlow = createMastraOrderSupportFlow(orderDesk, loadMastraOrderSupportModel(), slack);
const piOrderSupportFlow = createPiOrderSupportFlow(orderDesk, loadPiOrderSupportModel(), slack);

export const allExampleFlows: readonly Flow<any>[] = [
  moneyTransferFlow,
  orderProcessingFlow,
  orchestrationFlow,
  engagementFlow,
  subscriptionFlow,
  userOnboardingFlow,
  jobPostingFlow,
  dealDSLFlow,
  aiAgentFlow,
  ...(claudeAgentSdkOrderSupportFlow === undefined ? [] : [claudeAgentSdkOrderSupportFlow]),
  openAIAgentsOrderSupportFlow,
  vercelAiOrderSupportFlow,
  mastraOrderSupportFlow,
  piOrderSupportFlow,
  cronScheduleFlow,
  drainInternalChannelFlow,
  drainingExternalChannelFlow,
  interruptibleFlow,
  manualRecoveryFlow,
  staticParallelStepsFlow,
  dynamicParallelStepsFlow,
  awaitParallelStepsFlow,
  firstWinParallelStepsFlow,
  parallelExampleSubFlow,
  basicParentFlow,
  waitForHalfParentFlow,
  advancedLongLiveParentFlow,
  advancedShortLiveParentFlow,
  submitRequestFlow,
  pollingFlow,
  failureRecoveryFlow,
  reminderFlow,
  inactivenessTrackerFlow,
  userProfileFlow,
  chunkedSubscriberFlow,
  customerDirectoryFlow,
  flowGracefulTimeout,
  waitForStepCompletionFlow,
  offerResponseFlow,
  contractEditFlow,
  creditCheckFlow,
  loanApplicationFlow,
  exampleFlow,
  stepFlow,
  retryFlow,
  customRetryFlow,
  durabilityFlow,
  heartbeatFlow,
  optionsOverrideFlow,
  proceedOnWaitFailureFlow,
  stepExecutionLocalFlow,
  stepDecisionFlow,
  waitTypesFlow,
  attributeFlow,
  channelFlow,
  streamFlow,
  timerFlow,
  rpcFlow,
  reportGenerationFlow,
  subFlowChildFlow,
  subFlowParentFlow,
  clientApisFlow,
  ...workflowPatternFlows,
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
  userOnboardingFlow,
  jobPostingFlow,
  dealDSLFlow,
  aiAgentConfig,
  aiAgentFlow,
  claudeAgentSdkOrderSupportFlow,
  openAIAgentsOrderSupportFlow,
  vercelAiOrderSupportFlow,
  mastraOrderSupportFlow,
  piOrderSupportFlow,
  cronScheduleFlow,
  drainInternalChannelFlow,
  drainingExternalChannelFlow,
  interruptibleFlow,
  manualRecoveryFlow,
  staticParallelStepsFlow,
  dynamicParallelStepsFlow,
  awaitParallelStepsFlow,
  firstWinParallelStepsFlow,
  parallelExampleSubFlow,
  basicParentFlow,
  waitForHalfParentFlow,
  advancedLongLiveParentFlow,
  advancedShortLiveParentFlow,
  submitRequestFlow,
  pollingFlow,
  pollingJobs,
  failureRecoveryFlow,
  reminderFlow,
  inactivenessTrackerFlow,
  userProfileFlow,
  chunkedSubscriberFlow,
  customerDirectoryFlow,
  flowGracefulTimeout,
  waitForStepCompletionFlow,
  offerResponseFlow,
  contractEditFlow,
  creditCheckFlow,
  loanApplicationFlow,
  exampleFlow,
  stepFlow,
  retryFlow,
  customRetryFlow,
  durabilityFlow,
  heartbeatFlow,
  optionsOverrideFlow,
  proceedOnWaitFailureFlow,
  stepExecutionLocalFlow,
  stepDecisionFlow,
  waitTypesFlow,
  attributeFlow,
  channelFlow,
  streamFlow,
  timerFlow,
  rpcFlow,
  reportGenerationFlow,
  subFlowChildFlow,
  subFlowParentFlow,
  clientApisFlow,
};
