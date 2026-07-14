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

func (mutation *runMutation) MarkDurableTimerFired() {
	durableTimerFired := true
	mutation.update.DurableTimerFired = &durableTimerFired
	mutation.update.ActiveDurableTimerID = ptr.Any(ids.TaskID{})
	mutation.update.DurableTimerFireAt = ptr.Any(int64(0))
}

func (mutation *runMutation) armDurableTimerIfNeeded() {
	activeSteps := mutation.mergedActiveSteps()
	timerTask, _ := createDurableTimerFromActiveSteps(mutation.shardID, mutation.run, activeSteps)
	if timerTask == nil {
		return
	}
	mutation.newTasks = append(mutation.newTasks, p.TaskRow{Timer: timerTask})
	mutation.update.ActiveDurableTimerID = ptr.Any(timerTask.ID)
	mutation.update.DurableTimerFireAt = ptr.Any(timerTask.SortKey)
	mutation.update.DurableTimerFired = ptr.Any(false)
}

func createDurableTimerIfNeeded(shardID int32, run *p.RunRow, update *p.RunRowUpdate) (*p.TimerTaskRow, int64) {
	activeSteps := make(map[string]p.ActiveStepExecution)
	for key, value := range run.ActiveStepExecutions {
		activeSteps[key] = value
	}
	if update.ReplaceActiveStepExecutions != nil {
		activeSteps = make(map[string]p.ActiveStepExecution, len(*update.ReplaceActiveStepExecutions))
		for key, value := range *update.ReplaceActiveStepExecutions {
			activeSteps[key] = value
		}
	} else if update.ActiveStepExecutions != nil {
		for key, value := range update.ActiveStepExecutions {
			if value == nil {
				delete(activeSteps, key)
			} else {
				activeSteps[key] = *value
			}
		}
	}
	return createDurableTimerFromActiveSteps(shardID, run, activeSteps)
}

func createDurableTimerFromActiveSteps(shardID int32, run *p.RunRow, activeSteps map[string]p.ActiveStepExecution) (*p.TimerTaskRow, int64) {
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
		return nil, 0
	}
	if !run.ActiveDurableTimerID.IsZero() && run.DurableTimerFireAt > 0 && run.DurableTimerFireAt <= minFireAt {
		return nil, minFireAt
	}
	return &p.TimerTaskRow{
		ShardID:  shardID,
		ID:       ids.NewTaskID(),
		SortKey:  minFireAt,
		TaskType: p.TimerTaskStepWaitForTimer,
		TaskInfo: p.TimerTaskInfo{
			RunID:              run.ID,
			Namespace:          run.Namespace,
			CreatedByStepExeID: minStepExeID,
		},
	}, minFireAt
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
