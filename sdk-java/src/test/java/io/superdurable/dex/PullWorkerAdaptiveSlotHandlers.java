/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.google.common.util.concurrent.Uninterruptibles;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

// Records handler entries and releases blocked handlers of the adaptive slot test Flows.
final class PullWorkerAdaptiveSlotHandlers {
    static final String MODE_BLOCK = "block";
    static final String MODE_STUBBORN = "stubborn";
    static final String MODE_QUICK = "quick";
    static final String STUBBORN_RPC_KEY = "rpc";

    final BlockingQueue<Entry> entered = new LinkedBlockingQueue<Entry>();
    final CountDownLatch release = new CountDownLatch(1);
    volatile boolean isRpcStubborn;
    // Releases handlers that ignore cancellation, keyed by Flow ID or STUBBORN_RPC_KEY.
    private final Map<String, CountDownLatch> stubbornReleases = new HashMap<String, CountDownLatch>();

    static final class Entry {
        final String flowID;
        final String kind;
        final int attempt;

        Entry(final String flowID, final String kind, final int attempt) {
            this.flowID = flowID;
            this.kind = kind;
            this.attempt = attempt;
        }

        @Override
        public String toString() {
            return kind + " " + flowID + " attempt " + attempt;
        }
    }

    void runStep(final Context context, final String mode) {
        entered.add(new Entry(context.getFlowId(), "step", context.getAttempt()));
        if (MODE_BLOCK.equals(mode)) {
            awaitRelease();
        } else if (MODE_STUBBORN.equals(mode) && context.getAttempt() == 1) {
            // Only the first attempt ignores cancellation, so the retry after a restart completes.
            Uninterruptibles.awaitUninterruptibly(stubbornRelease(context.getFlowId()));
        }
    }

    String runBlockingRpc(final Context context) {
        entered.add(new Entry(context.getFlowId(), "rpc", context.getAttempt()));
        if (isRpcStubborn) {
            Uninterruptibles.awaitUninterruptibly(stubbornRelease(STUBBORN_RPC_KEY));
            return "released";
        }
        awaitRelease();
        return "released";
    }

    private void awaitRelease() {
        try {
            release.await();
        } catch (InterruptedException cancelled) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("handler cancelled", cancelled);
        }
    }

    synchronized CountDownLatch stubbornRelease(final String key) {
        CountDownLatch latch = stubbornReleases.get(key);
        if (latch == null) {
            latch = new CountDownLatch(1);
            stubbornReleases.put(key, latch);
        }
        return latch;
    }

    void releaseStubborn(final String... keys) {
        for (String key : keys) {
            stubbornRelease(key).countDown();
        }
    }

    void releaseStubborn(final List<String> keys) {
        releaseStubborn(keys.toArray(new String[0]));
    }

    // Lets every handler that ignores cancellation return, so a failed test can stop its Worker.
    void releaseAllStubborn() {
        final List<String> keys;
        synchronized (this) {
            keys = new ArrayList<String>(stubbornReleases.keySet());
        }
        keys.add(STUBBORN_RPC_KEY);
        releaseStubborn(keys);
    }

    List<Entry> awaitEntries(final int count) throws InterruptedException {
        final List<Entry> entries = new ArrayList<Entry>();
        final long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(60);
        while (entries.size() < count) {
            final Entry entry = entered.poll(Math.max(1L, deadline - System.nanoTime()), TimeUnit.NANOSECONDS);
            if (entry == null) {
                throw new AssertionError("only " + entries.size() + " of " + count + " handlers started");
            }
            entries.add(entry);
        }
        return entries;
    }

    List<Entry> drainEntries() {
        final List<Entry> entries = new ArrayList<Entry>();
        entered.drainTo(entries);
        return entries;
    }
}
