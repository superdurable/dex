package mongo

import (
	"context"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

// maxGetHistoryEventsLimit caps the page size returned by GetHistoryEvents
// regardless of the caller-requested value. Mirrors the spec for OpsService
// so a single API call can never page through more than this many events.
const maxGetHistoryEventsLimit = 1000

// Stable string discriminators stored in the BSON `payload_type` field. Keep
// in sync with HistoryEventPayload.TypeName() — never rename existing entries.
const (
	histTypeRunStart             = "run_start"
	histTypeRunStop              = "run_stop"
	histTypeStepExecuteCompleted = "step_execute_completed"
	histTypeStepWaitForCompleted = "step_wait_for_completed"
	histTypeChannelPublish       = "channel_publish"
	histTypeStepsUnblocked       = "steps_unblocked"
)

type mongoHistoryStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

// NewHistoryStoreWithDatabase opens an independent Mongo client targeting the
// history cluster's database. Used by ServerApp wiring (production) and by
// integration tests that ensure schema via EnsureSchemaForConfig.
func NewHistoryStoreWithDatabase(ctx context.Context, uri, database string, timeouts OperationTimeouts) (p.HistoryStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for history store", err)
	}
	return &mongoHistoryStore{
		client:   client,
		db:       client.Database(resolveDatabase(database, defaultHistoryDatabase)),
		timeouts: timeouts,
	}, nil
}

func (s *mongoHistoryStore) Close() error { return s.client.Disconnect(context.Background()) }

// BatchInsertHistory inserts every event. Implementation notes:
//   - Each payload variant is proto-marshaled and stored alongside a string
//     `payload_type` discriminator so reads can dispatch into the correct
//     concrete pb message without sniffing bytes.
//   - InsertMany with ordered=false: a duplicate-key error on one event does
//     NOT halt the rest of the batch, which matters for the OpsFIFO replay
//     model (the reader retries the same batch on partial failure; events
//     that already landed are skipped via duplicate-key, others insert).
//   - mongo.IsDuplicateKeyError check after BulkWriteException so the caller
//     sees nil for "everything inserted or already there" — which is the
//     correctness contract for the OpsFIFO offset advance.
func (s *mongoHistoryStore) BatchInsertHistory(ctx context.Context, events []p.HistoryEvent) errors.CategorizedError {
	if len(events) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	docs := make([]interface{}, len(events))
	for i := range events {
		e := events[i]
		if vErr := e.Payload.Validate(); vErr != nil {
			return p.NewInternalError("BatchInsertHistory: invalid payload", vErr)
		}
		payloadType, payloadBytes, marshalErr := marshalHistoryPayload(e.Payload)
		if marshalErr != nil {
			return p.NewInternalError("BatchInsertHistory: marshal payload", marshalErr)
		}
		docs[i] = bson.M{
			fieldRunID:       e.RunID,
			fieldEventID:     e.EventID,
			fieldNamespace:   e.Namespace,
			"occurred_at_ms": e.OccurredAtMs,
			"worker_id":      e.WorkerID,
			"payload_type":   payloadType,
			"payload":        primitive.Binary{Data: payloadBytes},
		}
	}

	coll := s.db.Collection(collHistory)
	_, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err == nil {
		return nil
	}
	// BulkWriteException is the type returned for partial inserts. If every
	// failure is a duplicate-key (replay), treat the batch as success.
	if bwe, ok := err.(mongo.BulkWriteException); ok {
		for _, we := range bwe.WriteErrors {
			if !mongo.IsDuplicateKeyError(we) {
				return p.NewInternalError("BatchInsertHistory partial failure", err)
			}
		}
		return nil
	}
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return p.NewInternalError("BatchInsertHistory failed", err)
}

