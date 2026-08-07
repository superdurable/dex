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

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

final class StateOptionsWorkflow implements Flow<Void> {
    final Attribute<String> waitValue = Attribute.define("DA_WAIT_UNTIL", String.class);
    final Attribute<String> executeValue = Attribute.define("DA_EXECUTE", String.class);
    final Attribute<String> bothValue = Attribute.define("DA_BOTH", String.class);
    private final OptionsFirstStep first = new OptionsFirstStep();
    private final OptionsSecondStep second = new OptionsSecondStep();
    private final OptionsThirdStep third = new OptionsThirdStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(first).otherSteps(second, third);
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
            executeValue.set(context, "execute");
            waitValue.set(context, "wait_until");
            bothValue.set(context, "both");
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
            if (!"wait_until".equals(waitValue.get(context))) {
                throw new IllegalStateException("waitFor attribute was not loaded");
            }
            if (!"execute".equals(executeValue.get(context))) {
                throw new IllegalStateException("execute attribute was not loaded in waitFor");
            }
            if (!"both".equals(bothValue.get(context))) {
                throw new IllegalStateException("shared attribute was not loaded in waitFor");
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!"execute".equals(executeValue.get(context))) {
                throw new IllegalStateException("execute attribute was not loaded");
            }
            if (!"wait_until".equals(waitValue.get(context))) {
                throw new IllegalStateException("waitFor attribute was not loaded in execute");
            }
            if (!"both".equals(bothValue.get(context))) {
                throw new IllegalStateException("shared attribute was not loaded in execute");
            }
            return StepDecision.goTo(third, null);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
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
        public Wait waitFor(final Context context, final Void input) {
            if (!"both".equals(bothValue.get(context))) {
                throw new IllegalStateException("shared attribute was not loaded in waitFor");
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!"both".equals(bothValue.get(context))) {
                throw new IllegalStateException("shared attribute was not loaded in execute");
            }
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
