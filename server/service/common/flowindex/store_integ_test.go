// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

//go:build paradedb

package flowindex

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParadeDBSchemaWriteSearchAndFencing(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("DEX_PARADEDB_DSN")
	if dsn == "" {
		dsn = "postgres://dex:dex@127.0.0.1:5433/dex?sslmode=disable"
	}
	schema := fmt.Sprintf("dex_test_%d", time.Now().UnixNano())
	store, err := NewParadeDBStore(ctx, &config.FlowIndexConfig{
		Backend: config.FlowIndexBackendParadeDB,
		ParadeDB: config.ParadeDBConfig{
			DSN: dsn, Schema: schema, Table: "flow_index", MaxConnections: 4,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := store.pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, quoteIdentifier(schema)))
		require.NoError(t, dropErr)
		store.Close()
	})

	_, err = store.Search(ctx, &dexpb.SearchFlowsRequest{})
	require.ErrorIs(t, err, ErrSchemaNotApplied)
	fields := allIntegrationFields()
	apply, err := store.ApplySchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: definitionVersion,
		Attributes:        fields,
	})
	require.NoError(t, err)
	require.True(t, apply.GetChanged())
	require.Equal(t, int64(1), apply.GetSchemaVersion())
	idempotent, err := store.ApplySchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: definitionVersion,
		Attributes:        fields,
	})
	require.NoError(t, err)
	require.False(t, idempotent.GetChanged())
	require.Equal(t, int64(1), idempotent.GetSchemaVersion())
	additiveFields := append(fields, &dexpb.FlowIndexField{Name: "additive", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD})
	additive, err := store.ApplySchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: definitionVersion,
		Attributes:        additiveFields,
	})
	require.NoError(t, err)
	require.True(t, additive.GetChanged())
	require.Equal(t, []string{"additive"}, additive.GetAddedFields())
	_, err = store.ApplySchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: definitionVersion,
		Attributes:        fields,
	})
	require.Error(t, err)
	require.True(t, IsRequestError(err))

	runStart := timestamppb.New(time.Now().UTC())
	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-1", RunId: "run-1", FlowType: "Checkout", RunStartedAt: runStart,
		Mutation: &dexpb.FlowIndexMutation{
			Sequence:   0,
			Upserts:    integrationValues(),
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
		},
	}))

	textResults, err := store.Search(ctx, &dexpb.SearchFlowsRequest{Query: `text:world`, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, textResults.GetFlowRuns(), 1)
	require.NotNil(t, textResults.GetFlowRuns()[0].Bm25Score)
	for _, query := range []string{
		`keyword:"ready"`,
		`keywords:"one"`,
		`integer:8`,
		`double:2.5`,
		`boolean:true`,
		`datetime:"2026-08-05T12:00:00Z"`,
	} {
		results, searchErr := store.Search(ctx, &dexpb.SearchFlowsRequest{Query: query, PageSize: 10})
		require.NoError(t, searchErr, query)
		require.Len(t, results.GetFlowRuns(), 1, query)
		require.NotNil(t, results.GetFlowRuns()[0].Bm25Score, query)
	}
	vectorResults, err := store.Search(ctx, &dexpb.SearchFlowsRequest{
		Query:       `boolean:true`,
		PageSize:    10,
		VectorQuery: &dexpb.SearchVectorQuery{IndexKey: "embedding", Vector: []float32{1, 2, 3}},
	})
	require.NoError(t, err)
	require.Len(t, vectorResults.GetFlowRuns(), 1)
	require.NotNil(t, vectorResults.GetFlowRuns()[0].VectorDistance)
	for _, vectorField := range []string{"embedding_l2", "embedding_ip"} {
		vectorResults, err = store.Search(ctx, &dexpb.SearchFlowsRequest{
			PageSize:    10,
			VectorQuery: &dexpb.SearchVectorQuery{IndexKey: vectorField, Vector: []float32{1, 2, 3}},
		})
		require.NoError(t, err)
		require.Len(t, vectorResults.GetFlowRuns(), 1)
		require.NotNil(t, vectorResults.GetFlowRuns()[0].VectorDistance)
	}
	_, err = store.Search(ctx, &dexpb.SearchFlowsRequest{
		VectorQuery: &dexpb.SearchVectorQuery{IndexKey: "embedding", Vector: []float32{1, 2}},
	})
	require.Error(t, err)
	require.True(t, IsRequestError(err))

	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-1", RunId: "run-1", FlowType: "Checkout", RunStartedAt: runStart,
		Mutation: &dexpb.FlowIndexMutation{Sequence: 0, Deletes: []string{"keyword"}},
	}))
	row := searchSingleFlow(t, store)
	require.Contains(t, attributeNames(row), "keyword")

	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-1", RunId: "run-1", FlowType: "Checkout", RunStartedAt: runStart,
		Mutation: &dexpb.FlowIndexMutation{
			Sequence:   1,
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
			CloseTime:  timestamppb.Now(),
		},
	}))
	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-1", RunId: "run-1", FlowType: "Checkout", RunStartedAt: runStart,
		Mutation: &dexpb.FlowIndexMutation{Sequence: 2, FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING},
	}))
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, searchSingleFlow(t, store).GetFlowStatus())

	resetStart := timestamppb.New(runStart.AsTime().Add(time.Minute))
	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-1", RunId: "run-2", FlowType: "Checkout", RunStartedAt: resetStart,
		Mutation: &dexpb.FlowIndexMutation{Sequence: 0, Replace: true, FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING},
	}))
	row = searchSingleFlow(t, store)
	require.Equal(t, "run-2", row.GetRunId())
	require.WithinDuration(t, resetStart.AsTime(), row.GetStartTime().AsTime(), time.Microsecond)
	require.NotContains(t, attributeNames(row), "keyword")
	require.Nil(t, row.GetCloseTime())

	require.NoError(t, store.Write(ctx, &dexpb.WriteFlowIndexActivityInput{
		FlowId: "flow-2", RunId: "run-1", FlowType: "Checkout", RunStartedAt: timestamppb.Now(),
		Mutation: &dexpb.FlowIndexMutation{Sequence: 0, FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING},
	}))
	firstPage, err := store.Search(ctx, &dexpb.SearchFlowsRequest{PageSize: 1})
	require.NoError(t, err)
	require.Len(t, firstPage.GetFlowRuns(), 1)
	require.NotEmpty(t, firstPage.GetNextPageToken())
	secondPage, err := store.Search(ctx, &dexpb.SearchFlowsRequest{
		PageSize: 1, NextPageToken: firstPage.GetNextPageToken(),
	})
	require.NoError(t, err)
	require.Len(t, secondPage.GetFlowRuns(), 1)
	require.NotEqual(t, firstPage.GetFlowRuns()[0].GetFlowId(), secondPage.GetFlowRuns()[0].GetFlowId())
}

