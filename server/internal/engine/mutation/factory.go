package mutation

import (
	"context"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/internal/engine/mutation/ops"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
)

type Factory struct {
	deps Deps
}

// Deps bundles long-lived dependencies for Factory.
type Deps struct {
	RunStore                   shardmanager.ShardedRunStore
	Logger                     log.Logger
	HeartbeatTimerDuration     time.Duration
	StepRetryLastErrorMaxBytes int
}

func NewFactory(deps Deps) *Factory {
	return &Factory{deps: deps}
}

func (factory *Factory) NewMutationForCreate(shardID int32, run *p.RunRow, now time.Time) RunMutation {
	return &runMutation{
		deps:    factory.deps,
		shardID: shardID,
		mode:    commitModeCreate,
		run:     run,
		update: &p.RunRowUpdate{
			// prevent nil panic
			ActiveStepExecutions: make(map[string]*p.ActiveStepExecution),
		},
		ops:              ops.NewForCreate(run, shardID, now, factory.deps.Logger),
		now:              now,
		transitionReason: TransitionReasonStartRun,
	}
}

// NewMutationForUpdate opens a server-side CAS update attempt.
func (factory *Factory) NewMutationForUpdate(
	ctx context.Context,
	shardID int32,
	namespace, runID string,
) (RunMutation, errors.CategorizedError) {
	return factory.openMutationForUpdate(ctx, shardID, namespace, runID)
}

// NewMutationForUpdateWithWorkerContext opens a worker RPC CAS attempt.
// duplicateReq=true when worker_request_counter already committed (idempotent).
// Callers still return catch-up on duplicateReq — lost-response retry(timeout), StopRequested.
func (factory *Factory) NewMutationForUpdateWithWorkerContext(
	ctx context.Context,
	shardID int32,
	namespace, runID string,
	workerContext *pb.WorkerCallContext,
) (rm RunMutation, duplicateReq bool, err errors.CategorizedError) {
	runMutation, err := factory.openMutationForUpdate(ctx, shardID, namespace, runID)
	if err != nil {
		return nil, false, err
	}
	duplicateReq, validateErr := validateWorkerCallContextAndRunStatus(runMutation.GetRun(), workerContext)
	if validateErr != nil {
		return nil, false, validateErr
	}
	return runMutation, duplicateReq, nil
}

func (factory *Factory) openMutationForUpdate(
	ctx context.Context,
	shardID int32,
	namespace, runID string,
) (RunMutation, errors.CategorizedError) {
	run, err := factory.deps.RunStore.GetRun(ctx, shardID, namespace, runID, p.GetRunOptions{})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &runMutation{
		deps:    factory.deps,
		shardID: shardID,
		mode:    commitModeUpdate,
		run:     run,
		update:  &p.RunRowUpdate{},
		ops:     ops.NewForUpdate(run, shardID, now, factory.deps.Logger),
		now:     now,
	}, nil
}
