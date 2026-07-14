package mongo

import (
	"context"
	"strings"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type mongoRunStore struct {
	client   *mongo.Client
	db       *mongo.Database
	timeouts OperationTimeouts
}

func NewRunStore(ctx context.Context, uri string) (p.RunStore, errors.CategorizedError) {
	return NewRunStoreWithDatabase(ctx, uri, "", DefaultOperationTimeouts())
}

func NewRunStoreWithDatabase(ctx context.Context, uri string, database string, timeouts OperationTimeouts) (p.RunStore, errors.CategorizedError) {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return nil, p.NewInternalError("failed to connect to MongoDB for run store", err)
	}
	return &mongoRunStore{client: client, db: client.Database(resolveDatabase(database, defaultRunsDatabase)), timeouts: timeouts}, nil
}

func (s *mongoRunStore) Close() error { return s.client.Disconnect(context.Background()) }

func (s *mongoRunStore) CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	sess, err := s.client.StartSession()
	if err != nil {
		return p.NewInternalError("start session failed", err)
	}
	defer sess.EndSession(ctx)

	_, txnErr := sess.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		coll := s.db.Collection(collRuns)

		run.RowType = p.RowTypeRun
		run.SortKey = 0
		run.Version = 1
		run.CreatedAt = time.Now()
		run.UpdatedAt = run.CreatedAt

		if _, err := coll.InsertOne(sc, runRowToDoc(run)); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return nil, p.NewConflictError("run already exists: " + run.ID)
			}
			return nil, err
		}

		for _, task := range tasks {
			doc := taskRowToDoc(task)
			if _, err := coll.InsertOne(sc, doc); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if txnErr != nil {
		if catErr, ok := txnErr.(errors.CategorizedError); ok {
			return catErr
		}
		return p.NewInternalError("CreateRunWithTasks transaction failed", txnErr)
	}
	return nil
}

func (s *mongoRunStore) GetRun(ctx context.Context, shardID int32, namespace, runID string, opts p.GetRunOptions) (*p.RunRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	// In mongo-driver v1 FindOne does not accept SetReadPreference; the read
	// preference is configured at the collection handle. Build a per-call
	// collection handle when SecondaryPreferred is requested so we don't
	// mutate the shared default collection (which other ops still rely on
	// for primary reads).
	var coll *mongo.Collection
	if opts.ReadPreference == p.ReadPrefSecondaryPreferred {
		coll = s.db.Collection(collRuns, options.Collection().SetReadPreference(readpref.SecondaryPreferred()))
	} else {
		coll = s.db.Collection(collRuns)
	}
	filter := bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeRun,
		fieldNamespace: namespace, fieldSortKey: int64(0), fieldID: runID,
	}
	var row p.RunRow
	if err := coll.FindOne(ctx, filter).Decode(&row); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, p.NewNotFoundError("run not found: " + runID)
		}
		return nil, p.NewInternalError("GetRun failed", err)
	}
	unescapeRunRowKeys(&row)
	return &row, nil
}

// escapeRunRowKeys prepares map fields for safe MongoDB storage by escaping
// dots in keys. Initial run creation goes through BSON marshal directly (not
// buildRunUpdateDoc), so we must escape keys here too or later $set/$unset
// operations will not target the same fields.
func escapeRunRowKeys(row *p.RunRow) {
	row.StateMap = escapeMapKeys(row.StateMap)
	row.StepExeIDCounters = escapeMapKeys(row.StepExeIDCounters)
	row.ActiveStepExecutions = escapeMapKeys(row.ActiveStepExecutions)
	row.UnconsumedChannelMessages = escapeMapKeys(row.UnconsumedChannelMessages)
}

// unescapeRunRowKeys restores original keys in all map fields that were
// escaped for safe MongoDB storage.
func unescapeRunRowKeys(row *p.RunRow) {
	row.StateMap = unescapeMapKeys(row.StateMap)
	row.StepExeIDCounters = unescapeMapKeys(row.StepExeIDCounters)
	row.ActiveStepExecutions = unescapeMapKeys(row.ActiveStepExecutions)
	row.UnconsumedChannelMessages = unescapeMapKeys(row.UnconsumedChannelMessages)
}

