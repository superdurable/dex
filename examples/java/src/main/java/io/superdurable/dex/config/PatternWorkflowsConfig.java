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

package io.superdurable.dex.config;

import io.superdurable.dex.patterns.services.ServiceDependency;
import io.superdurable.dex.patterns.workflow.cron.CronScheduleWorkflow;
import io.superdurable.dex.patterns.workflow.drainchannels.internal.DrainInternalChannelsWorkflow;
import io.superdurable.dex.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow;
import io.superdurable.dex.patterns.workflow.interruptible.InterruptibleExecutionWorkflow;
import io.superdurable.dex.patterns.workflow.intervention.ManualInterventionWorkflow;
import io.superdurable.dex.patterns.workflow.parallel.ParallelStatesWithAwaitWorkflow;
import io.superdurable.dex.patterns.workflow.parallel.SimpleParallelStatesWorkflow;
import io.superdurable.dex.patterns.workflow.parentchild.ParentWorkflowV2;
import io.superdurable.dex.patterns.workflow.polling.BackoffPollingWorkflow;
import io.superdurable.dex.patterns.workflow.polling.SimplePollingWorkflow;
import io.superdurable.dex.patterns.workflow.recovery.FailureRecoveryWorkflow;
import io.superdurable.dex.patterns.workflow.resettabletimer.ResettableTimerWorkflow;
import io.superdurable.dex.patterns.workflow.scalableparallel.ChildWorkflow;
import io.superdurable.dex.patterns.workflow.scalableparallel.ParentWorkflow;
import io.superdurable.dex.patterns.workflow.scalableparallel.RequestReceiverWorkflow;
import io.superdurable.dex.patterns.workflow.storage.StorageWorkflow;
import io.superdurable.dex.patterns.workflow.timeout.HandlingTimeoutWorkflow;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.WaitForStateCompletionWorkflow;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.WorkflowOptions;
import io.superdurable.dex.core.exceptions.WorkflowAlreadyStartedException;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
class PatternWorkflowsConfig {
    private static final String CRON_SCHEDULE_WORKFLOW_ID = "cron-schedule-sample";

    @Bean
    public ObjectWorkflow simplePollingWorkflow() {
        return new SimplePollingWorkflow();
    }

    @Bean
    public ObjectWorkflow backoffPollingWorkflow(ServiceDependency service) {
        return new BackoffPollingWorkflow(service);
    }

    @Bean
    public ObjectWorkflow resettableTimerWorkflow() {
        return new ResettableTimerWorkflow();
    }

    @Bean
    public ObjectWorkflow interruptibleExecutionWorkflow() {
        return new InterruptibleExecutionWorkflow();
    }

    @Bean
    public ObjectWorkflow manualInterventionWorkflow() {
        return new ManualInterventionWorkflow();
    }

    @Bean
    public ObjectWorkflow storageWorkflow() {
        return new StorageWorkflow();
    }

    @Bean
    public ObjectWorkflow cronScheduleWorkflow() {
        return new CronScheduleWorkflow();
    }

    @Bean
    public ObjectWorkflow failureRecoveryWorkflow() {
        return new FailureRecoveryWorkflow();
    }

    @Bean
    public SimpleParallelStatesWorkflow simpleParallelStatesWorkflow() {
        return new SimpleParallelStatesWorkflow();
    }

    @Bean
    public ParallelStatesWithAwaitWorkflow parallelStatesWithAwaitWorkflow() {
        return new ParallelStatesWithAwaitWorkflow();
    }

    @Bean
    public ObjectWorkflow requestReceiverWorkflow(final Client dexClient) {
        return new RequestReceiverWorkflow(dexClient);
    }

    @Bean
    public ObjectWorkflow parentWorkflow(final Client dexClient) {
        return new ParentWorkflow(dexClient);
    }

    @Bean
    public ObjectWorkflow childWorkflow(final Client dexClient) {
        return new ChildWorkflow(dexClient);
    }

    @Bean
    public ObjectWorkflow parentWorkflowV2(final Client dexClient) {
        return new ParentWorkflowV2(dexClient);
    }


    @Bean
    public WaitForStateCompletionWorkflow waitForStateCompletionWorkflow() {
        return new WaitForStateCompletionWorkflow(new ServiceDependency(), new ServiceDependency());
    }

    @Bean
    public DrainInternalChannelsWorkflow drainInternalChannelsWorkflow() {
        return new DrainInternalChannelsWorkflow(new ServiceDependency(), new ServiceDependency());
    }

    @Bean
    public DrainSignalChannelsWorkflow drainSignalChannelsWorkflow() {
        return new DrainSignalChannelsWorkflow();
    }

    @Bean
    public HandlingTimeoutWorkflow handlingTimeoutWorkflow() {
        return new HandlingTimeoutWorkflow();
    }
}
