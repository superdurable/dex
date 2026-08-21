/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.patterns.cron;

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
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class CronScheduleFlow implements Flow<CronScheduleFlow.Input> {
    public final Channel<Void> trigger = Channel.define("cron-schedule-trigger", Void.class);
    public final Channel<Void> skip = Channel.define("cron-schedule-skip", Void.class);

    private final Start start = new Start();
    private final WaitForSchedule waitForSchedule = new WaitForSchedule();
    private final Run run = new Run();

    @Override
    public StepList<Input> getSteps() {
        return StepList.startStep(start).otherSteps(waitForSchedule, run);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(trigger, skip);
    }

    public enum IntervalUnit {
        MINUTE,
        HOUR,
        DAY
    }

    public static class Interval {
        public int value;
        public IntervalUnit unit;

        public Interval() {
        }

        public Interval(final int value, final IntervalUnit unit) {
            this.value = value;
            this.unit = unit;
        }

        Duration duration() {
            if (unit == null) {
                return Duration.ZERO;
            }
            switch (unit) {
                case MINUTE:
                    return Duration.ofMinutes(value);
                case HOUR:
                    return Duration.ofHours(value);
                case DAY:
                    return Duration.ofDays(value);
                default:
                    return Duration.ZERO;
            }
        }
    }

    public static class Input {
        public Interval interval;
        public int runCount;

        public Input() {
        }

        public Input(final Interval interval, final int runCount) {
            this.interval = interval;
            this.runCount = runCount;
        }
    }

    static class ScheduleState {
        public Interval interval;
        public int remainingRuns;

        ScheduleState() {
        }

        ScheduleState(final Interval interval, final int remainingRuns) {
            this.interval = interval;
            this.remainingRuns = remainingRuns;
        }
    }

    static class RunInput {
        public int runNumber;
        public boolean isFinal;

        RunInput() {
        }

        RunInput(final int runNumber, final boolean isFinal) {
            this.runNumber = runNumber;
            this.isFinal = isFinal;
        }
    }

    final class Start implements Step<Input> {
        @Override
        public Class<Input> getInputType() {
            return Input.class;
        }

        @Override
        public StepDecision execute(final Context context, final Input input) {
            if (input.runCount <= 0 || input.interval == null || input.interval.value <= 0
                    || input.interval.duration().isZero()) {
                return StepDecision.forceFail("interval value and run count must be positive");
            }
            return StepDecision.goTo(waitForSchedule, new ScheduleState(input.interval, input.runCount));
        }
    }

    final class WaitForSchedule implements Step<ScheduleState> {
        @Override
        public Class<ScheduleState> getInputType() {
            return ScheduleState.class;
        }

        @Override
        public Wait waitFor(final Context context, final ScheduleState state) {
            return Wait.anyOf(
                    Timer.byDuration(state.interval.duration()),
                    trigger.forOne(),
                    skip.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final ScheduleState state) {
            if (!skip.getConditionResults(context).isEmpty()) {
                return nextSchedule(state);
            }
            return runNow(state);
        }
    }

    final class Run implements Step<RunInput> {
        @Override
        public Class<RunInput> getInputType() {
            return RunInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final RunInput input) {
            context.recordEvent("cron-schedule-run", "run-" + input.runNumber, String.class);
            return input.isFinal ? StepDecision.gracefulComplete() : StepDecision.deadEnd();
        }
    }

    private StepDecision nextSchedule(final ScheduleState state) {
        if (state.remainingRuns == 1) {
            return StepDecision.gracefulComplete();
        }
        return StepDecision.goTo(
                waitForSchedule,
                new ScheduleState(state.interval, state.remainingRuns - 1));
    }

    private StepDecision runNow(final ScheduleState state) {
        final RunInput runInput = new RunInput(state.remainingRuns, state.remainingRuns == 1);
        if (runInput.isFinal) {
            return StepDecision.goTo(run, runInput);
        }
        return StepDecision.goToMulti(
                StepMovement.of(run, runInput),
                StepMovement.of(
                        waitForSchedule,
                        new ScheduleState(state.interval, state.remainingRuns - 1)));
    }
}