func unescapeMapKeys[V any](m map[string]V) map[string]V {
	if m == nil {
		return m
	}
	needsUnescape := false
	for k := range m {
		if strings.Contains(k, mongoDotEscaped) {
			needsUnescape = true
			break
		}
	}
	if !needsUnescape {
		return m
	}
	result := make(map[string]V, len(m))
	for k, v := range m {
		result[unescapeMongoKey(k)] = v
	}
	return result
}

func escapeMapKeys[V any](m map[string]V) map[string]V {
	if m == nil {
		return m
	}
	needsEscape := false
	for k := range m {
		if strings.Contains(k, mongoDot) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return m
	}
	result := make(map[string]V, len(m))
	for k, v := range m {
		result[escapeMongoKey(k)] = v
	}
	return result
}

func (s *mongoRunStore) UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
	expectedVersion int64, update *p.RunRowUpdate, newTasks []p.TaskRow) errors.CategorizedError {

	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	sess, err := s.client.StartSession()
	if err != nil {
		return p.NewInternalError("start session failed", err)
	}
	defer sess.EndSession(ctx)

	var matched bool
	_, txnErr := sess.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		coll := s.db.Collection(collRuns)

		filter := bson.M{
			fieldShardID: shardID, fieldRowType: p.RowTypeRun,
			fieldNamespace: namespace, fieldSortKey: int64(0), fieldID: runID,
			fieldVersion: expectedVersion,
		}

		updateDoc := buildRunUpdateDoc(update)
		result, err := coll.UpdateOne(sc, filter, updateDoc)
		if err != nil {
			return nil, err
		}
		if result.MatchedCount == 0 {
			matched = false
			return nil, nil
		}
		matched = true

		for _, task := range newTasks {
			doc := taskRowToDoc(task)
			if _, err := coll.InsertOne(sc, doc); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if txnErr != nil {
		return p.NewInternalError("UpdateRunWithNewTasks transaction failed", txnErr)
	}
	if !matched {
		return p.NewVersionMismatchError("run " + runID)
	}
	return nil
}

// --- Task Range Reading ---

// RangeReadImmediateTasks reads immediate tasks with sort_key > afterSeq,
// ordered by (sort_key ASC, id ASC). sort_key is now TaskSeq (RangeID<<32 | LocalSeq).
func (s *mongoRunStore) RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*p.ImmediateTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	filter := bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeImmediateTask,
		fieldNamespace: "",
	}
	if afterSeq > 0 {
		filter[fieldSortKey] = bson.M{"$gt": afterSeq}
	}

	cursor, err := coll.Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: fieldSortKey, Value: 1}, {Key: fieldID, Value: 1}}).SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, p.NewInternalError("RangeReadImmediateTasks failed", err)
	}
	defer cursor.Close(ctx)

	var results []*p.ImmediateTaskRow
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, p.NewInternalError("decode immediate task failed", err)
		}
		row, decErr := docToStruct[p.ImmediateTaskRow](doc, "immediate task")
		if decErr != nil {
			return nil, decErr
		}
		results = append(results, row)
	}
	return results, nil
}

// RangeReadTimerTasks reads timer tasks with sort_key <= sortKeyUpTo and
// cursor position after (afterSortKey, afterID), ordered by (sort_key, id).
func (s *mongoRunStore) RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.TaskID, limit int) ([]*p.TimerTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	filter := bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeTimerTask,
		fieldNamespace: "", fieldSortKey: bson.M{"$lte": sortKeyUpTo},
	}
	if !afterID.IsZero() {
		filter["$or"] = bson.A{
			bson.M{fieldSortKey: bson.M{"$gt": afterSortKey}},
			bson.M{fieldSortKey: afterSortKey, fieldID: bson.M{"$gt": afterID}},
		}
	}

	cursor, err := coll.Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: fieldSortKey, Value: 1}, {Key: fieldID, Value: 1}}).SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, p.NewInternalError("RangeReadTimerTasks failed", err)
	}
	defer cursor.Close(ctx)

	var results []*p.TimerTaskRow
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, p.NewInternalError("decode timer task failed", err)
		}
		row, decErr := docToStruct[p.TimerTaskRow](doc, "timer task")
		if decErr != nil {
			return nil, decErr
		}
		results = append(results, row)
	}
	return results, nil
}

