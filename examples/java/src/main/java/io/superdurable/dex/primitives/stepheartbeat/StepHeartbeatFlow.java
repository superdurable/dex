/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.stepheartbeat;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class StepHeartbeatFlow implements Flow<Integer> {
    private final HeartbeatStep start = new HeartbeatStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class HeartbeatStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer batches) {
            final int completedBatches = context.hasLastHeartbeatValue()
                    ? context.getLastHeartbeatValue(Integer.class)
                    : 0;
            for (int batch = completedBatches; batch < batches; batch++) {
                if (context.isCancellationRequested()) {
                    return StepDecision.deadEnd();
                }
                try {
                    Thread.sleep(Duration.ofSeconds(2).toMillis());
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return StepDecision.deadEnd();
                }
                context.recordHeartbeat(batch + 1);
            }
            return StepDecision.gracefulComplete("processed");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeMethodTimeout(Duration.ofSeconds(60))
                    .heartbeatTimeout(Duration.ofSeconds(10))
                    .executeRetry(RetryPolicy.newBuilder().maximumAttempts(3).build())
                    .build();
        }
    }
}
