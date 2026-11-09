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
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-18 Milestone: a config change is enabled only while the change window is open. */
@Component
public class MilestoneFlow implements Flow<Void> {
    public static final Attribute<Boolean> isChangeWindowOpen =
            Attribute.define("isChangeWindowOpen", Boolean.class);
    public static final Channel<String> releaseApproved =
            Channel.define("releaseApproved", String.class);
    public static final Channel<String> releaseFinalized =
            Channel.define("releaseFinalized", String.class);

    private final OpenChangeWindow openChangeWindow = new OpenChangeWindow();
    private final AwaitReleaseApproval awaitReleaseApproval = new AwaitReleaseApproval();
    private final AwaitReleaseFinalization awaitReleaseFinalization = new AwaitReleaseFinalization();
    private final ApplyConfigChange applyConfigChange = new ApplyConfigChange();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(openChangeWindow)
                .otherSteps(awaitReleaseApproval, awaitReleaseFinalization, applyConfigChange);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(isChangeWindowOpen, releaseApproved, releaseFinalized);
    }

    /** Applies the change only while the milestone holds. */
    @RPC
    public RPCResult<Boolean> requestConfigChange(final Context context, final String change) {
        if (Boolean.TRUE.equals(isChangeWindowOpen.get(context))) {
            return RPCResult.of(true, StepMovement.of(ApplyConfigChange.class, change));
        }
        return RPCResult.of(false);
    }

    @RPC
    public void approveRelease(final Context context) {
        releaseApproved.publish(context, "approved");
    }

    @RPC
    public void finalizeRelease(final Context context) {
        releaseFinalized.publish(context, "finalized");
    }

    static final class OpenChangeWindow implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            isChangeWindowOpen.set(context, true);
            return StepDecision.goTo(AwaitReleaseApproval.class, null);
        }
    }

    static final class AwaitReleaseApproval implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(releaseApproved.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            isChangeWindowOpen.set(context, false);
            return StepDecision.goTo(AwaitReleaseFinalization.class, null);
        }
    }

    static final class AwaitReleaseFinalization implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(releaseFinalized.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class ApplyConfigChange implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String change) {
            return StepDecision.gracefulComplete(change);
        }
    }
}
