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

import org.springframework.boot.SpringApplication;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class TestWorker {
    
    ExecutorService executor = Executors.newSingleThreadExecutor();

    public void start() throws ExecutionException, InterruptedException {
        System.getProperties().put("server.port", 8802);
        
        executor.submit(() -> {
            SpringApplication.run(SpringMainApplication.class);
        }).get();
    }

    public void stop() {
        executor.shutdown();
    }
}
