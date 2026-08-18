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

package io.superdurable.dex.patterns.entitystore;

import java.time.Instant;

/** User profile fields stored as individual Dex Attributes. */
public class UserProfile {
    public String displayName;
    public String email;
    public boolean marketingOptIn;
    public long credits;
    public double weight;
    public Instant lastLoggedInTime;
    public UserProfileMetadata metadata;

    public UserProfile() {
    }

    public UserProfile(
            final String displayName,
            final String email,
            final boolean marketingOptIn,
            final long credits,
            final double weight,
            final Instant lastLoggedInTime,
            final UserProfileMetadata metadata) {
        this.displayName = displayName;
        this.email = email;
        this.marketingOptIn = marketingOptIn;
        this.credits = credits;
        this.weight = weight;
        this.lastLoggedInTime = lastLoggedInTime;
        this.metadata = metadata;
        validate();
    }

    public void validate() {
        if (displayName == null || displayName.isEmpty()) {
            throw new IllegalArgumentException("displayName is required");
        }
        if (email == null || email.isEmpty()) {
            throw new IllegalArgumentException("email is required");
        }
        if (!Double.isFinite(weight)) {
            throw new IllegalArgumentException("weight must be finite");
        }
        if (lastLoggedInTime == null) {
            throw new IllegalArgumentException("lastLoggedInTime is required");
        }
        if (metadata == null) {
            throw new IllegalArgumentException("metadata is required");
        }
        metadata.validate();
    }
}
