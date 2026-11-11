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

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/** A mock payment and shipping API that records each call. */
public final class ShopApi {
    private final List<String> charged = new CopyOnWriteArrayList<String>();
    private final List<String> shipped = new CopyOnWriteArrayList<String>();

    public void charge(final String orderId) {
        charged.add(orderId);
        System.out.println("Charged " + orderId);
    }

    public void ship(final String orderId) {
        shipped.add(orderId);
        System.out.println("Shipped " + orderId);
    }

    public List<String> charged() {
        return charged;
    }

    public List<String> shipped() {
        return shipped;
    }
}
