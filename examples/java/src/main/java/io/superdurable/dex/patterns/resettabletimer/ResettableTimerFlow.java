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

package io.superdurable.dex.patterns.resettabletimer;

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

/**
 * A flow that starts a timer whose expiry triggers an action. The timer resets when a message
 * is received on the reset channel.
 */
@Component
public class ResettableTimerFlow implements Flow<Void> {
    public static final String RESET_TIMER_CHANNEL = "RESET_TIMER_CHANNEL";
    public static final Duration TIMER_DURATION = Duration.ofMinutes(5);

    public final Channel<String> resetTimerChannel =
            Channel.define(RESET_TIMER_CHANNEL, String.class);

    private final ResettableTimerStep resettableTimer = new ResettableTimerStep();
    private final TimerExpired timerExpired = new TimerExpired();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(resettableTimer).otherSteps(timerExpired);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(resetTimerChannel);
    }

    @RPC
    public void sendResetMessage(final Context context) {
        resetTimerChannel.publish(context, "reset");
    }

    final class ResettableTimerStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(TIMER_DURATION),
                    resetTimerChannel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (context.hasTimerFired()) {
                return StepDecision.goTo(timerExpired, null);
            }
            return StepDecision.goTo(resettableTimer, null);
        }
    }

    final class TimerExpired implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            System.out.println("Timer fired; this is where we would send an email");
            return StepDecision.gracefulComplete();
        }
    }
}
