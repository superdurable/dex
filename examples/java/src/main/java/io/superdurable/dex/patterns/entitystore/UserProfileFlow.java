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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.StepList;
import java.time.Instant;
import org.springframework.stereotype.Component;

/** Keeps one user profile in Dex and projects its Attributes to PostgreSQL. */
@Component
public class UserProfileFlow implements Flow<Void> {
    public static final String STORE_NAME = "entityStore";

    public final Attribute<String> displayName =
            Attribute.define("display_name", String.class).syncToAttributeStore();
    public final Attribute<String> email =
            Attribute.define("email", String.class).syncToAttributeStore();
    public final Attribute<Boolean> marketingOptIn =
            Attribute.define("marketing_opt_in", Boolean.class).syncToAttributeStore();
    public final Attribute<Long> credits =
            Attribute.define("credits", Long.class).syncToAttributeStore();
    public final Attribute<Double> weight =
            Attribute.define("weight", Double.class).syncToAttributeStore();
    public final Attribute<Instant> lastLoggedInTime =
            Attribute.define("last_logged_in_time", Instant.class).syncToAttributeStore();
    public final Attribute<UserProfileMetadata> metadata =
            Attribute.define("metadata", UserProfileMetadata.class).syncToAttributeStore();

    @Override
    public StepList<Void> getSteps() {
        return StepList.empty();
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                displayName,
                email,
                marketingOptIn,
                credits,
                weight,
                lastLoggedInTime,
                metadata);
    }

    @RPC
    public void updateProfile(final Context context, final UserProfile profile) {
        profile.validate();
        displayName.set(context, profile.displayName);
        email.set(context, profile.email);
        marketingOptIn.set(context, profile.marketingOptIn);
        credits.set(context, profile.credits);
        weight.set(context, profile.weight);
        lastLoggedInTime.set(context, profile.lastLoggedInTime);
        metadata.set(context, profile.metadata);
    }

    @RPC
    public RPCResult<UserProfile> getProfile(final Context context) {
        return RPCResult.of(new UserProfile(
                displayName.get(context),
                email.get(context),
                marketingOptIn.get(context),
                credits.get(context),
                weight.get(context),
                lastLoggedInTime.get(context),
                metadata.get(context)));
    }

    @RPC
    public void clearProfile(final Context context) {
        displayName.delete(context);
        email.delete(context);
        marketingOptIn.delete(context);
        credits.delete(context);
        weight.delete(context);
        lastLoggedInTime.delete(context);
        metadata.delete(context);
    }
}
