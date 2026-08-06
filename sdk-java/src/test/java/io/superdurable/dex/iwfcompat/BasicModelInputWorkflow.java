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

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;

final class BasicModelInputWorkflow implements Flow<BasicModelInputWorkflow.Input> {
    private final Step<Input> start = new Step<Input>() {
        @Override
        public Class<Input> getInputType() {
            return Input.class;
        }

        @Override
        public StepDecision execute(final Context context, final Input input) {
            return StepDecision.gracefulComplete(input.value);
        }
    };

    @Override
    public StepList<Input> getSteps() {
        return StepList.startStep(start);
    }

    static final class Input {
        public int value;
    }
}
