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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import java.util.concurrent.ThreadLocalRandom;
import org.springframework.stereotype.Component;

@Component
public class AwaitParallelStepsFlow implements Flow<Integer> {
    public final Channel<Void> completeCh = Channel.define("parallel-complete", Void.class);

    private final InitStep init = new InitStep();
    private final DoWorkStep work = new DoWorkStep();
    private final AwaitStep await = new AwaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(init).otherSteps(work, await);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(completeCh);
    }

    final class InitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer count) {
            final StepMovement<?>[] movements = new StepMovement<?>[count + 1];
            movements[0] = StepMovement.of(AwaitStep.class, count);
            for (int index = 0; index < count; index++) {
                movements[index + 1] = StepMovement.of(DoWorkStep.class, index);
            }
            return StepDecision.goToMulti(movements);
        }
    }

    final class DoWorkStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            try {
                Thread.sleep(ThreadLocalRandom.current().nextInt(50, 500));
            } catch (final InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(error);
            }
            completeCh.publish(context, null);
            return StepDecision.deadEnd();
        }
    }

    final class AwaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer count) {
            return Wait.until(completeCh.forN(count));
        }

        @Override
        public StepDecision execute(final Context context, final Integer count) {
            return StepDecision.gracefulComplete(count);
        }
    }

}
