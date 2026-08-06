/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Wait;

import java.time.Instant;
import java.util.Collections;
import java.util.List;

final class BasicPersistenceFlow implements Flow<String> {
    final Attribute<String> initial = Attribute.define("data-obj-0", String.class);
    final Attribute<String> data = Attribute.define("data-obj-1", String.class);
    final Attribute<IwfFlows.ModelInput> model =
            Attribute.define("data-obj-2", IwfFlows.ModelInput.class);
    final AttributeMap<String> dataMap = AttributeMap.define("data-map", String.class);
    final Attribute<String> keyword = Attribute.define(
            "CustomKeywordField",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    final Attribute<Integer> integer = Attribute.define(
            "CustomIntField",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    final Attribute<Instant> datetime = Attribute.define(
            "CustomDatetimeField",
            Instant.class,
            new AttributeIndex(AttributeIndex.Type.DATETIME));
    private final PersistenceStep start = new PersistenceStep();

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                initial,
                data,
                model,
                dataMap,
                keyword,
                integer,
                datetime);
    }

    final class PersistenceStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            data.set(context, input);
            dataMap.set(context, "one", input);
            context.setStepExecutionLocal("local", input, String.class);
            context.recordEvent("written", input, String.class);
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            keyword.set(context, input);
            integer.set(context, 1);
            datetime.set(context, Instant.parse("2023-04-17T21:17:49Z"));
            model.set(context, new IwfFlows.ModelInput());
            dataMap.delete(context, "one");
            return StepDecision.gracefulComplete(data.get(context));
        }
    }
}
