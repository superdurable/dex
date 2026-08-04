/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ.persistence;

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.SearchAttributeValueType;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class SetSearchAttributeWorkflow implements ObjectWorkflow {
    public static final String SEARCH_ATTRIBUTE_KEYWORD = "CustomKeywordField";
    public static final String SEARCH_ATTRIBUTE_TEXT = "CustomTextField";
    public static final String SEARCH_ATTRIBUTE_DOUBLE = "CustomDoubleField";
    public static final String SEARCH_ATTRIBUTE_INT = "CustomIntField";
    public static final String SEARCH_ATTRIBUTE_BOOL = "CustomBoolField";
    public static final String SEARCH_ATTRIBUTE_KEYWORD_ARRAY = "CustomKeywordArrayField";
    public static final String SEARCH_ATTRIBUTE_DATE_TIME = "CustomDatetimeField";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new SetSearchAttributeWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                SearchAttributeDef.create(SearchAttributeValueType.INT, SEARCH_ATTRIBUTE_INT),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SEARCH_ATTRIBUTE_KEYWORD),
                SearchAttributeDef.create(SearchAttributeValueType.DATETIME, SEARCH_ATTRIBUTE_DATE_TIME),
                SearchAttributeDef.create(SearchAttributeValueType.TEXT, SEARCH_ATTRIBUTE_TEXT),
                SearchAttributeDef.create(SearchAttributeValueType.DOUBLE, SEARCH_ATTRIBUTE_DOUBLE),
                SearchAttributeDef.create(SearchAttributeValueType.BOOL, SEARCH_ATTRIBUTE_BOOL),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD_ARRAY, SEARCH_ATTRIBUTE_KEYWORD_ARRAY)
        );
    }
}
