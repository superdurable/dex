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

package io.superdurable.dex.patterns.childflows;

/**
 * One parcel of an order, identified by its shipment number.
 *
 * @param shipmentNumber the shipment number, which is also the shipment's Flow ID
 * @param destination where the parcel goes
 * @param deliveryDelaySeconds simulates how long the carrier takes to deliver
 */
public record Shipment(String shipmentNumber, String destination, int deliveryDelaySeconds) {
}
