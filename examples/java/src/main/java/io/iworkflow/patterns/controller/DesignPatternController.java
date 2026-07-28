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

package io.iworkflow.patterns.controller;

import io.iworkflow.patterns.workflow.drainchannels.internal.DrainInternalChannelsWorkflow;
import io.iworkflow.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.patterns.workflow.interruptible.InterruptibleExecutionWorkflow;
import io.iworkflow.patterns.workflow.intervention.ManualInterventionWorkflow;
import io.iworkflow.patterns.workflow.parallel.JobSeeker;
import io.iworkflow.patterns.workflow.parallel.ParallelStatesWithAwaitWorkflow;
import io.iworkflow.patterns.workflow.parallel.SimpleParallelStatesWorkflow;
import io.iworkflow.patterns.workflow.parentchild.ParentWorkflowV2;
import io.iworkflow.patterns.workflow.scalableparallel.RequestReceiverWorkflow;
import io.iworkflow.patterns.workflow.polling.BackoffPollingWorkflow;
import io.iworkflow.patterns.workflow.polling.SimplePollingWorkflow;
import io.iworkflow.patterns.workflow.recovery.FailureRecoveryWorkflow;
import io.iworkflow.patterns.workflow.recovery.ImmutableFailureRecoveryWorkflowInput;
import io.iworkflow.patterns.workflow.reminders.ReminderWorkflow;
import io.iworkflow.patterns.workflow.resettabletimer.ResettableTimerWorkflow;
import io.iworkflow.patterns.workflow.storage.AddStorageItemRequest;
import io.iworkflow.patterns.workflow.storage.StorageWorkflow;
import io.iworkflow.patterns.workflow.timeout.HandlingTimeoutWorkflow;
import io.iworkflow.patterns.workflow.waitforstatecompletion.ImmutableJobSeekerData;
import io.iworkflow.patterns.workflow.waitforstatecompletion.JobSeekerData;
import io.iworkflow.patterns.workflow.waitforstatecompletion.PersistDataState;
import io.iworkflow.patterns.workflow.waitforstatecompletion.WaitForStateCompletionWorkflow;
import io.iworkflow.core.Client;
import io.iworkflow.core.RpcDefinitions;
import io.iworkflow.core.WorkflowOptions;
import io.iworkflow.core.exceptions.NoRunningWorkflowException;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import static io.iworkflow.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow.QUEUE_SIGNAL_CHANNEL;
import static io.iworkflow.gen.models.IDReusePolicy.ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY;

@RestController
@RequestMapping("/design-pattern")
class DesignPatternController {

    private final static int TIMEOUT_SECONDS = 3600;

    private final Client iwfClient;
    private final ServiceDependency serviceDependency;

    public DesignPatternController(final Client iwfClient, ServiceDependency serviceDependency) {
        this.iwfClient = iwfClient;
        this.serviceDependency = serviceDependency;
    }

