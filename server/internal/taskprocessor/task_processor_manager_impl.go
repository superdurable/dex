package taskprocessor

import (
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shards"
)

type taskProcessorManagerImpl struct {
}

var _ shards.TaskProcessorsManager = (*taskProcessorManagerImpl)(nil)

func NewTaskProcessorManagerImpl() shards.TaskProcessorsManager {
	return &taskProcessorManagerImpl{}
}

func (t *taskProcessorManagerImpl) StartAll(shardID int32, rangeID int32) {
	//TODO implement me
	panic("implement me")
}

func (t *taskProcessorManagerImpl) StopAll(shardID int32) {
	//TODO implement me
	panic("implement me")
}

func (t *taskProcessorManagerImpl) GetShardMetadata(shardID int32) *p.ShardMetadata {
	//TODO implement me
	panic("implement me")
}

func (t *taskProcessorManagerImpl) NotifyNewImmediateTask(shardID int32) {
	//TODO implement me
	panic("implement me")
}

func (t *taskProcessorManagerImpl) NotifyNewTimerTask(shardID int32, fireAtUnixMs int64) {
	//TODO implement me
	panic("implement me")
}
