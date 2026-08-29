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

package io.superdurable.dex.patterns.waitforstepcompletion;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.shared.ServiceDependency;
import org.springframework.stereotype.Component;

@Component
public class WaitForStepCompletionFlow implements Flow<JobSeekerData> {
    public static final String JOB_SEEKER_DATA = "job_seeker_data";

    private static final ObjectMapper MAPPER = new ObjectMapper();

    public final Attribute<JobSeekerData> jobSeekerData =
            Attribute.define(JOB_SEEKER_DATA, JobSeekerData.class);

    private final ServiceDependency mongoCollection;
    private final ServiceDependency externalService;

    private final PersistData persistData = new PersistData();
    private final UpdateExternalSystem updateExternalSystem = new UpdateExternalSystem();

    public WaitForStepCompletionFlow(final ServiceDependency serviceDependency) {
        this.mongoCollection = serviceDependency;
        this.externalService = serviceDependency;
    }

    @Override
    public StepList<JobSeekerData> getSteps() {
        return StepList.startStep(persistData).otherSteps(updateExternalSystem);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(jobSeekerData);
    }

    @RPC
    public RPCResult<JobSeekerData> getJobSeekerData(final Context context) {
        final JobSeekerData data = jobSeekerData.get(context);
        if (data == null) {
            throw new IllegalStateException("Job seeker data was not persisted to the data store");
        }
        return RPCResult.of(data);
    }

    final class PersistData implements Step<JobSeekerData> {
        @Override
        public Class<JobSeekerData> getInputType() {
            return JobSeekerData.class;
        }

        @Override
        public StepDecision execute(final Context context, final JobSeekerData input) {
            try {
                mongoCollection.upsert(input);
            } catch (final JsonProcessingException e) {
                throw new RuntimeException(e);
            }
            jobSeekerData.set(context, input);
            return StepDecision.goTo(UpdateExternalSystem.class, input);
        }
    }

    final class UpdateExternalSystem implements Step<JobSeekerData> {
        @Override
        public Class<JobSeekerData> getInputType() {
            return JobSeekerData.class;
        }

        @Override
        public StepDecision execute(final Context context, final JobSeekerData input) {
            try {
                externalService.updateExternalSystem(MAPPER.writeValueAsString(input));
            } catch (final JsonProcessingException e) {
                throw new RuntimeException(e);
            }
            return StepDecision.gracefulComplete();
        }
    }
}
