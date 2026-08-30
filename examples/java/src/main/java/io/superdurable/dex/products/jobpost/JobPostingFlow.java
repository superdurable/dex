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

package io.superdurable.dex.products.jobpost;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.List;

@Component
public class JobPostingFlow implements Flow<Void> {
    public final Attribute<String> title = Attribute.define(
            "Title",
            String.class,
            new AttributeIndex(AttributeIndex.Type.FULL_TEXT));
    public final Attribute<String> jobDescription = Attribute.define(
            "JobDescription",
            String.class,
            new AttributeIndex(AttributeIndex.Type.FULL_TEXT));
    public final Attribute<Long> lastUpdateTimeMillis = Attribute.define(
            "LastUpdateTimeMillis",
            Long.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    public final Attribute<String> notes = Attribute.define("Notes", String.class);
    public final Attribute<Integer> updateVersion = Attribute.define("UpdateVersion", Integer.class);
    public final Attribute<Void> updatePostingLock =
            Attribute.define("UpdatePostingLock", Void.class);
    public final Channel<PostingUpdate> linkedInPostingUpdates =
            Channel.define("LinkedInPostingUpdates", PostingUpdate.class);
    public final Channel<PostingUpdate> indeedPostingUpdates =
            Channel.define("IndeedPostingUpdates", PostingUpdate.class);

    private final MyDependencyService service;
    private final InitStep init = new InitStep();
    private final UpdateLinkedInPosting updateLinkedInPosting = new UpdateLinkedInPosting();
    private final UpdateIndeedPosting updateIndeedPosting = new UpdateIndeedPosting();

    public JobPostingFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(init)
                .otherSteps(updateLinkedInPosting, updateIndeedPosting);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                title,
                jobDescription,
                lastUpdateTimeMillis,
                notes,
                updateVersion,
                updatePostingLock,
                linkedInPostingUpdates,
                indeedPostingUpdates);
    }

    @RPC
    public RPCResult<JobInfo> get(final Context context) {
        return RPCResult.of(readJobInfo(context));
    }

    @RPC
    public RPCResult<JobInfo> getWithStrongConsistency(final Context context) {
        return get(context);
    }

    @RPC(lockAttributes = {"UpdatePostingLock"})
    public RPCResult<Integer> update(final Context context, final JobInfo input) {
        final int version = updateVersion.get(context) + 1;
        title.set(context, input.title);
        jobDescription.set(context, input.description);
        lastUpdateTimeMillis.set(context, System.currentTimeMillis());
        if (input.notes != null) {
            notes.set(context, input.notes);
        }
        updateVersion.set(context, version);
        final PostingUpdate update = new PostingUpdate(
                version,
                context.getFlowId() + ":" + version,
                input);
        linkedInPostingUpdates.publish(context, update);
        indeedPostingUpdates.publish(context, update);
        return RPCResult.of(version);
    }

    private JobInfo readJobInfo(final Context context) {
        return new JobInfo(
                title.get(context),
                jobDescription.get(context),
                notes.get(context));
    }

    private StepOptions jobBoardUpdateOptions() {
        return StepOptions.newBuilder()
                .executeRetry(RetryPolicy.newBuilder()
                        .backoffCoefficient(2.0)
                        .maximumAttempts(100)
                        .totalDuration(Duration.ofHours(1))
                        .initialInterval(Duration.ofSeconds(3))
                        .maximumInterval(Duration.ofSeconds(60))
                        .build())
                .build();
    }

    final class InitStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(UpdateLinkedInPosting.class, null),
                    StepMovement.of(UpdateIndeedPosting.class, null));
        }
    }

    final class UpdateLinkedInPosting implements Step<Void> {
        private final StepOptions options = jobBoardUpdateOptions();

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return options;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(linkedInPostingUpdates.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<PostingUpdate> updates = linkedInPostingUpdates.getConditionResults(context);
            final PostingUpdate update = updates.get(0);
            service.updateExternalSystem(String.format(
                    "update LinkedIn job posting v%d [%s]: %s",
                    update.version,
                    update.idempotencyKey,
                    update.posting.title));
            return StepDecision.goTo(UpdateLinkedInPosting.class, null);
        }
    }

    final class UpdateIndeedPosting implements Step<Void> {
        private final StepOptions options = jobBoardUpdateOptions();

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return options;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(indeedPostingUpdates.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<PostingUpdate> updates = indeedPostingUpdates.getConditionResults(context);
            final PostingUpdate update = updates.get(0);
            service.updateExternalSystem(String.format(
                    "update Indeed job posting v%d [%s]: %s",
                    update.version,
                    update.idempotencyKey,
                    update.posting.title));
            return StepDecision.goTo(UpdateIndeedPosting.class, null);
        }
    }
}
