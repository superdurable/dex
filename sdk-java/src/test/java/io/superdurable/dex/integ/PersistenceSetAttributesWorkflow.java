/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Wait;

import java.time.Instant;

class PersistenceSetAttributesWorkflow implements Flow<String> {
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

    @RPC
    public void setIndexed(final Context context) {
        keyword.set(context, "keyword-1");
        text.set(context, "text-1");
        decimal.set(context, 1.0);
        integer.set(context, 1);
        bool.set(context, true);
        keywords.set(context, new String[]{"keyword-1", "keyword-2"});
        datetime.set(context, Instant.parse("2024-11-13T00:00:01.731Z"));
    }

    @RPC
    public void setData(final Context context, final String input) {
        data.set(context, input);
    }

    @RPC
    public void setMapOne(final Context context, final String input) {
        dataMap.set(context, "one", input);
    }

    @RPC
    public void setMapSpecial(final Context context, final String input) {
        dataMap.set(context, "special % key", input);
    }

    @RPC
    public void setInteger(final Context context, final Integer input) {
        integer.set(context, input);
    }

    @RPC
    public void setModel(
            final Context context,
            final PersistenceWorkflow.ModelInput input) {
        model.set(context, input);
    }

    @RPC
    public void complete(final Context context) {
        proceed.publish(context, null);
    }

    final class CompleteStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.until(proceed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete("test-result");
        }
    }
}
