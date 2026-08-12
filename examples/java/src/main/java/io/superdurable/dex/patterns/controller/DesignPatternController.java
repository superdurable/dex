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

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.controller.ExampleFlows;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.patterns.workflow.drainchannels.signal.DrainSignalChannelsFlow;
import io.superdurable.dex.patterns.workflow.drainchannels.internal.DrainInternalChannelsFlow;
import io.superdurable.dex.patterns.workflow.interruptible.InterruptibleExecutionFlow;
import io.superdurable.dex.patterns.workflow.intervention.ManualInterventionFlow;
import io.superdurable.dex.patterns.workflow.parallel.JobSeeker;
import io.superdurable.dex.patterns.workflow.parallel.ParallelStatesWithAwaitFlow;
import io.superdurable.dex.patterns.workflow.parallel.SimpleParallelStatesFlow;
import io.superdurable.dex.patterns.workflow.parentchild.ParentFlowV2;
import io.superdurable.dex.patterns.workflow.polling.BackoffPollingFlow;
import io.superdurable.dex.patterns.workflow.polling.SimplePollingFlow;
import io.superdurable.dex.patterns.workflow.recovery.FailureRecoveryFlow;
import io.superdurable.dex.patterns.workflow.recovery.FailureRecoveryWorkflowInput;
import io.superdurable.dex.patterns.workflow.reminders.ReminderFlow;
import io.superdurable.dex.patterns.workflow.resettabletimer.ResettableTimerFlow;
import io.superdurable.dex.patterns.workflow.scalableparallel.RequestReceiverFlow;
import io.superdurable.dex.patterns.workflow.entitystore.UserProfile;
import io.superdurable.dex.patterns.workflow.entitystore.UserProfileFlow;
import io.superdurable.dex.patterns.workflow.entitystore.UserProfileRequest;
import io.superdurable.dex.patterns.workflow.timeout.FlowGracefulTimeout;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.JobSeekerData;
import io.superdurable.dex.patterns.workflow.waitforstatecompletion.WaitForStateCompletionFlow;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;

@RestController
@RequestMapping("/design-pattern")
public class DesignPatternController {
    private final Client client;

    private final SimplePollingFlow simplePollingFlow;
    private final BackoffPollingFlow backoffPollingFlow;
    private final InterruptibleExecutionFlow interruptibleExecutionFlow;
    private final ReminderFlow reminderFlow;
    private final UserProfileFlow userProfileFlow;
    private final ManualInterventionFlow manualInterventionFlow;
    private final ResettableTimerFlow resettableTimerFlow;
    private final SimpleParallelStatesFlow simpleParallelStatesFlow;
    private final ParallelStatesWithAwaitFlow parallelStatesWithAwaitFlow;
    private final FailureRecoveryFlow failureRecoveryFlow;
    private final RequestReceiverFlow requestReceiverFlow;
    private final ParentFlowV2 parentFlowV2;
    private final DrainInternalChannelsFlow drainInternalChannelsFlow;
    private final DrainSignalChannelsFlow drainSignalChannelsFlow;
    private final WaitForStateCompletionFlow waitForStateCompletionFlow;
    private final FlowGracefulTimeout flowGracefulTimeout;

    public DesignPatternController(
            final Client client,
            final SimplePollingFlow simplePollingFlow,
            final BackoffPollingFlow backoffPollingFlow,
            final InterruptibleExecutionFlow interruptibleExecutionFlow,
            final ReminderFlow reminderFlow,
            final UserProfileFlow userProfileFlow,
            final ManualInterventionFlow manualInterventionFlow,
            final ResettableTimerFlow resettableTimerFlow,
            final SimpleParallelStatesFlow simpleParallelStatesFlow,
            final ParallelStatesWithAwaitFlow parallelStatesWithAwaitFlow,
            final FailureRecoveryFlow failureRecoveryFlow,
            final RequestReceiverFlow requestReceiverFlow,
            final ParentFlowV2 parentFlowV2,
            final DrainInternalChannelsFlow drainInternalChannelsFlow,
            final DrainSignalChannelsFlow drainSignalChannelsFlow,
            final WaitForStateCompletionFlow waitForStateCompletionFlow,
            final FlowGracefulTimeout flowGracefulTimeout) {
        this.client = client;
        this.simplePollingFlow = simplePollingFlow;
        this.backoffPollingFlow = backoffPollingFlow;
        this.interruptibleExecutionFlow = interruptibleExecutionFlow;
        this.reminderFlow = reminderFlow;
        this.userProfileFlow = userProfileFlow;
        this.manualInterventionFlow = manualInterventionFlow;
        this.resettableTimerFlow = resettableTimerFlow;
        this.simpleParallelStatesFlow = simpleParallelStatesFlow;
        this.parallelStatesWithAwaitFlow = parallelStatesWithAwaitFlow;
        this.failureRecoveryFlow = failureRecoveryFlow;
        this.requestReceiverFlow = requestReceiverFlow;
        this.parentFlowV2 = parentFlowV2;
        this.drainInternalChannelsFlow = drainInternalChannelsFlow;
        this.drainSignalChannelsFlow = drainSignalChannelsFlow;
        this.waitForStateCompletionFlow = waitForStateCompletionFlow;
        this.flowGracefulTimeout = flowGracefulTimeout;
    }

