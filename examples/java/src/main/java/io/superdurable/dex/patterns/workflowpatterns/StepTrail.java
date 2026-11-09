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

import java.util.Arrays;

/** Builds the Trail value: the Step types run so far. */
final class StepTrail {
    private StepTrail() {
    }

    static String[] append(final String[] trail, final String stepType) {
        final String[] extended = Arrays.copyOf(trail, trail.length + 1);
        extended[trail.length] = stepType;
        return extended;
    }
}