    @GetMapping("/polling/start/simple")
    ResponseEntity<String> startSimple(@RequestParam String workflowId) {
        String runId = iwfClient.startWorkflow(SimplePollingWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/polling/start/backoff")
    ResponseEntity<String> startBackoffPolling(@RequestParam String workflowId) {
        String runId = iwfClient.startWorkflow(BackoffPollingWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/start")
    ResponseEntity<String> startInterruptible(@RequestParam String workflowId) {
        String runId = iwfClient.startWorkflow(InterruptibleExecutionWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/cancel")
    ResponseEntity<String> cancelInterruptible(@RequestParam String workflowId) {
        final InterruptibleExecutionWorkflow rpcStub = iwfClient.newRpcStub(InterruptibleExecutionWorkflow.class, workflowId);
        iwfClient.invokeRPC(rpcStub::interrupt);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/workflow-with-reminder/start")
    public ResponseEntity<String> start() {
        final String wfId = "reminder_test_id_" + System.currentTimeMillis() / 1000;
        iwfClient.startWorkflow(ReminderWorkflow.class, wfId, TIMEOUT_SECONDS, null);

        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/workflow-with-reminder/accept")
    public ResponseEntity<String> accept(@RequestParam String workflowId) {
        final ReminderWorkflow rpcStub = iwfClient.newRpcStub(ReminderWorkflow.class, workflowId);
        iwfClient.invokeRPC(rpcStub::accept);

        return ResponseEntity.ok("accepted");
    }

    @GetMapping("/workflow-with-reminder/optout")
    public ResponseEntity<String> optout(@RequestParam String workflowId) {
        iwfClient.signalWorkflow(ReminderWorkflow.class, workflowId, ReminderWorkflow.SIGNAL_NAME_OPT_OUT_REMINDER, null);
        return ResponseEntity.ok("done");
    }

    @PostMapping("/storage/add")
    ResponseEntity<String> addStorageItem(@RequestBody AddStorageItemRequest request) {
        final StorageWorkflow rpcStub = iwfClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        invokeStorageRpc(rpcStub::addItem, request, true);
        return ResponseEntity.ok("Added storage item");
    }

    @GetMapping("/storage/get")
    ResponseEntity<String> getStorageItem(@RequestParam String itemKey) {
        final StorageWorkflow rpcStub = iwfClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        final String itemValue = invokeStorageRpc(rpcStub::getItem, itemKey, true);
        return ResponseEntity.ok("Item: " + itemValue);
    }

    @PostMapping("/storage/remove")
    ResponseEntity<String> removeStorageItem(@RequestParam String itemKey) {
        final StorageWorkflow rpcStub = iwfClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        invokeStorageRpc(rpcStub::removeItem, itemKey, true);
        return ResponseEntity.ok("Removed storage item");
    }

    private <I> void invokeStorageRpc(RpcDefinitions.RpcProc1<I> rpcStubMethod, I input, boolean attemptStart) {
        try {
            iwfClient.invokeRPC(rpcStubMethod, input);
        } catch (final NoRunningWorkflowException e) {
            if (attemptStart) {
                // Start singleton workflow
                iwfClient.startWorkflow(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId(), TIMEOUT_SECONDS, null);
                invokeStorageRpc(rpcStubMethod, input, false);
            } else {
                // Rethrow the exception
                throw e;
            }
        }
    }

    private <I, O> O invokeStorageRpc(RpcDefinitions.RpcFunc1<I, O> rpcStubMethod, I input, boolean attemptStart) {
        try {
            return iwfClient.invokeRPC(rpcStubMethod, input);
        } catch (final NoRunningWorkflowException e) {
            if (attemptStart) {
                // Start singleton workflow
                iwfClient.startWorkflow(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId(), TIMEOUT_SECONDS, null);
                return invokeStorageRpc(rpcStubMethod, input, false);
            } else {
                // Rethrow the exception
                throw e;
            }
        }
    }

    @GetMapping("/intervention/start")
    ResponseEntity<String> startIntervention (@RequestParam final String workflowId) {
        final String runId = iwfClient.startWorkflow(ManualInterventionWorkflow.class, workflowId, 3600, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/start")
    ResponseEntity<String> startResettableTimer(@RequestParam String workflowId) {
        String runId = iwfClient.startWorkflow(ResettableTimerWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/reset")
    ResponseEntity<String> resetResettableTimer(@RequestParam String workflowId) {
        final ResettableTimerWorkflow rpcStub = iwfClient.newRpcStub(ResettableTimerWorkflow.class, workflowId);
        iwfClient.invokeRPC(rpcStub::sendResetMessage);
        return ResponseEntity.ok("reset");
    }

    @GetMapping("/parallel/start/simple")
    ResponseEntity<String> startParallelSimple(@RequestParam String workflowId) {
        final JobSeeker jobSeeker = new JobSeeker("123", "jobseeker@indeed.com", "0987654321");

        final String runId = iwfClient.startWorkflow(SimpleParallelStatesWorkflow.class, workflowId, 3600, jobSeeker);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/parallel/start/withAwait")
    ResponseEntity<String> startParallelWithAwait(@RequestParam String workflowId) {
        final String runId = iwfClient.startWorkflow(ParallelStatesWithAwaitWorkflow.class, workflowId, 3600, 50);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/recovery/start")
    ResponseEntity<String> startRecovery(
            @RequestParam final String workflowId,
            @RequestParam final String itemName,
            @RequestParam final int quantity
    ) {
        iwfClient.startWorkflow(FailureRecoveryWorkflow.class, workflowId, TIMEOUT_SECONDS, ImmutableFailureRecoveryWorkflowInput.builder()
                .itemName(itemName)
                .requestedQuantity(quantity)
                .build());
        return ResponseEntity.ok("recovery workflow started");
    }

    @GetMapping("scalableparallel/start")
    ResponseEntity<String> scalableparallel(
            // This is the workflowId of the RequestReceiverWorkflow to process this dummy batch request
            @RequestParam String workflowId,
            // This is a dummy input specifying how many requests should be sent(each will be processed in a childWorkflow) -- could be a list of Objects passed in @RequestBody in a real scenario
            @RequestParam int numOfChildWfs) {

        iwfClient.startWorkflow(
                RequestReceiverWorkflow.class, workflowId, 3600, numOfChildWfs,
                WorkflowOptions.basicBuilder().workflowIdReusePolicy(ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY).build());

        return ResponseEntity.ok("success");
    }

    @GetMapping("parentchild/start")
    ResponseEntity<String> parentchild(
            // This is the workflowId of the ParentWorkflowV2 to process this dummy batch request
            @RequestParam String workflowId,
            // This is a dummy input specifying how many requests should be sent(each will be processed in a childWorkflow) -- could be a list of Objects passed in @RequestBody in a real scenario
            @RequestParam int numOfChildWfs) {

        iwfClient.startWorkflow(
                ParentWorkflowV2.class, workflowId, 3600, numOfChildWfs,
                WorkflowOptions.basicBuilder().workflowIdReusePolicy(ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY).build());

        return ResponseEntity.ok("success");
    }

    @GetMapping("/drainchannels/internal/start")
    ResponseEntity<String> startDrainInternalChannels(@RequestParam final String workflowId) {
        final String runId = iwfClient.startWorkflow(DrainInternalChannelsWorkflow.class, workflowId, 3600);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/drainchannels/signal/startorsignal")
    ResponseEntity<String> startDrainSignalChannels(@RequestParam final String workflowId) throws InterruptedException {
        String response;
        try {
            iwfClient.signalWorkflow(DrainSignalChannelsWorkflow.class, workflowId, QUEUE_SIGNAL_CHANNEL, "signal from startorsignal endpoint");
            response = "Signaled the workflow";
        } catch (final NoRunningWorkflowException e) {
            final String runId = iwfClient.startWorkflow(DrainSignalChannelsWorkflow.class, workflowId, 3600, "first message from start");
            response = "Started the workflow with runId " + runId;
        }
        return ResponseEntity.ok(response);
    }

    @GetMapping("/waitforstatecompletion/start")
    ResponseEntity<String> startWaitForStateCompletion(
            @RequestParam final String workflowId
    ) throws JsonProcessingException {
        final ObjectMapper objectMapper = new ObjectMapper();
        final JobSeekerData data = ImmutableJobSeekerData.builder()
                        .id(1)
                        .build();
        iwfClient.startWorkflow(
                WaitForStateCompletionWorkflow.class,
                workflowId,
                3600,
                data,
                WorkflowOptions.extendedBuilder()
                        .waitForCompletionState(PersistDataState.class)
                        .getBuilder()
                        .build());
        iwfClient.waitForStateExecutionCompletion(workflowId, PersistDataState.class);
        final WaitForStateCompletionWorkflow rpcStub = iwfClient.newRpcStub(WaitForStateCompletionWorkflow.class, workflowId);
        final JobSeekerData persistedData = iwfClient.invokeRPC(rpcStub::getJobSeekerData);

        return ResponseEntity.ok(String.format("success for workflow %s with data %s", workflowId, objectMapper.writeValueAsString(persistedData)));
    }

    @GetMapping("/timeout/start")
    ResponseEntity<String> startTimeoutWorkflow(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "true") final Boolean successfulWorkflow
    ) {
        iwfClient.startWorkflow(HandlingTimeoutWorkflow.class, workflowId, 3600, successfulWorkflow);

        return ResponseEntity.ok(String.format("success for workflow %s", workflowId));
    }
}
