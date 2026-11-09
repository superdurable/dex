/*
 * Copyright (c) 2026 Super Durable, Inc.
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

import type { Flow } from "@superdurable/dex";

import { generalizedAndJoinFlow, threadSplitMergeFlow } from "./and-join-and-threads.js";
import {
  exclusiveChoiceSimpleMergeFlow,
  parallelSplitSynchronizationFlow,
  sequenceFlow,
} from "./basic-control-flow.js";
import {
  cancelCaseFlow,
  cancelMultipleInstanceTaskFlow,
  cancelRegionFlow,
  cancelTaskFlow,
  completeMultipleInstanceTaskFlow,
} from "./cancellation.js";
import { arbitraryCyclesFlow, factorialRecursionFlow, structuredLoopFlow } from "./iteration.js";
import {
  generalSynchronizingMergeFlow,
  multiChoiceSynchronizingMergeFlow,
  multiMergeFlow,
  reviewRegionFlow,
} from "./multi-choice-and-merges.js";
import {
  cancellingPartialJoinForMultipleInstancesFlow,
  designTimeMultipleInstancesFlow,
  dynamicPartialJoinForMultipleInstancesFlow,
  multipleInstancesWithoutSynchronizationFlow,
  noPriorKnowledgeMultipleInstancesFlow,
  runTimeMultipleInstancesFlow,
  staticPartialJoinForMultipleInstancesFlow,
} from "./multiple-instances.js";
import {
  blockingPartialJoinFlow,
  cancellingPartialJoinFlow,
  structuredPartialJoinFlow,
} from "./partial-joins.js";
import {
  criticalSectionFlow,
  deferredChoiceFlow,
  interleavedParallelRoutingFlow,
  interleavedRoutingFlow,
  milestoneFlow,
} from "./state-based.js";
import { explicitTerminationFlow, implicitTerminationFlow } from "./termination.js";
import { persistentTriggerFlow, transientTriggerFlow } from "./triggers.js";

/** Every Workflow Patterns example Flow, for one Registry. */
export const workflowPatternFlows: readonly Flow<any>[] = [
  sequenceFlow,
  parallelSplitSynchronizationFlow,
  exclusiveChoiceSimpleMergeFlow,
  multiChoiceSynchronizingMergeFlow,
  multiMergeFlow,
  reviewRegionFlow,
  generalSynchronizingMergeFlow,
  structuredPartialJoinFlow,
  blockingPartialJoinFlow,
  cancellingPartialJoinFlow,
  generalizedAndJoinFlow,
  threadSplitMergeFlow,
  multipleInstancesWithoutSynchronizationFlow,
  designTimeMultipleInstancesFlow,
  runTimeMultipleInstancesFlow,
  noPriorKnowledgeMultipleInstancesFlow,
  staticPartialJoinForMultipleInstancesFlow,
  cancellingPartialJoinForMultipleInstancesFlow,
  dynamicPartialJoinForMultipleInstancesFlow,
  deferredChoiceFlow,
  interleavedParallelRoutingFlow,
  milestoneFlow,
  criticalSectionFlow,
  interleavedRoutingFlow,
  cancelTaskFlow,
  cancelCaseFlow,
  cancelRegionFlow,
  cancelMultipleInstanceTaskFlow,
  completeMultipleInstanceTaskFlow,
  arbitraryCyclesFlow,
  structuredLoopFlow,
  factorialRecursionFlow,
  implicitTerminationFlow,
  explicitTerminationFlow,
  transientTriggerFlow,
  persistentTriggerFlow,
];
