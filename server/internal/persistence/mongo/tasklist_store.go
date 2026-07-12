package mongo

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoTasklistStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

func NewTasklistStore(ctx context.Context, uri string) (p.TasklistStore, errors.CategorizedError) {
	return NewTasklistStoreWithDatabase(ctx, uri, "", DefaultOperationTimeouts())
}

func NewTasklistStoreWithDatabase(ctx context.Context, uri string, database string, timeouts OperationTimeouts) (p.TasklistStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for tasklist store", err)
	}
	return &mongoTasklistStore{client: client, db: client.Database(resolveDatabase(database, defaultTasklistsDatabase)), timeouts: timeouts}, nil
}

func (s *mongoTasklistStore) Close() error { return s.client.Disconnect(context.Background()) }

// metadataDocID builds the _id for a tasklist metadata document.
func metadataDocID(namespace, tasklistName string, partitionID int32) string {
	return fmt.Sprintf("m/%s/%s/%d", namespace, tasklistName, partitionID)
}

// taskDocID builds the _id for a tasklist task document.
func taskDocID(namespace, tasklistName string, partitionID int32, taskID int64) string {
	return fmt.Sprintf("t/%s/%s/%d/%d", namespace, tasklistName, partitionID, taskID)
}

// tasklistKey builds a grouping key for queries across metadata + tasks.
func tasklistKey(namespace, tasklistName string, partitionID int32) string {
	return fmt.Sprintf("%s/%s/%d", namespace, tasklistName, partitionID)
}

// ClaimTasklist atomically claims ownership of a tasklist partition by
// incrementing range_id via FindOneAndUpdate with upsert. Any member can
// claim at any time — the previous owner discovers it lost ownership on
// its next fenced write (CreateTasks or UpdateTasklistMetadata).
func (s *mongoTasklistStore) ClaimTasklist(ctx context.Context, namespace, tasklistName string, partitionID int32, memberID, matchingAddress string) (*p.TasklistMetadata, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	now := time.Now()
	id := metadataDocID(namespace, tasklistName, partitionID)

	filter := bson.M{fieldMongoID: id}
	update := bson.M{
		"$inc": bson.M{fieldRangeID: int32(1)},
		"$set": bson.M{
			fieldOwnerMemberID: memberID,
			fieldOwnerAddress:  matchingAddress,
			fieldClaimedAt:     now,
		},
		"$setOnInsert": bson.M{
			"tasklist_key":    tasklistKey(namespace, tasklistName, partitionID),
			fieldRowType:      rowTypeTasklistMetadata,
			fieldNamespace:    namespace,
			fieldTaskListName: tasklistName,
			fieldPartitionID:  partitionID,
			fieldAckLevel:     int64(0),
		},
	}

	var result bson.M
	err := coll.FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&result)
	if err != nil {
		return nil, p.NewInternalError("ClaimTasklist failed", err)
	}
	return &p.TasklistMetadata{
		Namespace:     namespace,
		TasklistName:  tasklistName,
		PartitionID:   partitionID,
		RangeID:       toInt32(result[fieldRangeID]),
		AckLevel:      toInt64(result[fieldAckLevel]),
		OwnerMemberID: memberID,
		OwnerAddress:  matchingAddress,
		ClaimedAt:     now,
	}, nil
}

// UpdateTasklistMetadata performs a fenced update of ack_level. Returns
// OwnerVersionMismatchError if range_id doesn't match (ownership transferred).
func (s *mongoTasklistStore) UpdateTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, ackLevel int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	id := metadataDocID(namespace, tasklistName, partitionID)

	res, err := coll.UpdateOne(ctx,
		bson.M{fieldMongoID: id, fieldRangeID: rangeID},
		bson.M{"$set": bson.M{
			fieldAckLevel:  ackLevel,
			fieldUpdatedAt: time.Now(),
		}},
	)
	if err != nil {
		return p.NewInternalError("UpdateTasklistMetadata failed", err)
	}
	if res.MatchedCount == 0 {
		return p.NewRangeIDMismatchError(
			fmt.Sprintf("tasklist %s/%s/%d: expected range_id=%d", namespace, tasklistName, partitionID, rangeID))
	}
	return nil
}

