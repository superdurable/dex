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
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;

import java.time.Instant;

final class PersistenceSetAttributesWorkflow implements Flow<String> {
    final Attribute<String> data = Attribute.define("data", String.class);
    final AttributeMap<String> dataMap = AttributeMap.define("data-map", String.class);
    final Attribute<PersistenceWorkflow.ModelInput> model = Attribute.define(
            "data-model",
            PersistenceWorkflow.ModelInput.class);
    final Attribute<String> keyword = Attribute.define(
            "CustomKeywordField",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    final Attribute<String> text = Attribute.define(
            "CustomTextField",
            String.class,
            new AttributeIndex(AttributeIndex.Type.FULL_TEXT));
    final Attribute<Double> decimal = Attribute.define(
            "CustomDoubleField",
            Double.class,
            new AttributeIndex(AttributeIndex.Type.DOUBLE));
    final Attribute<Integer> integer = Attribute.define(
            "CustomIntField",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    final Attribute<Boolean> bool = Attribute.define(
            "CustomBoolField",
            Boolean.class,
            new AttributeIndex(AttributeIndex.Type.BOOL));
    final Attribute<String[]> keywords = Attribute.define(
            "CustomKeywordArrayField",
            String[].class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD_ARRAY));
    final Attribute<Instant> datetime = Attribute.define(
            "CustomDatetimeField",
            Instant.class,
            new AttributeIndex(AttributeIndex.Type.DATETIME));
    final Channel<Void> proceed = Channel.define("proceed", Void.class);
    private final CompleteStep start = new CompleteStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                data,
                dataMap,
                model,
                keyword,
                text,
                decimal,
                integer,
                bool,
                keywords,
                datetime,
                proceed);
    }

    final class CompleteStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.allOf(proceed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete("test-result");
        }
    }
}
