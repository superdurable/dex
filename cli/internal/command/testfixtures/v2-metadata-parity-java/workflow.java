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
import java.util.List;
import java.util.Map;
import java.util.Optional;

class OrderFlow implements Flow<OrderFlow.Input> {
    enum Choice { SEND, HOLD }
    record Input(Choice choice, Optional<String> note, List<String> recipients) {}

    // dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Order state."
    private final Attribute<String> state = Attribute.define("state", String.class, new AttributeIndex(AttributeIndex.Type.KEYWORD));
    private final Attribute<String> statusText = Attribute.define("statusText", String.class);

    public StepList<Input> getSteps() { return StepList.startStep(new Begin()).otherSteps(new Finish()); }
    public PersistenceSchema getPersistenceSchema() { return PersistenceSchema.of(state, statusText); }

    // dex:field attribute-key:statusText value-type:string editable:false description:"Order status."
    @RPC(name = "GetDexSummary")
    public RPCResult<Map<String, Object>> summary(Context context) {
        return RPCResult.of(Map.of("statusText", statusText.get(context)));
    }

    // dex:field attribute-key:state value-type:string editable:false description:"Order state."
    @RPC(name = "GetDexDisplay")
    public RPCResult<Map<String, Object>> display(Context context) {
        return RPCResult.of(Map.of("state", state.get(context)));
    }

    // dex:group group-id:shipping group-label:"Shipping"
    // dex:explanation text:"Begin to ship the order."
    class Begin implements Step<Input> {
        public Class<Input> getInputType() { return Input.class; }

        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().addWaitForLock(AttributeLock.of(state))
                    .addExecuteLock(AttributeLock.of(statusText)).build();
        }

        public StepDecision execute(Context context, Input input) {
            String status = state.get(context);
            statusText.set(context, status);
            boolean ready = "ready".equals(status);
            if (ready) return StepDecision.goTo(Finish.class, null);
            return StepDecision.gracefulComplete();
        }
    }

    // dex:group group-id:shipping group-label:"Shipping"
    // dex:explanation text:"Finish shipping the order."
    static class Finish implements Step<Void> {
        public Class<Void> getInputType() { return Void.class; }

        public StepDecision execute(Context context, Void input) {
            return StepDecision.gracefulComplete();
        }
    }
}
