// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// These tests decode the golden payload encryption vectors with the SDK's own decoder and the
// built-in AES-256-GCM codec. See protos/payload-encryption.md for the rules.

type payloadEncryptionVectorFile struct {
	KeyFile         json.RawMessage               `json:"keyFile"`
	Limits          payloadEncryptionVectorLimits `json:"limits"`
	ErrorCategories []string                      `json:"errorCategories"`
	Passthrough     []struct {
		Name  string                       `json:"name"`
		Input payloadEncryptionVectorInput `json:"input"`
	} `json:"passthrough"`
	Positive []struct {
		Name     string                       `json:"name"`
		Input    payloadEncryptionVectorInput `json:"input"`
		Envelope struct {
			Cipher      int32  `json:"cipher"`
			KeyID       string `json:"keyId"`
			Compression int32  `json:"compression"`
			NonceHex    string `json:"nonceHex"`
			AADHex      string `json:"aadHex"`
		} `json:"envelope"`
		ExpectedPlaintext string                         `json:"expectedPlaintext"`
		ExpectedValue     json.RawMessage                `json:"expectedValue"`
		ExpectedText      *string                        `json:"expectedText"`
		Limits            *payloadEncryptionVectorLimits `json:"limits"`
	} `json:"positive"`
	Negative []struct {
		Name          string                         `json:"name"`
		Input         payloadEncryptionVectorInput   `json:"input"`
		ExpectedError string                         `json:"expectedError"`
		Limits        *payloadEncryptionVectorLimits `json:"limits"`
	} `json:"negative"`
}

type payloadEncryptionVectorLimits struct {
	MaxEnvelopeBytes      int `json:"maxEnvelopeBytes"`
	MaxEncodedTextBytes   int `json:"maxEncodedTextBytes"`
	MaxDataBytes          int `json:"maxDataBytes"`
	MaxPlaintextBytes     int `json:"maxPlaintextBytes"`
	MaxKeyIDBytes         int `json:"maxKeyIdBytes"`
	MaxMetadataEntries    int `json:"maxMetadataEntries"`
	MaxMetadataTotalBytes int `json:"maxMetadataTotalBytes"`
}

type payloadEncryptionVectorInput struct {
	Value json.RawMessage `json:"value"`
	Text  *string         `json:"text"`
}

var conformancePayloadContext = PayloadContext{FlowType: "ConformanceFlow", FlowID: "conformance-1"}

func TestPayloadEncryptionVectorsPositiveDecode(t *testing.T) {
	file, keys := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Positive)
	for _, vector := range file.Positive {
		t.Run(vector.Name, func(t *testing.T) {
			encryption := newConformancePayloadEncryption(t, keys, file.Limits, vector.Limits)
			envelope, values, plaintexts := decodeConformanceVectorParts(t, encryption, vector.Input)
			wantPlaintext, err := base64.StdEncoding.DecodeString(vector.ExpectedPlaintext)
			require.NoError(t, err)
			require.Equal(t, wantPlaintext, plaintexts[0])
			want := &dexpb.Value{}
			require.NoError(t, protojson.Unmarshal(vector.ExpectedValue, want))
			requireConformanceValuesEqual(t, want, values[0])
			require.Equal(t, vector.Envelope.Cipher, int32(envelope.GetCipher()))
			require.Equal(t, vector.Envelope.KeyID, envelope.GetKeyId())
			require.Equal(t, vector.Envelope.Compression, int32(envelope.GetCompression()))
			require.Equal(t, vector.Envelope.NonceHex, hex.EncodeToString(envelope.GetData()[:aesGCMPayloadNonceBytes]))
			require.Equal(t, vector.Envelope.AADHex, hex.EncodeToString(payloadAdditionalAuthenticatedData(
				envelope.GetCipher(), envelope.GetCompression(), envelope.GetKeyId(),
			)))

			// The SDK entry points give the same result.
			if vector.Input.Text != nil {
				text, err := encryption.decodeErrorText(context.Background(), conformancePayloadContext, *vector.Input.Text)
				require.NoError(t, err)
				require.NotNil(t, vector.ExpectedText)
				require.Equal(t, *vector.ExpectedText, text)
				return
			}
			decoded := decryptConformanceValue(t, encryption, vector.Input.Value)
			requireConformanceValuesEqual(t, want, decoded)
		})
	}
}

func TestPayloadEncryptionVectorsPassthrough(t *testing.T) {
	file, keys := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Passthrough)
	encryption := newConformancePayloadEncryption(t, keys, file.Limits, nil)
	for _, vector := range file.Passthrough {
		t.Run(vector.Name, func(t *testing.T) {
			if vector.Input.Text != nil {
				text, err := encryption.decodeErrorText(context.Background(), conformancePayloadContext, *vector.Input.Text)
				require.NoError(t, err)
				require.Equal(t, *vector.Input.Text, text)
				return
			}
			input := &dexpb.Value{}
			require.NoError(t, protojson.Unmarshal(vector.Input.Value, input))
			value := input
			require.NoError(t, encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
				conformancePayloadContext, []**dexpb.Value{&value},
			)))
			require.Same(t, input, value)
		})
	}
}

func TestPayloadEncryptionVectorsNegativeRejected(t *testing.T) {
	file, keys := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Negative)
	for _, vector := range file.Negative {
		t.Run(vector.Name, func(t *testing.T) {
			require.Contains(t, file.ErrorCategories, vector.ExpectedError)
			encryption := newConformancePayloadEncryption(t, keys, file.Limits, vector.Limits)
			var err error
			if vector.Input.Text != nil {
				_, err = encryption.decodeErrorText(context.Background(), conformancePayloadContext, *vector.Input.Text)
			} else {
				input := &dexpb.Value{}
				require.NoError(t, protojson.Unmarshal(vector.Input.Value, input))
				value := input
				err = encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
					conformancePayloadContext, []**dexpb.Value{&value},
				))
				require.Same(t, input, value, "a rejected value must not be replaced")
			}
			var decodeErr *PayloadDecodeError
			require.True(t, errors.As(err, &decodeErr), "decode returned %v", err)
			require.Equal(t, PayloadErrorCategory(vector.ExpectedError), decodeErr.Category, err.Error())
		})
	}
}

