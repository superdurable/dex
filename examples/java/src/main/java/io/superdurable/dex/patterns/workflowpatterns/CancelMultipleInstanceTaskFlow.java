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
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-26 Cancel Multiple Instance Activity: an RPC withdraws the unfinished instances, and
 * completed instances keep their results.
 */
@Component
public class CancelMultipleInstanceTaskFlow implements Flow<Integer> {
    public static final Attribute<Integer> finishedTranslationCount =
            Attribute.define("finishedTranslationCount", Integer.class);
    public static final Channel<String> translationReleased =
            Channel.define("translationReleased", String.class);

    private final StartTranslations startTranslations = new StartTranslations();
    private final TranslateDocument translateDocument = new TranslateDocument();
    private final ReportTranslations reportTranslations = new ReportTranslations();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(startTranslations).otherSteps(translateDocument, reportTranslations);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(finishedTranslationCount, translationReleased);
    }

    @RPC
    public void releaseTranslation(final Context context) {
        translationReleased.publish(context, "released");
    }

    @RPC
    @SuppressWarnings("unchecked")
    public RPCResult<Void> cancelTranslations(final Context context) {
        return RPCResult.of((Void) null, StepMovement.of(ReportTranslations.class, null))
                .withCancelingSteps(TranslateDocument.class);
    }

    private static int finishedTranslations(final Context context) {
        final Integer finished = finishedTranslationCount.get(context);
        return finished == null ? 0 : finished;
    }

    static final class StartTranslations implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer translationCount) {
            final StepMovement<?>[] movements = new StepMovement<?>[translationCount];
            for (int index = 0; index < translationCount; index++) {
                movements[index] = StepMovement.of(TranslateDocument.class, index);
            }
            return StepDecision.goToMany(movements);
        }
    }

    static final class TranslateDocument implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLock(AttributeLock.of(finishedTranslationCount))
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final Integer documentIndex) {
            return Wait.until(translationReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer documentIndex) {
            finishedTranslationCount.set(context, finishedTranslations(context) + 1);
            return StepDecision.gracefulComplete(documentIndex);
        }
    }

    static final class ReportTranslations implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(finishedTranslations(context));
        }
    }
}
