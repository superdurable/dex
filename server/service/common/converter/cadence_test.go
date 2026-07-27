// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package converter

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"google.golang.org/protobuf/proto"
)

func TestCadenceSingleProtoRoundTrip(t *testing.T) {
	dc := NewCadenceDataConverter()
	in := &iwfpb.SkipTimerSignalRequest{
		StepExecutionId:     "s-1",
		TimerConditionId:    "t-1",
		TimerConditionIndex: 2,
	}
	data, err := dc.ToData(in)
	require.NoError(t, err)
	require.True(t, len(data) >= cadenceHeaderLen)
	require.Equal(t, cadenceMagic, string(data[:5]))
	require.Equal(t, cadenceVersion, data[5])
	require.Equal(t, uint32(1), binary.BigEndian.Uint32(data[6:10]))
	require.Equal(t, kindProto, data[10])

	out := &iwfpb.SkipTimerSignalRequest{}
	require.NoError(t, dc.FromData(data, &out))
	require.True(t, proto.Equal(in, out))
}

func TestCadenceMultiFrameMixedKinds(t *testing.T) {
	dc := NewCadenceDataConverter()
	protoMsg := &iwfpb.FailFlowSignalRequest{Reason: "boom"}
	jsonVal := map[string]string{"k": "v"}
	raw := []byte{9, 8, 7}

	data, err := dc.ToData(protoMsg, jsonVal, raw)
	require.NoError(t, err)
	require.Equal(t, uint32(3), binary.BigEndian.Uint32(data[6:10]))

	var outProto *iwfpb.FailFlowSignalRequest
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
	var typedNil *iwfpb.FailFlowSignalRequest
	data, err := dc.ToData(typedNil)
	require.NoError(t, err)
	require.Equal(t, kindProto, data[10])
	require.Equal(t, nilFlagTrue, data[11])
	require.Equal(t, uint32(0), binary.BigEndian.Uint32(data[12:16]))

	var out *iwfpb.FailFlowSignalRequest
	out = &iwfpb.FailFlowSignalRequest{Reason: "stale"}
	require.NoError(t, dc.FromData(data, &out))
	require.Nil(t, out)
}

func TestCadenceMapOneofRoundTrip(t *testing.T) {
	dc := NewCadenceDataConverter()
	in := &iwfpb.GetCurrentTimerInfosQueryResponse{
		StepExecutionCurrentTimerInfos: map[string]*iwfpb.TimerInfoList{
			"exe-1": {
				Timers: []*iwfpb.TimerInfo{
					{
						ConditionId:                "c1",
						FiringUnixTimestampSeconds: 42,
						Status:                     iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
					},
				},
			},
		},
	}
	data, err := dc.ToData(in)
	require.NoError(t, err)
	out := &iwfpb.GetCurrentTimerInfosQueryResponse{}
	require.NoError(t, dc.FromData(data, &out))
	require.True(t, proto.Equal(in, out))
}

func TestCadenceRejectsCorruptPayloads(t *testing.T) {
	dc := NewCadenceDataConverter()
	var out *iwfpb.Value

	require.Error(t, dc.FromData([]byte("XXXXX"), &out))
	require.Error(t, dc.FromData([]byte("IWFDC"), &out))

	badVersion := append([]byte(cadenceMagic), 99, 0, 0, 0, 1)
	require.Error(t, dc.FromData(badVersion, &out))

	// Valid header claiming 1 frame but truncated frame header.
	truncated := make([]byte, cadenceHeaderLen)
	copy(truncated, cadenceMagic)
	truncated[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(truncated[6:10], 1)
	require.Error(t, dc.FromData(truncated, &out))

	// Declared length larger than remaining bytes.
	oversized := make([]byte, cadenceHeaderLen+cadenceFrameHdrLen)
	copy(oversized, cadenceMagic)
	oversized[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(oversized[6:10], 1)
	oversized[10] = kindProto
	oversized[11] = nilFlagFalse
	binary.BigEndian.PutUint32(oversized[12:16], 100)
	require.Error(t, dc.FromData(oversized, &out))

	// Declared length exceeds maxCadenceFrameBytes.
	tooBig := make([]byte, cadenceHeaderLen+cadenceFrameHdrLen)
	copy(tooBig, cadenceMagic)
	tooBig[5] = byte(cadenceVersion)
	binary.BigEndian.PutUint32(tooBig[6:10], 1)
	tooBig[10] = kindProto
	tooBig[11] = nilFlagFalse
	binary.BigEndian.PutUint32(tooBig[12:16], maxCadenceFrameBytes+1)
	require.Error(t, dc.FromData(tooBig, &out))

	// Wrong arity.
	good, err := dc.ToData(&iwfpb.Value{Kind: &iwfpb.Value_IntValue{IntValue: 1}})
	require.NoError(t, err)
	var a, b *iwfpb.Value
	require.Error(t, dc.FromData(good, &a, &b))

	// Trailing bytes.
	trailing := append(append([]byte{}, good...), 0xFF)
	require.Error(t, dc.FromData(trailing, &out))

	// Unknown kind.
	unknownKind := append([]byte{}, good...)
	unknownKind[10] = 99
	require.Error(t, dc.FromData(unknownKind, &out))
}
