/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

import io.superdurable.dex.*;
import java.time.Duration;
import java.util.List;

class BrokenFlow implements Flow<OutsideInput> {
    private static final Attribute<Boolean> state = Attribute.define("state", Boolean.class);
    public StepList<OutsideInput> getSteps() { return StepList.startStep(new Begin()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(state); }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Choose the next Step."
    static class Begin implements Step<OutsideInput> {
        public Class<OutsideInput> getInputType() { return OutsideInput.class; }
        public Wait waitFor(Context context, OutsideInput input) { return await(input); }
        private Wait await(OutsideInput input) { return Wait.skipImmediately(); }

        public StepDecision execute(Context context, OutsideInput input) {
            state.set(context, true);
            if (input.keepGoing()) return StepDecision.goTo(input.target(), null);
            return decide();
        }

        private StepDecision decide() { return StepDecision.goTo(OutsideStep.class, null); }
    }
}

class ExternalFlow implements Flow<Void> {
    private static final Attribute<String> state = Attribute.define("state", String.class);
    private static final String FLOW_TYPE = "RegisteredFlow";
    public String getFlowType() { return FLOW_TYPE; }
    public StepList<Void> getSteps() { return StepList.startStep(new Start()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(state); }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Start the external Step."
    static class Start implements Step<Void> {
        public Class<Void> getInputType() { return Void.class; }

        public StepDecision execute(Context context, Void input) {
            state.set(context, "started");
            return StepDecision.goTo(OutsideStep.class, null);
        }
    }
}

class ProgressFlow implements Flow<String> {
    private static final Stream<String> progress = Stream.define("progress", String.class, 1024);

    public String getFlowType() { return getClass().getSimpleName(); }
    public StepList<String> getSteps() { return StepList.startStep(new Start()).otherSteps(new Done()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(List.of(progress)); }

    @RPC
    public RPCResult<Boolean> report(Context context) {
        writer(context).write("progress");
        return RPCResult.of(true);
    }

    private BufferedTextStream writer(Context context) {
        return BufferedTextStream.create(context, progress, Duration.ofMillis(500), 1024);
    }

    public StepDecision handleTimeout(Context context) {
        BufferedTextStream.create(context, progress);
        return StepDecision.forceComplete();
    }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Route the input."
    static class Start implements Step<String> {
        public Class<String> getInputType() { return String.class; }
        public String getStepType() { return getClass().getSimpleName(); }

        public StepDecision execute(Context context, String input) {
            return switch (input) {
                case "x" -> StepDecision.goTo(Done.class, null);
                default -> StepDecision.forceComplete();
            };
        }
    }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Finish processing the input."
    static class Done implements Step<Void> {
        public Class<Void> getInputType() { return Void.class; }

        public StepDecision execute(Context context, Void input) {
            return StepDecision.forceComplete();
        }
    }
}
