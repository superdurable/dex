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
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

func TestCadenceSingleProtoRoundTrip(t *testing.T) {
	dc := NewCadenceDataConverter()
	in := &dexpb.SkipTimerSignalRequest{
		StepExecutionId:     "s-1",
		TimerConditionId:    "t-1",
		TimerConditionIndex: 2,
	}
	data, err := dc.ToData(in)
	require.NoError(t, err)
	frames := decodeCadenceEnvelope(t, data)
	require.True(t, len(frames) >= cadenceHeaderLen)
	require.Equal(t, cadenceMagic, string(frames[:5]))
	require.Equal(t, cadenceVersion, frames[5])
	require.Equal(t, uint32(1), binary.BigEndian.Uint32(frames[6:10]))
	require.Equal(t, kindProto, frames[10])

	out := &dexpb.SkipTimerSignalRequest{}
	require.NoError(t, dc.FromData(data, &out))
	require.True(t, proto.Equal(in, out))
}

func TestCadenceMultiFrameMixedKinds(t *testing.T) {
	dc := NewCadenceDataConverter()
	protoMsg := &dexpb.FailFlowSignalRequest{Reason: "boom"}
	jsonVal := map[string]string{"k": "v"}
	raw := []byte{9, 8, 7}

	data, err := dc.ToData(protoMsg, jsonVal, raw)
	require.NoError(t, err)
	frames := decodeCadenceEnvelope(t, data)
	require.Equal(t, uint32(3), binary.BigEndian.Uint32(frames[6:10]))

	var outProto *dexpb.FailFlowSignalRequest
	var outJSON map[string]string
	var outRaw []byte
	require.NoError(t, dc.FromData(data, &outProto, &outJSON, &outRaw))
	require.True(t, proto.Equal(protoMsg, outProto))
	require.Equal(t, jsonVal, outJSON)
	require.Equal(t, raw, outRaw)
}

func TestCadenceSingleRawByteSlicePassthrough(t *testing.T) {
	dc := NewCadenceDataConverter()
	raw := []byte{1, 2, 3, 4, 5}
	data, err := dc.ToData(raw)
	require.NoError(t, err)
	require.Equal(t, raw, data)

	var out []byte
	require.NoError(t, dc.FromData(data, &out))
	require.Equal(t, raw, out)
}

func TestCadenceTypedNilProto(t *testing.T) {
	dc := NewCadenceDataConverter()
	var typedNil *dexpb.FailFlowSignalRequest
	data, err := dc.ToData(typedNil)
	require.NoError(t, err)
	frames := decodeCadenceEnvelope(t, data)
	require.Equal(t, kindProto, frames[10])
	require.Equal(t, nilFlagTrue, frames[11])
	require.Equal(t, uint32(0), binary.BigEndian.Uint32(frames[12:16]))

	var out *dexpb.FailFlowSignalRequest
	out = &dexpb.FailFlowSignalRequest{Reason: "stale"}
	require.NoError(t, dc.FromData(data, &out))
	require.Nil(t, out)
}

func TestCadenceMapOneofRoundTrip(t *testing.T) {
	dc := NewCadenceDataConverter()
	in := &dexpb.GetCurrentTimerInfosQueryResponse{
		StepExecutionCurrentTimerInfos: map[string]*dexpb.TimerInfoList{
			"exe-1": {
				Timers: []*dexpb.TimerInfo{
					{
						ConditionId:                "c1",
						FiringUnixTimestampSeconds: 42,
						Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
					},
				},
			},
		},
	}
	data, err := dc.ToData(in)
	require.NoError(t, err)
	out := &dexpb.GetCurrentTimerInfosQueryResponse{}
	require.NoError(t, dc.FromData(data, &out))
	require.True(t, proto.Equal(in, out))
}

func TestCadenceEmptyNoArgRoundTrip(t *testing.T) {
	dc := NewCadenceDataConverter()
	data, err := dc.ToData()
	require.NoError(t, err)
	require.Empty(t, data)
	require.NoError(t, dc.FromData(nil))
	require.NoError(t, dc.FromData([]byte{}))
	require.Error(t, dc.FromData(nil, new(dexpb.Value)))
}

func TestCadenceRejectsCorruptPayloads(t *testing.T) {
	dc := NewCadenceDataConverter()
	var out *dexpb.Value

	require.Error(t, dc.FromData([]byte("XXXXX"), &out))
	require.Error(t, dc.FromData([]byte("DEXDC"), &out))

	badVersion := cadenceEnvelope(append([]byte(cadenceMagic), 99, 0, 0, 0, 1))
	require.Error(t, dc.FromData(badVersion, &out))

	// Valid header claiming 1 frame but truncated frame header.
	truncated := make([]byte, cadenceHeaderLen)
	copy(truncated, cadenceMagic)
	truncated[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(truncated[6:10], 1)
	require.Error(t, dc.FromData(cadenceEnvelope(truncated), &out))

	// Declared length larger than remaining bytes.
	oversized := make([]byte, cadenceHeaderLen+cadenceFrameHdrLen)
	copy(oversized, cadenceMagic)
	oversized[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(oversized[6:10], 1)
	oversized[10] = kindProto
	oversized[11] = nilFlagFalse
	binary.BigEndian.PutUint32(oversized[12:16], 100)
	require.Error(t, dc.FromData(cadenceEnvelope(oversized), &out))

	// Declared length exceeds maxCadenceFrameBytes.
	tooBig := make([]byte, cadenceHeaderLen+cadenceFrameHdrLen)
	copy(tooBig, cadenceMagic)
	tooBig[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(tooBig[6:10], 1)
	tooBig[10] = kindProto
	tooBig[11] = nilFlagFalse
	binary.BigEndian.PutUint32(tooBig[12:16], maxCadenceFrameBytes+1)
	require.Error(t, dc.FromData(cadenceEnvelope(tooBig), &out))

	// Wrong arity.
	good, err := dc.ToData(&dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 1}})
	require.NoError(t, err)
	var a, b *dexpb.Value
	require.Error(t, dc.FromData(good, &a, &b))

	// Trailing bytes.
	trailing := append(decodeCadenceEnvelope(t, good), 0xFF)
	require.Error(t, dc.FromData(cadenceEnvelope(trailing), &out))

	// Unknown kind.
	unknownKind := decodeCadenceEnvelope(t, good)
	unknownKind[10] = 99
	require.Error(t, dc.FromData(cadenceEnvelope(unknownKind), &out))
}

func decodeCadenceEnvelope(t *testing.T, data []byte) []byte {
	t.Helper()
	require.True(t, strings.HasPrefix(string(data), cadenceWirePrefix))
	decoded, err := base64.RawStdEncoding.DecodeString(string(data[len(cadenceWirePrefix):]))
	require.NoError(t, err)
	return decoded
}

func cadenceEnvelope(frames []byte) []byte {
	return []byte(cadenceWirePrefix + base64.RawStdEncoding.EncodeToString(frames))
}
