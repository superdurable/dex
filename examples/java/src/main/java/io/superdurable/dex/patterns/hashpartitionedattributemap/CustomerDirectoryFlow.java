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

package io.superdurable.dex.patterns.hashpartitionedattributemap;

import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.StepList;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.springframework.stereotype.Component;

@Component
public class CustomerDirectoryFlow implements Flow<Void> {
    public static final String FLOW_ID = "customer-directory";
    public static final int PARTITION_COUNT = 1000;
    private static final long FNV_OFFSET_BASIS_32 = 2166136261L;
    private static final long FNV_PRIME_32 = 16777619L;

    public final AttributeMap<CustomerProfilePartition> customerProfilesByEmailPartition =
            AttributeMap.define(
                    "customer_profiles_by_email_partition",
                    CustomerProfilePartition.class);

    @Override
    public StepList<Void> getSteps() {
        return StepList.empty();
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(customerProfilesByEmailPartition);
    }

    @RPC(name = "UpsertCustomerProfile")
    public RPCResult<CustomerProfile> upsertCustomerProfile(
            final Context context,
            final CustomerProfile input) {
        final EmailPartition partition = emailPartition(input.emailAddress);
        input.emailAddress = partition.canonicalEmail;
        CustomerProfilePartition profiles = customerProfilesByEmailPartition.get(
                context,
                partition.partitionName);
        if (profiles == null) {
            profiles = new CustomerProfilePartition(new HashMap<String, CustomerProfile>());
        }
        if (profiles.profilesByCanonicalEmail == null) {
            profiles.profilesByCanonicalEmail = new HashMap<String, CustomerProfile>();
        }
        profiles.profilesByCanonicalEmail.put(partition.canonicalEmail, input);
        customerProfilesByEmailPartition.set(context, partition.partitionName, profiles);
        return RPCResult.of(input);
    }

    @RPC(name = "GetCustomerProfileByEmail")
    public RPCResult<CustomerProfile> getCustomerProfileByEmail(
            final Context context,
            final String emailAddress) {
        final EmailPartition partition = emailPartition(emailAddress);
        final CustomerProfilePartition profiles = customerProfilesByEmailPartition.get(
                context,
                partition.partitionName);
        final CustomerProfile profile = profiles == null
                ? null
                : profiles.profilesByCanonicalEmail.get(partition.canonicalEmail);
        if (profile == null) {
            throw new IllegalArgumentException(
                    "customer profile \"" + partition.canonicalEmail + "\" not found");
        }
        return RPCResult.of(profile);
    }

    public static EmailPartition emailPartition(final String emailAddress) {
        final String canonicalEmail = canonicalEmailAddress(emailAddress);
        final long hash = fnv1a32(canonicalEmail.getBytes(StandardCharsets.US_ASCII));
        return new EmailPartition(
                canonicalEmail,
                hash,
                String.format("partition-%03d", hash % PARTITION_COUNT));
    }

    public static String canonicalEmailAddress(final String emailAddress) {
        if (emailAddress == null) {
            throw new IllegalArgumentException("emailAddress is required");
        }
        int start = 0;
        int end = emailAddress.length();
        for (int index = 0; index < emailAddress.length(); index++) {
            if (emailAddress.charAt(index) > 0x7f) {
                throw new IllegalArgumentException(
                        "emailAddress must contain only ASCII characters");
            }
        }
        while (start < end && isAsciiWhitespace(emailAddress.charAt(start))) {
            start++;
        }
        while (end > start && isAsciiWhitespace(emailAddress.charAt(end - 1))) {
            end--;
        }
        if (start == end) {
            throw new IllegalArgumentException("emailAddress is required");
        }
        final char[] canonical = emailAddress.substring(start, end).toCharArray();
        for (int index = 0; index < canonical.length; index++) {
            if (canonical[index] >= 'A' && canonical[index] <= 'Z') {
                canonical[index] = (char) (canonical[index] + ('a' - 'A'));
            }
        }
        return new String(canonical);
    }

    public static long fnv1a32(final byte[] value) {
        long hash = FNV_OFFSET_BASIS_32;
        for (byte octet : value) {
            hash ^= octet & 0xff;
            hash = (hash * FNV_PRIME_32) & 0xffffffffL;
        }
        return hash;
    }

    private static boolean isAsciiWhitespace(final char value) {
        return value == ' ' || (value >= '\t' && value <= '\r');
    }

    public static class CustomerProfile {
        public String emailAddress;
        public String fullName;
        public String companyName;
        public String customerTier;

        public CustomerProfile() {
        }

        public CustomerProfile(
                final String emailAddress,
                final String fullName,
                final String companyName,
                final String customerTier) {
            this.emailAddress = emailAddress;
            this.fullName = fullName;
            this.companyName = companyName;
            this.customerTier = customerTier;
        }
    }

    public static class CustomerProfilePartition {
        public Map<String, CustomerProfile> profilesByCanonicalEmail;

        public CustomerProfilePartition() {
        }

        public CustomerProfilePartition(
                final Map<String, CustomerProfile> profilesByCanonicalEmail) {
            this.profilesByCanonicalEmail = profilesByCanonicalEmail;
        }
    }

    public static class EmailPartition {
        public String canonicalEmail;
        public long hash;
        public String partitionName;

        public EmailPartition() {
        }

        public EmailPartition(
                final String canonicalEmail,
                final long hash,
                final String partitionName) {
            this.canonicalEmail = canonicalEmail;
            this.hash = hash;
            this.partitionName = partitionName;
        }
    }
}
