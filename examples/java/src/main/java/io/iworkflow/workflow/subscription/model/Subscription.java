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

package io.iworkflow.workflow.subscription.model;

import com.fasterxml.jackson.databind.annotation.JsonDeserialize;
import org.immutables.value.Value;

import java.time.Duration;

@Value.Immutable
@JsonDeserialize(as = ImmutableSubscription.class)
public abstract class Subscription {
    public abstract Duration getTrialPeriod();

    public abstract Duration getBillingPeriod();

    public abstract int getMaxBillingPeriods();

    public abstract int getBillingPeriodCharge();
}
