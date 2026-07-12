package mongo

import (
	"context"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type mongoBlobStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

func NewBlobStore(ctx context.Context, uri string) (p.BlobStore, errors.CategorizedError) {
	return NewBlobStoreWithDatabase(ctx, uri, "", DefaultOperationTimeouts())
}

func NewBlobStoreWithDatabase(ctx context.Context, uri string, database string, timeouts OperationTimeouts) (p.BlobStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for blob store", err)
	}
	return &mongoBlobStore{client: client, db: client.Database(resolveDatabase(database, defaultBlobsDatabase)), timeouts: timeouts}, nil
}

func (s *mongoBlobStore) Close() error { return s.client.Disconnect(context.Background()) }

func (s *mongoBlobStore) BatchInsertBlobs(ctx context.Context, shardID int32, namespace, runID string, blobs []p.BlobEntry) errors.CategorizedError {
	if len(blobs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collBlobs)
	docs := make([]interface{}, len(blobs))
	for i, b := range blobs {
		docs[i] = bson.M{
			fieldShardID:   shardID,
			fieldNamespace: namespace,
			fieldRunID:     runID,
			fieldID:        b.BlobID,
			"encoding":     b.Encoding,
			fieldData:      b.Payload,
		}
	}
	_, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return p.NewInternalError("BatchInsertBlobs failed", err)
	}
	return nil
}

func (s *mongoBlobStore) BatchGetBlobs(ctx context.Context, shardID int32, namespace, runID string, blobIDs []ids.BlobID) ([]p.BlobEntry, errors.CategorizedError) {
	if len(blobIDs) == 0 {
		return nil, nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collBlobs)
	filter := bson.M{
		fieldShardID:   shardID,
		fieldNamespace: namespace,
		fieldRunID:     runID,
		fieldID:        bson.M{"$in": blobIDs},
	}
	cursor, err := coll.Find(ctx, filter)
	if err != nil {
		return nil, p.NewInternalError("BatchGetBlobs failed", err)
	}
	defer cursor.Close(ctx)

	var results []p.BlobEntry
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, p.NewInternalError("decode blob failed", err)
		}
		results = append(results, p.BlobEntry{
			BlobID:   ids.MustParseBlobID(toString(doc[fieldID])),
			Encoding: toString(doc["encoding"]),
			Payload:  extractBytes(doc[fieldData]),
		})
	}
	return results, nil
}
