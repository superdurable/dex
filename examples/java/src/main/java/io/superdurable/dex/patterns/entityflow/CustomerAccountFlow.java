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

package io.superdurable.dex.patterns.entityflow;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import java.time.Duration;
import org.springframework.stereotype.Component;

@Component
public class CustomerAccountFlow implements Flow<Void> {
    public final Attribute<String> displayName = Attribute.define("display_name", String.class);
    public final Attribute<String> email = Attribute.define("email", String.class);
    public final Attribute<Long> creditBalance = Attribute.define("credit_balance", Long.class);
    public final Attribute<Boolean> retired = Attribute.define("retired", Boolean.class);

    private final FakeAccountArchive archive;

    public CustomerAccountFlow(final FakeAccountArchive archive) {
        this.archive = archive;
    }

    public static String customerAccountFlowId(final String accountId) {
        return "customer-account-" + accountId;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.withoutStartStep(new CloseAccountStep(archive));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(displayName, email, creditBalance, retired);
    }

    @RPC(name = "UpdateContactDetails", lockAttributes = {"credit_balance"})
    public void updateContactDetails(final Context context, final ContactDetails contact) {
        requireOpenAccount(context);
        contact.validate();
        displayName.set(context, contact.displayName);
        email.set(context, contact.email);
    }

    @RPC(name = "AdjustCreditBalance", lockAttributes = {"credit_balance"})
    public RPCResult<Long> adjustCreditBalance(
            final Context context,
            final CreditAdjustment adjustment) {
        requireOpenAccount(context);
        if (adjustment.amount == 0) {
            throw new IllegalArgumentException("amount must not be zero");
        }
        final long balance = creditBalance.get(context) + adjustment.amount;
        if (balance < 0) {
            throw new IllegalArgumentException("credit balance cannot go below zero");
        }
        creditBalance.set(context, balance);
        return RPCResult.of(balance);
    }

    @RPC(name = "RetireAccount", lockAttributes = {"credit_balance"})
    public RPCResult<Void> retireAccount(final Context context) {
        if (Boolean.TRUE.equals(retired.get(context))) {
            return RPCResult.of((Void) null);
        }
        if (creditBalance.get(context) != 0) {
            throw new IllegalArgumentException(
                    "spend or refund the credit balance before retiring");
        }
        retired.set(context, true);
        return RPCResult.of((Void) null, StepMovement.of(CloseAccountStep.class, null));
    }

    private void requireOpenAccount(final Context context) {
        if (Boolean.TRUE.equals(retired.get(context))) {
            throw new IllegalStateException("account is retired");
        }
    }

    // dex:group group-id:account-retirement group-label:"Account retirement"
    // dex:explanation text:"Archive the retired account, then complete its Flow."
    public static final class CloseAccountStep implements Step<Void> {
        private final FakeAccountArchive archive;

        CloseAccountStep(final FakeAccountArchive archive) {
            this.archive = archive;
        }

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(1))
                            .maximumInterval(Duration.ofSeconds(1))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            archive.archiveAccount(context.getFlowId());
            return StepDecision.gracefulComplete();
        }
    }

    public static class ContactDetails {
        public String displayName;
        public String email;

        public ContactDetails() {
        }

        public ContactDetails(final String displayName, final String email) {
            this.displayName = displayName;
            this.email = email;
        }

        public void validate() {
            if (displayName == null || displayName.isBlank()) {
                throw new IllegalArgumentException("display name is required");
            }
            if (email == null || !email.contains("@")) {
                throw new IllegalArgumentException("email must contain @");
            }
        }
    }

    public static class CreditAdjustment {
        public long amount;

        public CreditAdjustment() {
        }

        public CreditAdjustment(final long amount) {
            this.amount = amount;
        }
    }
}
