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

/**
 * WCP-17 Interleaved Parallel Routing: a shared Execute lock keeps tasks from overlapping, and
 * GoTo keeps PrepareContract before SignContract.
 */
@Component
public class InterleavedParallelRoutingFlow implements Flow<Void> {
    public static final Attribute<Boolean> onboardingTaskLock =
            Attribute.define("onboardingTaskLock", Boolean.class);
    public static final Channel<TaskInterval> onboardingTaskFinished =
            Channel.define("onboardingTaskFinished", TaskInterval.class);
    private static final int TASK_COUNT = 3;
    private static final long TASK_MILLIS = 200L;

    private final StartOnboardingTasks startOnboardingTasks = new StartOnboardingTasks();
    private final PrepareContract prepareContract = new PrepareContract();
    private final SignContract signContract = new SignContract();
    private final RunCreditCheck runCreditCheck = new RunCreditCheck();
    private final CompleteOnboardingTasks completeOnboardingTasks = new CompleteOnboardingTasks();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startOnboardingTasks)
                .otherSteps(prepareContract, signContract, runCreditCheck, completeOnboardingTasks);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(onboardingTaskLock, onboardingTaskFinished);
    }

    private static StepOptions onboardingTaskLockOptions() {
        return StepOptions.newBuilder()
                .addExecuteLock(AttributeLock.of(onboardingTaskLock))
                .build();
    }

    private static void runOnboardingTask(final Context context, final String task) {
        final long startedAtMillis = System.currentTimeMillis();
        TaskInterval.sleepInsideTask(TASK_MILLIS);
        onboardingTaskFinished.publish(context, TaskInterval.endingNow(task, startedAtMillis));
    }

    static final class StartOnboardingTasks implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(PrepareContract.class, null),
                    StepMovement.of(RunCreditCheck.class, null),
                    StepMovement.of(CompleteOnboardingTasks.class, TASK_COUNT));
        }
    }

    static final class PrepareContract implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return onboardingTaskLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            runOnboardingTask(context, getStepType());
            return StepDecision.goTo(SignContract.class, null);
        }
    }

    static final class SignContract implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return onboardingTaskLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            runOnboardingTask(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class RunCreditCheck implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return onboardingTaskLockOptions();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            runOnboardingTask(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class CompleteOnboardingTasks implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer taskCount) {
            return Wait.until(onboardingTaskFinished.forN(taskCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer taskCount) {
            return StepDecision.gracefulComplete(onboardingTaskFinished.getConditionResults(context));
        }
    }
}
