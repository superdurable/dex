package blobs

import (
	"context"
	"sort"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/internal/engine/pbconv"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// resolverImpl resolves a run's blob-backed persistence values into proto.
// Load fills blobMap once; the Resolve* methods read only the cache.
type resolverImpl struct {
	blobStore p.BlobStore
	run       *p.RunRow
	blobMap   map[ids.BlobID]p.BlobEntry
	loaded    bool
}

func (r *resolverImpl) LoadAllForRunRow(ctx context.Context) errors.CategorizedError {
	if r.loaded {
		return nil
	}
	blobMap, err := loadBlobsToMap(ctx, r.blobStore, r.run.ShardID, r.run.Namespace, r.run.ID, collectBlobIDsFromRunRow(r.run))
	if err != nil {
		return err
	}
	r.blobMap = blobMap
	r.loaded = true
	return nil
}

func (r *resolverImpl) ResolveStateMap() map[string]*pb.Value {
	if len(r.run.StateMap) == 0 {
		return map[string]*pb.Value{}
	}
	out := make(map[string]*pb.Value, len(r.run.StateMap))
	for k, v := range r.run.StateMap {
		out[k] = r.ResolveSingleValue(v)
	}
	return out
}

func (r *resolverImpl) ResolveUnconsumedChannelMessages() map[string]*pb.ChannelMessages {
	if len(r.run.UnconsumedChannelMessages) == 0 {
		return map[string]*pb.ChannelMessages{}
	}
	out := make(map[string]*pb.ChannelMessages, len(r.run.UnconsumedChannelMessages))
	for ch, msgs := range r.run.UnconsumedChannelMessages {
		pbMsgs := make([]*pb.ChannelMessage, len(msgs))
		for i, m := range msgs {
			pbMsgs[i] = &pb.ChannelMessage{Id: m.ID, Value: r.ResolveSingleValue(m.Value)}
		}
		out[ch] = &pb.ChannelMessages{Messages: pbMsgs}
	}
	return out
}

func (r *resolverImpl) ResolveActiveStepExecutions() map[string]*pb.ActiveStepExecution {
	if len(r.run.ActiveStepExecutions) == 0 {
		return map[string]*pb.ActiveStepExecution{}
	}
	out := make(map[string]*pb.ActiveStepExecution, len(r.run.ActiveStepExecutions))
	for id, stepExe := range r.run.ActiveStepExecutions {
		pbStep := &pb.ActiveStepExecution{
			Input:         r.ResolveSingleValue(stepExe.Input),
			Status:        pb.StepExecutionStatus(stepExe.Status),
			FromStepExeId: stepExe.FromStepExeID,
		}
		if stepExe.WaitForCondition != nil {
			pbStep.WaitForCondition = pbconv.PersistenceWaitForConditionToPb(stepExe.WaitForCondition)
		}
		if len(stepExe.ConditionResults) > 0 {
			pbStep.ConditionResults = pbconv.PersistenceConditionResultsToPb(stepExe.ConditionResults)
		}
		if stepExe.WaitForRetryState != nil {
			pbStep.WaitForRetryState = pbconv.PersistenceRetryStateToPb(stepExe.WaitForRetryState)
		}
		if stepExe.ExecuteRetryState != nil {
			pbStep.ExecuteRetryState = pbconv.PersistenceRetryStateToPb(stepExe.ExecuteRetryState)
		}
		pbStep.WaitForMethodExeId = stepExe.WaitForMethodExeID
		pbStep.ExecuteMethodExeId = stepExe.ExecuteMethodExeID
		out[id] = pbStep
	}
	return out
}

// ResolveUnreceivedChannelMessages builds the worker catch-up payload of
// channel messages with id > lastReceivedID, sorted by id ASC.
//
// Self-contained: it fetches ONLY the unreceived messages' blobs (not Load's
// full run set) and returns early before any fetch when nothing is unreceived
// — this runs on every worker call, where the caught-up case is the norm.
func (r *resolverImpl) LoadAndResolveUnreceivedChannelMessagesSorted(ctx context.Context, lastReceivedID int64) ([]*pb.UnreceivedChannelMessage, errors.CategorizedError) {
	type pendingMsg struct {
		chName string
		id     int64
		value  p.Value
	}
	var pendings []pendingMsg
	var blobIDs []ids.BlobID
	for chName, msgs := range r.run.UnconsumedChannelMessages {
		for _, m := range msgs {
			if m.ID > lastReceivedID {
				pendings = append(pendings, pendingMsg{chName: chName, id: m.ID, value: m.Value})
				if m.Value.Type == p.ValueTypeBlobRef && !m.Value.BlobID.IsZero() {
					blobIDs = append(blobIDs, m.Value.BlobID)
				}
			}
		}
	}
	if len(pendings) == 0 {
		return nil, nil
	}

	blobMap, fetchErr := loadBlobsToMap(ctx, r.blobStore, r.run.ShardID, r.run.Namespace, r.run.ID, blobIDs)
	if fetchErr != nil {
		return nil, fetchErr
	}

	missed := make([]*pb.UnreceivedChannelMessage, 0, len(pendings))
	for _, pnd := range pendings {
		missed = append(missed, &pb.UnreceivedChannelMessage{
			ChannelName: pnd.chName,
			Id:          pnd.id,
			Value:       valueToPb(pnd.value, blobMap),
		})
	}
	sort.Slice(missed, func(i, j int) bool { return missed[i].Id < missed[j].Id })
	return missed, nil
}

// ResolveSingleValue resolves one persistence Value against the Load cache.
// For blob refs, a pre-fetched entry from Load is required.
func (r *resolverImpl) ResolveSingleValue(val p.Value) *pb.Value {
	return valueToPb(val, r.blobMap)
}

// valueToPb converts a persistence Value to proto, resolving blob refs against
// the supplied map. A blob ref missing from the map becomes Null.
func valueToPb(val p.Value, blobMap map[ids.BlobID]p.BlobEntry) *pb.Value {
	switch val.Type {
	case p.ValueTypeInt:
		if val.IntVal != nil {
			return &pb.Value{Kind: &pb.Value_IntValue{IntValue: *val.IntVal}}
		}
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
	case p.ValueTypeDouble:
		if val.DoubleVal != nil {
			return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: *val.DoubleVal}}
		}
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
	case p.ValueTypeBool:
		if val.BoolVal != nil {
			return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: *val.BoolVal}}
		}
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
	case p.ValueTypeBlobRef:
		if blob, ok := blobMap[val.BlobID]; ok {
			return &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
				Encoding: blob.Encoding,
				Payload:  blob.Payload,
			}}}
		}
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
	default:
		return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
	}
}

