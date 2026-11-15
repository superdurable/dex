/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;

class CloneForkSessionWorkflow implements Flow<String> {
    static final Attribute<String> LABEL = Attribute.define("clone-fork-label", String.class);
    static final Channel<String> RELEASE = Channel.define("clone-fork-release", String.class);

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(new CloneForkWaitStep());
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(LABEL, RELEASE);
    }

    @RPC
    public void releaseSession(final Context context, final String answer) {
        RELEASE.publish(context, answer);
    }
}

final class CloneForkWaitStep implements Step<String> {
    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public Wait waitFor(final Context context, final String label) {
        CloneForkSessionWorkflow.LABEL.set(context, label);
        return Wait.until(CloneForkSessionWorkflow.RELEASE.forOne());
    }

    @Override
    public StepDecision execute(final Context context, final String label) {
        final String answer = CloneForkSessionWorkflow.RELEASE.getConditionResults(context).get(0);
        return StepDecision.gracefulComplete(CloneForkSessionWorkflow.LABEL.get(context) + "|" + answer);
    }
}
