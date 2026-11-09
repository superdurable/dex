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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import org.springframework.stereotype.Component;

/** WCP-10 Arbitrary Cycles: a cycle between two Steps with two entry points. */
@Component
public class ArbitraryCyclesFlow implements Flow<ArbitraryCyclesFlow.ArticleSubmission> {
    private static final int FINAL_REVISION = 1;

    /** Whether the submission already contains a draft. */
    public record ArticleSubmission(boolean hasDraft) {
    }

    /** The article revision and the Step types run so far. */
    public record ArticleRevision(int revision, String[] trail) {
    }

    private final RouteSubmission routeSubmission = new RouteSubmission();
    private final DraftArticle draftArticle = new DraftArticle();
    private final EditArticle editArticle = new EditArticle();
    private final PublishArticle publishArticle = new PublishArticle();

    @Override
    public StepList<ArticleSubmission> getSteps() {
        return StepList.startStep(routeSubmission).otherSteps(draftArticle, editArticle, publishArticle);
    }

    static final class RouteSubmission implements Step<ArticleSubmission> {
        @Override
        public Class<ArticleSubmission> getInputType() {
            return ArticleSubmission.class;
        }

        @Override
        public StepDecision execute(final Context context, final ArticleSubmission submission) {
            final ArticleRevision revision = new ArticleRevision(0, new String[] {getStepType()});
            if (submission.hasDraft()) {
                return StepDecision.goTo(EditArticle.class, revision);
            }
            return StepDecision.goTo(DraftArticle.class, revision);
        }
    }

    static final class DraftArticle implements Step<ArticleRevision> {
        @Override
        public Class<ArticleRevision> getInputType() {
            return ArticleRevision.class;
        }

        @Override
        public StepDecision execute(final Context context, final ArticleRevision revision) {
            return StepDecision.goTo(
                    EditArticle.class,
                    new ArticleRevision(
                            revision.revision(), StepTrail.append(revision.trail(), getStepType())));
        }
    }

    static final class EditArticle implements Step<ArticleRevision> {
        @Override
        public Class<ArticleRevision> getInputType() {
            return ArticleRevision.class;
        }

        @Override
        public StepDecision execute(final Context context, final ArticleRevision revision) {
            final String[] trail = StepTrail.append(revision.trail(), getStepType());
            if (revision.revision() >= FINAL_REVISION) {
                return StepDecision.goTo(PublishArticle.class, trail);
            }
            return StepDecision.goTo(
                    DraftArticle.class, new ArticleRevision(revision.revision() + 1, trail));
        }
    }

    static final class PublishArticle implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] trail) {
            return StepDecision.gracefulComplete(StepTrail.append(trail, getStepType()));
        }
    }
}
