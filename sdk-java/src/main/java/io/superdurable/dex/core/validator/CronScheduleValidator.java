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

package io.superdurable.dex.core.validator;

import com.cronutils.model.definition.CronDefinition;
import com.cronutils.model.definition.CronDefinitionBuilder;
import com.cronutils.parser.CronParser;

import java.util.Optional;

import static com.cronutils.model.CronType.UNIX;

public class CronScheduleValidator {
    public static String validate(Optional<String> cronTab) {
        if (!cronTab.isPresent()) {
            return null;
        }
        CronDefinition cronDefinition =
                CronDefinitionBuilder.instanceDefinitionFor(UNIX);
        CronParser parser = new CronParser(cronDefinition);
        parser.parse(cronTab.get());
        return cronTab.get();
    }
}