// GetHistoryEvents reads events for runID with EventID > afterID, ordered
// ASC by EventID, capped at limit (defaulted+clamped to maxGetHistoryEventsLimit).
// Each returned event has exactly one Payload variant set, ready for the
// OpsService handler to splat into the matching pb.HistoryEvent oneof.
func (s *mongoHistoryStore) GetHistoryEvents(ctx context.Context, namespace, runID string, afterID int64, limit int) ([]p.HistoryEvent, errors.CategorizedError) {
	if runID == "" {
		return nil, errors.NewInvalidInputError("GetHistoryEvents: run_id is required", nil)
	}
	if limit <= 0 || limit > maxGetHistoryEventsLimit {
		limit = maxGetHistoryEventsLimit
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	filter := bson.M{
		fieldRunID:   runID,
		fieldEventID: bson.M{"$gt": afterID},
	}
	if namespace != "" {
		// Defensive: helps debugging when a caller asks for a (namespace, run_id)
		// that doesn't exist together. Matches when the row's namespace is set
		// (the OpsFIFO writer always sets it).
		filter[fieldNamespace] = namespace
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: fieldEventID, Value: 1}}).
		SetLimit(int64(limit))

	cursor, err := s.db.Collection(collHistory).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, p.NewInternalError("GetHistoryEvents find failed", err)
	}
	defer cursor.Close(ctx)

	var out []p.HistoryEvent
	for cursor.Next(ctx) {
		var doc historyDoc
		if decErr := cursor.Decode(&doc); decErr != nil {
			return nil, p.NewInternalError("GetHistoryEvents decode failed", decErr)
		}
		event, convErr := doc.toEvent()
		if convErr != nil {
			return nil, p.NewInternalError("GetHistoryEvents decode payload", convErr)
		}
		out = append(out, event)
	}
	if iterErr := cursor.Err(); iterErr != nil {
		return nil, p.NewInternalError("GetHistoryEvents cursor failed", iterErr)
	}
	return out, nil
}

func (s *mongoHistoryStore) GetLatestEvent(ctx context.Context, namespace, runID string) (*p.HistoryEvent, errors.CategorizedError) {
	if runID == "" {
		return nil, errors.NewInvalidInputError("GetLatestEvent: run_id is required", nil)
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	filter := bson.M{fieldRunID: runID}
	if namespace != "" {
		filter[fieldNamespace] = namespace
	}
	findOpts := options.FindOne().SetSort(bson.D{{Key: fieldEventID, Value: -1}})

	var doc historyDoc
	err := s.db.Collection(collHistory).FindOne(ctx, filter, findOpts).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, p.NewInternalError("GetLatestEvent find failed", err)
	}
	event, convErr := doc.toEvent()
	if convErr != nil {
		return nil, p.NewInternalError("GetLatestEvent decode payload", convErr)
	}
	return &event, nil
}

func (s *mongoHistoryStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.db.Collection(collHistory).DeleteMany(ctx, bson.M{})
	return err
}

// marshalHistoryPayload returns the (discriminator, proto-marshaled bytes)
// pair for the active variant. Caller must have validated the payload first.
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
	default:
		return "", nil, fmt.Errorf("no payload variant set")
	}
	bytes, err := proto.Marshal(msg)
	if err != nil {
		return "", nil, err
	}
	return typeName, bytes, nil
}

// unmarshalHistoryPayload routes the bytes into the matching pb message
// pointer based on the type discriminator.
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
	default:
		return p.HistoryEventPayload{}, fmt.Errorf("unknown payload_type %q", typeName)
	}
}

// historyDoc is the BSON projection used for cursor decoding.
type historyDoc struct {
	RunID        string           `bson:"run_id"`
	EventID      int64            `bson:"event_id"`
	Namespace    string           `bson:"namespace"`
	OccurredAtMs int64            `bson:"occurred_at_ms"`
	WorkerID     string           `bson:"worker_id"`
	PayloadType  string           `bson:"payload_type"`
	Payload      primitive.Binary `bson:"payload"`
}

func (d historyDoc) toEvent() (p.HistoryEvent, error) {
	payload, err := unmarshalHistoryPayload(d.PayloadType, d.Payload.Data)
	if err != nil {
		return p.HistoryEvent{}, err
	}
	return p.HistoryEvent{
		Namespace:    d.Namespace,
		RunID:        d.RunID,
		EventID:      d.EventID,
		OccurredAtMs: d.OccurredAtMs,
		WorkerID:     d.WorkerID,
		Payload:      payload,
	}, nil
}
