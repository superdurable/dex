/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

final class StateOptionsFlow implements Flow<Void> {
    final Attribute<String> waitValue = Attribute.define("DA_WAIT_UNTIL", String.class);
    final Attribute<String> executeValue = Attribute.define("DA_EXECUTE", String.class);
    final Attribute<String> bothValue = Attribute.define("DA_BOTH", String.class);
    private final OptionsFirstStep first = new OptionsFirstStep();
    private final OptionsSecondStep second = new OptionsSecondStep();
    private final OptionsThirdStep third = new OptionsThirdStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(first),
                StepDef.nonStartStep(second),
                StepDef.nonStartStep(third));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(waitValue, executeValue, bothValue);
    }

    final class OptionsFirstStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(second, null);
        }
    }

    final class OptionsSecondStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            waitValue.set(context, "wait");
            bothValue.set(context, "wait");
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            executeValue.set(context, "execute");
            bothValue.set(context, "execute");
            final StepOptions override = StepOptions.newBuilder()
                    .executeMethodTimeout(Duration.ofSeconds(2))
                    .build();
            return StepDecision.goToMulti(StepMovement.of(third, null, override));
        }

        @Override
        public StepOptions getStepOptions() {
            final RetryPolicy retry = RetryPolicy.newBuilder()
                    .initialInterval(Duration.ofMillis(10))
                    .maximumAttempts(3)
                    .build();
            return StepOptions.newBuilder()
                    .waitForMethodTimeout(Duration.ofSeconds(1))
                    .executeMethodTimeout(Duration.ofSeconds(1))
                    .waitForRetry(retry)
                    .executeRetry(retry)
                    .waitForDurability(StepDurability.SYNC)
                    .executeDurability(StepDurability.ASYNC)
                    .addWaitForLock(AttributeLock.of(waitValue))
                    .addExecuteLock(AttributeLock.of(executeValue))
                    .build();
        }
    }

    final class OptionsThirdStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete("success");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addWaitForLock(AttributeLock.of(bothValue))
                    .addExecuteLock(AttributeLock.of(bothValue))
                    .build();
        }
    }
}
