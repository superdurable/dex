function enumLabel(value: unknown, labels: Record<string, string>): string {
  const key = String(value ?? 0);
  return labels[key] ?? `Unknown (${key})`;
}

export function durabilityLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Synchronous',
    2: 'Asynchronous',
    STEP_DURABILITY_UNSPECIFIED: 'Unspecified',
    STEP_DURABILITY_SYNC: 'Synchronous',
    STEP_DURABILITY_ASYNC: 'Asynchronous',
  });
}

export function waitingConditionTypeLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'All completed',
    2: 'Any completed',
    3: 'Any combination completed',
    WAITING_CONDITION_TYPE_UNSPECIFIED: 'Unspecified',
    WAITING_CONDITION_TYPE_ALL_COMPLETED: 'All completed',
    WAITING_CONDITION_TYPE_ANY_COMPLETED: 'Any completed',
    WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED: 'Any combination completed',
  });
}

export function conditionStatusLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Waiting',
    2: 'Completed',
    CONDITION_STATUS_UNSPECIFIED: 'Unspecified',
    CONDITION_STATUS_WAITING: 'Waiting',
    CONDITION_STATUS_COMPLETED: 'Completed',
  });
}

export function flowStatusLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Running',
    2: 'Completed',
    3: 'Failed',
    4: 'Timed out',
    5: 'Terminated',
    6: 'Canceled',
    7: 'Continued as new',
    FLOW_STATUS_UNSPECIFIED: 'Unspecified',
    FLOW_STATUS_RUNNING: 'Running',
    FLOW_STATUS_COMPLETED: 'Completed',
    FLOW_STATUS_FAILED: 'Failed',
    FLOW_STATUS_TIMEOUT: 'Timed out',
    FLOW_STATUS_TERMINATED: 'Terminated',
    FLOW_STATUS_CANCELED: 'Canceled',
    FLOW_STATUS_CONTINUED_AS_NEW: 'Continued as new',
  });
}

export function flowErrorTypeLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Step decision failed the flow',
    2: 'Client API failed the flow',
    3: 'Worker method failed',
    4: 'Invalid flow code',
    6: 'Internal error',
    FLOW_ERROR_TYPE_UNSPECIFIED: 'Unspecified',
    FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW: 'Step decision failed the flow',
    FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW: 'Client API failed the flow',
    FLOW_ERROR_TYPE_WORKER_API_FAIL: 'Worker method failed',
    FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE: 'Invalid flow code',
    FLOW_ERROR_TYPE_INTERNAL: 'Internal error',
  });
}

export function closeDecisionTypeLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Complete when channels are empty',
    2: 'Graceful complete',
    3: 'Force complete',
    4: 'Force fail',
    5: 'Dead end',
    CLOSE_DECISION_TYPE_UNSPECIFIED: 'Unspecified',
    CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY: 'Complete when channels are empty',
    CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE: 'Graceful complete',
    CLOSE_DECISION_TYPE_FORCE_COMPLETE: 'Force complete',
    CLOSE_DECISION_TYPE_FORCE_FAIL: 'Force fail',
    CLOSE_DECISION_TYPE_DEAD_END: 'Dead end',
  });
}

export function activeStepSearchModeLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'All steps',
    2: 'Steps with WaitFor',
    3: 'Disabled',
    ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED: 'Unspecified',
    ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL: 'All steps',
    ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR: 'Steps with WaitFor',
    ACTIVE_STEP_SEARCH_MODE_DISABLED: 'Disabled',
  });
}

export function waitForFailurePolicyLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Fail flow',
    2: 'Proceed',
    WAIT_FOR_METHOD_FAILURE_POLICY_UNSPECIFIED: 'Unspecified',
    WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE: 'Fail flow',
    WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE: 'Proceed',
  });
}

export function executeFailurePolicyLabel(value: unknown): string {
  return enumLabel(value, {
    0: 'Unspecified',
    1: 'Fail flow',
    2: 'Proceed to configured step',
    EXECUTE_METHOD_FAILURE_POLICY_UNSPECIFIED: 'Unspecified',
    EXECUTE_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_EXECUTE_METHOD_FAILURE: 'Fail flow',
    EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP: 'Proceed to configured step',
  });
}
