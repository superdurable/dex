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
import io.superdurable.dex.gen.models.Context;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component
public class SetDataAttributeWorkflow implements ObjectWorkflow {
    public static final String DATA_OBJECT_KEY = "data-obj-key-1";
    public static final String DATA_OBJECT_MODEL_KEY = "data-obj-1";
    public static final String DATA_OBJECT_KEY_PREFIX = "data-obj-key-prefix-";

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(StateDef.startingState(new SetDataAttributeWorkflowState1()));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DATA_OBJECT_KEY),
                DataAttributeDef.create(Context.class, DATA_OBJECT_MODEL_KEY),
                DataAttributeDef.createByPrefix(Long.class, DATA_OBJECT_KEY_PREFIX)
        );
    }
}