// CreateTasks batch-inserts task rows in a single transaction that also
// verifies range_id on the metadata row (fence). Fence failure rolls back
// the entire transaction and returns a fencing error.
func (s *mongoTasklistStore) CreateTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, tasks []*p.TasklistTaskRow) errors.CategorizedError {
	if len(tasks) == 0 {
		return nil
	}

	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	session, sessErr := s.client.StartSession()
	if sessErr != nil {
		return p.NewInternalError("CreateTasks: start session failed", sessErr)
	}
	defer session.EndSession(ctx)

	_, txnErr := session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		coll := s.db.Collection(collTasklist)
		id := metadataDocID(namespace, tasklistName, partitionID)

		res, err := coll.UpdateOne(sc,
			bson.M{fieldMongoID: id, fieldRangeID: rangeID},
			bson.M{"$set": bson.M{fieldUpdatedAt: time.Now()}},
		)
		if err != nil {
			return nil, err
		}
		if res.MatchedCount == 0 {
			return nil, fmt.Errorf("range_id_mismatch")
		}

		tlk := tasklistKey(namespace, tasklistName, partitionID)
		docs := make([]interface{}, len(tasks))
		for i, t := range tasks {
			docs[i] = bson.M{
				fieldMongoID:      taskDocID(namespace, tasklistName, partitionID, t.TaskID),
				"tasklist_key":    tlk,
				fieldRowType:      rowTypeTasklistTask,
				fieldNamespace:    namespace,
				fieldTaskListName: tasklistName,
				fieldPartitionID:  partitionID,
				fieldTaskID:       t.TaskID,
				fieldRunID:        t.RunID,
				fieldShardID:      t.ShardID,
				fieldCreatedAt:    t.CreatedAt,
			}
		}
		_, err = coll.InsertMany(sc, docs)
		return nil, err
	})
	if txnErr != nil {
		if txnErr.Error() == "range_id_mismatch" {
			return p.NewRangeIDMismatchError(
				fmt.Sprintf("CreateTasks: tasklist %s/%s/%d: expected range_id=%d", namespace, tasklistName, partitionID, rangeID))
		}
		return p.NewInternalError("CreateTasks failed", txnErr)
	}
	return nil
}

// GetTasks reads task rows with task_id in (readLevel, maxReadLevel], ordered
// by task_id ASC, limited to batchSize. No fence.
func (s *mongoTasklistStore) GetTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, readLevel, maxReadLevel int64, batchSize int) ([]*p.TasklistTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	tlk := tasklistKey(namespace, tasklistName, partitionID)

	cursor, err := coll.Find(ctx,
		bson.M{
			"tasklist_key": tlk,
			fieldTaskID:    bson.M{"$gt": readLevel, "$lte": maxReadLevel},
		},
		options.Find().SetSort(bson.D{{Key: fieldTaskID, Value: 1}}).SetLimit(int64(batchSize)),
	)
	if err != nil {
		return nil, p.NewInternalError("GetTasks failed", err)
	}
	defer cursor.Close(ctx)

	var results []*p.TasklistTaskRow
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, p.NewInternalError("decode tasklist task failed", err)
		}
		results = append(results, &p.TasklistTaskRow{
			Namespace:    namespace,
			TasklistName: tasklistName,
			PartitionID:  partitionID,
			TaskID:       toInt64(doc[fieldTaskID]),
			RunID:        toString(doc[fieldRunID]),
			ShardID:      toInt32(doc[fieldShardID]),
			CreatedAt:    toTime(doc[fieldCreatedAt]),
		})
	}
	return results, nil
}

// DeleteTasksLessThan deletes every task row with task_id <= ackLevel in a
// single DeleteMany. No fence — any owner can GC completed tasks. Returns the
// number of rows actually deleted.
//
// The `limit` parameter is intentionally IGNORED: Mongo's DeleteMany has no
// native LIMIT.
func (s *mongoTasklistStore) DeleteTasksLessThan(ctx context.Context, namespace, tasklistName string, partitionID int32, ackLevel int64, limit int) (int, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	tlk := tasklistKey(namespace, tasklistName, partitionID)

	res, err := coll.DeleteMany(ctx, bson.M{
		"tasklist_key": tlk,
		fieldRowType:   rowTypeTasklistTask,
		fieldTaskID:    bson.M{"$lte": ackLevel},
	})
	if err != nil {
		return 0, p.NewInternalError("DeleteTasksLessThan failed", err)
	}
	return int(res.DeletedCount), nil
}

// DeleteTasksByIDBatch deletes task rows by exact task_id list. Used during
// shutdown to clean up completed-above-watermark tasks. No fence.
func (s *mongoTasklistStore) DeleteTasksByIDBatch(ctx context.Context, namespace, tasklistName string, partitionID int32, taskIDs []int64) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	mongoIDs := make([]interface{}, len(taskIDs))
	for i, tid := range taskIDs {
		mongoIDs[i] = taskDocID(namespace, tasklistName, partitionID, tid)
	}

	_, err := coll.DeleteMany(ctx, bson.M{fieldMongoID: bson.M{"$in": mongoIDs}})
	if err != nil {
		return p.NewInternalError("DeleteTasksByIDBatch failed", err)
	}
	return nil
}

// GetTasklistMetadata reads the metadata row for a tasklist partition.
func (s *mongoTasklistStore) GetTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32) (*p.TasklistMetadata, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collTasklist)
	id := metadataDocID(namespace, tasklistName, partitionID)

	var result bson.M
	err := coll.FindOne(ctx, bson.M{fieldMongoID: id}).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, p.NewNotFoundError("tasklist metadata not found")
		}
		return nil, p.NewInternalError("GetTasklistMetadata failed", err)
	}
	return &p.TasklistMetadata{
		Namespace:     toString(result[fieldNamespace]),
		TasklistName:  toString(result[fieldTaskListName]),
		PartitionID:   toInt32(result[fieldPartitionID]),
		RangeID:       toInt32(result[fieldRangeID]),
		AckLevel:      toInt64(result[fieldAckLevel]),
		OwnerMemberID: toString(result[fieldOwnerMemberID]),
		OwnerAddress:  toString(result[fieldOwnerAddress]),
		ClaimedAt:     toTime(result[fieldClaimedAt]),
	}, nil
}
