/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;

final class BasicModelInputWorkflow implements Flow<BasicModelInputWorkflow.Input> {
    private final BasicModelInputStep start = new BasicModelInputStep();

    @Override
    public StepList<Input> getSteps() {
        return StepList.startStep(start);
    }

    static final class Input {
        public int value;
    }
}

final class BasicModelInputStep implements Step<BasicModelInputWorkflow.Input> {
    @Override
    public Class<BasicModelInputWorkflow.Input> getInputType() {
        return BasicModelInputWorkflow.Input.class;
    }

    @Override
    public StepDecision execute(
            final Context context,
            final BasicModelInputWorkflow.Input input) {
        return StepDecision.gracefulComplete(input.value);
    }
}
