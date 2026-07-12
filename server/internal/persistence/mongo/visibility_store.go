package mongo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// maxListRunsLimit caps the page size returned by VisibilityStore.ListRuns
// regardless of the caller-requested value. Mirrors the MVP-spec cap on the
// OpsService.GetHistoryEvents API for symmetry.
const maxListRunsLimit = 1000

type mongoVisibilityStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

// NewVisibilityStoreWithDatabase opens an independent Mongo client targeting
// the visibility cluster's database. Used by ServerApp wiring (production)
// and by integration tests that ensure schema via EnsureSchemaForConfig.
func NewVisibilityStoreWithDatabase(ctx context.Context, uri, database string, timeouts OperationTimeouts) (p.VisibilityStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for visibility store", err)
	}
	return &mongoVisibilityStore{
		client:   client,
		db:       client.Database(resolveDatabase(database, defaultVisibilityDatabase)),
		timeouts: timeouts,
	}, nil
}

func (s *mongoVisibilityStore) Close() error { return s.client.Disconnect(context.Background()) }

// BatchUpsertVisibility upserts the supplied entries by (namespace, run_id).
// Implementation notes:
//   - Uses BulkWrite with one UpdateOne(upsert=true) per entry. MongoDB's
//     unordered=true would be slightly faster but ordered=true preserves
//     ops-tag tracing across the batch with no measurable throughput hit at
//     the batch sizes the OpsFIFO produces.
//   - $setOnInsert pins start_time so a stale replay (e.g. after the OpsFIFO
//     reader retries the same batch) cannot rewrite the run's true start.
//   - $set updates everything else from the latest entry; the OpsFIFO writer
//     is responsible for merging entries for the same run before calling so
//     that "latest" really is the most recent state. See ops_batch_reader.
func (s *mongoVisibilityStore) BatchUpsertVisibility(ctx context.Context, entries []p.VisibilityEntry) errors.CategorizedError {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	models := make([]mongo.WriteModel, 0, len(entries))
	for i := range entries {
		e := entries[i]
		filter := bson.M{fieldNamespace: e.Namespace, fieldRunID: e.RunID}
		setOnInsert := bson.M{fieldStartTime: e.StartTime}
		set := bson.M{
			fieldFlowType:     e.FlowType,
			fieldTaskListName: e.TaskListName,
			fieldStatus:       int32(e.Status),
			fieldUpdatedAt:    e.UpdatedAt,
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(bson.M{"$setOnInsert": setOnInsert, "$set": set}).
			SetUpsert(true))
	}

	coll := s.db.Collection(collVisibility)
	if _, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(true)); err != nil {
		return p.NewInternalError("BatchUpsertVisibility failed", err)
	}
	return nil
}

// ListRuns returns one page of runs matching the query. FlowType and
// Status are optional: an empty FlowType / nil Status means "any" and is
// dropped from the BSON filter. Ordered by start_time DESC or updated_at
// DESC. The cursor is (orderField, run_id) — descending on the order
// field, ascending on run_id to break ties deterministically.
//
// Index usage:
//   - Both filters set: hint the matching (namespace, flow_type, status,
//     orderField) compound index so the planner doesn't accidentally pick
//     the other one.
//   - Either or both filters omitted: no hint. The planner falls back to
//     a namespace-shard scan + in-memory sort. Acceptable for typical ops
//     volumes; a (namespace, status, orderField) index would lift the
//     "any flow_type" case if scale demands.
func (s *mongoVisibilityStore) ListRuns(ctx context.Context, q p.ListRunsQuery) (*p.ListRunsResult, errors.CategorizedError) {
	if q.Namespace == "" {
		return nil, errors.NewInvalidInputError("ListRuns: namespace is required (every supported index is namespace-prefixed)", nil)
	}
	limit := q.Limit
	if limit <= 0 || limit > maxListRunsLimit {
		limit = maxListRunsLimit
	}
	orderField, indexHint, err := orderFieldAndHint(q.OrderBy)
	if err != nil {
		return nil, err
	}

	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	filter := bson.M{fieldNamespace: q.Namespace}
	if q.FlowType != "" {
		filter[fieldFlowType] = q.FlowType
	}
	if q.Status != nil {
		filter[fieldStatus] = int32(*q.Status)
	}
	if q.PageToken != "" {
		ts, runID, parseErr := parseListPageToken(q.PageToken)
		if parseErr != nil {
			return nil, errors.NewInvalidInputError("ListRuns: invalid page_token", parseErr)
		}
		// Compound cursor:
		//   orderField < ts            -> definitely after the previous page
		//   orderField == ts AND id > runID -> tie-break on run_id ASC
		filter["$or"] = bson.A{
			bson.M{orderField: bson.M{"$lt": ts}},
			bson.M{orderField: ts, fieldRunID: bson.M{"$gt": runID}},
		}
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: orderField, Value: -1}, {Key: fieldRunID, Value: 1}}).
		SetLimit(int64(limit))
	// Only hint the compound index when both prefix-filters are present.
	// Without flow_type or status the index is not a usable prefix and
	// hinting it would force a worse plan than letting Mongo choose.
	if q.FlowType != "" && q.Status != nil {
		findOpts = findOpts.SetHint(indexHint)
	}

	coll := s.db.Collection(collVisibility)
	cursor, findErr := coll.Find(ctx, filter, findOpts)
	if findErr != nil {
		return nil, p.NewInternalError("ListRuns find failed", findErr)
	}
	defer cursor.Close(ctx)

	var entries []p.VisibilityEntry
	for cursor.Next(ctx) {
		var doc visibilityDoc
		if decErr := cursor.Decode(&doc); decErr != nil {
			return nil, p.NewInternalError("ListRuns decode failed", decErr)
		}
		entries = append(entries, doc.toEntry())
	}
	if iterErr := cursor.Err(); iterErr != nil {
		return nil, p.NewInternalError("ListRuns cursor failed", iterErr)
	}

	result := &p.ListRunsResult{Entries: entries}
	if len(entries) == limit {
		last := entries[len(entries)-1]
		var lastTime time.Time
		if q.OrderBy == p.ListByUpdatedAtDesc {
			lastTime = last.UpdatedAt
		} else {
			lastTime = last.StartTime
		}
		result.NextPageToken = encodeListPageToken(lastTime, last.RunID)
	}
	return result, nil
}

