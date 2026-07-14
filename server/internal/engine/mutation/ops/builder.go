package ops

import (
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type OpsTasksBuilder struct {
	shardID         int32
	namespace       string
	runID           string
	now             time.Time
	startTime       time.Time
	flowType        string
	taskListName    string
	logger          log.Logger
	highWaterAtRead int64
	nextEventID     int64
	out             []p.TaskRow
}

func NewForUpdate(run *p.RunRow, shardID int32, now time.Time, logger log.Logger) *OpsTasksBuilder {
	return &OpsTasksBuilder{
		shardID:         shardID,
		namespace:       run.Namespace,
		runID:           run.ID,
		now:             now,
		startTime:       run.CreatedAt,
		flowType:        run.FlowType,
		taskListName:    run.TaskListName,
		logger:          logger,
		highWaterAtRead: run.LastHistoryEventID,
		nextEventID:     run.LastHistoryEventID,
	}
}

func NewForCreate(run *p.RunRow, shardID int32, now time.Time, logger log.Logger) *OpsTasksBuilder {
	return &OpsTasksBuilder{
		shardID:         shardID,
		namespace:       run.Namespace,
		runID:           run.ID,
		now:             now,
		startTime:       run.CreatedAt,
		flowType:        run.FlowType,
		taskListName:    run.TaskListName,
		logger:          logger,
		highWaterAtRead: 0,
		nextEventID:     0,
	}
}

func (builder *OpsTasksBuilder) Tasks() []p.TaskRow { return builder.out }

func (builder *OpsTasksBuilder) HasHistoryEvents() bool {
	return builder.nextEventID > builder.highWaterAtRead
}

func (builder *OpsTasksBuilder) LastHistoryEventIDPtr() *int64 {
	if !builder.HasHistoryEvents() {
		return nil
	}
	eventID := builder.nextEventID
	return &eventID
}

func (builder *OpsTasksBuilder) AddVisibility(status p.RunStatus) {
	builder.out = append(builder.out, p.TaskRow{OpsFIFO: &p.OpsFIFOTaskRow{
		ShardID:  builder.shardID,
		ID:       ids.NewTaskID(),
		TaskType: p.OpsFIFOTaskVisibilityWrite,
		VisibilityPayload: &p.VisibilityEntry{
			Namespace:    builder.namespace,
			RunID:        builder.runID,
			FlowType:     builder.flowType,
			TaskListName: builder.taskListName,
			Status:       status,
			StartTime:    builder.startTime,
			UpdatedAt:    builder.now,
		},
	}})
}

func (builder *OpsTasksBuilder) AddHistoryRunStart(req *pb.StartRunRequest) {
	payload := &pb.HistoryRunStartPayload{
		FlowType:      req.FlowType,
		TaskListName:  req.TaskListName,
		Options:       req.Options,
		StartingSteps: req.StartingSteps,
	}
	builder.appendHistory(p.HistoryEventPayload{RunStart: payload}, "")
}

func (builder *OpsTasksBuilder) AddHistoryRunStop(status p.RunStatus, reason string) {
	builder.appendHistory(p.HistoryEventPayload{
		RunStop: &pb.HistoryRunStopPayload{RunStatus: int32(status), Reason: reason},
	}, "")
}

func (builder *OpsTasksBuilder) AddHistoryStepExecuteCompleted(
	req *pb.StepExecuteCompletedRequest,
	fromStepExeID string,
	conditionResults []*pb.ConditionResult,
	workerID string,
) {
	payload := &pb.HistoryStepExecuteCompletedPayload{
		StepExeId:              req.StepExeId,
		FromStepExeId:          fromStepExeID,
		WorkerRequestCounter:   req.GetContext().GetWorkerRequestCounter(),
		RequestToDrainChannels: req.RequestToDrainChannels,
		StopDecision:           req.StopDecision,
		StateToUpsert:          req.StateToUpsert,
		NextSteps:              req.NextSteps,
		CanceledStepExecutions: req.CanceledStepExecutions,
		ConditionResults:       conditionResults,
		ChannelPublish:         req.ChannelPublish,
		StepsUnblocked:         req.StepsUnblocked,
		ExecuteMethod:          req.ExecuteMethod,
	}
	builder.appendHistory(p.HistoryEventPayload{StepExecuteCompleted: payload}, workerID)
}

func (builder *OpsTasksBuilder) AddHistoryStepWaitForCompleted(req *pb.StepWaitForCompletedRequest, fromStepExeID string, workerID string) {
	payload := &pb.HistoryStepWaitForCompletedPayload{
		StepExeId:            req.StepExeId,
		FromStepExeId:        fromStepExeID,
		WorkerRequestCounter: req.GetContext().GetWorkerRequestCounter(),
		WaitForCondition:     req.WaitForCondition,
		StateToUpsert:        req.StateToUpsert,
		ChannelPublish:       req.ChannelPublish,
		StepsUnblocked:       req.StepsUnblocked,
		WaitForMethod:        req.WaitForMethod,
		NextSteps:            req.NextSteps,
	}
	builder.appendHistory(p.HistoryEventPayload{StepWaitForCompleted: payload}, workerID)
}

func (builder *OpsTasksBuilder) AddHistoryStepsUnblocked(req *pb.StepsUnblockedRequest, workerID string) {
	payload := &pb.HistoryStepsUnblockedPayload{
		WorkerRequestCounter: req.GetContext().GetWorkerRequestCounter(),
		StepsUnblocked:       req.StepsUnblocked,
	}
	builder.appendHistory(p.HistoryEventPayload{StepsUnblocked: payload}, workerID)
}

func (builder *OpsTasksBuilder) AddHistoryChannelPublish(req *pb.PublishToChannelRequest) {
	payload := &pb.HistoryChannelPublishPayload{
		ChannelName: req.ChannelName,
		Values:      req.Values,
	}
	builder.appendHistory(p.HistoryEventPayload{ChannelPublish: payload}, "")
}

func (builder *OpsTasksBuilder) AddHistoryRunFork(toEventId int64, reason string) {
	payload := &pb.HistoryRunForkPayload{
		ForkToEventId: toEventId,
		Reason:        reason,
	}
	builder.appendHistory(p.HistoryEventPayload{RunFork: payload}, "")
}

func (builder *OpsTasksBuilder) appendHistory(payload p.HistoryEventPayload, workerID string) {
	builder.nextEventID++
	builder.out = append(builder.out, p.TaskRow{OpsFIFO: &p.OpsFIFOTaskRow{
		ShardID:  builder.shardID,
		ID:       ids.NewTaskID(),
		TaskType: p.OpsFIFOTaskHistoryWrite,
		HistoryPayload: &p.HistoryEvent{
			Namespace:    builder.namespace,
			RunID:        builder.runID,
			EventID:      builder.nextEventID,
			OccurredAtMs: builder.now.UnixMilli(),
			WorkerID:     workerID,
			Payload:      payload,
		},
	}})
}
