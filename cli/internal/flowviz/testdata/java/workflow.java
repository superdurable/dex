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
import java.util.Map;
import java.util.Optional;

class Workflow implements Flow<Workflow.Input> {
    enum Choice { SEND, HOLD }
    record Input(Choice choice, Optional<String> note, List<String> recipients) {}

    // dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"Order state."
    private final Attribute<String> state = Attribute.define("state", String.class, new AttributeIndex(AttributeIndex.Type.KEYWORD));
    private final Attribute<String> summary = Attribute.define("statusText", String.class);
    private final AttributeMap<String> orders = AttributeMap.define("orders", String.class);
    private final Channel<String> signal = Channel.define("signal", String.class);
    private final ChannelMap<String> approvals = ChannelMap.define("approvals", String.class);
    private final Stream<String> progress = Stream.define("progress", String.class, 1024);
    private final Join<String> shipping = Join.define("shipping", String.class);
    private final Begin begin = new Begin();
    private final Ship ship = new Ship();
    private final Finish finish = new Finish();

    public String getFlowType() { return "OrderFlow"; }

    public StepList<Input> getSteps() {
        return StepList.startStep(begin).otherSteps(ship, finish);
    }

    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(state, summary, orders, signal, approvals, progress, shipping);
    }

    public StepDecision handleTimeout(Context context) {
        return StepDecision.goTo(Finish.class, null);
    }

    private FlowTimeoutHandlerOptions timeoutOptions() {
        return FlowTimeoutHandlerOptions.newBuilder().onFailureProceedTo(Finish.class).build();
    }

    // dex:field attribute-key:statusText value-type:string editable:false description:"Order status."
    @RPC(name = "GetDexSummary")
    public RPCResult<Map<String, Object>> readSummary(Context context) {
        return RPCResult.of(Map.of("statusText", summary.get(context)));
    }

    // dex:field attribute-key:state value-type:string editable:false description:"Order state."
    @RPC
    public RPCResult<Map<String, Object>> GetDexDisplay(Context context) {
        return RPCResult.of(Map.ofEntries(Map.entry("state", state.get(context))));
    }

    @RPC(lockAttributes = {"state"}, lockAttributeMaps = {@RPCAttributeMapLock(attribute = "orders", instance = "current")})
    public RPCResult<Boolean> expedite(Context context) {
        signal.publish(context, "expedite");
        return RPCResult.of(true, StepMovement.of(Finish.class, null)).withCancelingSteps(Ship.class);
    }

    // dex:group group-id:shipping group-label:"Shipping"
    // dex:explanation text:"Choose how to ship the order."
    class Begin implements Step<Input> {
        public Class<Input> getInputType() { return Input.class; }

        public StepDecision execute(Context context, Input input) {
            orders.instance("current").set(context, "new");
            state.set(context, "ready");
            progress.write(context, "dispatch");
            return input.choice() == Choice.HOLD
                    ? StepDecision.goTo(Finish.class, null)
                    : StepDecision.goToMany(StepMovement.of(Ship.class, "current"), StepMovement.of(Finish.class, null));
        }
    }

    // dex:group group-id:shipping group-label:"Shipping"
    // dex:explanation text:"Wait for approval and ship the order."
    class Ship implements Step<String> {
        public Class<String> getInputType() { return String.class; }
        public String getStepType() { return "ShipOrder"; }

        public StepOptions getStepOptions() {
            return StepOptions.newBuilder().onExecuteFailureProceedTo(Finish.class, null).addExecuteLock(state.lock()).build();
        }

        public Wait waitFor(Context context, String input) {
            return Wait.anyCombinationOf(
                    ConditionCombination.of(signal.forOne(), approvals.forN(input, 2), shipping.forFirstBranch(null)),
                    ConditionCombination.of(Timer.byDuration(Duration.ofMinutes(5)),
                            SubFlow.run(LabelFlow.class, input), GracefulCompletion.requested()));
        }

        public StepDecision execute(Context context, String input) {
            if (orders.get(context, input) == null) return StepDecision.forceFail("missing order");
            if ("pending".equals(input)) {
                return StepDecision.forceCompleteIfChannelsEmpty("done", StepMovement.of(Finish.class, null), signal, approvals);
            }
            if ("branch".equals(input)) return shipping.endBranch(context, null, "shipped");
            return StepDecision.gracefulComplete().withCancelingSiblingSteps(Finish.class);
        }
    }
}

// dex:group group-id:shipping group-label:"Shipping"
// dex:explanation text:"Close the order."
class Finish implements Step<Void> {
    public Class<Void> getInputType() { return Void.class; }

    public StepDecision execute(Context context, Void input) {
        return StepDecision.forceComplete();
    }
}