// collectBlobIDsFromRunRow gathers all blob IDs from a RunRow's state, unconsumed
// channel messages, and active step execution inputs.
func collectBlobIDsFromRunRow(run *p.RunRow) []ids.BlobID {
	seen := make(map[ids.BlobID]struct{})
	var blobIDs []ids.BlobID
	add := func(v p.Value) {
		if v.Type == p.ValueTypeBlobRef && !v.BlobID.IsZero() {
			if _, dup := seen[v.BlobID]; !dup {
				seen[v.BlobID] = struct{}{}
				blobIDs = append(blobIDs, v.BlobID)
			}
		}
	}

	for _, v := range run.StateMap {
		add(v)
	}
	for _, msgs := range run.UnconsumedChannelMessages {
		for _, m := range msgs {
			add(m.Value)
		}
	}
	for _, stepExe := range run.ActiveStepExecutions {
		add(stepExe.Input)
	}
	return blobIDs
}

// loadBlobsToMap batch-fetches all blobs by ID and returns a map for quick lookup.
func loadBlobsToMap(
	ctx context.Context,
	blobStore p.BlobStore,
	shardID int32,
	namespace, runID string,
	blobIDs []ids.BlobID,
) (map[ids.BlobID]p.BlobEntry, errors.CategorizedError) {
	if len(blobIDs) == 0 || blobStore == nil {
		return nil, nil
	}
	entries, err := blobStore.BatchGetBlobs(ctx, shardID, namespace, runID, blobIDs)
	if err != nil {
		return nil, err
	}
	m := make(map[ids.BlobID]p.BlobEntry, len(entries))
	for _, e := range entries {
		m[e.BlobID] = e
	}
	return m, nil
}
