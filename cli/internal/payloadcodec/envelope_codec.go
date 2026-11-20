// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package payloadcodec encrypts and decrypts Dex payload envelopes in process, remotely, or as a local codec server.
package payloadcodec

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

const (
	// EnvelopeMarker is the object encoding of an encrypted Value.
	EnvelopeMarker = "dex/1"
	// ReservedEncodingPrefix starts every encoding that Dex reserves.
	ReservedEncodingPrefix = "dex/"
	// EncodedTextPrefix starts an encrypted error text or reason.
	EncodedTextPrefix = "dex-encoded:"

	defaultMaxEnvelopeBytes  = 16*1024*1024 + 64*1024
	defaultMaxPlaintextBytes = 64 * 1024 * 1024
	maxKeyIDBytes            = 64
	maxMetadataEntries       = 16
	maxMetadataTotalBytes    = 4096
)

// Error categories that the codec server protocol adds to the decoding categories of the format.
const (
	CategoryMalformed        = "malformed"
	CategoryUnauthenticated  = "unauthenticated"
	CategoryForbidden        = "forbidden"
	CategoryLimitExceeded    = "limit_exceeded"
	CategoryInvalidPlaintext = "invalid_plaintext"
	CategoryInternal         = "internal"
)

// EnvelopeCodec encrypts Values into serialized PayloadEnvelopes and back, one entry per input, in order.
type EnvelopeCodec interface {
	// EncodeValues returns the serialized PayloadEnvelope of each Value.
	EncodeValues(ctx context.Context, payloadContext dex.PayloadContext, values []*dexpb.Value) ([][]byte, error)
	// DecodeEnvelopes returns the Value inside each serialized PayloadEnvelope.
	DecodeEnvelopes(ctx context.Context, payloadContext dex.PayloadContext, envelopes [][]byte) ([]*dexpb.Value, error)
}

// Limits holds the decoding size limits of the payload encryption format. Zero fields use the defaults.
type Limits struct {
	MaxEnvelopeBytes int
	// MaxEncodedTextBytes defaults to the base64 length of MaxEnvelopeBytes.
	MaxEncodedTextBytes int
	MaxDataBytes        int
	MaxPlaintextBytes   int
}

func (limits Limits) withDefaults() Limits {
	if limits.MaxEnvelopeBytes == 0 {
		limits.MaxEnvelopeBytes = defaultMaxEnvelopeBytes
	}
	if limits.MaxEncodedTextBytes == 0 {
		limits.MaxEncodedTextBytes = 4 * ((limits.MaxEnvelopeBytes + 2) / 3)
	}
	if limits.MaxDataBytes == 0 {
		limits.MaxDataBytes = limits.MaxEnvelopeBytes
	}
	if limits.MaxPlaintextBytes == 0 {
		limits.MaxPlaintextBytes = defaultMaxPlaintextBytes
	}
	return limits
}

// CodecError is a payload encoding or decoding failure. Its text never contains payload bytes or keys.
type CodecError struct {
	// Category is a decoding category of the format or a codec server protocol category.
	Category string
	// Message describes the failure without payload bytes, plaintext, or keys.
	Message string
	// Index is the first failing entry of the batch, or -1 when the failure is not about one entry.
	Index int
	// KeyID is the validated key ID of an unknown_key failure, or empty.
	KeyID string
}

func (codecError *CodecError) Error() string {
	if codecError.Index >= 0 {
		return fmt.Sprintf("payload entry %d: %s: %s", codecError.Index, codecError.Category, codecError.Message)
	}
	return fmt.Sprintf("payload codec: %s: %s", codecError.Category, codecError.Message)
}

func newEntryError(index int, category string, format string, arguments ...any) *CodecError {
	return &CodecError{Category: category, Message: fmt.Sprintf(format, arguments...), Index: index}
}

// IsEnvelopeObject reports whether an object encoding is reserved for Dex and must be decoded.
func IsEnvelopeObject(encoding string) bool {
	return strings.HasPrefix(encoding, ReservedEncodingPrefix)
}

// CheckEnvelopeMarker runs decode step 1 on an object whose encoding is reserved for Dex.
func CheckEnvelopeMarker(encoding string) error {
	if encoding != EnvelopeMarker {
		return &CodecError{Category: string(dex.PayloadErrorUnsupportedMarker), Message: fmt.Sprintf("encoding %q", encoding), Index: -1}
	}
	return nil
}

