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

package io.superdurable.dex.patterns.criticalsection;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

/**
 * Lets several reviewers edit one contract, one at a time. A one-message Channel is the token that
 * guards the two-Step edit section.
 */
@Component
public class ContractEditFlow implements Flow<ContractEditFlow.ContractEdit[]> {
    private static final Duration CONTRACT_EDIT_DURATION = Duration.ofMillis(100);
    private static final String TOKEN = "contract";

    /** One reviewer's turn to edit the shared contract. */
    public record ContractEdit(String reviewer, boolean shouldFailEdit, long startedAtMillis) {
    }

    /** When one edit held the contract and how it ended. */
    public record ContractEditRecord(
            String reviewer, String outcome, long startedAtMillis, long finishedAtMillis) {
    }

    public static final Channel<String> contractEditToken =
            Channel.define("contractEditToken", String.class);
    public static final Channel<ContractEditRecord> contractEditFinished =
            Channel.define("contractEditFinished", ContractEditRecord.class);

    private final StartContractEdits startContractEdits = new StartContractEdits();
    private final EnterContractEdit enterContractEdit = new EnterContractEdit();
    private final SaveContractEdit saveContractEdit = new SaveContractEdit();
    private final ReleaseContractEditToken releaseContractEditToken =
            new ReleaseContractEditToken();
    private final FinishContractEdits finishContractEdits = new FinishContractEdits();

    @Override
    public StepList<ContractEdit[]> getSteps() {
        return StepList.startStep(startContractEdits).otherSteps(
                enterContractEdit, saveContractEdit, releaseContractEditToken, finishContractEdits);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(contractEditToken, contractEditFinished);
    }

    /** Routes an execute failure after the token was consumed to the Step that returns it. */
    private static StepOptions contractEditOptions() {
        return StepOptions.newBuilder()
                .executeRetry(RetryPolicy.newBuilder()
                        .initialInterval(Duration.ofSeconds(1))
                        .maximumAttempts(2)
                        .build())
                .onExecuteFailureProceedTo(ReleaseContractEditToken.class)
                .build();
    }

    /** Publishes the only token and starts one branch per reviewer. */
    static final class StartContractEdits implements Step<ContractEdit[]> {
        @Override
        public Class<ContractEdit[]> getInputType() {
            return ContractEdit[].class;
        }

        @Override
        public StepDecision execute(final Context context, final ContractEdit[] edits) {
            contractEditToken.publish(context, TOKEN);
            final StepMovement<?>[] movements = new StepMovement<?>[edits.length + 1];
            for (int index = 0; index < edits.length; index++) {
                movements[index] = StepMovement.of(EnterContractEdit.class, edits[index]);
            }
            movements[edits.length] = StepMovement.of(FinishContractEdits.class, edits.length);
            return StepDecision.goToMany(movements);
        }
    }

    /** Enters the section by consuming the token. */
    static final class EnterContractEdit implements Step<ContractEdit> {
        @Override
        public Class<ContractEdit> getInputType() {
            return ContractEdit.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return contractEditOptions();
        }

        @Override
        public Wait waitFor(final Context context, final ContractEdit edit) {
            return Wait.until(contractEditToken.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final ContractEdit edit) {
            final ContractEdit started = new ContractEdit(
                    edit.reviewer(), edit.shouldFailEdit(), System.currentTimeMillis());
            holdContract();
            return StepDecision.goTo(SaveContractEdit.class, started);
        }
    }

    /** The last Step of the section; it publishes the token back. */
    static final class SaveContractEdit implements Step<ContractEdit> {
        @Override
        public Class<ContractEdit> getInputType() {
            return ContractEdit.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return contractEditOptions();
        }

        @Override
        public StepDecision execute(final Context context, final ContractEdit edit) {
            holdContract();
            if (edit.shouldFailEdit()) {
                throw new IllegalStateException("contract edit failed");
            }
            leaveContractEdit(context, edit, "saved");
            return StepDecision.deadEnd();
        }
    }

    /** The failure exit of the section. */
    static final class ReleaseContractEditToken implements Step<ContractEdit> {
        @Override
        public Class<ContractEdit> getInputType() {
            return ContractEdit.class;
        }

        @Override
        public StepDecision execute(final Context context, final ContractEdit edit) {
            leaveContractEdit(context, edit, "failed");
            return StepDecision.deadEnd();
        }
    }

    /** Joins the branches after every reviewer leaves the section. */
    static final class FinishContractEdits implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer editCount) {
            return Wait.until(contractEditFinished.forN(editCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer editCount) {
            return StepDecision.gracefulComplete(contractEditFinished.getConditionResults(context));
        }
    }

    /** Records the edit and publishes the token for the next reviewer. */
    private static void leaveContractEdit(
            final Context context, final ContractEdit edit, final String outcome) {
        contractEditFinished.publish(context, new ContractEditRecord(
                edit.reviewer(), outcome, edit.startedAtMillis(), System.currentTimeMillis()));
        contractEditToken.publish(context, TOKEN);
    }

    private static void holdContract() {
        try {
            Thread.sleep(CONTRACT_EDIT_DURATION.toMillis());
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("contract edit interrupted", interrupted);
        }
    }
}
