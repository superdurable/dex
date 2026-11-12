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
package io.superdurable.dex.quickstart;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * A mock payment and shipping API that drops a repeated idempotency key. With a keys path, it
 * remembers keys across processes.
 */
public final class ShopApi {
    private final Path keysPath;
    private final Duration chargeDelay;
    private final Set<String> usedKeys = new TreeSet<String>();
    private final List<String> charged = new CopyOnWriteArrayList<String>();
    private final List<String> shipped = new CopyOnWriteArrayList<String>();
    private final List<String> repeatedKeys = new CopyOnWriteArrayList<String>();

    /** Keeps keys in memory. */
    public ShopApi() {
        this(null, Duration.ZERO);
    }

    /** Keeps keys in keysPath and waits chargeDelay after each new charge. */
    public ShopApi(final Path keysPath, final Duration chargeDelay) {
        this.keysPath = keysPath;
        this.chargeDelay = chargeDelay;
    }

    public void charge(final String orderId, final String idempotencyKey) {
        if (isRepeat(idempotencyKey)) {
            System.out.println("Charge for " + orderId + " already done (key " + idempotencyKey
                    + "); not charged again");
            return;
        }
        charged.add(orderId);
        System.out.println("Charged " + orderId + " with key " + idempotencyKey);
        // Leaves time to stop the Worker after the payment, before Dex records the Step.
        try {
            Thread.sleep(chargeDelay.toMillis());
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
    }

    public void ship(final String orderId, final String idempotencyKey) {
        if (isRepeat(idempotencyKey)) {
            System.out.println("Ship for " + orderId + " already done (key " + idempotencyKey
                    + "); not shipped again");
            return;
        }
        shipped.add(orderId);
        System.out.println("Shipped " + orderId + " with key " + idempotencyKey);
    }

    public List<String> charged() {
        return charged;
    }

    public List<String> shipped() {
        return shipped;
    }

    public List<String> repeatedKeys() {
        return repeatedKeys;
    }

    private synchronized boolean isRepeat(final String idempotencyKey) {
        try {
            if (keysPath != null && Files.exists(keysPath)) {
                usedKeys.addAll(Files.readAllLines(keysPath));
            }
            if (usedKeys.contains(idempotencyKey)) {
                repeatedKeys.add(idempotencyKey);
                return true;
            }
            usedKeys.add(idempotencyKey);
            if (keysPath != null) {
                Files.write(keysPath, usedKeys);
            }
            return false;
        } catch (final IOException failure) {
            throw new UncheckedIOException("cannot read or write " + keysPath, failure);
        }
    }
}
