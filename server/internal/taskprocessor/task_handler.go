package taskprocessor

import (
	"context"

	"github.com/superdurable/dex/server/internal/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type TaskHandler interface {
	HandleImmediateTask(ctx context.Context, shardID int32, task *p.ImmediateTaskRow) errors.CategorizedError
	HandleTimerTask(ctx context.Context, shardID int32, task *p.TimerTaskRow) errors.CategorizedError
}

type taskHandlerImpl struct{}

func NewTaskHandlerImpl() TaskHandler {
	return &taskHandlerImpl{}
}

func (t taskHandlerImpl) HandleImmediateTask(ctx context.Context, shardID int32, task *p.ImmediateTaskRow) errors.CategorizedError {
	//TODO implement me
	panic("implement me")
}

func (t taskHandlerImpl) HandleTimerTask(ctx context.Context, shardID int32, task *p.TimerTaskRow) errors.CategorizedError {
	//TODO implement me
	panic("implement me")
}