func (s *mongoVisibilityStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.db.Collection(collVisibility).DeleteMany(ctx, bson.M{})
	return err
}

// orderFieldAndHint maps the public OrderBy enum to the matching BSON field
// name + the index hint that serves it. Returning the hint explicitly avoids
// surprising index plans when, e.g., MongoDB picks the start_time index for
// an updated_at sort because both have the same prefix.
func orderFieldAndHint(o p.ListRunsOrderBy) (string, bson.D, errors.CategorizedError) {
	switch o {
	case p.ListByStartTimeDesc:
		return fieldStartTime, bson.D{
			{Key: fieldNamespace, Value: 1},
			{Key: fieldFlowType, Value: 1},
			{Key: fieldStatus, Value: 1},
			{Key: fieldStartTime, Value: -1},
			{Key: fieldRunID, Value: 1},
		}, nil
	case p.ListByUpdatedAtDesc:
		return fieldUpdatedAt, bson.D{
			{Key: fieldNamespace, Value: 1},
			{Key: fieldFlowType, Value: 1},
			{Key: fieldStatus, Value: 1},
			{Key: fieldUpdatedAt, Value: -1},
			{Key: fieldRunID, Value: 1},
		}, nil
	default:
		return "", nil, errors.NewInvalidInputError(fmt.Sprintf("ListRuns: unknown OrderBy %d", o), nil)
	}
}

// encodeListPageToken / parseListPageToken use a simple `<unix_millis>:<run_id>`
// format. Plain text avoids base64 noise and the colon delimiter is safe
// because run_ids are UUIDs (no colons).
func encodeListPageToken(t time.Time, runID string) string {
	return strconv.FormatInt(t.UnixMilli(), 10) + ":" + runID
}

func parseListPageToken(token string) (time.Time, string, error) {
	idx := strings.IndexByte(token, ':')
	if idx <= 0 || idx == len(token)-1 {
		return time.Time{}, "", fmt.Errorf("malformed page_token %q", token)
	}
	ms, err := strconv.ParseInt(token[:idx], 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("malformed page_token unix_millis: %w", err)
	}
	return time.UnixMilli(ms), token[idx+1:], nil
}

// visibilityDoc is the BSON projection used for both decode and insert. Field
// names match the visibility collection's schema (see schema.EnsureSchemaVisibility).
type visibilityDoc struct {
	Namespace    string    `bson:"namespace"`
	RunID        string    `bson:"run_id"`
	FlowType     string    `bson:"flow_type"`
	TaskListName string    `bson:"task_list_name"`
	Status       int32     `bson:"status"`
	StartTime    time.Time `bson:"start_time"`
	UpdatedAt    time.Time `bson:"updated_at"`
}

func (d visibilityDoc) toEntry() p.VisibilityEntry {
	return p.VisibilityEntry{
		Namespace:    d.Namespace,
		RunID:        d.RunID,
		FlowType:     d.FlowType,
		TaskListName: d.TaskListName,
		Status:       p.RunStatus(d.Status),
		StartTime:    d.StartTime,
		UpdatedAt:    d.UpdatedAt,
	}
}
