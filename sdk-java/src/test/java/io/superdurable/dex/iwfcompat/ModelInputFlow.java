/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;

import java.util.Collections;
import java.util.List;

final class ModelInputFlow implements Flow<IwfFlows.ModelInput> {
    private final Step<IwfFlows.ModelInput> start = new Step<IwfFlows.ModelInput>() {
        @Override
        public Class<IwfFlows.ModelInput> getInputType() {
            return IwfFlows.ModelInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final IwfFlows.ModelInput input) {
            return StepDecision.gracefulComplete(input.value);
        }
    };

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
    }
}
