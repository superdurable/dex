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

package io.iworkflow.config;

import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.patterns.workflow.cron.CronScheduleWorkflow;
import io.iworkflow.patterns.workflow.drainchannels.internal.DrainInternalChannelsWorkflow;
import io.iworkflow.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow;
import io.iworkflow.patterns.workflow.interruptible.InterruptibleExecutionWorkflow;
import io.iworkflow.patterns.workflow.intervention.ManualInterventionWorkflow;
import io.iworkflow.patterns.workflow.parallel.ParallelStatesWithAwaitWorkflow;
import io.iworkflow.patterns.workflow.parallel.SimpleParallelStatesWorkflow;
import io.iworkflow.patterns.workflow.parentchild.ParentWorkflowV2;
import io.iworkflow.patterns.workflow.polling.BackoffPollingWorkflow;
import io.iworkflow.patterns.workflow.polling.SimplePollingWorkflow;
import io.iworkflow.patterns.workflow.recovery.FailureRecoveryWorkflow;
import io.iworkflow.patterns.workflow.resettabletimer.ResettableTimerWorkflow;
import io.iworkflow.patterns.workflow.scalableparallel.ChildWorkflow;
import io.iworkflow.patterns.workflow.scalableparallel.ParentWorkflow;
import io.iworkflow.patterns.workflow.scalableparallel.RequestReceiverWorkflow;
import io.iworkflow.patterns.workflow.storage.StorageWorkflow;
import io.iworkflow.patterns.workflow.timeout.HandlingTimeoutWorkflow;
import io.iworkflow.patterns.workflow.waitforstatecompletion.WaitForStateCompletionWorkflow;
import io.iworkflow.core.Client;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.WorkflowOptions;
import io.iworkflow.core.exceptions.WorkflowAlreadyStartedException;
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
    public ObjectWorkflow requestReceiverWorkflow(final Client iwfClient) {
        return new RequestReceiverWorkflow(iwfClient);
    }

    @Bean
    public ObjectWorkflow parentWorkflow(final Client iwfClient) {
        return new ParentWorkflow(iwfClient);
    }

    @Bean
    public ObjectWorkflow childWorkflow(final Client iwfClient) {
        return new ChildWorkflow(iwfClient);
    }

    @Bean
    public ObjectWorkflow parentWorkflowV2(final Client iwfClient) {
        return new ParentWorkflowV2(iwfClient);
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
