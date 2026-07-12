package mongo

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoShardStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

func NewShardStore(ctx context.Context, uri string) (p.ShardStore, errors.CategorizedError) {
	return NewShardStoreWithDatabase(ctx, uri, "", DefaultOperationTimeouts())
}

func NewShardStoreWithDatabase(ctx context.Context, uri string, database string, timeouts OperationTimeouts) (p.ShardStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for shard store", err)
	}
	return &mongoShardStore{client: client, db: client.Database(resolveDatabase(database, defaultShardsDatabase)), timeouts: timeouts}, nil
}

func (s *mongoShardStore) Close() error { return s.client.Disconnect(context.Background()) }

func (s *mongoShardStore) ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*p.Shard, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collShards)
	now := time.Now()
	leaseExpiry := now.Add(leaseDuration)

	var existing bson.M
	err := coll.FindOne(ctx, bson.M{"_id": shardID}).Decode(&existing)

	if err == mongo.ErrNoDocuments {
		// First claim: range_id starts at 1
		initialMetadata := bson.M{
			"range_id":                      int32(1),
			"immediate_task_committed_seq":  int64(0),
			"timer_task_committed_sort_key": int64(0),
			"timer_task_committed_id":       ids.TaskID{},
			"ops_fifo_task_committed_seq":   int64(0),
		}
		doc := bson.M{
			"_id": shardID, fieldVersion: int64(1), fieldMemberID: memberID,
			fieldClaimedAt: now, fieldLeaseExpiresAt: leaseExpiry,
			fieldReleasedAt: nil, fieldMetadata: initialMetadata,
		}
		if _, insertErr := coll.InsertOne(ctx, doc); insertErr != nil {
			if mongo.IsDuplicateKeyError(insertErr) {
				return s.ClaimShard(ctx, shardID, memberID, leaseDuration)
			}
			return nil, p.NewInternalError("insert shard failed", insertErr)
		}
		return &p.Shard{
			ShardID: shardID, Version: 1, MemberID: memberID,
			ClaimedAt: now, LeaseExpiresAt: leaseExpiry,
			Metadata: p.ShardMetadata{RangeID: 1},
		}, nil
	}
	if err != nil {
		return nil, p.NewInternalError("read shard failed", err)
	}

	releasedAt := existing[fieldReleasedAt]
	leaseExp := existing[fieldLeaseExpiresAt]
	if releasedAt == nil && leaseExp != nil {
		if expTime, ok := leaseExp.(time.Time); ok && expTime.After(now) {
			if existingMember, _ := existing[fieldMemberID].(string); existingMember != memberID {
				return nil, p.NewLeaseNotExpiredError(fmt.Sprintf("shard %d owned by %s, lease expires at %v", shardID, existingMember, expTime))
			}
		}
	}

	oldVersion := toInt64(existing[fieldVersion])
	newVersion := oldVersion + 1
	// Atomically increment range_id alongside version on claim
	result := coll.FindOneAndUpdate(ctx,
		bson.M{"_id": shardID, fieldVersion: oldVersion},
		bson.M{
			"$set": bson.M{fieldVersion: newVersion, fieldMemberID: memberID, fieldClaimedAt: now, fieldLeaseExpiresAt: leaseExpiry, fieldReleasedAt: nil},
			"$inc": bson.M{fieldMetadata + ".range_id": int32(1)},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return nil, p.NewVersionMismatchError("shard version changed during claim")
		}
		return nil, p.NewInternalError("claim shard update failed", result.Err())
	}

	var afterDoc bson.M
	if decErr := result.Decode(&afterDoc); decErr != nil {
		return nil, p.NewInternalError("decode shard after claim failed", decErr)
	}

	metadata := extractShardMetadata(afterDoc)
	return &p.Shard{
		ShardID: shardID, Version: newVersion, MemberID: memberID,
		ClaimedAt: now, LeaseExpiresAt: leaseExpiry,
		Metadata: metadata,
	}, nil
}

func (s *mongoShardStore) RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collShards)
	leaseExpiry := time.Now().Add(leaseDuration)
	setDoc := bson.M{fieldLeaseExpiresAt: leaseExpiry}
	if metadata != nil {
		setDoc[fieldMetadata+".immediate_task_committed_seq"] = metadata.ImmediateTaskCommittedSeq
		setDoc[fieldMetadata+".timer_task_committed_sort_key"] = metadata.TimerTaskCommittedSortKey
		setDoc[fieldMetadata+".timer_task_committed_id"] = metadata.TimerTaskCommittedID
		setDoc[fieldMetadata+".ops_fifo_task_committed_seq"] = metadata.OpsFIFOTaskCommittedSeq
	}
	result, err := coll.UpdateOne(ctx,
		bson.M{"_id": shardID, fieldVersion: expectedVersion, fieldMemberID: memberID},
		bson.M{"$set": setDoc},
	)
	if err != nil {
		return time.Time{}, p.NewInternalError("renew shard lease failed", err)
	}
	if result.MatchedCount == 0 {
		return time.Time{}, p.NewVersionMismatchError("shard lease renewal: version or member mismatch")
	}
	return leaseExpiry, nil
}

func (s *mongoShardStore) ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	coll := s.db.Collection(collShards)
	result, err := coll.UpdateOne(ctx,
		bson.M{"_id": shardID, fieldVersion: expectedVersion, fieldMemberID: memberID},
		bson.M{"$set": bson.M{fieldReleasedAt: time.Now()}},
	)
	if err != nil {
		return p.NewInternalError("release shard failed", err)
	}
	if result.MatchedCount == 0 {
		return p.NewVersionMismatchError("shard release: version or member mismatch")
	}
	return nil
}

func extractShardMetadata(doc bson.M) p.ShardMetadata {
	md := p.ShardMetadata{}
	if metaRaw, ok := doc[fieldMetadata]; ok {
		if metaDoc, ok := metaRaw.(bson.M); ok {
			if v, ok := metaDoc["range_id"]; ok {
				md.RangeID = toInt32(v)
			}
			if v, ok := metaDoc["immediate_task_committed_seq"]; ok {
				md.ImmediateTaskCommittedSeq = toInt64(v)
			}
			if v, ok := metaDoc["timer_task_committed_sort_key"]; ok {
				md.TimerTaskCommittedSortKey = toInt64(v)
			}
			if v, ok := metaDoc["timer_task_committed_id"]; ok {
				if s, ok := v.(string); ok {
					md.TimerTaskCommittedID = ids.MustParseTaskID(s)
				}
			}
			if v, ok := metaDoc["ops_fifo_task_committed_seq"]; ok {
				md.OpsFIFOTaskCommittedSeq = toInt64(v)
			}
		}
	}
	return md
}

func (s *mongoShardStore) BatchReleaseShards(ctx context.Context, memberID string, entries []p.ShardReleaseEntry) errors.CategorizedError {
	if len(entries) == 0 {
		return nil
	}

	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collShards)
	now := time.Now()

	var models []mongo.WriteModel
	for _, entry := range entries {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{
				"_id":         entry.ShardID,
				fieldVersion:  entry.ExpectedVersion,
				fieldMemberID: memberID,
			}).
			SetUpdate(bson.M{
				"$set": bson.M{fieldReleasedAt: now},
			}))
	}

	_, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return p.NewInternalError("batch release shards failed", err)
	}
	return nil
}
