// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// These tests decode the golden payload encryption vectors with a test-only reference decoder.
// See protos/payload-encryption.md for the rules it follows.

const (
	conformancePayloadEnvelopeMarker = "dex/1"
	conformanceEncodedStringPrefix   = "dex-encoded:"
)

type payloadEncryptionVectorFile struct {
	KeyFile struct {
		CurrentKeyID string            `json:"current_key_id"`
		Keys         map[string]string `json:"keys"`
	} `json:"keyFile"`
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

func TestPayloadEncryptionVectorsPositiveDecode(t *testing.T) {
	file, decoder := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Positive)
	for _, vector := range file.Positive {
		t.Run(vector.Name, func(t *testing.T) {
			result, err := decoder.withLimits(vector.Limits).decode(vector.Input)
			require.NoError(t, err)
			require.False(t, result.passthrough)
			wantPlaintext, err := base64.StdEncoding.DecodeString(vector.ExpectedPlaintext)
			require.NoError(t, err)
			require.Equal(t, wantPlaintext, result.plaintext)
			want := &dexpb.Value{}
			require.NoError(t, protojson.Unmarshal(vector.ExpectedValue, want))
			requireConformanceValuesEqual(t, want, result.value)
			if vector.ExpectedText != nil {
				require.NotNil(t, vector.Input.Text)
				require.Equal(t, *vector.ExpectedText, result.value.GetStringValue())
			}
			envelope := result.envelope
			require.Equal(t, vector.Envelope.Cipher, int32(envelope.GetCipher()))
			require.Equal(t, vector.Envelope.KeyID, envelope.GetKeyId())
			require.Equal(t, vector.Envelope.Compression, int32(envelope.GetCompression()))
			require.Equal(t, vector.Envelope.NonceHex, hex.EncodeToString(envelope.GetData()[:12]))
			require.Equal(t, vector.Envelope.AADHex, hex.EncodeToString(conformanceAdditionalAuthenticatedData(envelope)))
		})
	}
}

func TestPayloadEncryptionVectorsPassthrough(t *testing.T) {
	file, decoder := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Passthrough)
	for _, vector := range file.Passthrough {
		t.Run(vector.Name, func(t *testing.T) {
			result, err := decoder.decode(vector.Input)
			require.NoError(t, err)
			require.True(t, result.passthrough)
		})
	}
}

func TestPayloadEncryptionVectorsNegativeRejected(t *testing.T) {
	file, decoder := loadPayloadEncryptionVectors(t)
	require.NotEmpty(t, file.Negative)
	for _, vector := range file.Negative {
		t.Run(vector.Name, func(t *testing.T) {
			require.Contains(t, file.ErrorCategories, vector.ExpectedError)
			_, err := decoder.withLimits(vector.Limits).decode(vector.Input)
			var rejection *conformanceEnvelopeRejection
			require.True(t, errors.As(err, &rejection), "decode returned %v", err)
			require.Equal(t, vector.ExpectedError, rejection.category, rejection.reason)
		})
	}
}

func loadPayloadEncryptionVectors(t *testing.T) (payloadEncryptionVectorFile, conformanceEnvelopeDecoder) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protos", "conformance", "payload-encryption", "vectors.json"))
	require.NoError(t, err)
	var file payloadEncryptionVectorFile
	require.NoError(t, json.Unmarshal(data, &file))
	keys := map[string][]byte{}
	for keyID, encoded := range file.KeyFile.Keys {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		require.NoError(t, err)
		require.Len(t, key, 32, keyID)
		keys[keyID] = key
	}
	require.Contains(t, keys, file.KeyFile.CurrentKeyID)
	return file, conformanceEnvelopeDecoder{keys: keys, limits: file.Limits}
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

// conformanceEnvelopeRejection is a decode failure with one specification error category.
type conformanceEnvelopeRejection struct {
	category string
	reason   string
}

func (rejection *conformanceEnvelopeRejection) Error() string {
	return rejection.category + ": " + rejection.reason
}

func rejectConformanceEnvelope(category string, format string, arguments ...any) error {
	return &conformanceEnvelopeRejection{category: category, reason: fmt.Sprintf(format, arguments...)}
}

type conformanceDecodeResult struct {
	passthrough bool
	envelope    *dexpb.PayloadEnvelope
	plaintext   []byte
	value       *dexpb.Value
}

// conformanceEnvelopeDecoder follows the specification's decode check order.
type conformanceEnvelopeDecoder struct {
	keys   map[string][]byte
	limits payloadEncryptionVectorLimits
}

// withLimits returns a decoder that uses override, or the receiver when override is nil.
func (decoder conformanceEnvelopeDecoder) withLimits(override *payloadEncryptionVectorLimits) conformanceEnvelopeDecoder {
	if override != nil {
		decoder.limits = *override
	}
	return decoder
}

