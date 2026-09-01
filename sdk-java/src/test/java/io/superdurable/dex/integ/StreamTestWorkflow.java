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

import io.superdurable.dex.BufferedTextStream;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Stream;

final class StreamTestWorkflow implements Flow<Void> {
    final Stream<String> progress = Stream.define("stream-test-progress", String.class, 1L << 20);
    private final StreamTestStep start = new StreamTestStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(progress);
    }

    final class StreamTestStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final BufferedTextStream writer = BufferedTextStream.create(context, progress);
            writer.write("step-progress-");
            writer.write("1");
            writer.flush();
            writer.write("step-progress-");
            writer.write("2");
            return StepDecision.gracefulComplete();
        }
    }
}
