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

package io.superdurable.dex.patterns.controller;

import io.superdurable.dex.patterns.workflow.drainchannels.internal.DrainInternalChannelsWorkflow;
import io.superdurable.dex.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.patterns.services.ServiceDependency;
import io.superdurable.dex.patterns.workflow.interruptible.InterruptibleExecutionWorkflow;
import io.superdurable.dex.patterns.workflow.intervention.ManualInterventionWorkflow;
import io.superdurable.dex.patterns.workflow.parallel.JobSeeker;
import io.superdurable.dex.patterns.workflow.parallel.ParallelStatesWithAwaitWorkflow;
import io.superdurable.dex.patterns.workflow.parallel.SimpleParallelStatesWorkflow;
import io.superdurable.dex.patterns.workflow.parentchild.ParentWorkflowV2;
import io.superdurable.dex.patterns.workflow.scalableparallel.RequestReceiverWorkflow;
import io.superdurable.dex.patterns.workflow.polling.BackoffPollingWorkflow;
import io.superdurable.dex.patterns.workflow.polling.SimplePollingWorkflow;
import io.superdurable.dex.patterns.workflow.recovery.FailureRecoveryWorkflow;
import io.superdurable.dex.patterns.workflow.recovery.ImmutableFailureRecoveryWorkflowInput;
import io.superdurable.dex.patterns.workflow.reminders.ReminderWorkflow;
import io.superdurable.dex.patterns.workflow.resettabletimer.ResettableTimerWorkflow;
import io.superdurable.dex.patterns.workflow.storage.AddStorageItemRequest;
import io.superdurable.dex.patterns.workflow.storage.StorageWorkflow;
import io.superdurable.dex.patterns.workflow.timeout.HandlingTimeoutWorkflow;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.ImmutableJobSeekerData;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.JobSeekerData;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.PersistDataState;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.WaitForStateCompletionWorkflow;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.RpcDefinitions;
import io.superdurable.dex.core.WorkflowOptions;
import io.superdurable.dex.core.exceptions.NoRunningWorkflowException;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import static io.superdurable.dex.patterns.workflow.drainchannels.signal.DrainSignalChannelsWorkflow.QUEUE_SIGNAL_CHANNEL;
import static io.superdurable.dex.gen.models.IDReusePolicy.ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY;

@RestController
@RequestMapping("/design-pattern")
class DesignPatternController {

    private final static int TIMEOUT_SECONDS = 3600;

    private final Client dexClient;
    private final ServiceDependency serviceDependency;

    public DesignPatternController(final Client dexClient, ServiceDependency serviceDependency) {
        this.dexClient = dexClient;
        this.serviceDependency = serviceDependency;
    }