func (decoder conformanceEnvelopeDecoder) decode(input payloadEncryptionVectorInput) (conformanceDecodeResult, error) {
	if input.Text != nil {
		encoded, found := strings.CutPrefix(*input.Text, conformanceEncodedStringPrefix)
		if !found {
			return conformanceDecodeResult{passthrough: true}, nil
		}
		if len(encoded) > decoder.limits.MaxEncodedTextBytes {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "encoded text has %d bytes", len(encoded))
		}
		envelopeBytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(envelopeBytes) != encoded {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "text is not canonical padded base64")
		}
		if len(envelopeBytes) > decoder.limits.MaxEnvelopeBytes {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "envelope has %d bytes", len(envelopeBytes))
		}
		return decoder.decodeEnvelope(envelopeBytes, true)
	}
	value := &dexpb.Value{}
	if err := protojson.Unmarshal(input.Value, value); err != nil {
		return conformanceDecodeResult{}, fmt.Errorf("input value: %w", err)
	}
	object := value.GetObjValue()
	if object == nil || !strings.HasPrefix(object.GetEncoding(), "dex/") {
		return conformanceDecodeResult{passthrough: true}, nil
	}
	if object.GetEncoding() != conformancePayloadEnvelopeMarker {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("unsupported_marker", "encoding %q", object.GetEncoding())
	}
	if len(object.GetPayload()) > decoder.limits.MaxEnvelopeBytes {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "envelope has %d bytes", len(object.GetPayload()))
	}
	return decoder.decodeEnvelope(object.GetPayload(), false)
}

func (decoder conformanceEnvelopeDecoder) decodeEnvelope(envelopeBytes []byte, isText bool) (conformanceDecodeResult, error) {
	limits := decoder.limits
	envelope := &dexpb.PayloadEnvelope{}
	if err := proto.Unmarshal(envelopeBytes, envelope); err != nil {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "envelope: %v", err)
	}
	keyID := envelope.GetKeyId()
	if len(keyID) == 0 || len(keyID) > limits.MaxKeyIDBytes {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "key id has %d bytes", len(keyID))
	}
	for index := range len(keyID) {
		if keyID[index] < 0x21 || keyID[index] > 0x7e {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "key id byte %d is outside 0x21 to 0x7e", index)
		}
	}
	metadataBytes := 0
	for key, value := range envelope.GetMetadata() {
		metadataBytes += len(key) + len(value)
	}
	if len(envelope.GetMetadata()) > limits.MaxMetadataEntries || metadataBytes > limits.MaxMetadataTotalBytes {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "metadata has %d entries and %d bytes", len(envelope.GetMetadata()), metadataBytes)
	}
	if envelope.GetCipher() != dexpb.PayloadCipher_PAYLOAD_CIPHER_AES256_GCM {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("unsupported_cipher", "cipher %d", envelope.GetCipher())
	}
	if len(envelope.GetMetadata()) > 0 {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "metadata must be empty for AES-256-GCM")
	}
	compression := envelope.GetCompression()
	if compression != dexpb.PayloadCompression_PAYLOAD_COMPRESSION_NONE && compression != dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("unsupported_compression", "compression %d", compression)
	}
	key, ok := decoder.keys[keyID]
	if !ok {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("unknown_key", "key id %q", keyID)
	}
	data := envelope.GetData()
	if len(data) < 12+16 {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "data has %d bytes", len(data))
	}
	if len(data) > limits.MaxDataBytes {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "data has %d bytes", len(data))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return conformanceDecodeResult{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return conformanceDecodeResult{}, err
	}
	plaintext, err := aead.Open(nil, data[:12], data[12:], conformanceAdditionalAuthenticatedData(envelope))
	if err != nil {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("authentication_failed", "key id %q", keyID)
	}
	if compression == dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP {
		reader, err := gzip.NewReader(bytes.NewReader(plaintext))
		if err != nil {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "gzip: %v", err)
		}
		plaintext, err = io.ReadAll(io.LimitReader(reader, int64(limits.MaxPlaintextBytes)+1))
		if err != nil {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("malformed", "gzip: %v", err)
		}
	}
	if len(plaintext) > limits.MaxPlaintextBytes {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("limit_exceeded", "plaintext exceeds %d bytes", limits.MaxPlaintextBytes)
	}
	value := &dexpb.Value{}
	if err := proto.Unmarshal(plaintext, value); err != nil {
		return conformanceDecodeResult{}, rejectConformanceEnvelope("invalid_plaintext", "plaintext is not a Value: %v", err)
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
	case *dexpb.Value_ObjValue:
		if isText || strings.HasPrefix(kind.ObjValue.GetEncoding(), "dex/") {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("invalid_plaintext", "object plaintext is not allowed here")
		}
	case *dexpb.Value_IntValue, *dexpb.Value_DoubleValue, *dexpb.Value_BoolValue:
		if isText {
			return conformanceDecodeResult{}, rejectConformanceEnvelope("invalid_plaintext", "text plaintext must be a string Value")
		}
	default:
		return conformanceDecodeResult{}, rejectConformanceEnvelope("invalid_plaintext", "plaintext kind %T is never encrypted", kind)
	}
	return conformanceDecodeResult{envelope: envelope, plaintext: plaintext, value: value}, nil
}

// conformanceAdditionalAuthenticatedData returns "dex/1" 0x00 cipher 0x00 compression 0x00 key_id.
func conformanceAdditionalAuthenticatedData(envelope *dexpb.PayloadEnvelope) []byte {
	aad := []byte(conformancePayloadEnvelopeMarker + "\x00")
	aad = strconv.AppendInt(aad, int64(envelope.GetCipher()), 10)
	aad = append(aad, 0x00)
	aad = strconv.AppendInt(aad, int64(envelope.GetCompression()), 10)
	aad = append(aad, 0x00)
	return append(aad, envelope.GetKeyId()...)
}
