// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"encoding/json"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
	"google.golang.org/protobuf/proto"
)

// Stable history payload discriminators. Keep in sync with the Mongo backend
// and HistoryEventPayload.TypeName() — never rename existing entries.
const (
	histTypeRunStart             = "run_start"
	histTypeRunStop              = "run_stop"
	histTypeStepExecuteCompleted = "step_execute_completed"
	histTypeStepWaitForCompleted = "step_wait_for_completed"
	histTypeChannelPublish       = "channel_publish"
	histTypeStepsUnblocked       = "steps_unblocked"
	histTypeRunFork              = "run_fork"
)

// jsonbOf marshals a value to JSON bytes for a jsonb column. pgx encodes a
// []byte into a jsonb param as raw JSON, so no SQL cast is needed.
func jsonbOf(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// marshalHistoryPayload returns the (discriminator, proto bytes) pair for the
// active variant. Mirrors the Mongo backend so the wire encoding is identical.
func marshalHistoryPayload(payload p.HistoryEventPayload) (string, []byte, error) {
	var (
		typeName string
		msg      proto.Message
	)
	switch {
	case payload.RunStart != nil:
		typeName, msg = histTypeRunStart, payload.RunStart
	case payload.RunStop != nil:
		typeName, msg = histTypeRunStop, payload.RunStop
	case payload.StepExecuteCompleted != nil:
		typeName, msg = histTypeStepExecuteCompleted, payload.StepExecuteCompleted
	case payload.StepWaitForCompleted != nil:
		typeName, msg = histTypeStepWaitForCompleted, payload.StepWaitForCompleted
	case payload.ChannelPublish != nil:
		typeName, msg = histTypeChannelPublish, payload.ChannelPublish
	case payload.StepsUnblocked != nil:
		typeName, msg = histTypeStepsUnblocked, payload.StepsUnblocked
	case payload.RunFork != nil:
		typeName, msg = histTypeRunFork, payload.RunFork
	default:
		return "", nil, fmt.Errorf("no payload variant set")
	}
	bytes, err := proto.Marshal(msg)
	if err != nil {
		return "", nil, err
	}
	return typeName, bytes, nil
}

// unmarshalHistoryPayload routes proto bytes into the variant named by typeName.
func unmarshalHistoryPayload(typeName string, bytes []byte) (p.HistoryEventPayload, error) {
	switch typeName {
	case histTypeRunStart:
		out := &pb.HistoryRunStartPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{RunStart: out}, nil
	case histTypeRunStop:
		out := &pb.HistoryRunStopPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{RunStop: out}, nil
	case histTypeStepExecuteCompleted:
		out := &pb.HistoryStepExecuteCompletedPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{StepExecuteCompleted: out}, nil
	case histTypeStepWaitForCompleted:
		out := &pb.HistoryStepWaitForCompletedPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{StepWaitForCompleted: out}, nil
	case histTypeChannelPublish:
		out := &pb.HistoryChannelPublishPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{ChannelPublish: out}, nil
	case histTypeStepsUnblocked:
		out := &pb.HistoryStepsUnblockedPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{StepsUnblocked: out}, nil
	case histTypeRunFork:
		out := &pb.HistoryRunForkPayload{}
		if err := proto.Unmarshal(bytes, out); err != nil {
			return p.HistoryEventPayload{}, err
		}
		return p.HistoryEventPayload{RunFork: out}, nil
	default:
		return p.HistoryEventPayload{}, fmt.Errorf("unknown payload_type %q", typeName)
	}
}

// opsHistoryJSON is the JSON envelope for an OpsFIFO HistoryWrite payload. The
// proto bytes ride along base64-encoded (encoding/json encodes []byte as
// base64) so the whole task payload is a single jsonb value.
type opsHistoryJSON struct {
	Namespace    string `json:"namespace"`
	RunID        string `json:"run_id"`
	EventID      int64  `json:"event_id"`
	OccurredAtMs int64  `json:"occurred_at_ms"`
	WorkerID     string `json:"worker_id"`
	PayloadType  string `json:"payload_type"`
	Payload      []byte `json:"payload"`
}

// encodeOpsFIFOPayload serializes the active payload of an OpsFIFOTaskRow into
// jsonb bytes, discriminated by task.TaskType.
func encodeOpsFIFOPayload(task *p.OpsFIFOTaskRow) ([]byte, error) {
	switch task.TaskType {
	case p.OpsFIFOTaskHistoryWrite:
		if task.HistoryPayload == nil {
			return nil, fmt.Errorf("OpsFIFO HistoryWrite task has nil HistoryPayload")
		}
		typeName, payloadBytes, err := marshalHistoryPayload(task.HistoryPayload.Payload)
		if err != nil {
			return nil, err
		}
		return json.Marshal(opsHistoryJSON{
			Namespace:    task.HistoryPayload.Namespace,
			RunID:        task.HistoryPayload.RunID,
			EventID:      task.HistoryPayload.EventID,
			OccurredAtMs: task.HistoryPayload.OccurredAtMs,
			WorkerID:     task.HistoryPayload.WorkerID,
			PayloadType:  typeName,
			Payload:      payloadBytes,
		})
	case p.OpsFIFOTaskVisibilityWrite:
		if task.VisibilityPayload == nil {
			return nil, fmt.Errorf("OpsFIFO VisibilityWrite task has nil VisibilityPayload")
		}
		return json.Marshal(task.VisibilityPayload)
	default:
		return nil, fmt.Errorf("unknown OpsFIFOTaskType %d", task.TaskType)
	}
}

// decodeOpsFIFOPayload reconstructs the HistoryPayload / VisibilityPayload of
// an OpsFIFOTaskRow from its jsonb bytes.
func decodeOpsFIFOPayload(taskType p.OpsFIFOTaskType, data []byte) (*p.HistoryEvent, *p.VisibilityEntry, error) {
	switch taskType {
	case p.OpsFIFOTaskHistoryWrite:
		var env opsHistoryJSON
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, nil, err
		}
		payload, err := unmarshalHistoryPayload(env.PayloadType, env.Payload)
		if err != nil {
			return nil, nil, err
		}
		return &p.HistoryEvent{
			Namespace:    env.Namespace,
			RunID:        env.RunID,
			EventID:      env.EventID,
			OccurredAtMs: env.OccurredAtMs,
			WorkerID:     env.WorkerID,
			Payload:      payload,
		}, nil, nil
	case p.OpsFIFOTaskVisibilityWrite:
		var entry p.VisibilityEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, nil, err
		}
		return nil, &entry, nil
	default:
		return nil, nil, fmt.Errorf("unknown OpsFIFOTaskType %d", taskType)
	}
}
