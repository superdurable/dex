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
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;

import java.time.Instant;
import java.util.Collections;
import java.util.List;

final class SetAttributesFlow implements Flow<String> {
    final Attribute<String> data = Attribute.define("data", String.class);
    final AttributeMap<String> dataMap = AttributeMap.define("data-map", String.class);
    final Attribute<String> keyword = Attribute.define(
            "keyword",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    final Attribute<String> text = Attribute.define(
            "text",
            String.class,
            new AttributeIndex(AttributeIndex.Type.FULL_TEXT));
    final Attribute<Double> decimal = Attribute.define(
            "double",
            Double.class,
            new AttributeIndex(AttributeIndex.Type.DOUBLE));
    final Attribute<Integer> integer = Attribute.define(
            "int",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    final Attribute<Boolean> bool = Attribute.define(
            "bool",
            Boolean.class,
            new AttributeIndex(AttributeIndex.Type.BOOL));
    final Attribute<String[]> keywords = Attribute.define(
            "keywords",
            String[].class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD_ARRAY));
    final Attribute<Instant> datetime = Attribute.define(
            "datetime",
            Instant.class,
            new AttributeIndex(AttributeIndex.Type.DATETIME));
    private final IwfFlows.CompleteStringStep start = new IwfFlows.CompleteStringStep();

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                data,
                dataMap,
                keyword,
                text,
                decimal,
                integer,
                bool,
                keywords,
                datetime);
    }
}
