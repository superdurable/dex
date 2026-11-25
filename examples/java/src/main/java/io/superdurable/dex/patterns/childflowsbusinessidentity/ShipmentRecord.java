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

package io.superdurable.dex.patterns.childflowsbusinessidentity;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * The order's record of one shipment. A report or a reconcile read fills {@code outcome}; a
 * reconcile read fills {@code status}.
 *
 * @param status a terminal Flow status, "rejected", or "unavailable"; {@code null} when unset
 * @param outcome the shipment's outcome; {@code null} when the shipment stored none
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ShipmentRecord(String status, String outcome) {
}
