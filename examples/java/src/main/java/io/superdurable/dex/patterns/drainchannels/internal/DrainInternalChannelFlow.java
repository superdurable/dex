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

package io.superdurable.dex.patterns.drainchannels.internal;

import com.fasterxml.jackson.core.JsonProcessingException;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.ServiceDependency;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class DrainInternalChannelFlow implements Flow<String> {
    public static final String SIDE_STEP_DATA_CHANNEL = "SideStepData";
    public static final String MAIN_STEP_EXECUTION_COUNTER = "main_step_execution_counter";

    public final Attribute<Integer> mainStepExecutionCounter =
            Attribute.define(MAIN_STEP_EXECUTION_COUNTER, Integer.class);
    public final Channel<MongoDocument> sideStepData =
            Channel.define(SIDE_STEP_DATA_CHANNEL, MongoDocument.class);

    private final ServiceDependency externalService;
    private final ServiceDependency mongoCollection;
    private final Init init = new Init();
    private final SideStep sideStep = new SideStep();
    private final MainStep mainStep = new MainStep();
    private final Finalize finalize = new Finalize();

    public DrainInternalChannelFlow(final ServiceDependency service) {
        this.externalService = service;
        this.mongoCollection = service;
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(init)
                .otherSteps(sideStep, mainStep, finalize);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(mainStepExecutionCounter, sideStepData);
    }

    final class Init implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            mainStepExecutionCounter.set(context, 0);
            return StepDecision.goToMulti(
                    StepMovement.of(sideStep, null),
                    StepMovement.of(mainStep, input));
        }
    }

    final class SideStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(sideStepData.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<MongoDocument> documents = sideStepData.getConditionResults(context);
            if (documents.isEmpty()) {
                throw new IllegalStateException("No document was sent");
            }

            final MongoDocument document = documents.get(0);
            if (document == null) {
                throw new IllegalStateException("No data was sent");
            }

            try {
                mongoCollection.upsert(document);
            } catch (final JsonProcessingException e) {
                throw new RuntimeException(e);
            }

            if (document.finalCommand) {
                return StepDecision.gracefulComplete();
            }
            return StepDecision.goTo(sideStep, null);
        }
    }

    final class MainStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final int executionCount = mainStepExecutionCounter.get(context) + 1;
            mainStepExecutionCounter.set(context, executionCount);

            final String status;
            switch (executionCount) {
                case 1:
                    status = "RECEIVED";
                    break;
                case 2:
                    status = "ACCEPTED";
                    break;
                case 3:
                    status = "PASSED";
                    break;
                default:
                    status = "ERROR";
                    break;
            }
            sideStepData.publish(
                    context,
                    new MongoDocument(input, status, false));

            externalService.externalApiCall(
                    "external service call to process data (e.g. notify the job seeker)");
            externalService.externalApiCall(
                    "a call to send metrics or add a log to logrepo");

            if (executionCount <= 3) {
                return StepDecision.goTo(mainStep, input);
            }
            return StepDecision.goTo(Finalize.class, null);
        }
    }

    final class Finalize implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            sideStepData.publish(
                    context,
                    new MongoDocument("documentId-1", "FINALIZED", true));
            return StepDecision.gracefulComplete();
        }
    }
}
