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
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-23 Transient Trigger: Channels keep messages, so the RPC drops a trigger unless a Step is
 * waiting for it.
 */
@Component
public class TransientTriggerFlow implements Flow<Void> {
    public static final Attribute<Boolean> isAwaitingMotionEvent =
            Attribute.define("isAwaitingMotionEvent", Boolean.class);
    public static final Channel<String> motionSensorArmed =
            Channel.define("motionSensorArmed", String.class);
    public static final Channel<Integer> motionEvent = Channel.define("motionEvent", Integer.class);
    public static final Channel<String> motionMonitoringStopped =
            Channel.define("motionMonitoringStopped", String.class);

    private final AwaitMotionSensorArmed awaitMotionSensorArmed = new AwaitMotionSensorArmed();
    private final AwaitMotionEvent awaitMotionEvent = new AwaitMotionEvent();
    private final AwaitMonitoringStop awaitMonitoringStop = new AwaitMonitoringStop();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(awaitMotionSensorArmed).otherSteps(awaitMotionEvent, awaitMonitoringStop);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                isAwaitingMotionEvent, motionSensorArmed, motionEvent, motionMonitoringStopped);
    }

    @RPC
    public void armMotionSensor(final Context context) {
        motionSensorArmed.publish(context, "armed");
    }

    @RPC
    public void stopMotionMonitoring(final Context context) {
        motionMonitoringStopped.publish(context, "stopped");
    }

    /** Delivers one event while a Step waits for it; otherwise drops it and returns false. */
    @RPC(lockAttributes = {"isAwaitingMotionEvent"})
    public RPCResult<Boolean> sendMotionEvent(final Context context, final Integer value) {
        if (!Boolean.TRUE.equals(isAwaitingMotionEvent.get(context))) {
            return RPCResult.of(false);
        }
        // Clearing the guard in this locked commit accepts only the first trigger.
        isAwaitingMotionEvent.set(context, false);
        motionEvent.publish(context, value);
        return RPCResult.of(true);
    }

    static final class AwaitMotionSensorArmed implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(motionSensorArmed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(AwaitMotionEvent.class, null);
        }
    }

    static final class AwaitMotionEvent implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addWaitForLock(AttributeLock.of(isAwaitingMotionEvent))
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            isAwaitingMotionEvent.set(context, true);
            return Wait.until(motionEvent.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(
                    AwaitMonitoringStop.class, motionEvent.getConditionResults(context).get(0));
        }
    }

    static final class AwaitMonitoringStop implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer motionValue) {
            return Wait.until(motionMonitoringStopped.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer motionValue) {
            return StepDecision.gracefulComplete(motionValue);
        }
    }
}