    @GetMapping("/polling/start/simple")
    ResponseEntity<String> startSimple(@RequestParam String workflowId) {
        String runId = dexClient.startWorkflow(SimplePollingWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/polling/start/backoff")
    ResponseEntity<String> startBackoffPolling(@RequestParam String workflowId) {
        String runId = dexClient.startWorkflow(BackoffPollingWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/start")
    ResponseEntity<String> startInterruptible(@RequestParam String workflowId) {
        String runId = dexClient.startWorkflow(InterruptibleExecutionWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/cancel")
    ResponseEntity<String> cancelInterruptible(@RequestParam String workflowId) {
        final InterruptibleExecutionWorkflow rpcStub = dexClient.newRpcStub(InterruptibleExecutionWorkflow.class, workflowId);
        dexClient.invokeRPC(rpcStub::interrupt);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/workflow-with-reminder/start")
    public ResponseEntity<String> start() {
        final String wfId = "reminder_test_id_" + System.currentTimeMillis() / 1000;
        dexClient.startWorkflow(ReminderWorkflow.class, wfId, TIMEOUT_SECONDS, null);

        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/workflow-with-reminder/accept")
    public ResponseEntity<String> accept(@RequestParam String workflowId) {
        final ReminderWorkflow rpcStub = dexClient.newRpcStub(ReminderWorkflow.class, workflowId);
        dexClient.invokeRPC(rpcStub::accept);

        return ResponseEntity.ok("accepted");
    }

    @GetMapping("/workflow-with-reminder/optout")
    public ResponseEntity<String> optout(@RequestParam String workflowId) {
        dexClient.signalWorkflow(ReminderWorkflow.class, workflowId, ReminderWorkflow.SIGNAL_NAME_OPT_OUT_REMINDER, null);
        return ResponseEntity.ok("done");
    }

    @PostMapping("/storage/add")
    ResponseEntity<String> addStorageItem(@RequestBody AddStorageItemRequest request) {
        final StorageWorkflow rpcStub = dexClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        invokeStorageRpc(rpcStub::addItem, request, true);
        return ResponseEntity.ok("Added storage item");
    }

    @GetMapping("/storage/get")
    ResponseEntity<String> getStorageItem(@RequestParam String itemKey) {
        final StorageWorkflow rpcStub = dexClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        final String itemValue = invokeStorageRpc(rpcStub::getItem, itemKey, true);
        return ResponseEntity.ok("Item: " + itemValue);
    }

    @PostMapping("/storage/remove")
    ResponseEntity<String> removeStorageItem(@RequestParam String itemKey) {
        final StorageWorkflow rpcStub = dexClient.newRpcStub(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId());
        invokeStorageRpc(rpcStub::removeItem, itemKey, true);
        return ResponseEntity.ok("Removed storage item");
    }

    private <I> void invokeStorageRpc(RpcDefinitions.RpcProc1<I> rpcStubMethod, I input, boolean attemptStart) {
        try {
            dexClient.invokeRPC(rpcStubMethod, input);
        } catch (final NoRunningWorkflowException e) {
            if (attemptStart) {
                // Start singleton workflow
                dexClient.startWorkflow(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId(), TIMEOUT_SECONDS, null);
                invokeStorageRpc(rpcStubMethod, input, false);
            } else {
                // Rethrow the exception
                throw e;
            }
        }
    }

    private <I, O> O invokeStorageRpc(RpcDefinitions.RpcFunc1<I, O> rpcStubMethod, I input, boolean attemptStart) {
        try {
            return dexClient.invokeRPC(rpcStubMethod, input);
        } catch (final NoRunningWorkflowException e) {
            if (attemptStart) {
                // Start singleton workflow
                dexClient.startWorkflow(StorageWorkflow.class, StorageWorkflow.getStorageWorkflowId(), TIMEOUT_SECONDS, null);
                return invokeStorageRpc(rpcStubMethod, input, false);
            } else {
                // Rethrow the exception
                throw e;
            }
        }
    }

    @GetMapping("/intervention/start")
    ResponseEntity<String> startIntervention (@RequestParam final String workflowId) {
        final String runId = dexClient.startWorkflow(ManualInterventionWorkflow.class, workflowId, 3600, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/start")
    ResponseEntity<String> startResettableTimer(@RequestParam String workflowId) {
        String runId = dexClient.startWorkflow(ResettableTimerWorkflow.class, workflowId, TIMEOUT_SECONDS, null);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/reset")
    ResponseEntity<String> resetResettableTimer(@RequestParam String workflowId) {
        final ResettableTimerWorkflow rpcStub = dexClient.newRpcStub(ResettableTimerWorkflow.class, workflowId);
        dexClient.invokeRPC(rpcStub::sendResetMessage);
        return ResponseEntity.ok("reset");
    }

    @GetMapping("/parallel/start/simple")
    ResponseEntity<String> startParallelSimple(@RequestParam String workflowId) {
        final JobSeeker jobSeeker = new JobSeeker("123", "jobseeker@indeed.com", "0987654321");

        final String runId = dexClient.startWorkflow(SimpleParallelStatesWorkflow.class, workflowId, 3600, jobSeeker);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/parallel/start/withAwait")
    ResponseEntity<String> startParallelWithAwait(@RequestParam String workflowId) {
        final String runId = dexClient.startWorkflow(ParallelStatesWithAwaitWorkflow.class, workflowId, 3600, 50);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/recovery/start")
    ResponseEntity<String> startRecovery(
            @RequestParam final String workflowId,
            @RequestParam final String itemName,
            @RequestParam final int quantity
    ) {
        dexClient.startWorkflow(FailureRecoveryWorkflow.class, workflowId, TIMEOUT_SECONDS, ImmutableFailureRecoveryWorkflowInput.builder()
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

        dexClient.startWorkflow(
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

        dexClient.startWorkflow(
                ParentWorkflowV2.class, workflowId, 3600, numOfChildWfs,
                WorkflowOptions.basicBuilder().workflowIdReusePolicy(ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY).build());

        return ResponseEntity.ok("success");
    }

    @GetMapping("/drainchannels/internal/start")
    ResponseEntity<String> startDrainInternalChannels(@RequestParam final String workflowId) {
        final String runId = dexClient.startWorkflow(DrainInternalChannelsWorkflow.class, workflowId, 3600);
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/drainchannels/signal/startorsignal")
    ResponseEntity<String> startDrainSignalChannels(@RequestParam final String workflowId) throws InterruptedException {
        String response;
        try {
            dexClient.signalWorkflow(DrainSignalChannelsWorkflow.class, workflowId, QUEUE_SIGNAL_CHANNEL, "signal from startorsignal endpoint");
            response = "Signaled the workflow";
        } catch (final NoRunningWorkflowException e) {
            final String runId = dexClient.startWorkflow(DrainSignalChannelsWorkflow.class, workflowId, 3600, "first message from start");
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
        dexClient.startWorkflow(
                WaitForStateCompletionWorkflow.class,
                workflowId,
                3600,
                data,
                WorkflowOptions.extendedBuilder()
                        .waitForCompletionState(PersistDataState.class)
                        .getBuilder()
                        .build());
        dexClient.waitForStateExecutionCompletion(workflowId, PersistDataState.class);
        final WaitForStateCompletionWorkflow rpcStub = dexClient.newRpcStub(WaitForStateCompletionWorkflow.class, workflowId);
        final JobSeekerData persistedData = dexClient.invokeRPC(rpcStub::getJobSeekerData);

        return ResponseEntity.ok(String.format("success for workflow %s with data %s", workflowId, objectMapper.writeValueAsString(persistedData)));
    }

    @GetMapping("/timeout/start")
    ResponseEntity<String> startTimeoutWorkflow(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "true") final Boolean successfulWorkflow
    ) {
        dexClient.startWorkflow(HandlingTimeoutWorkflow.class, workflowId, 3600, successfulWorkflow);

        return ResponseEntity.ok(String.format("success for workflow %s", workflowId));
    }
}
