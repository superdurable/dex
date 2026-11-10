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

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.patterns.cancelcase.LoanApplicationFlow;
import io.superdurable.dex.patterns.cancelcase.LoanApplicationFlow.LoanApplication;
import io.superdurable.dex.patterns.criticalsection.ContractEditFlow;
import io.superdurable.dex.patterns.criticalsection.ContractEditFlow.ContractEdit;
import io.superdurable.dex.patterns.criticalsection.ContractEditFlow.ContractEditRecord;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Asserts the deferred choice, critical section, and cancel case pattern examples. */
@ExtendWith(SharedIntegExtension.class)
public class ControlStructurePatternsIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    @Test
    void deferredChoiceRoutesOnTheAcceptResponse() {
        assertOfferResponse(
                flowId -> client().invokeRPC(offerStub(flowId)::acceptCustomerOffer, "looks good"),
                "FulfillOffer",
                "fulfilled offer-1");
    }

    @Test
    void deferredChoiceRoutesOnTheRejectResponse() {
        assertOfferResponse(
                flowId -> client().invokeRPC(offerStub(flowId)::rejectCustomerOffer, "too expensive"),
                "CloseRejectedOffer",
                "rejected offer-1");
    }

    @Test
    void deferredChoiceRoutesOnTheOfferExpiry() {
        assertOfferResponse(
                ControlStructurePatternsIntegTest::skipOfferExpiry, "ExpireOffer", "expired offer-1");
    }

    @Test
    void deferredChoiceConsumesOnlyTheWinningResponse() {
        final String flowId =
                startPatternFlow(OfferResponseFlow.class, "deferred-choice-race", "offer-2");
        final OfferResponseFlow stub = offerStub(flowId);
        client().invokeRPC(stub::acceptCustomerOffer, "yes");
        client().invokeRPC(stub::rejectCustomerOffer, "no");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(1, completions.size());
        assertTrue(Set.of("FulfillOffer", "CloseRejectedOffer")
                .contains(completions.get(0).getStepType()));
    }

    @Test
    void criticalSectionRunsOneEditAtATime() {
        final String flowId = startPatternFlow(
                ContractEditFlow.class,
                "critical-section",
                new ContractEdit[] {
                    new ContractEdit("legal", false, false, false, 0L),
                    new ContractEdit("finance", false, false, false, 0L),
                    new ContractEdit("procurement", false, false, false, 0L),
                });
        final List<ContractEditRecord> records = contractEditRecords(flowId);
        assertEquals(
                Set.of("legal", "finance", "procurement"),
                Set.copyOf(records.stream().map(ContractEditRecord::reviewer).toList()));
        assertEquals(3, records.size());
        for (final ContractEditRecord record : records) {
            assertEquals("saved", record.outcome());
        }
        assertContractEditsDoNotOverlap(records);
    }

    @Test
    void criticalSectionReturnsTheTokenWhenAnEditFails() {
        final String flowId = startPatternFlow(
                ContractEditFlow.class,
                "critical-section-failure",
                new ContractEdit[] {
                    new ContractEdit("legal", false, false, true, 0L),
                    new ContractEdit("finance", false, false, false, 0L),
                });
        final List<ContractEditRecord> records = contractEditRecords(flowId);
        final Map<String, String> outcomes = new HashMap<String, String>();
        for (final ContractEditRecord record : records) {
            outcomes.put(record.reviewer(), record.outcome());
        }
        assertEquals(Map.of("legal", "failed", "finance", "saved"), outcomes);
        assertContractEditsDoNotOverlap(records);
    }

    @Test
    void criticalSectionReturnsTheTokenWhenTheHandoffFails() {
        assertContractEditOutcomes(
                "critical-section-handoff-failure",
                new ContractEdit[] {
                    new ContractEdit("legal", true, false, false, 0L),
                    new ContractEdit("finance", false, false, false, 0L),
                },
                Map.of("legal", "failed", "finance", "saved"));
    }

    @Test
    void criticalSectionReturnsTheTokenOnAnEarlyExit() {
        assertContractEditOutcomes(
                "critical-section-withdrawal",
                new ContractEdit[] {
                    new ContractEdit("legal", false, true, false, 0L),
                    new ContractEdit("finance", false, false, false, 0L),
                },
                Map.of("legal", "withdrawn", "finance", "saved"));
    }

    @Test
    void cancelCaseStopsTheRunningChildFlow() {
        final String flowId = startPatternFlow(
                LoanApplicationFlow.class, "cancel-case", new LoanApplication("applicant-1", 3600));
        client().invokeRPC(loanStub(flowId)::cancelApplication, "applicant withdrew");
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus(), result.getErrorMessage());
        assertTrue(result.getErrorMessage().contains("application canceled: applicant withdrew"),
                result.getErrorMessage());
        final String childFlowId = client()
                .getAttributes(flowId, LoanApplicationFlow.stoppedCreditCheckFlowId)
                .get(LoanApplicationFlow.stoppedCreditCheckFlowId)
                .orElseThrow(() -> new AssertionError("stoppedCreditCheckFlowId was not set"));
        assertFalse(childFlowId.isEmpty());
        awaitCondition(
                () -> client().describeFlow(childFlowId).getStatus() == FlowStatus.CANCELED,
                "child Flow " + childFlowId + " never reached CANCELED");
    }

    @Test
    void cancelCaseAfterTheChildFlowFinished() {
        final String flowId = startPatternFlow(
                LoanApplicationFlow.class, "cancel-case-late", new LoanApplication("applicant-2", 1));
        waitForStep(flowId, "RunCreditCheck");
        client().invokeRPC(loanStub(flowId)::cancelApplication, "applicant withdrew");
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.FAILED, result.getStatus(), result.getErrorMessage());
        assertTrue(result.getErrorMessage().contains("application canceled: applicant withdrew"),
                result.getErrorMessage());
    }

    @Test
    void cancelCaseCompletesWithTheUnderwriterDecision() {
        final String flowId = startPatternFlow(
                LoanApplicationFlow.class, "cancel-case-decided", new LoanApplication("applicant-3", 1));
        waitForStep(flowId, "RunCreditCheck");
        client().invokeRPC(loanStub(flowId)::decideApplication, "approved");
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of("approved after credit report for applicant-3"),
                outputsOf(completions, "AwaitUnderwriterDecision", String.class));
    }

    private static void assertOfferResponse(
            final Consumer<String> respond, final String expectedStep, final String expectedOutput) {
        final String flowId = startPatternFlow(OfferResponseFlow.class, "deferred-choice", "offer-1");
        respond.accept(flowId);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(
                List.of(expectedStep),
                completions.stream().map(StepCompletion::getStepType).toList());
        assertEquals(List.of(expectedOutput), outputsOf(completions, expectedStep, String.class));
    }

    // Retries until the Timer of AwaitOfferResponse is registered.
    private static void skipOfferExpiry(final String flowId) {
        awaitCondition(() -> {
            try {
                client().skipTimer(
                        flowId,
                        StepExecutionId.of("AwaitOfferResponse", 1),
                        TimerId.byConditionIndex(0));
                return true;
            } catch (final DexServiceException notReady) {
                return false;
            }
        }, flowId + " offer expiry Timer was never skipped");
    }

    private static void assertContractEditOutcomes(
            final String prefix,
            final ContractEdit[] edits,
            final Map<String, String> expectedOutcomes) {
        final List<ContractEditRecord> records =
                contractEditRecords(startPatternFlow(ContractEditFlow.class, prefix, edits));
        final Map<String, String> outcomes = new HashMap<String, String>();
        for (final ContractEditRecord record : records) {
            outcomes.put(record.reviewer(), record.outcome());
        }
        assertEquals(expectedOutcomes, outcomes);
        assertContractEditsDoNotOverlap(records);
    }

    private static List<ContractEditRecord> contractEditRecords(final String flowId) {
        final List<ContractEditRecord[]> outputs = outputsOf(
                waitForCompletedFlow(flowId), "FinishContractEdits", ContractEditRecord[].class);
        assertEquals(1, outputs.size());
        return List.of(outputs.get(0));
    }

    private static void assertContractEditsDoNotOverlap(final List<ContractEditRecord> records) {
        for (int leftIndex = 0; leftIndex < records.size(); leftIndex++) {
            for (int rightIndex = leftIndex + 1; rightIndex < records.size(); rightIndex++) {
                final ContractEditRecord left = records.get(leftIndex);
                final ContractEditRecord right = records.get(rightIndex);
                final boolean isDisjoint = left.finishedAtMillis() <= right.startedAtMillis()
                        || right.finishedAtMillis() <= left.startedAtMillis();
                assertTrue(isDisjoint, left + " overlaps " + right);
            }
        }
    }

    private static IntegEnvironment environment() {
        return SharedIntegExtension.environment();
    }

    private static Client client() {
        return environment().client();
    }

    private static OfferResponseFlow offerStub(final String flowId) {
        return client().newRpcStub(OfferResponseFlow.class, flowId);
    }

    private static LoanApplicationFlow loanStub(final String flowId) {
        return client().newRpcStub(LoanApplicationFlow.class, flowId);
    }

    private static <I, F extends Flow<I>> String startPatternFlow(
            final Class<F> flowClass, final String prefix, final I input) {
        final String flowId = environment().newFlowId("pattern-" + prefix);
        client().startFlow(
                environment().workflowPatternFlow(flowClass), flowId, input, environment().startOptions());
        return flowId;
    }

    private static List<StepCompletion> waitForCompletedFlow(final String flowId) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        return result.getCompletions();
    }

    private static void waitForStep(final String flowId, final String stepType) {
        client().waitForStepCompletion(
                flowId,
                StepExecutionId.of(stepType, 1),
                WaitForStepCompletionOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
    }

    private static <T> List<T> outputsOf(
            final List<StepCompletion> completions, final String stepType, final Class<T> outputType) {
        return completions.stream()
                .filter(completion -> completion.getStepType().equals(stepType))
                .map(completion -> completion.getOutput(outputType))
                .toList();
    }

    private static void awaitCondition(
            final BooleanSupplier condition, final String message) {
        try {
            environment().awaitCondition(
                    condition::getAsBoolean, Boolean::booleanValue, WAIT_TIMEOUT, message);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted waiting: " + message, interrupted);
        }
    }
}
