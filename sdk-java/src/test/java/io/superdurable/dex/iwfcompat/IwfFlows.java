/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Context;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;

final class IwfFlows {
    static final BasicFlow BASIC = new BasicFlow();
    static final AbnormalExitFlow ABNORMAL_EXIT = new AbnormalExitFlow();
    static final EmptyInputFlow EMPTY_INPUT = new EmptyInputFlow();
    static final ModelInputFlow MODEL_INPUT = new ModelInputFlow();
    static final ProceedOnWaitFailureFlow PROCEED_ON_WAIT_FAILURE =
            new ProceedOnWaitFailureFlow();
    static final MixedWaitFlow MIXED_WAIT = new MixedWaitFlow();
    static final ExecuteOnlyFlow EXECUTE_ONLY = new ExecuteOnlyFlow();
    static final AnyCombinationFailFlow ANY_COMBINATION_FAIL = new AnyCombinationFailFlow();
    static final ConditionalCompleteFlow CONDITIONAL_COMPLETE = new ConditionalCompleteFlow();
    static final BasicInternalChannelFlow BASIC_INTERNAL = new BasicInternalChannelFlow();
    static final WaitingInternalChannelFlow WAITING_INTERNAL = new WaitingInternalChannelFlow();
    static final NoStartFlow NO_START = new NoStartFlow();
    static final NoStateFlow NO_STATE = new NoStateFlow();
    static final DeadEndFlow DEAD_END = new DeadEndFlow();
    static final BasicPersistenceFlow BASIC_PERSISTENCE = new BasicPersistenceFlow();
    static final SetAttributesFlow SET_ATTRIBUTES = new SetAttributesFlow();
    static final RpcFlow RPC = new RpcFlow();
    static final RpcLockingFlow RPC_LOCKING = new RpcLockingFlow();
    static final SignalFlow SIGNAL = new SignalFlow();
    static final StateOptionsFlow STATE_OPTIONS = new StateOptionsFlow();
    static final StateOptionsOverrideFlow STATE_OPTIONS_OVERRIDE =
            new StateOptionsOverrideFlow();
    static final StateRecoveryFlow STATE_RECOVERY = new StateRecoveryFlow();
    static final StateRecoveryNoWaitFlow STATE_RECOVERY_NO_WAIT =
            new StateRecoveryNoWaitFlow();
    static final TimerFlow TIMER = new TimerFlow();
    static final ForceFailFlow FORCE_FAIL = new ForceFailFlow();
    static final StateTimeoutFlow STATE_TIMEOUT = new StateTimeoutFlow();
    static final StateFailureFlow STATE_FAILURE = new StateFailureFlow();
    static final EmptyDecisionFlow EMPTY_DECISION = new EmptyDecisionFlow();

    private IwfFlows() {
    }

    static final class ModelInput {
        public int value;
    }

    static final class CompleteStringStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }
}