// --- Task Range Deletion ---

// RangeDeleteImmediateTasks deletes immediate tasks with sort_key <= upToSeq,
// RangeDeleteImmediateTasks deletes all immediate tasks with sort_key <= upToSeq.
func (s *mongoRunStore) RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeImmediateTask,
		fieldNamespace: "", fieldSortKey: bson.M{"$lte": upToSeq},
	})
	if err != nil {
		return p.NewInternalError("RangeDeleteImmediateTasks failed", err)
	}
	return nil
}

// RangeDeleteTimerTasks deletes all timer tasks where (sort_key, id) < (upToSortKey, upToID).
// The upper bound is exclusive because the watermark points at min(pendingSet), which
// is still in-flight. Unlike the immediate deleter (which uses min.seq-1 for an inclusive
// bound), compound keys don't allow integer subtraction, so we use strict-less-than.
func (s *mongoRunStore) RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.TaskID) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeTimerTask,
		fieldNamespace: "",
		"$or": bson.A{
			bson.M{fieldSortKey: bson.M{"$lt": upToSortKey}},
			bson.M{fieldSortKey: upToSortKey, fieldID: bson.M{"$lt": upToID}},
		},
	})
	if err != nil {
		return p.NewInternalError("RangeDeleteTimerTasks failed", err)
	}
	return nil
}

// --- Task Deletion by ID Batch (shutdown path only) ---

func (s *mongoRunStore) DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeImmediateTask,
		fieldNamespace: "", fieldID: bson.M{"$in": taskIDs},
	})
	if err != nil {
		return p.NewInternalError("DeleteImmediateTasksByIDBatch failed", err)
	}
	return nil
}

func (s *mongoRunStore) DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeTimerTask,
		fieldNamespace: "", fieldID: bson.M{"$in": taskIDs},
	})
	if err != nil {
		return p.NewInternalError("DeleteTimerTasksByIDBatch failed", err)
	}
	return nil
}

// --- OpsFIFO Task Range Reading / Deletion ---

// RangeReadOpsFIFOTasks reads OpsFIFO tasks with sort_key > afterSeq,
// ordered by (sort_key ASC, id ASC). Mirrors RangeReadImmediateTasks since
// both queues use the same per-shard monotonic TaskSeq encoding.
func (s *mongoRunStore) RangeReadOpsFIFOTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*p.OpsFIFOTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	filter := bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeOpsFIFOTask,
		fieldNamespace: "",
	}
	if afterSeq > 0 {
		filter[fieldSortKey] = bson.M{"$gt": afterSeq}
	}

	cursor, err := coll.Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: fieldSortKey, Value: 1}, {Key: fieldID, Value: 1}}).SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, p.NewInternalError("RangeReadOpsFIFOTasks failed", err)
	}
	defer cursor.Close(ctx)

	var results []*p.OpsFIFOTaskRow
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, p.NewInternalError("decode OpsFIFO task failed", err)
		}
		row, decErr := docToStruct[p.OpsFIFOTaskRow](doc, "OpsFIFO task")
		if decErr != nil {
			return nil, decErr
		}
		results = append(results, row)
	}
	return results, nil
}

// RangeDeleteOpsFIFOTasks deletes OpsFIFO tasks with sort_key <= upToSeq.
func (s *mongoRunStore) RangeDeleteOpsFIFOTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeOpsFIFOTask,
		fieldNamespace: "", fieldSortKey: bson.M{"$lte": upToSeq},
	})
	if err != nil {
		return p.NewInternalError("RangeDeleteOpsFIFOTasks failed", err)
	}
	return nil
}

