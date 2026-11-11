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

package io.superdurable.dex.patterns.parallel;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ThreadLocalRandom;
import java.util.function.Function;
import org.springframework.stereotype.Component;

@Component
public class AwaitParallelStepsFlow implements Flow<Integer> {
    public final Join<Integer> workCompleted = Join.define("parallel-complete", Integer.class);

    /** One worker: its index and the Join round it reports to. */
    public record WorkItem(int index, JoinRound round) {
    }

    private final InitStep init = new InitStep();
    private final DoWork work = new DoWork();
    private final Await await = new Await();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(init).otherSteps(work, await);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(workCompleted);
    }

    final class InitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer count) {
            final List<Function<JoinRound, StepMovement<?>>> workers =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (int index = 0; index < count; index++) {
                final int workIndex = index;
                workers.add(round -> StepMovement.of(DoWork.class, new WorkItem(workIndex, round)));
            }
            return StepDecision.goToMany(workCompleted.fork(
                    context, workers, round -> StepMovement.of(Await.class, round)));
        }
    }

    final class DoWork implements Step<WorkItem> {
        @Override
        public Class<WorkItem> getInputType() {
            return WorkItem.class;
        }

        @Override
        public StepDecision execute(final Context context, final WorkItem item) {
            try {
                Thread.sleep(ThreadLocalRandom.current().nextInt(50, 500));
            } catch (final InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(error);
            }
            return workCompleted.endBranch(context, item.round(), item.index());
        }
    }

    final class Await implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(workCompleted.forAllBranches(round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return StepDecision.gracefulComplete(workCompleted.getBranchResults(context, round).size());
        }
    }

}
