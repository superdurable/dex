// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

const (
	testEncryptedEncoding        = "binary/encrypted"
	testOriginalEncodingMetadata = "test/original-encoding"
)

type testEncryptionCodec struct{}

func (c *testEncryptionCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		cloned := protoClonePayload(p)
		cloned.Metadata[testOriginalEncodingMetadata] = append(
			[]byte(nil),
			p.GetMetadata()["encoding"]...,
		)
		cloned.Metadata["encoding"] = []byte(testEncryptedEncoding)
		out[i] = cloned
	}
	return out, nil
}

func (c *testEncryptionCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()["encoding"]) != testEncryptedEncoding {
			out[i] = p
			continue
		}
		out[i] = protoClonePayload(p)
		out[i].Metadata["encoding"] = append(
			[]byte(nil),
			p.GetMetadata()[testOriginalEncodingMetadata]...,
		)
		delete(out[i].Metadata, testOriginalEncodingMetadata)
	}
	return out, nil
}

func protoClonePayload(p *commonpb.Payload) *commonpb.Payload {
	cloned := &commonpb.Payload{
		Metadata: make(map[string][]byte, len(p.GetMetadata())),
		Data:     append([]byte(nil), p.GetData()...),
	}
	for k, v := range p.GetMetadata() {
		cloned.Metadata[k] = append([]byte(nil), v...)
	}
	return cloned
}

func TestGetMemoAndDecryptIfNeeded(t *testing.T) {
	cryptoConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &testEncryptionCodec{})
	client := &temporalClient{dataConverter: cryptoConverter, memoEncryption: true}

	encoded := &dexpb.EncodedObject{
		Encoding: "json",
		Payload:  []byte("TestValue"),
	}
	expected := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: encoded}}

	innerPayload, err := cryptoConverter.ToPayload(encoded)
	require.NoError(t, err)

	t.Run("new SDK format - data converter applied to memo", func(t *testing.T) {
		memoField, err := cryptoConverter.ToPayload(innerPayload)
		require.NoError(t, err)

		out, err := client.getMemoAndDecryptIfNeeded(&commonpb.Memo{
			Fields: map[string]*commonpb.Payload{"TestKey": memoField},
		})
		require.NoError(t, err)
		assert.Equal(t, expected.GetObjValue().GetEncoding(), out["TestKey"].GetObjValue().GetEncoding())
		assert.Equal(t, expected.GetObjValue().GetPayload(), out["TestKey"].GetObjValue().GetPayload())
	})

	t.Run("legacy format - default converter applied to memo", func(t *testing.T) {
		memoField, err := converter.GetDefaultDataConverter().ToPayload(innerPayload)
		require.NoError(t, err)

		out, err := client.getMemoAndDecryptIfNeeded(&commonpb.Memo{
			Fields: map[string]*commonpb.Payload{"TestKey": memoField},
		})
		require.NoError(t, err)
		assert.Equal(t, expected.GetObjValue().GetEncoding(), out["TestKey"].GetObjValue().GetEncoding())
		assert.Equal(t, expected.GetObjValue().GetPayload(), out["TestKey"].GetObjValue().GetPayload())
	})
}
