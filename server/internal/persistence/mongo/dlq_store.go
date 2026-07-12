package mongo

import (
	"context"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const collTaskDLQ = "task_dlq"

type mongoDLQStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

func NewDLQStore(ctx context.Context, uri string) (p.DLQStore, errors.CategorizedError) {
	return NewDLQStoreWithDatabase(ctx, uri, "", DefaultOperationTimeouts())
}

func NewDLQStoreWithDatabase(ctx context.Context, uri string, database string, timeouts OperationTimeouts) (p.DLQStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for DLQ store", err)
	}
	return &mongoDLQStore{client: client, db: client.Database(resolveDatabase(database, defaultRunsDatabase)), timeouts: timeouts}, nil
}

// NewDLQStoreFromClient creates a DLQStore using an existing mongo.Database.
// Used in tests and server wiring where the connection is already established.
func NewDLQStoreFromClient(db *mongo.Database, timeouts OperationTimeouts) p.DLQStore {
	return &mongoDLQStore{db: db, timeouts: timeouts}
}

// WriteDLQ inserts one DLQ entry. Idempotent on (shard_id, task_id): if a
// previous owner already wrote this task to the DLQ (lease-handoff race —
// see v0.js comment on task_dlq.pk_idx), the duplicate-key error is
// silently swallowed and the first record is preserved. The first record
// is typically the more diagnostic one (the original failure that
// motivated the DLQ); the second write would otherwise just overwrite or
// shadow it.
func (s *mongoDLQStore) WriteDLQ(ctx context.Context, entry *p.DLQEntry) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	doc := bson.M{
		fieldShardID:      entry.ShardID,
		"task_id":         entry.TaskID,
		fieldRowType:      int32(entry.QueueType),
		fieldTaskType:     entry.TaskType,
		fieldRunID:        entry.RunID,
		fieldNamespace:    entry.Namespace,
		fieldTaskListName: entry.TaskListName,
		fieldSortKey:      entry.SortKey,
		"error":           entry.Error,
		"error_category":  entry.ErrorCategory,
		fieldCreatedAt:    entry.CreatedAt,
		"dlq_at":          time.Now(),
		"member_id":       entry.MemberID,
	}

	coll := s.db.Collection(collTaskDLQ)
	if _, err := coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			// Already DLQ'd by a previous owner — the first record is
			// authoritative. No-op.
			return nil
		}
		return p.NewInternalError("WriteDLQ failed", err)
	}
	return nil
}

func (s *mongoDLQStore) Close() error {
	if s.client != nil {
		return s.client.Disconnect(context.Background())
	}
	return nil
}