// TestPayloadEncryptionVectorsWithoutCodec checks the error for encrypted input and no codec.
func TestPayloadEncryptionVectorsWithoutCodec(t *testing.T) {
	file, _ := loadPayloadEncryptionVectors(t)
	encryption := newDisabledPayloadEncryption()
	for _, vector := range file.Positive {
		var err error
		if vector.Input.Text != nil {
			_, err = encryption.decodeErrorText(context.Background(), conformancePayloadContext, *vector.Input.Text)
		} else {
			value := &dexpb.Value{}
			require.NoError(t, protojson.Unmarshal(vector.Input.Value, value))
			err = encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
				conformancePayloadContext, []**dexpb.Value{&value},
			))
			var decoded string
			require.ErrorIs(t, decodeValue(value, &decoded), ErrPayloadCodecNotConfigured)
		}
		require.ErrorIs(t, err, ErrPayloadCodecNotConfigured, vector.Name)
		require.Contains(t, err.Error(), "value is encrypted; configure a PayloadCodec")
	}
}

func loadPayloadEncryptionVectors(t *testing.T) (payloadEncryptionVectorFile, *FilePayloadEncryptionKeyProvider) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protos", "conformance", "payload-encryption", "vectors.json"))
	require.NoError(t, err)
	var file payloadEncryptionVectorFile
	require.NoError(t, json.Unmarshal(data, &file))
	keyFilePath := filepath.Join(t.TempDir(), "keys.json")
	require.NoError(t, os.WriteFile(keyFilePath, file.KeyFile, 0o600))
	keys, err := NewFilePayloadEncryptionKeyProvider(keyFilePath)
	require.NoError(t, err)
	return file, keys
}

func newConformancePayloadEncryption(
	t *testing.T,
	keys PayloadEncryptionKeyProvider,
	defaults payloadEncryptionVectorLimits,
	override *payloadEncryptionVectorLimits,
) *payloadEncryption {
	t.Helper()
	limits := defaults
	if override != nil {
		limits = *override
	}
	require.Equal(t, maxPayloadKeyIDBytes, limits.MaxKeyIDBytes)
	require.Equal(t, maxPayloadMetadataEntries, limits.MaxMetadataEntries)
	require.Equal(t, maxPayloadMetadataTotalBytes, limits.MaxMetadataTotalBytes)
	codec, err := NewAESGCMPayloadCodec(keys, AESGCMPayloadCodecOptions{
		MaxDecompressedBytes: limits.MaxPlaintextBytes,
		MaxDataBytes:         limits.MaxDataBytes,
	})
	require.NoError(t, err)
	encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: codec})
	require.NoError(t, err)
	encryption.limits = payloadDecodeLimits{
		maxEnvelopeBytes:    limits.MaxEnvelopeBytes,
		maxEncodedTextBytes: limits.MaxEncodedTextBytes,
		maxPlaintextBytes:   limits.MaxPlaintextBytes,
	}
	return encryption
}

// decodeConformanceVectorParts returns the envelope, the decoded Value, and its plaintext bytes.
func decodeConformanceVectorParts(
	t *testing.T,
	encryption *payloadEncryption,
	input payloadEncryptionVectorInput,
) (*dexpb.PayloadEnvelope, []*dexpb.Value, [][]byte) {
	t.Helper()
	var envelope *dexpb.PayloadEnvelope
	var err error
	isText := input.Text != nil
	if isText {
		encodedText, found := strings.CutPrefix(*input.Text, payloadEncodedTextPrefix)
		require.True(t, found)
		envelope, err = encryption.parseEncodedText(encodedText)
	} else {
		value := &dexpb.Value{}
		require.NoError(t, protojson.Unmarshal(input.Value, value))
		require.NotNil(t, value.GetObjValue())
		envelope, err = encryption.parseEnvelopeObject(value.GetObjValue())
	}
	require.NoError(t, err)
	values, plaintexts, err := encryption.decodeEnvelopes(
		context.Background(), conformancePayloadContext, []*dexpb.PayloadEnvelope{envelope}, isText,
	)
	require.NoError(t, err)
	return envelope, values, plaintexts
}

func decryptConformanceValue(t *testing.T, encryption *payloadEncryption, input json.RawMessage) *dexpb.Value {
	t.Helper()
	value := &dexpb.Value{}
	require.NoError(t, protojson.Unmarshal(input, value))
	require.NoError(t, encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
		conformancePayloadContext, []**dexpb.Value{&value},
	)))
	return value
}

func requireConformanceValuesEqual(t *testing.T, want *dexpb.Value, got *dexpb.Value) {
	t.Helper()
	wantDouble, wantIsDouble := want.GetKind().(*dexpb.Value_DoubleValue)
	if !wantIsDouble {
		require.True(t, proto.Equal(want, got), "got %v, want %v", got, want)
		return
	}
	gotDouble, gotIsDouble := got.GetKind().(*dexpb.Value_DoubleValue)
	require.True(t, gotIsDouble, "got %v, want a double", got)
	if math.IsNaN(wantDouble.DoubleValue) {
		require.True(t, math.IsNaN(gotDouble.DoubleValue))
		return
	}
	require.Equal(t, math.Float64bits(wantDouble.DoubleValue), math.Float64bits(gotDouble.DoubleValue))
}
