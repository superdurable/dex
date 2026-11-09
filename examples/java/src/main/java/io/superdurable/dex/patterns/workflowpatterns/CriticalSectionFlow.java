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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-39 Critical Section: a single-message Channel is the token, so two multi-Step sections never
 * run at the same time.
 */
@Component
public class CriticalSectionFlow implements Flow<Void> {
    public static final Channel<String> criticalSectionToken =
            Channel.define("criticalSectionToken", String.class);
    public static final Channel<TaskInterval> criticalSectionFinished =
            Channel.define("criticalSectionFinished", TaskInterval.class);
    private static final int SECTION_COUNT = 2;
    private static final long SECTION_STEP_MILLIS = 100L;
    private static final String TOKEN = "token";

    private final StartCriticalSections startCriticalSections = new StartCriticalSections();
    private final ReserveStock reserveStock = new ReserveStock();
    private final CommitStock commitStock = new CommitStock();
    private final RecalculatePrice recalculatePrice = new RecalculatePrice();
    private final PublishPrice publishPrice = new PublishPrice();
    private final FinishCriticalSections finishCriticalSections = new FinishCriticalSections();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startCriticalSections).otherSteps(
                reserveStock, commitStock, recalculatePrice, publishPrice, finishCriticalSections);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(criticalSectionToken, criticalSectionFinished);
    }

    private static StepDecision finishSection(
            final Context context, final String section, final long startedAtMillis) {
        TaskInterval.sleepInsideTask(SECTION_STEP_MILLIS);
        criticalSectionFinished.publish(context, TaskInterval.endingNow(section, startedAtMillis));
        criticalSectionToken.publish(context, TOKEN);
        return StepDecision.deadEnd();
    }

    static final class StartCriticalSections implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            criticalSectionToken.publish(context, TOKEN);
            return StepDecision.goToMany(
                    StepMovement.of(ReserveStock.class, null),
                    StepMovement.of(RecalculatePrice.class, null),
                    StepMovement.of(FinishCriticalSections.class, SECTION_COUNT));
        }
    }

    static final class ReserveStock implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(criticalSectionToken.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final long startedAtMillis = System.currentTimeMillis();
            TaskInterval.sleepInsideTask(SECTION_STEP_MILLIS);
            return StepDecision.goTo(CommitStock.class, startedAtMillis);
        }
    }

    static final class CommitStock implements Step<Long> {
        @Override
        public Class<Long> getInputType() {
            return Long.class;
        }

        @Override
        public StepDecision execute(final Context context, final Long startedAtMillis) {
            return finishSection(context, "inventory", startedAtMillis);
        }
    }

    static final class RecalculatePrice implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(criticalSectionToken.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final long startedAtMillis = System.currentTimeMillis();
            TaskInterval.sleepInsideTask(SECTION_STEP_MILLIS);
            return StepDecision.goTo(PublishPrice.class, startedAtMillis);
        }
    }

    static final class PublishPrice implements Step<Long> {
        @Override
        public Class<Long> getInputType() {
            return Long.class;
        }

        @Override
        public StepDecision execute(final Context context, final Long startedAtMillis) {
            return finishSection(context, "pricing", startedAtMillis);
        }
    }

    static final class FinishCriticalSections implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer sectionCount) {
            return Wait.until(criticalSectionFinished.forN(sectionCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer sectionCount) {
            return StepDecision.gracefulComplete(criticalSectionFinished.getConditionResults(context));
        }
    }
}