// DeleteOpsFIFOTasksByIDBatch deletes the listed OpsFIFO tasks by id (shutdown path).
func (s *mongoRunStore) DeleteOpsFIFOTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	coll := s.db.Collection(collRuns)
	_, err := coll.DeleteMany(ctx, bson.M{
		fieldShardID: shardID, fieldRowType: p.RowTypeOpsFIFOTask,
		fieldNamespace: "", fieldID: bson.M{"$in": taskIDs},
	})
	if err != nil {
		return p.NewInternalError("DeleteOpsFIFOTasksByIDBatch failed", err)
	}
	return nil
}

// --- Document helpers ---

func taskRowToDoc(t p.TaskRow) bson.M {
	if t.Immediate != nil {
		return immediateTaskRowToDoc(t.Immediate)
	}
	if t.Timer != nil {
		return timerTaskRowToDoc(t.Timer)
	}
	if t.OpsFIFO != nil {
		return opsFIFOTaskRowToDoc(t.OpsFIFO)
	}
	return bson.M{}
}

// runRowToDoc uses BSON struct tags for proper nested struct serialization.
func runRowToDoc(r *p.RunRow) bson.M {
	row := *r
	escapeRunRowKeys(&row)
	data, err := bson.Marshal(&row)
	if err != nil {
		return bson.M{}
	}
	var doc bson.M
	if err := bson.Unmarshal(data, &doc); err != nil {
		return bson.M{}
	}
	return doc
}

func immediateTaskRowToDoc(t *p.ImmediateTaskRow) bson.M {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.RowType = p.RowTypeImmediateTask
	t.Namespace = ""
	// SortKey is now TaskSeq (RangeID<<32 | LocalSeq), set by the caller.
	// Do NOT overwrite to 0.

	data, _ := bson.Marshal(t)
	var doc bson.M
	bson.Unmarshal(data, &doc)
	return doc
}

func timerTaskRowToDoc(t *p.TimerTaskRow) bson.M {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.RowType = p.RowTypeTimerTask
	t.Namespace = ""

	data, _ := bson.Marshal(t)
	var doc bson.M
	bson.Unmarshal(data, &doc)
	return doc
}

// opsFIFOTaskRowToDoc serializes an OpsFIFOTaskRow for the runs collection.
// Same shape as immediate / timer tasks (lives in `runs`, namespace="" so
// the per-row-type prefix on pk_idx selects OpsFIFO rows). SortKey is the
// per-shard OpsFIFO TaskSeq filled in by ShardedRunStore under the
// OpsFIFO seq lock; we do NOT overwrite it.
func opsFIFOTaskRowToDoc(t *p.OpsFIFOTaskRow) bson.M {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.RowType = p.RowTypeOpsFIFOTask
	t.Namespace = ""

	data, _ := bson.Marshal(t)
	var doc bson.M
	bson.Unmarshal(data, &doc)
	return doc
}

