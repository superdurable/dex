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

package io.superdurable.dex.patterns.controllongrunningwork;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

/** Processes records in batches that two control Attributes steer. */
@Component
public class RecordBackfillFlow implements Flow<Void> {
    public static final int TOTAL_RECORDS = 1000;
    public static final int DEFAULT_BATCH_SIZE = 10;
    public static final int MAX_BATCH_SIZE = 1000;
    public static final Duration BATCH_INTERVAL = Duration.ofSeconds(1);

    public static final Attribute<Integer> batchSize =
            Attribute.define("batchSize", Integer.class);
    public static final Attribute<Boolean> stopRequested =
            Attribute.define("stopRequested", Boolean.class);

    private final StartRecordBackfill startRecordBackfill = new StartRecordBackfill();
    private final ProcessRecordBatch processRecordBatch = new ProcessRecordBatch();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startRecordBackfill).otherSteps(processRecordBatch);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(batchSize, stopRequested);
    }

    /** Changes how many records each later batch processes. */
    @RPC
    public void setBatchSize(final Context context, final int size) {
        if (size < 1 || size > MAX_BATCH_SIZE) {
            throw new IllegalArgumentException(
                    "batch size must be between 1 and " + MAX_BATCH_SIZE);
        }
        batchSize.set(context, size);
    }

    /** Asks the loop to stop before its next batch. */
    @RPC
    public void requestStop(final Context context) {
        stopRequested.set(context, true);
    }

    /** The range of records that the loop has not processed yet. */
    public static final class BackfillProgress {
        public int nextRecord;
        public int lastRecord;

        public BackfillProgress() {
        }

        public BackfillProgress(final int nextRecord, final int lastRecord) {
            this.nextRecord = nextRecord;
            this.lastRecord = lastRecord;
        }
    }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Start the backfill at the first record."
    static final class StartRecordBackfill implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void unused) {
            return StepDecision.goTo(
                    ProcessRecordBatch.class, new BackfillProgress(1, TOTAL_RECORDS));
        }
    }

    // dex:group group-id:batches group-label:"Batches"
    // dex:explanation text:"Read the controls, then process one batch of records or stop."
    static final class ProcessRecordBatch implements Step<BackfillProgress> {
        @Override
        public Class<BackfillProgress> getInputType() {
            return BackfillProgress.class;
        }

        @Override
        public Wait waitFor(final Context context, final BackfillProgress progress) {
            return Wait.until(Timer.byDuration(BATCH_INTERVAL));
        }

        @Override
        public StepDecision execute(final Context context, final BackfillProgress progress) {
            if (stopRequested.get(context)) {
                return StepDecision.gracefulComplete(
                        "stopped before record " + progress.nextRecord);
            }
            // An unset Integer Attribute reads as 0, and a set batch size is at least 1.
            final int storedSize = batchSize.get(context);
            final int size = storedSize == 0 ? DEFAULT_BATCH_SIZE : storedSize;
            final int last = Math.min(progress.nextRecord + size - 1, progress.lastRecord);
            System.out.printf("processing records %d to %d%n", progress.nextRecord, last);
            if (last == progress.lastRecord) {
                return StepDecision.gracefulComplete("processed " + last + " records");
            }
            return StepDecision.goTo(
                    ProcessRecordBatch.class, new BackfillProgress(last + 1, progress.lastRecord));
        }
    }
}
