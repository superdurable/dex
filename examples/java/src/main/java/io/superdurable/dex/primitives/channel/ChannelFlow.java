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

package io.superdurable.dex.primitives.channel;

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
import java.util.List;

@Component
public class ChannelFlow implements Flow<Integer> {
    public final Channel<String> approval = Channel.define("Approval", String.class);
    public final Channel<String> queued = Channel.define("Queued", String.class);
    public final Channel<String> moved = Channel.define("Moved", String.class);
    private final ChannelWaitStep waitForApproval = new ChannelWaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(waitForApproval);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(approval, queued, moved);
    }

    @RPC
    public void approve(final Context context) {
        approval.publish(context, "approved");
    }

    @RPC(isTransactional = true)
    public void move(final Context context, final String messageId) {
        queued.delete(context, messageId);
        moved.publish(context, "moved");
    }

    final class ChannelWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyOf(
                    approval.forOne(),
                    Timer.byDuration(Duration.ofSeconds(input)));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            if (context.hasTimerFired()) {
                return StepDecision.gracefulComplete("approval timed out");
            }
            final List<String> approvals = approval.getConditionResults(context);
            return StepDecision.gracefulComplete(approvals.get(0));
        }
    }
}
