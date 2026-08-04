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
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.Context;
import io.superdurable.dex.gen.models.SearchAttributeValueType;
import io.superdurable.dex.integ.basic.FakContextImpl;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class BasicPersistenceWorkflow implements ObjectWorkflow {
    public static final String TEST_INIT_DATA_OBJECT_KEY = "data-obj-0";
    public static final String TEST_DATA_OBJECT_KEY = "data-obj-1";
    public static final String TEST_DATA_OBJECT_MODEL_1 = "data-obj-2";

    public static final String TEST_DATA_OBJECT_MODEL_2 = "data-obj-3";
    public static final String TEST_DATA_OBJECT_PREFIX = "data-obj-prefix-";

    public static final String TEST_SEARCH_ATTRIBUTE_KEYWORD = "CustomKeywordField";
    public static final String TEST_SEARCH_ATTRIBUTE_INT = "CustomIntField";

    public static final String TEST_SEARCH_ATTRIBUTE_DATE_TIME = "CustomDatetimeField";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new BasicPersistenceWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, TEST_INIT_DATA_OBJECT_KEY),
                DataAttributeDef.create(String.class, TEST_DATA_OBJECT_KEY),
                DataAttributeDef.create(Context.class, TEST_DATA_OBJECT_MODEL_1),
                DataAttributeDef.create(FakContextImpl.class, TEST_DATA_OBJECT_MODEL_2),
                DataAttributeDef.createByPrefix(Long.class, TEST_DATA_OBJECT_PREFIX),
                SearchAttributeDef.create(SearchAttributeValueType.INT, TEST_SEARCH_ATTRIBUTE_INT),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, TEST_SEARCH_ATTRIBUTE_KEYWORD),
                SearchAttributeDef.create(SearchAttributeValueType.DATETIME, TEST_SEARCH_ATTRIBUTE_DATE_TIME)
        );
    }
}