func buildRunUpdateDoc(u *p.RunRowUpdate) bson.M {
	setFields := bson.M{fieldUpdatedAt: time.Now()}

	if u.Status != nil {
		setFields[fieldStatus] = *u.Status
	}
	if u.WorkerID != nil {
		setFields[fieldWorkerID] = *u.WorkerID
	}
	if u.WorkerRequestCounter != nil {
		setFields[fieldWorkerRequestCounter] = *u.WorkerRequestCounter
	}
	if u.StepMethodExeCounter != nil {
		setFields[fieldStepMethodExeCounter] = *u.StepMethodExeCounter
	}
	if u.ExternalChannelMessageCounter != nil {
		setFields[fieldExternalChannelMessageCounter] = *u.ExternalChannelMessageCounter
	}
	if u.LastHeartbeatTime != nil {
		setFields[fieldLastHeartbeatTime] = *u.LastHeartbeatTime
	}
	if u.HeartbeatTimerID != nil {
		setFields[fieldHeartbeatTimerID] = *u.HeartbeatTimerID
	}
	if u.ActiveDurableTimerID != nil {
		setFields[fieldActiveDurableTimerID] = *u.ActiveDurableTimerID
	}
	if u.DurableTimerFireAt != nil {
		setFields[fieldDurableTimerFireAt] = *u.DurableTimerFireAt
	}
	if u.DurableTimerFired != nil {
		setFields["durable_timer_fired"] = *u.DurableTimerFired
	}
	if u.LastHistoryEventID != nil {
		setFields[fieldLastHistoryEventID] = *u.LastHistoryEventID
	}
	// For map fields whose keys may contain dots (e.g. "mypkg.MyStep"),
	// we use escaped keys. MongoDB treats dots in $set paths as nested
	// document separators, so "counters.mypkg.MyStep" would create
	// {counters: {mypkg: {MyStep: 1}}} instead of {counters: {"mypkg.MyStep": 1}}.
	for k, v := range u.StateMap {
		setFields[fieldStateMap+"."+escapeMongoKey(k)] = v
	}
	for k, v := range u.StepExeIDCounters {
		setFields[fieldStepExeIDCounters+"."+escapeMongoKey(k)] = v
	}

	updateDoc := bson.M{
		"$set": setFields,
		"$inc": bson.M{fieldVersion: int64(1)}, // version NOT in $set to avoid conflict
	}

	unsetFields := bson.M{}
	for k, v := range u.ActiveStepExecutions {
		ek := escapeMongoKey(k)
		if v == nil {
			unsetFields[fieldActiveStepExecutions+"."+ek] = ""
		} else {
			setFields[fieldActiveStepExecutions+"."+ek] = *v
		}
	}
	if len(unsetFields) > 0 {
		updateDoc["$unset"] = unsetFields
	}

	for ch, vals := range u.ReplaceUnconsumedChannels {
		path := fieldUnconsumedChannelMessages + "." + escapeMongoKey(ch)
		setFields[path] = vals
	}

	if u.ReplaceStateMap != nil {
		setFields[fieldStateMap] = escapeMapKeys(*u.ReplaceStateMap)
	}
	if u.ReplaceStepExeIDCounters != nil {
		setFields[fieldStepExeIDCounters] = escapeMapKeys(*u.ReplaceStepExeIDCounters)
	}
	if u.ReplaceActiveStepExecutions != nil {
		setFields[fieldActiveStepExecutions] = escapeMapKeys(*u.ReplaceActiveStepExecutions)
	}
	if u.ReplaceAllUnconsumedChannels != nil {
		setFields[fieldUnconsumedChannelMessages] = escapeMapKeys(*u.ReplaceAllUnconsumedChannels)
	}

	return updateDoc
}

// bson round-trip a mongo doc into a struct via its BSON tags. Surfaces
// decode failures instead of returning a zero-value row (which would look
// like a phantom fresh record and mask corruption).
func docToStruct[T any](doc bson.M, what string) (*T, errors.CategorizedError) {
	data, err := bson.Marshal(doc)
	if err != nil {
		return nil, p.NewInternalError("marshal "+what+" doc", err)
	}
	var row T
	if err := bson.Unmarshal(data, &row); err != nil {
		return nil, p.NewInternalError("unmarshal "+what+" doc", err)
	}
	return &row, nil
}

func (s *mongoRunStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	_, err := s.db.Collection(collRuns).DeleteMany(ctx, bson.M{})
	return err
}

// MongoDB treats dots in $set/$unset paths as nested-document separators.
// We escape them in map keys so "mypkg.MyStep" is stored as "mypkg\uff0eMyStep"
// (using the Unicode fullwidth full stop) and survives round-trips correctly.
// The BSON struct tags on RunRow use Go maps which store the escaped keys;
// the RunEngine unescapes them when building proto responses.

const (
	mongoDot        = "."
	mongoDotEscaped = "\uff0e" // Unicode fullwidth full stop U+FF0E
)

func escapeMongoKey(k string) string {
	return strings.ReplaceAll(k, mongoDot, mongoDotEscaped)
}

func unescapeMongoKey(k string) string {
	return strings.ReplaceAll(k, mongoDotEscaped, mongoDot)
}
