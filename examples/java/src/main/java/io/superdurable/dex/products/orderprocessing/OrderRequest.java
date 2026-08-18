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

package io.superdurable.dex.products.orderprocessing;

public class OrderRequest {
    public String orderId;
    public String email;
    public String customerId;
    public int amount;
    public boolean testFailAtShipping;

    public OrderRequest() {
    }

    public OrderRequest(
            final String orderId,
            final String email,
            final String customerId,
            final int amount,
            final boolean testFailAtShipping) {
        this.orderId = orderId;
        this.email = email;
        this.customerId = customerId;
        this.amount = amount;
        this.testFailAtShipping = testFailAtShipping;
    }
}
