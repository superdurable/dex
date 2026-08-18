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

package io.superdurable.dex.integ.smoke;

final class FlowSmokeFlags {
    final boolean stepStartMayFail;
    final boolean noStartStep;

    private FlowSmokeFlags(final boolean stepStartMayFail, final boolean noStartStep) {
        this.stepStartMayFail = stepStartMayFail;
        this.noStartStep = noStartStep;
    }

    static FlowSmokeFlags none() {
        return new FlowSmokeFlags(false, false);
    }

    static FlowSmokeFlags stepStartMayFail() {
        return new FlowSmokeFlags(true, false);
    }

    static FlowSmokeFlags noStartStep() {
        return new FlowSmokeFlags(false, true);
    }
}
