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
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class JobPostFlow implements Flow<Void> {
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

    private final MyDependencyService service;
    private final ExternalUpdate externalUpdate = new ExternalUpdate();

    public JobPostFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.withoutStartStep(externalUpdate);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(title, jobDescription, lastUpdateTimeMillis, notes);
    }

    @RPC
    public RPCResult<JobInfo> get(final Context context) {
        return RPCResult.of(readJobInfo(context));
    }

    @RPC
    public RPCResult<JobInfo> getWithStrongConsistency(final Context context) {
        return get(context);
    }

    @RPC
    public RPCResult<Void> update(final Context context, final JobInfo input) {
        title.set(context, input.title);
        jobDescription.set(context, input.description);
        lastUpdateTimeMillis.set(context, System.currentTimeMillis());
        if (input.notes != null) {
            notes.set(context, input.notes);
        }
        return RPCResult.of(null, StepMovement.of(ExternalUpdate.class, null));
    }

    private JobInfo readJobInfo(final Context context) {
        return new JobInfo(
                title.get(context),
                jobDescription.get(context),
                notes.get(context));
    }

    final class ExternalUpdate implements Step<Void> {
        private final StepOptions options = StepOptions.newBuilder()
                .executeRetry(RetryPolicy.newBuilder()
                        .backoffCoefficient(2.0)
                        .maximumAttempts(100)
                        .totalDuration(Duration.ofHours(1))
                        .initialInterval(Duration.ofSeconds(3))
                        .maximumInterval(Duration.ofSeconds(60))
                        .build())
                .build();

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return options;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            service.updateExternalSystem("this is an update to external service");
            return StepDecision.deadEnd();
        }
    }
}
