// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package payloadcodec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type goldenVectorLimits struct {
	MaxEnvelopeBytes    int `json:"maxEnvelopeBytes"`
	MaxEncodedTextBytes int `json:"maxEncodedTextBytes"`
	MaxDataBytes        int `json:"maxDataBytes"`
	MaxPlaintextBytes   int `json:"maxPlaintextBytes"`
}

type goldenVectorCase struct {
	Name  string `json:"name"`
	Input struct {
		Value json.RawMessage `json:"value"`
		Text  string          `json:"text"`
	} `json:"input"`
	Limits        *goldenVectorLimits `json:"limits"`
	ExpectedValue json.RawMessage     `json:"expectedValue"`
	ExpectedError string              `json:"expectedError"`
}

type goldenVectors struct {
	KeyFile  json.RawMessage    `json:"keyFile"`
	Positive []goldenVectorCase `json:"positive"`
	Negative []goldenVectorCase `json:"negative"`
}

// TestLocalCodecDecodesGoldenVectors checks the checks that dexcli runs around the Go SDK codec against the
// cross-SDK contract, because the codec server and Flow commands rely on them.
func TestLocalCodecDecodesGoldenVectors(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "protos", "conformance", "payload-encryption", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors goldenVectors
	if err := json.Unmarshal(content, &vectors); err != nil {
		t.Fatal(err)
	}
	keyFilePath := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(keyFilePath, vectors.KeyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors.Positive {
		t.Run(vector.Name, func(t *testing.T) {
			value, err := decodeGoldenVector(t, keys, vector)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			expected := &dexpb.Value{}
			if err := protojson.Unmarshal(vector.ExpectedValue, expected); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(value, expected) {
				t.Fatalf("decoded %v, want %v", value, expected)
			}
		})
	}
	for _, vector := range vectors.Negative {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := decodeGoldenVector(t, keys, vector)
			var codecError *CodecError
			if !errors.As(err, &codecError) || codecError.Category != vector.ExpectedError {
				t.Fatalf("error = %v, want category %s", err, vector.ExpectedError)
			}
		})
	}
}

func decodeGoldenVector(t *testing.T, keys dex.PayloadEncryptionKeyProvider, vector goldenVectorCase) (*dexpb.Value, error) {
	t.Helper()
	var limits Limits
	if vector.Limits != nil {
		limits = Limits(*vector.Limits)
	}
	codec, err := newLocalCodec(keys, limits)
	if err != nil {
		t.Fatal(err)
	}
	payloadContext := dex.PayloadContext{FlowType: "OrderFlow", FlowID: "order-1"}
	if vector.Input.Text != "" {
		envelope, err := DecodeEncodedText(strings.TrimPrefix(vector.Input.Text, EncodedTextPrefix), limits)
		if err != nil {
			return nil, err
		}
		values, err := codec.DecodeEnvelopes(context.Background(), payloadContext, [][]byte{envelope})
		if err != nil {
			return nil, err
		}
		if _, isText := values[0].GetKind().(*dexpb.Value_StringValue); !isText {
			return nil, &CodecError{Category: CategoryInvalidPlaintext, Index: -1}
		}
		return values[0], nil
	}
	value := &dexpb.Value{}
	if err := protojson.Unmarshal(vector.Input.Value, value); err != nil {
		t.Fatal(err)
	}
	if err := CheckEnvelopeMarker(value.GetObjValue().GetEncoding()); err != nil {
		return nil, err
	}
	values, err := codec.DecodeEnvelopes(context.Background(), payloadContext, [][]byte{value.GetObjValue().GetPayload()})
	if err != nil {
		return nil, err
	}
	return values[0], nil
}
