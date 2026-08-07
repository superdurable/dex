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

package io.superdurable.dex;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class StepList<StartInput> {
    private final List<StepDef> definitions;

    private StepList(final List<StepDef> definitions) {
        this.definitions = Collections.unmodifiableList(definitions);
    }

    public static <StartInput> StepList<StartInput> empty() {
        return new StepList<StartInput>(Collections.<StepDef>emptyList());
    }

    public static <StartInput> StepList<StartInput> startStep(
            final Step<StartInput> startStep) {
        final List<StepDef> definitions = new ArrayList<StepDef>();
        definitions.add(StepDef.startStep(Objects.requireNonNull(startStep, "startStep")));
        return new StepList<StartInput>(definitions);
    }

    public static <StartInput> StepList<StartInput> withoutStartStep(
            final Step<?>... steps) {
        return new StepList<StartInput>(nonStartDefinitions(steps));
    }

    public StepList<StartInput> otherSteps(final Step<?>... steps) {
        final List<StepDef> combined = new ArrayList<StepDef>(definitions);
        combined.addAll(nonStartDefinitions(steps));
        return new StepList<StartInput>(combined);
    }

    List<StepDef> getDefinitions() {
        return definitions;
    }

    private static List<StepDef> nonStartDefinitions(final Step<?>... steps) {
        Objects.requireNonNull(steps, "steps");
        final List<StepDef> definitions = new ArrayList<StepDef>(steps.length);
        for (Step<?> step : steps) {
            definitions.add(StepDef.nonStartStep(Objects.requireNonNull(step, "step")));
        }
        return definitions;
    }
}
