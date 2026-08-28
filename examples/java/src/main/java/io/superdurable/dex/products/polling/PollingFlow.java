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

package io.superdurable.dex.products.polling;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class PollingFlow implements Flow<Integer> {
    public static final String TASK_A_COMPLETED = "task-a-completed";
    public static final String TASK_B_COMPLETED = "task-b-completed";
    public static final String TASK_C_COMPLETED = "task-c-completed";

    public final Attribute<Integer> currentPolls =
            Attribute.define("current-polls", Integer.class);
    public final Channel<Void> taskACompleted = Channel.define(TASK_A_COMPLETED, Void.class);
    public final Channel<Void> taskBCompleted = Channel.define(TASK_B_COMPLETED, Void.class);
    public final Channel<Void> taskCCompleted = Channel.define(TASK_C_COMPLETED, Void.class);

    private final MyDependencyService service;
    private final Initialize initialize = new Initialize();
    private final Poll poll = new Poll();
    private final WaitForTasks waitForTasks = new WaitForTasks();

    public PollingFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(initialize).otherSteps(poll, waitForTasks);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                currentPolls,
                taskACompleted,
                taskBCompleted,
                taskCCompleted);
    }

    final class Initialize implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer maximumPolls) {
            currentPolls.set(context, 0);
            return StepDecision.goToMany(
                    StepMovement.of(Poll.class, maximumPolls),
                    StepMovement.of(WaitForTasks.class, null));
        }
    }

    final class WaitForTasks implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.allOf(
                    taskACompleted.forOne(),
                    taskBCompleted.forOne(),
                    taskCCompleted.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete("all tasks completed");
        }
    }

    final class Poll implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer maximumPolls) {
            return Wait.until(Timer.byDuration(Duration.ofSeconds(1)));
        }

        @Override
        public StepDecision execute(final Context context, final Integer maximumPolls) {
            service.callAPI1("calling API1 for polling service C");
            final int polls = currentPolls.get(context);
            if (polls >= maximumPolls) {
                taskCCompleted.publish(context, null);
                return StepDecision.deadEnd();
            }
            currentPolls.set(context, polls + 1);
            return StepDecision.goTo(Poll.class, maximumPolls);
        }
    }
}
