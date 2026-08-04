/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.spring;

import java.util.concurrent.ExecutionException;

public class TestSingletonWorkerService {
    private static TestWorker testWorker;

    public static void startWorkerIfNotUp() throws ExecutionException, InterruptedException {
        if (testWorker == null) {
            testWorker = new TestWorker();
            testWorker.start();
        }
    }
}
