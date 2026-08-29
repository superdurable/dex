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

package io.superdurable.dex.patterns.inactivenesstracker;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class InactivenessTrackerFlow implements Flow<Void> {
    public static final String ACTIVE_CHANNEL = "Active";
    public static final Duration TRACKER_DURATION = Duration.ofMinutes(5);

    public final Channel<Void> activeChannel = Channel.define(ACTIVE_CHANNEL, Void.class);

    private final TrackerStep trackerStep = new TrackerStep();
    private final ProcessInactivenessStep processInactivenessStep =
            new ProcessInactivenessStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(trackerStep).otherSteps(processInactivenessStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(activeChannel);
    }

    @RPC
    public void recordActivity(final Context context) {
        activeChannel.publish(context, null);
    }

    final class TrackerStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(TRACKER_DURATION),
                    activeChannel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (context.hasTimerFired()) {
                return StepDecision.goTo(ProcessInactivenessStep.class, null);
            }
            return StepDecision.goTo(TrackerStep.class, null);
        }
    }

    final class ProcessInactivenessStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            System.out.println("No activity arrived before the timer fired");
            return StepDecision.gracefulComplete();
        }
    }
}
