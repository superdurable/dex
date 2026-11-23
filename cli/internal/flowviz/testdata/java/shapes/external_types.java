/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

import io.superdurable.dex.*;

class LabelFlow implements Flow<String> {}

record OutsideInput(boolean keepGoing, Class<OutsideStep> target) {}

class OutsideStep implements Step<Void> {
    public Class<Void> getInputType() { return Void.class; }

    public StepDecision execute(Context context, Void input) {
        return StepDecision.forceComplete();
    }
}
