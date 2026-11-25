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

package io.superdurable.dex.shared;

import io.superdurable.dex.exceptions.RpcLockConflictException;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;

import java.util.concurrent.ThreadLocalRandom;
import java.util.function.Supplier;

/** Retries RPCs that lose a registered lock race, for the HTTP controllers. */
public final class LockConflictRetry {
    private static final int LOCK_CONFLICT_ATTEMPTS = 5;
    private static final long LOCK_CONFLICT_BACKOFF_MILLIS = 200;

    private LockConflictRetry() {
    }

    /**
     * Invokes an RPC and retries a lock conflict with bounded backoff. Retrying is safe: a
     * conflicting call committed nothing.
     *
     * @return 200 with the RPC result, or 409 when every attempt conflicts
     */
    public static <T> ResponseEntity<?> invokeAndRespond(final Supplier<T> invocation) {
        for (int attempt = 1; ; attempt++) {
            try {
                return ResponseEntity.ok(invocation.get());
            } catch (final RpcLockConflictException conflict) {
                if (attempt == LOCK_CONFLICT_ATTEMPTS) {
                    return ResponseEntity.status(HttpStatus.CONFLICT)
                            .body("the Flow is busy with another update; retry the request");
                }
                // Jitter spreads out callers that conflicted at the same time.
                final long jitter = ThreadLocalRandom.current().nextLong(LOCK_CONFLICT_BACKOFF_MILLIS);
                sleepBeforeRetry(attempt * LOCK_CONFLICT_BACKOFF_MILLIS + jitter);
            }
        }
    }

    private static void sleepBeforeRetry(final long millis) {
        try {
            Thread.sleep(millis);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted while retrying a lock conflict", interrupted);
        }
    }
}