func allIntegrationFields() []*dexpb.FlowIndexField {
	return []*dexpb.FlowIndexField{
		{Name: "keyword", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD},
		{Name: "text", Type: dexpb.IndexType_INDEX_TYPE_TEXT},
		{Name: "keywords", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY},
		{Name: "integer", Type: dexpb.IndexType_INDEX_TYPE_INT},
		{Name: "double", Type: dexpb.IndexType_INDEX_TYPE_DOUBLE},
		{Name: "boolean", Type: dexpb.IndexType_INDEX_TYPE_BOOL},
		{Name: "datetime", Type: dexpb.IndexType_INDEX_TYPE_DATETIME},
		{Name: "embedding", Type: dexpb.IndexType_INDEX_TYPE_VECTOR, VectorDimensions: 3,
			VectorDistanceMetric: dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE},
		{Name: "embedding_l2", Type: dexpb.IndexType_INDEX_TYPE_VECTOR, VectorDimensions: 3,
			VectorDistanceMetric: dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_L2},
		{Name: "embedding_ip", Type: dexpb.IndexType_INDEX_TYPE_VECTOR, VectorDimensions: 3,
			VectorDistanceMetric: dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_INNER_PRODUCT},
	}
}

func integrationValues() map[string]*dexpb.Value {
	return map[string]*dexpb.Value{
		"keyword":      {Kind: &dexpb.Value_StringValue{StringValue: "ready"}},
		"text":         {Kind: &dexpb.Value_StringValue{StringValue: "hello world"}},
		"keywords":     {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`["one","two"]`)}}},
		"integer":      {Kind: &dexpb.Value_IntValue{IntValue: 8}},
		"double":       {Kind: &dexpb.Value_DoubleValue{DoubleValue: 2.5}},
		"boolean":      {Kind: &dexpb.Value_BoolValue{BoolValue: true}},
		"datetime":     {Kind: &dexpb.Value_StringValue{StringValue: "2026-08-05T12:00:00Z"}},
		"embedding":    {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`[1,2,3]`)}}},
		"embedding_l2": {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`[1,2,3]`)}}},
		"embedding_ip": {Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte(`[1,2,3]`)}}},
	}
}

func searchSingleFlow(t *testing.T, store *ParadeDBStore) *dexpb.SearchFlowsResponseEntry {
	result, err := store.Search(context.Background(), &dexpb.SearchFlowsRequest{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, result.GetFlowRuns(), 1)
	return result.GetFlowRuns()[0]
}

func attributeNames(entry *dexpb.SearchFlowsResponseEntry) []string {
	names := make([]string, 0, len(entry.GetSearchAttributes()))
	for _, attribute := range entry.GetSearchAttributes() {
		names = append(names, attribute.GetKey())
	}
	return names
}
