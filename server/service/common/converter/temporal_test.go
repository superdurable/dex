// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package converter

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

type plainNonProtoStruct struct {
	FlowType string `json:"flowType"`
}

func TestTemporalProtoPayloadIsBinaryProtobuf(t *testing.T) {
	dc := NewTemporalDataConverter()

	in := &dexpb.InterpreterWorkflowInput{
		FlowType:  "order",
		StepInput: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "hi"}},
		Config: &dexpb.FlowConfig{
			WorkerTarget: &dexpb.WorkerTarget{Address: "127.0.0.1:9000"},
		},
		InitAttributes: []*dexpb.KV{
			{Key: "k", Value: &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 7}}},
		},
	}

	payload, err := dc.ToPayload(in)
	require.NoError(t, err)
	require.Equal(t, converter.MetadataEncodingProto, string(payload.Metadata[converter.MetadataEncoding]))

	out := &dexpb.InterpreterWorkflowInput{}
	require.NoError(t, dc.FromPayload(payload, out))
	require.True(t, proto.Equal(in, out))
}

func TestTemporalNilAndBytesRoundTrip(t *testing.T) {
	dc := NewTemporalDataConverter()

	nilPayload, err := dc.ToPayload(nil)
	require.NoError(t, err)
	var nilOut interface{}
	require.NoError(t, dc.FromPayload(nilPayload, &nilOut))
	require.Nil(t, nilOut)

	raw := []byte{1, 2, 3, 4}
	rawPayload, err := dc.ToPayload(raw)
	require.NoError(t, err)
	var rawOut []byte
	require.NoError(t, dc.FromPayload(rawPayload, &rawOut))
	require.Equal(t, raw, rawOut)
}

func TestTemporalMapOneofRoundTrip(t *testing.T) {
	dc := NewTemporalDataConverter()
	in := &dexpb.PrepareRpcQueryResponse{
		RunId:        "run-1",
		FlowType:     "ft",
		WorkerTarget: &dexpb.WorkerTarget{Address: "host:1"},
		ChannelInfos: map[string]*dexpb.ChannelInfo{
			"ch": {Size: 2},
		},
		Attributes: []*dexpb.KV{
			{Key: "a", Value: &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: true}}},
		},
	}
	payload, err := dc.ToPayload(in)
	require.NoError(t, err)
	require.Equal(t, converter.MetadataEncodingProto, string(payload.Metadata[converter.MetadataEncoding]))

	out := &dexpb.PrepareRpcQueryResponse{}
	require.NoError(t, dc.FromPayload(payload, out))
	require.True(t, proto.Equal(in, out))
}

func TestTemporalJSONEscapeHatchForNonProto(t *testing.T) {
	dc := NewTemporalDataConverter()
	in := plainNonProtoStruct{FlowType: "should-be-json"}
	payload, err := dc.ToPayload(in)
	require.NoError(t, err)
	require.Equal(t, converter.MetadataEncodingJSON, string(payload.Metadata[converter.MetadataEncoding]))

	var out plainNonProtoStruct
	require.NoError(t, dc.FromPayload(payload, &out))
	require.Equal(t, in, out)
}

func TestMarshalDeterministicStableForMaps(t *testing.T) {
	msg := &dexpb.ContinueAsNewDump{
		ChannelReceived: map[string]*dexpb.ChannelValues{
			"b": {Values: []*dexpb.Value{{Kind: &dexpb.Value_StringValue{StringValue: "2"}}}},
			"a": {Values: []*dexpb.Value{{Kind: &dexpb.Value_StringValue{StringValue: "1"}}}},
		},
	}
	first, err := MarshalDeterministic(msg)
	require.NoError(t, err)
	second, err := MarshalDeterministic(msg)
	require.NoError(t, err)
	require.Equal(t, first, second)
}
