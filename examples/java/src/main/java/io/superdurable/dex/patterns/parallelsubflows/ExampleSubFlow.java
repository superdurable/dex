/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package io.superdurable.dex.patterns.parallelsubflows;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import org.springframework.stereotype.Component;

@Component
public final class ExampleSubFlow implements Flow<String> {
    private final DoWorkStep doWorkStep = new DoWorkStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(doWorkStep);
    }

    static final class DoWorkStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String request) {
            try {
                Thread.sleep(50L + request.length() % 10L * 50L);
            } catch (final InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException("SubFlow work interrupted", interrupted);
            }
            return StepDecision.gracefulComplete(request);
        }
    }
}
