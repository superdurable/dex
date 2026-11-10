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

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

/** WCP-38: a locked live-thread counter publishes a sentinel once no thread can reach the merge. */
@Component
public class GeneralSynchronizingMergeFlow
        implements Flow<GeneralSynchronizingMergeFlow.DocumentReviewPlan> {
    public static final Attribute<Integer> liveBranchCount =
            Attribute.define("liveBranchCount", Integer.class);
    public static final Attribute<String[]> mergedReviews =
            Attribute.define("mergedReviews", String[].class);
    public static final Channel<String> mergeSentinel =
            Channel.define("mergeSentinel", String.class);

    /** Which checks run after drafting, how often finance sends the draft back, and archiving. */
    public record DocumentReviewPlan(
            boolean isLegalCheckNeeded,
            boolean isFinanceCheckNeeded,
            int financeReworkRounds,
            boolean isFinanceArchived) {
    }

    /** One draft revision of the document under review. */
    public record DocumentDraft(DocumentReviewPlan plan, int revision) {
    }

    private final StartDocumentReview startDocumentReview = new StartDocumentReview();
    private final DraftDocument draftDocument = new DraftDocument();
    private final CheckLegalTerms checkLegalTerms = new CheckLegalTerms();
    private final CheckFinanceTerms checkFinanceTerms = new CheckFinanceTerms();
    private final ArchiveDocument archiveDocument = new ArchiveDocument();
    private final PublishDocument publishDocument = new PublishDocument();

    @Override
    public StepList<DocumentReviewPlan> getSteps() {
        return StepList.startStep(startDocumentReview).otherSteps(
                draftDocument, checkLegalTerms, checkFinanceTerms, archiveDocument, publishDocument);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(liveBranchCount, mergedReviews, mergeSentinel);
    }

    private static StepOptions liveBranchCountLockOptions() {
        return StepOptions.newBuilder().addExecuteLock(AttributeLock.of(liveBranchCount)).build();
    }

    private static void changeLiveBranchCount(final Context context, final int delta) {
        final Integer current = liveBranchCount.get(context);
        final int updated = (current == null ? 0 : current) + delta;
        liveBranchCount.set(context, updated);
        if (updated == 0) {
            mergeSentinel.publish(context, "done");
        }
    }

    private static StepDecision arriveAtMerge(final Context context, final String review) {
        final String[] merged = mergedReviews.get(context);
        final List<String> updated = new ArrayList<String>(
                merged == null ? List.of() : List.of(merged));
        updated.add(review);
        mergedReviews.set(context, updated.toArray(new String[0]));
        changeLiveBranchCount(context, -1);
        return StepDecision.deadEnd();
    }

    static final class StartDocumentReview implements Step<DocumentReviewPlan> {
        @Override
        public Class<DocumentReviewPlan> getInputType() {
            return DocumentReviewPlan.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return liveBranchCountLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final DocumentReviewPlan plan) {
            liveBranchCount.set(context, 1);
            return StepDecision.goToMany(
                    StepMovement.of(DraftDocument.class, new DocumentDraft(plan, 0)),
                    StepMovement.of(PublishDocument.class, null));
        }
    }

    static final class DraftDocument implements Step<DocumentDraft> {
        @Override
        public Class<DocumentDraft> getInputType() {
            return DocumentDraft.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return liveBranchCountLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final DocumentDraft draft) {
            final boolean isRework = draft.revision() > 0;
            final List<StepMovement<?>> checks = new ArrayList<StepMovement<?>>();
            if (isRework || draft.plan().isLegalCheckNeeded()) {
                checks.add(StepMovement.of(CheckLegalTerms.class, draft.revision()));
            }
            if (isRework || draft.plan().isFinanceCheckNeeded()) {
                checks.add(StepMovement.of(CheckFinanceTerms.class, draft));
            }
            changeLiveBranchCount(context, checks.size() - 1);
            if (checks.isEmpty()) {
                return StepDecision.deadEnd();
            }
            return StepDecision.goToMany(checks.toArray(new StepMovement<?>[0]));
        }
    }

    static final class CheckLegalTerms implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return liveBranchCountLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Integer revision) {
            return arriveAtMerge(context, getStepType() + "-" + revision);
        }
    }

    static final class CheckFinanceTerms implements Step<DocumentDraft> {
        @Override
        public Class<DocumentDraft> getInputType() {
            return DocumentDraft.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return liveBranchCountLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final DocumentDraft draft) {
            if (draft.revision() < draft.plan().financeReworkRounds()) {
                return StepDecision.goTo(
                        DraftDocument.class, new DocumentDraft(draft.plan(), draft.revision() + 1));
            }
            if (draft.plan().isFinanceArchived()) {
                changeLiveBranchCount(context, -1);
                return StepDecision.goTo(ArchiveDocument.class, null);
            }
            return arriveAtMerge(context, getStepType() + "-" + draft.revision());
        }
    }

    static final class ArchiveDocument implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class PublishDocument implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return liveBranchCountLockOptions();
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(mergeSentinel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String[] merged = mergedReviews.get(context);
            return StepDecision.gracefulComplete(
                    (merged == null ? List.<String>of() : List.of(merged)).stream().sorted().toList());
        }
    }
}
