// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { AbnormalExitFlow } from "./abnormal_exit_flow.js";
import { AnyCombinationFailFlow } from "./any_combination_fail_flow.js";
import { BasicFlow } from "./basic_flow.js";
import { BasicInternalChannelFlow } from "./basic_internal_channel_flow.js";
import { BasicPersistenceFlow } from "./basic_persistence_flow.js";
import { ConditionalCompleteFlow } from "./conditional_complete_flow.js";
import { DeadEndFlow } from "./dead_end_flow.js";
import { EmptyDecisionFlow } from "./empty_decision_flow.js";
import { EmptyInputFlow } from "./empty_input_flow.js";
import { ExecuteOnlyFlow } from "./execute_only_flow.js";
import { ForceFailFlow } from "./force_fail_flow.js";
import { MixedWaitFlow } from "./mixed_wait_flow.js";
import { ModelInputFlow } from "./model_input_flow.js";
import { NoStartFlow } from "./no_start_flow.js";
import { NoStateFlow } from "./no_state_flow.js";
import { ProceedOnWaitFailureFlow } from "./proceed_on_wait_failure_flow.js";
import { RpcFlow } from "./rpc_flow.js";
import { RpcLockingFlow } from "./rpc_locking_flow.js";
import { RpcMemoReplacementFlow } from "./rpc_memo_replacement_flow.js";
import { SetAttributesFlow } from "./set_attributes_flow.js";
import { SignalFlow } from "./signal_flow.js";
import { StateFailureFlow } from "./state_failure_flow.js";
import { StateOptionsFlow } from "./state_options_flow.js";
import { StateOptionsOverrideFlow } from "./state_options_override_flow.js";
import { StateRecoveryFlow } from "./state_recovery_flow.js";
import { StateRecoveryNoWaitFlow } from "./state_recovery_no_wait_flow.js";
import { StateTimeoutFlow } from "./state_timeout_flow.js";
import { TimerFlow } from "./timer_flow.js";
import { WaitingInternalChannelFlow } from "./waiting_internal_channel_flow.js";

export const BASIC = new BasicFlow();
export const ABNORMAL_EXIT = new AbnormalExitFlow();
export const EMPTY_INPUT = new EmptyInputFlow();
export const MODEL_INPUT = new ModelInputFlow();
export const PROCEED_ON_WAIT_FAILURE = new ProceedOnWaitFailureFlow();
export const MIXED_WAIT = new MixedWaitFlow();
export const EXECUTE_ONLY = new ExecuteOnlyFlow();
export const ANY_COMBINATION_FAIL = new AnyCombinationFailFlow();
export const CONDITIONAL_COMPLETE = new ConditionalCompleteFlow();
export const BASIC_INTERNAL = new BasicInternalChannelFlow();
export const WAITING_INTERNAL = new WaitingInternalChannelFlow();
export const NO_START = new NoStartFlow();
export const NO_STATE = new NoStateFlow();
export const DEAD_END = new DeadEndFlow();
export const BASIC_PERSISTENCE = new BasicPersistenceFlow();
export const SET_ATTRIBUTES = new SetAttributesFlow();
export const RPC = new RpcFlow();
export const RPC_LOCKING = new RpcLockingFlow();
export const RPC_MEMO_REPLACEMENT = new RpcMemoReplacementFlow();
export const SIGNAL = new SignalFlow();
export const STATE_OPTIONS = new StateOptionsFlow();
export const STATE_OPTIONS_OVERRIDE = new StateOptionsOverrideFlow();
export const STATE_RECOVERY = new StateRecoveryFlow();
export const STATE_RECOVERY_NO_WAIT = new StateRecoveryNoWaitFlow();
export const TIMER = new TimerFlow();
export const FORCE_FAIL = new ForceFailFlow();
export const STATE_TIMEOUT = new StateTimeoutFlow();
export const STATE_FAILURE = new StateFailureFlow();
export const EMPTY_DECISION = new EmptyDecisionFlow();
