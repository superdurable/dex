/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

class BrokenFlow implements Flow<OutsideInput> {
    private static final Attribute<Boolean> state = Attribute.define("state", Boolean.class);
    public StepList<OutsideInput> getSteps() { return StepList.startStep(new Begin()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(state); }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Choose the next Step."
    static class Begin implements Step<OutsideInput> {
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
    public String getFlowType() { return "RegisteredFlow"; }
    public StepList<Void> getSteps() { return StepList.startStep(new Start()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(state); }

    // dex:group group-id:start group-label:"Start"
    // dex:explanation text:"Start the external Step."
    static class Start implements Step<Void> {
        public StepDecision execute(Context context, Void input) {
            state.set(context, "started");
            return StepDecision.goTo(OutsideStep.class, null);
        }
    }
}
