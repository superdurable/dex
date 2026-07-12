package dex

import (
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func stepOptionsToSnapshot(step stepCommon) *pb.StepOptionsSnapshot {
	opts := step.GetStepOptions()
	var waitForTimeout, executeTimeout time.Duration
	var waitForRetry, executeRetry *RetryPolicy
	if opts != nil {
		waitForTimeout = opts.WaitForMethodTimeout
		executeTimeout = opts.ExecuteMethodTimeout
		waitForRetry = opts.WaitForMethodRetryPolicy
		executeRetry = opts.ExecuteMethodRetryPolicy
	}
	snapshot := &pb.StepOptionsSnapshot{
		WaitForMethodTimeoutMs:   int64(resolveMethodTimeout(waitForTimeout, defaultWaitForMethodTimeout).Milliseconds()),
		ExecuteMethodTimeoutMs:   int64(resolveMethodTimeout(executeTimeout, defaultExecuteMethodTimeout).Milliseconds()),
		WaitForMethodRetryPolicy: retryPolicyToSnapshot(waitForRetry),
		ExecuteMethodRetryPolicy: retryPolicyToSnapshot(executeRetry),
	}
	if opts != nil {
		if handler := opts.WaitForMethodProceedToAfterRetryExhausted; handler != nil {
			snapshot.WaitForMethodProceedToAfterRetryExhaustedStepId = stepIDFromCommon(handler)
		}
		if handler := opts.ExecuteMethodProceedToAfterRetryExhausted; handler != nil {
			snapshot.ExecuteMethodProceedToAfterRetryExhaustedStepId = stepIDFromCommon(handler)
		}
	}
	return snapshot
}

func retryPolicyToSnapshot(policy *RetryPolicy) *pb.StepRetryPolicySnapshot {
	if policy == nil {
		policy = defaultRetryPolicy
	}
	return &pb.StepRetryPolicySnapshot{
		MaxAttempts:        int32(policy.MaxAttempts),
		InitialIntervalMs:  policy.InitialInterval.Milliseconds(),
		BackoffCoefficient: policy.BackoffCoefficient,
		MaximumIntervalMs:  policy.MaximumInterval.Milliseconds(),
		TotalTimeoutMs:     policy.TotalTimeout.Milliseconds(),
	}
}
