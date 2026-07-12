package mutation

import (
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func applyWorkerRetryUpdates(
	logger log.Logger,
	run *p.RunRow,
	retryUpdates map[string]*pb.StepRetryStateUpdate,
	patch map[string]*p.ActiveStepExecution,
	maxBytes int,
) errors.CategorizedError {
	if len(retryUpdates) == 0 {
		return nil
	}
	for stepExeID, retryUpdate := range retryUpdates {
		if retryUpdate == nil {
			continue
		}
		if patched, ok := patch[stepExeID]; ok && patched == nil {
			continue
		}
		existing, ok := run.ActiveStepExecutions[stepExeID]
		if patched, hasPatch := patch[stepExeID]; hasPatch && patched != nil {
			existing = *patched
			ok = true
		}
		if !ok {
			logger.Debug("Retry update for unknown active step",
				tag.RunID(run.ID), tag.Namespace(run.Namespace),
				tag.StepExeID(stepExeID))
			continue
		}
		step := existing
		if retryUpdate.ClearWaitForRetryState {
			step.WaitForRetryState = nil
		}
		if retryUpdate.WaitForRetryState != nil {
			waitForRetry, err := convertStepRetryState(retryUpdate.WaitForRetryState, maxBytes)
			if err != nil {
				logger.Debug("StepRetryState field exceeds max bytes",
					tag.RunID(run.ID), tag.Namespace(run.Namespace),
					tag.StepExeID(stepExeID), tag.Error(err))
				return err
			}
			step.WaitForRetryState = waitForRetry
		}
		if retryUpdate.ExecuteRetryState != nil {
			executeRetry, err := convertStepRetryState(retryUpdate.ExecuteRetryState, maxBytes)
			if err != nil {
				logger.Debug("StepRetryState field exceeds max bytes",
					tag.RunID(run.ID), tag.Namespace(run.Namespace),
					tag.StepExeID(stepExeID), tag.Error(err))
				return err
			}
			step.ExecuteRetryState = executeRetry
		}
		patch[stepExeID] = &step
	}
	return nil
}

func convertStepRetryState(pbState *pb.StepRetryState, maxBytes int) (*p.RetryState, errors.CategorizedError) {
	if pbState == nil {
		return nil, nil
	}
	if err := validateStepRetryStatePB(pbState, maxBytes); err != nil {
		return nil, err
	}
	return &p.RetryState{
		FirstAttemptTime:    time.UnixMilli(pbState.FirstAttemptTimeMs),
		CurrentAttempts:     pbState.CurrentAttempts,
		LastError:           pbState.LastError,
		LastErrorStackTrace: pbState.LastErrorStackTrace,
	}, nil
}

func validateStepRetryStatePB(pbState *pb.StepRetryState, maxBytes int) errors.CategorizedError {
	if pbState == nil {
		return nil
	}
	if err := validateRetryStringSize(pbState.LastError, maxBytes, "last_error"); err != nil {
		return err
	}
	if err := validateRetryStringSize(pbState.LastErrorStackTrace, maxBytes, "last_error_stack_trace"); err != nil {
		return err
	}
	return nil
}

func validateRetryStringSize(value string, maxBytes int, field string) errors.CategorizedError {
	if len(value) > maxBytes {
		return errors.NewInvalidInputError(
			fmt.Sprintf("StepRetryState.%s exceeds %d bytes (got %d)", field, maxBytes, len(value)), nil)
	}
	return nil
}
