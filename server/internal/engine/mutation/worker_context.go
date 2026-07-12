package mutation

import (
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// duplicateReq=true only when worker_request_counter matches run (idempotent retry).
func validateWorkerCallContextAndRunStatus(run *p.RunRow, workerCtx *pb.WorkerCallContext) (duplicateReq bool, err errors.CategorizedError) {
	if workerCtx == nil {
		return false, errors.NewInvalidInputError("WorkerCallContext is required", nil)
	}
	// Idempotency first: safe no-op even if run is terminal since.
	if workerCtx.WorkerRequestCounter == run.WorkerRequestCounter {
		return true, nil
	}
	if run.Status != p.RunStatusRunning {
		// InvalidInput (not Conflict): the SDK maps InvalidArgument to
		// ownership-lost and drops the run. "expected Running" is asserted.
		return false, errors.NewInvalidInputError(
			fmt.Sprintf("run not running (status=%s), expected Running", run.Status.Name()), nil)
	}
	if run.WorkerID != "" && run.WorkerID != workerCtx.WorkerId {
		return false, errors.NewConflictError(
			fmt.Sprintf("worker_id mismatch: run owned by %q, call from %q", run.WorkerID, workerCtx.WorkerId), nil)
	}
	switch {
	case workerCtx.WorkerRequestCounter == run.WorkerRequestCounter+1:
		return false, nil
	case workerCtx.WorkerRequestCounter > run.WorkerRequestCounter+1:
		return false, errors.NewInvalidInputError(
			fmt.Sprintf("worker_request_counter gap: got %d, expected %d", workerCtx.WorkerRequestCounter, run.WorkerRequestCounter+1), nil)
	default:
		return false, errors.NewInvalidInputError(
			fmt.Sprintf("worker_request_counter regression: got %d, last seen %d", workerCtx.WorkerRequestCounter, run.WorkerRequestCounter), nil)
	}
}
