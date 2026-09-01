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

package io.superdurable.dex.primitives.stream;

import io.superdurable.dex.BufferedTextStream;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Stream;
import org.springframework.stereotype.Component;

@Component
public final class StreamFlow implements Flow<String> {
    public final Stream<String> progress =
            Stream.define("Progress", String.class, 10L * 1024L * 1024L);
    private final RenderPreview renderPreview = new RenderPreview();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(renderPreview);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(progress);
    }

    final class RenderPreview implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final BufferedTextStream writer = BufferedTextStream.create(context, progress);
            writer.write("Rendering preview for " + input);
            writer.write("Preview ready for " + input);
            return StepDecision.gracefulComplete("Rendered " + input);
        }
    }
}