// DecodeEncodedText runs decode steps 2 and 3 on the base64 text after the "dex-encoded:" prefix, or on a
// codec server envelope. It returns the serialized envelope.
func DecodeEncodedText(encodedText string, limits Limits) ([]byte, error) {
	limits = limits.withDefaults()
	if len(encodedText) > limits.MaxEncodedTextBytes {
		return nil, &CodecError{
			Category: CategoryLimitExceeded,
			Message:  fmt.Sprintf("encoded text has %d bytes, more than %d", len(encodedText), limits.MaxEncodedTextBytes),
			Index:    -1,
		}
	}
	envelope, err := base64.StdEncoding.Strict().DecodeString(encodedText)
	if err != nil || base64.StdEncoding.EncodeToString(envelope) != encodedText {
		return nil, &CodecError{Category: CategoryMalformed, Message: "text is not canonical padded base64", Index: -1}
	}
	if len(envelope) > limits.MaxEnvelopeBytes {
		return nil, &CodecError{
			Category: CategoryLimitExceeded,
			Message:  fmt.Sprintf("envelope has %d bytes, more than %d", len(envelope), limits.MaxEnvelopeBytes),
			Index:    -1,
		}
	}
	return envelope, nil
}

// CheckEncryptableValue rejects a Value that the format never encrypts.
func CheckEncryptableValue(value *dexpb.Value) error {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue, *dexpb.Value_IntValue, *dexpb.Value_DoubleValue, *dexpb.Value_BoolValue:
		return nil
	case *dexpb.Value_ObjValue:
		if IsEnvelopeObject(kind.ObjValue.GetEncoding()) {
			return fmt.Errorf("object encoding %q is reserved for Dex", kind.ObjValue.GetEncoding())
		}
		return nil
	default:
		return fmt.Errorf("value kind %T is never encrypted", kind)
	}
}

// parseEnvelope runs decode steps 2 (size), 4, 5, and 6 on one serialized envelope.
func parseEnvelope(serialized []byte, limits Limits) (*dexpb.PayloadEnvelope, error) {
	if len(serialized) > limits.MaxEnvelopeBytes {
		return nil, &CodecError{
			Category: CategoryLimitExceeded,
			Message:  fmt.Sprintf("envelope has %d bytes, more than %d", len(serialized), limits.MaxEnvelopeBytes),
			Index:    -1,
		}
	}
	envelope := &dexpb.PayloadEnvelope{}
	if err := proto.Unmarshal(serialized, envelope); err != nil {
		return nil, &CodecError{Category: CategoryMalformed, Message: "envelope is not a PayloadEnvelope", Index: -1}
	}
	if err := validateKeyID(envelope.GetKeyId()); err != nil {
		return nil, &CodecError{Category: CategoryMalformed, Message: err.Error(), Index: -1}
	}
	metadataBytes := 0
	for key, value := range envelope.GetMetadata() {
		metadataBytes += len(key) + len(value)
	}
	if len(envelope.GetMetadata()) > maxMetadataEntries || metadataBytes > maxMetadataTotalBytes {
		return nil, &CodecError{
			Category: CategoryLimitExceeded,
			Message:  fmt.Sprintf("metadata has %d entries and %d bytes", len(envelope.GetMetadata()), metadataBytes),
			Index:    -1,
		}
	}
	return envelope, nil
}

func validateKeyID(keyID string) error {
	if len(keyID) == 0 || len(keyID) > maxKeyIDBytes {
		return fmt.Errorf("key id has %d bytes; it needs 1 to %d", len(keyID), maxKeyIDBytes)
	}
	for index := range len(keyID) {
		if keyID[index] < 0x21 || keyID[index] > 0x7e {
			return fmt.Errorf("key id byte %d is outside printable ASCII 0x21 to 0x7e", index)
		}
	}
	return nil
}

// parsePlaintext runs decode step 14: the plaintext is a Value of an encryptable kind.
func parsePlaintext(plaintext []byte) (*dexpb.Value, error) {
	value := &dexpb.Value{}
	if err := proto.Unmarshal(plaintext, value); err != nil {
		return nil, &CodecError{Category: CategoryInvalidPlaintext, Message: "plaintext is not a Value", Index: -1}
	}
	if err := CheckEncryptableValue(value); err != nil {
		return nil, &CodecError{Category: CategoryInvalidPlaintext, Message: err.Error(), Index: -1}
	}
	return value, nil
}
