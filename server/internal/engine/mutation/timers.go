package mutation

import (
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/common/utils/ptr"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func newImmediateTask(shardID int32, taskType p.ImmediateTaskType, info p.ImmediateTaskInfo) p.TaskRow {
	return p.TaskRow{
		Immediate: &p.ImmediateTaskRow{
			ShardID:  shardID,
			ID:       ids.NewTaskID(),
			TaskType: taskType,
			TaskInfo: info,
		},
	}
}

func (mutation *runMutation) armDurableTimerIfNeeded() {
	activeSteps := mutation.getCurrentMergedActiveStepsView()
	var minFireAt int64
	var minStepExeID string
	for stepExeID, step := range activeSteps {
		if step.WaitForCondition == nil {
			continue
		}
		for _, condition := range step.WaitForCondition.Conditions {
			if condition.Timer != nil {
				if minFireAt == 0 || condition.Timer.FireAtUnixMs < minFireAt {
					minFireAt = condition.Timer.FireAtUnixMs
					minStepExeID = stepExeID
				}
			}
		}
	}
	if minFireAt == 0 {
		return
	}
	timerTask := &p.TimerTaskRow{
		ShardID:  mutation.shardID,
		ID:       ids.NewTaskID(),
		SortKey:  minFireAt,
		TaskType: p.TimerTaskStepWaitForTimer,
		TaskInfo: p.TimerTaskInfo{
			RunID:              mutation.run.ID,
			Namespace:          mutation.run.Namespace,
			CreatedByStepExeID: minStepExeID,
		},
	}

	mutation.newTasks = append(mutation.newTasks, p.TaskRow{Timer: timerTask})
	mutation.update.ActiveDurableTimerID = ptr.Any(timerTask.ID)
}

func (mutation *runMutation) TransitionToWaitingForWorker(reason TransitionReason) {
	waiting := p.RunStatusWaitingForWorker
	mutation.update.Status = &waiting
	mutation.update.HeartbeatTimerID = ptr.Any(ids.TaskID{})
	mutation.transitionReason = reason

	switch reason {
	case TransitionReasonHeartbeatTimeout:
		mutation.appendResumeDispatchTask()
		mutation.ops.AddVisibility(waiting)
	case TransitionReasonReleaseRunYield:
		emptyWorker := ""
		mutation.update.WorkerID = &emptyWorker
		mutation.newTasks = append(mutation.newTasks, p.TaskRow{Immediate: &p.ImmediateTaskRow{
			ShardID:   mutation.shardID,
			ID:        ids.NewTaskID(),
			TaskType:  p.ImmediateTaskRunResumeDispatch,
			TaskInfo:  p.ImmediateTaskInfo{RunID: mutation.run.ID, Namespace: mutation.run.Namespace, TaskListName: mutation.run.TaskListName},
			SortKey:   mutation.now.UnixMilli(),
			CreatedAt: mutation.now,
		}})
		mutation.ops.AddVisibility(waiting)
	case TransitionReasonHandleRunDispatchResult:
		mutation.ops.AddVisibility(waiting)
	}
}

func (mutation *runMutation) RecordWorkerCounter(counter int64) {
	mutation.update.WorkerRequestCounter = &counter
}