    @GetMapping("/polling/start/simple")
    ResponseEntity<String> startSimple(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                simplePollingFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/polling/start/backoff")
    ResponseEntity<String> startBackoffPolling(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                backoffPollingFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/start")
    ResponseEntity<String> startInterruptible(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                interruptibleExecutionFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/interruptible/cancel")
    ResponseEntity<String> cancelInterruptible(@RequestParam final String workflowId) {
        final InterruptibleExecutionFlow stub =
                client.newRpcStub(InterruptibleExecutionFlow.class, workflowId);
        client.invokeRPC(stub::interrupt);
        return ResponseEntity.ok("done");
    }

    @GetMapping("/workflow-with-reminder/start")
    public ResponseEntity<String> start() {
        final String wfId = "reminder_test_id_" + System.nanoTime();
        client.startFlow(reminderFlow, wfId, null, ExampleFlows.startOptions());
        return ResponseEntity.ok(String.format("started workflowId: %s", wfId));
    }

    @GetMapping("/workflow-with-reminder/accept")
    public ResponseEntity<String> accept(@RequestParam final String workflowId) {
        final ReminderFlow stub = client.newRpcStub(ReminderFlow.class, workflowId);
        client.invokeRPC(stub::accept);
        return ResponseEntity.ok("accepted");
    }

    @GetMapping("/workflow-with-reminder/optout")
    public ResponseEntity<String> optout(@RequestParam final String workflowId) {
        client.publish(workflowId, reminderFlow.optOutReminder, (Void) null);
        return ResponseEntity.ok("done");
    }

    @PostMapping("/entity-store/profile")
    ResponseEntity<String> createUserProfile(@RequestBody final UserProfileRequest request) {
        final UserProfile profile = request.toProfile();
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofHours(1))
                .addAttribute(userProfileFlow.displayName, profile.displayName)
                .addAttribute(userProfileFlow.email, profile.email)
                .addAttribute(userProfileFlow.marketingOptIn, profile.marketingOptIn)
                .addAttribute(userProfileFlow.credits, profile.credits)
                .addAttribute(userProfileFlow.weight, profile.weight)
                .addAttribute(userProfileFlow.lastLoggedInTime, profile.lastLoggedInTime)
                .addAttribute(userProfileFlow.metadata, profile.metadata)
                .configOverride(FlowConfig.newBuilder()
                        .attributeStoreName(UserProfileFlow.STORE_NAME)
                        .build())
                .build();
        final String runId = client.startFlow(
                userProfileFlow,
                request.userId,
                null,
                options);
        return ResponseEntity.ok(runId);
    }

    @PostMapping("/entity-store/profile/update")
    ResponseEntity<String> updateUserProfile(@RequestBody final UserProfileRequest request) {
        final UserProfileFlow stub =
                client.newRpcStub(UserProfileFlow.class, request.userId);
        client.invokeRPC(stub::updateProfile, request.toProfile());
        return ResponseEntity.ok("Updated user profile");
    }

    @GetMapping("/entity-store/profile")
    ResponseEntity<UserProfile> getUserProfile(@RequestParam final String userId) {
        final UserProfileFlow stub = client.newRpcStub(UserProfileFlow.class, userId);
        return ResponseEntity.ok(client.invokeRPC(stub::getProfile));
    }

    @PostMapping("/entity-store/profile/clear")
    ResponseEntity<String> clearUserProfile(@RequestParam final String userId) {
        final UserProfileFlow stub = client.newRpcStub(UserProfileFlow.class, userId);
        client.invokeRPC(stub::clearProfile);
        return ResponseEntity.ok("Cleared user profile");
    }

    @GetMapping("/intervention/start")
    ResponseEntity<String> startIntervention(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                manualInterventionFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/start")
    ResponseEntity<String> startResettableTimer(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                resettableTimerFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/resettabletimer/reset")
    ResponseEntity<String> resetResettableTimer(@RequestParam final String workflowId) {
        final ResettableTimerFlow stub =
                client.newRpcStub(ResettableTimerFlow.class, workflowId);
        client.invokeRPC(stub::sendResetMessage);
        return ResponseEntity.ok("reset");
    }

    @GetMapping("/parallel/start/simple")
    ResponseEntity<String> startParallelSimple(@RequestParam final String workflowId) {
        final JobSeeker jobSeeker =
                new JobSeeker("123", "jobseeker@indeed.com", "0987654321");
        final String runId = client.startFlow(
                simpleParallelStatesFlow,
                workflowId,
                jobSeeker,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/parallel/start/withAwait")
    ResponseEntity<String> startParallelWithAwait(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                parallelStatesWithAwaitFlow,
                workflowId,
                50,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/recovery/start")
    ResponseEntity<String> startRecovery(
            @RequestParam final String workflowId,
            @RequestParam final String itemName,
            @RequestParam final int quantity) {
        client.startFlow(
                failureRecoveryFlow,
                workflowId,
                new FailureRecoveryWorkflowInput(itemName, quantity),
                ExampleFlows.startOptions());
        return ResponseEntity.ok("recovery workflow started");
    }

    @GetMapping("scalableparallel/start")
    ResponseEntity<String> scalableparallel(
            @RequestParam final String workflowId,
            @RequestParam final int numOfChildWfs) {
        client.startFlow(
                requestReceiverFlow,
                workflowId,
                numOfChildWfs,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofHours(1))
                        .idReusePolicy(IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED)
                        .build());
        return ResponseEntity.ok("success");
    }

    @GetMapping("parentchild/start")
    ResponseEntity<String> parentchild(
            @RequestParam final String workflowId,
            @RequestParam final int numOfChildWfs) {
        client.startFlow(
                parentFlowV2,
                workflowId,
                numOfChildWfs,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofHours(1))
                        .idReusePolicy(IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED)
                        .build());
        return ResponseEntity.ok("success");
    }

    @GetMapping("/drainchannels/internal/start")
    ResponseEntity<String> startDrainInternalChannels(@RequestParam final String workflowId) {
        final String runId = client.startFlow(
                drainInternalChannelsFlow,
                workflowId,
                null,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/drainchannels/signal/startorsignal")
    ResponseEntity<String> startDrainSignalChannels(@RequestParam final String workflowId) {
        String response;
        try {
            client.publish(
                    workflowId,
                    drainSignalChannelsFlow.queueSignalChannel,
                    "signal from startorsignal endpoint");
            response = "Signaled the workflow";
        } catch (final FlowNotActiveException inactive) {
            final String runId = client.startFlow(
                    drainSignalChannelsFlow,
                    workflowId,
                    "first message from start",
                    ExampleFlows.startOptions());
            response = "Started the workflow with runId " + runId;
        }
        return ResponseEntity.ok(response);
    }

    @GetMapping("/waitforstatecompletion/start")
    ResponseEntity<String> startWaitForStateCompletion(@RequestParam final String workflowId)
            throws JsonProcessingException {
        final ObjectMapper objectMapper = new ObjectMapper();
        final JobSeekerData data = new JobSeekerData(1);
        client.startFlow(
                waitForStateCompletionFlow,
                workflowId,
                data,
                ExampleFlows.startOptions());
        client.waitForStepCompletion(
                workflowId,
                StepExecutionId.of("PersistData"),
                Duration.ofMinutes(5));
        final WaitForStateCompletionFlow stub =
                client.newRpcStub(WaitForStateCompletionFlow.class, workflowId);
        final JobSeekerData persistedData = client.invokeRPC(stub::getJobSeekerData);
        return ResponseEntity.ok(String.format(
                "success for workflow %s with data %s",
                workflowId,
                objectMapper.writeValueAsString(persistedData)));
    }

    @GetMapping("/timeout/start")
    ResponseEntity<String> startTimeoutWorkflow(
            @RequestParam final String workflowId,
            @RequestParam(defaultValue = "true") final Boolean successfulWorkflow) {
        client.startFlow(
                flowGracefulTimeout,
                workflowId,
                successfulWorkflow,
                ExampleFlows.startOptions());
        return ResponseEntity.ok(String.format("success for workflow %s", workflowId));
    }
}
