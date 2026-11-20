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
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

// decodeErrorMessages are the only texts that decoding failures carry, so key provider text never leaks.
var decodeErrorMessages = map[dex.PayloadErrorCategory]string{
	dex.PayloadErrorMalformed:              "envelope is malformed",
	dex.PayloadErrorUnsupportedMarker:      "envelope marker is not supported",
	dex.PayloadErrorUnsupportedCipher:      "envelope cipher is not supported",
	dex.PayloadErrorUnsupportedCompression: "envelope compression is not supported",
	dex.PayloadErrorUnknownKey:             "key id is unknown",
	dex.PayloadErrorAuthenticationFailed:   "envelope authentication failed",
	dex.PayloadErrorLimitExceeded:          "envelope exceeds a size limit",
	dex.PayloadErrorInvalidPlaintext:       "plaintext is not an encryptable Value",
}

// LocalCodec is the Go SDK's AES-256-GCM codec wrapped with the format checks that the SDK runs around it.
type LocalCodec struct {
	keys   dex.PayloadEncryptionKeyProvider
	codec  dex.PayloadCodec
	limits Limits
}

// NewLocalCodecFromKeyFile returns a LocalCodec with the keys of a payload key file and the default limits.
func NewLocalCodecFromKeyFile(keyFilePath string) (*LocalCodec, error) {
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFilePath)
	if err != nil {
		return nil, err
	}
	return newLocalCodec(keys, Limits{})
}

func newLocalCodec(keys dex.PayloadEncryptionKeyProvider, limits Limits) (*LocalCodec, error) {
	limits = limits.withDefaults()
	codec, err := newAESGCMCodec(keys, limits, limits.MaxPlaintextBytes)
	if err != nil {
		return nil, err
	}
	return &LocalCodec{keys: keys, codec: codec, limits: limits}, nil
}

func newAESGCMCodec(keys dex.PayloadEncryptionKeyProvider, limits Limits, maxPlaintextBytes int) (dex.PayloadCodec, error) {
	return dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{
		MaxDecompressedBytes: maxPlaintextBytes,
		MaxDataBytes:         limits.MaxDataBytes,
	})
}

// EncodeValues encrypts each Value with the key file's current key.
func (codec *LocalCodec) EncodeValues(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	values []*dexpb.Value,
) ([][]byte, error) {
	plaintexts := make([][]byte, 0, len(values))
	for index, value := range values {
		if err := CheckEncryptableValue(value); err != nil {
			return nil, newEntryError(index, CategoryInvalidPlaintext, "%v", err)
		}
		plaintext, err := proto.Marshal(value)
		if err != nil {
			return nil, newEntryError(index, CategoryInternal, "serialize value: %v", err)
		}
		if len(plaintext) > codec.limits.MaxPlaintextBytes {
			return nil, newEntryError(index, CategoryLimitExceeded, "plaintext has %d bytes, more than %d", len(plaintext), codec.limits.MaxPlaintextBytes)
		}
		plaintexts = append(plaintexts, plaintext)
	}
	envelopes, err := codec.codec.Encode(ctx, payloadContext, plaintexts)
	if err != nil {
		// The SDK error may hold key provider text, so only the in-process caller sees it.
		return nil, &CodecError{Category: CategoryInternal, Message: err.Error(), Index: -1}
	}
	if len(envelopes) != len(values) {
		return nil, &CodecError{Category: CategoryInternal, Message: fmt.Sprintf("codec returned %d envelopes for %d values", len(envelopes), len(values)), Index: -1}
	}
	serialized := make([][]byte, 0, len(envelopes))
	for index, envelope := range envelopes {
		envelopeBytes, err := proto.Marshal(envelope)
		if err != nil {
			return nil, newEntryError(index, CategoryInternal, "serialize envelope: %v", err)
		}
		if len(envelopeBytes) > codec.limits.MaxEnvelopeBytes {
			return nil, newEntryError(index, CategoryLimitExceeded, "envelope has %d bytes, more than %d", len(envelopeBytes), codec.limits.MaxEnvelopeBytes)
		}
		serialized = append(serialized, envelopeBytes)
	}
	return serialized, nil
}

// DecodeEnvelopes runs decode steps 2 and 4 to 14, one envelope at a time to name a failing entry.
func (codec *LocalCodec) DecodeEnvelopes(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	envelopes [][]byte,
) ([]*dexpb.Value, error) {
	values := make([]*dexpb.Value, 0, len(envelopes))
	for index, serialized := range envelopes {
		value, _, err := codec.decodeEnvelope(ctx, payloadContext, serialized, codec.codec, codec.limits.MaxPlaintextBytes)
		if err != nil {
			return nil, withIndex(err, index)
		}
		values = append(values, value)
	}
	return values, nil
}

// decodeEnvelopeWithinLimit decodes one envelope whose plaintext may have at most maxPlaintextBytes.
// It returns the Value and the decrypted plaintext length in bytes, or a *CodecError whose message never
// contains key provider text.
func (codec *LocalCodec) decodeEnvelopeWithinLimit(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	serialized []byte,
	maxPlaintextBytes int,
) (*dexpb.Value, int, error) {
	limitedCodec := codec.codec
	if maxPlaintextBytes < codec.limits.MaxPlaintextBytes {
		var err error
		if limitedCodec, err = newAESGCMCodec(codec.keys, codec.limits, maxPlaintextBytes); err != nil {
			return nil, 0, &CodecError{Category: CategoryInternal, Message: "codec failure", Index: -1}
		}
	}
	return codec.decodeEnvelope(ctx, payloadContext, serialized, limitedCodec, maxPlaintextBytes)
}

// decodeEnvelope returns the Value and the decrypted plaintext length, which can exceed proto.Size of the
// Value when the plaintext repeats a field.
func (codec *LocalCodec) decodeEnvelope(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	serialized []byte,
	aesCodec dex.PayloadCodec,
	maxPlaintextBytes int,
) (*dexpb.Value, int, error) {
	envelope, err := parseEnvelope(serialized, codec.limits)
	if err != nil {
		return nil, 0, err
	}
	plaintexts, err := aesCodec.Decode(ctx, payloadContext, []*dexpb.PayloadEnvelope{envelope})
	if err != nil {
		return nil, 0, sanitizeDecodeError(err)
	}
	if len(plaintexts) != 1 {
		return nil, 0, &CodecError{Category: CategoryInternal, Message: "codec failure", Index: -1}
	}
	if len(plaintexts[0]) > maxPlaintextBytes {
		return nil, 0, &CodecError{Category: CategoryLimitExceeded, Message: decodeErrorMessages[dex.PayloadErrorLimitExceeded], Index: -1}
	}
	value, err := parsePlaintext(plaintexts[0])
	return value, len(plaintexts[0]), err
}

// sanitizeDecodeError returns a fixed message for each format category, and "internal" for anything else.
func sanitizeDecodeError(err error) *CodecError {
	var decodeError *dex.PayloadDecodeError
	if !errors.As(err, &decodeError) {
		return &CodecError{Category: CategoryInternal, Message: "codec failure", Index: -1}
	}
	message, known := decodeErrorMessages[decodeError.Category]
	if !known {
		return &CodecError{Category: CategoryInternal, Message: "codec failure", Index: -1}
	}
	codecError := &CodecError{Category: string(decodeError.Category), Message: message, Index: -1}
	if decodeError.Category == dex.PayloadErrorUnknownKey && validateKeyID(decodeError.KeyID) == nil {
		codecError.KeyID = decodeError.KeyID
		codecError.Message = fmt.Sprintf("key id %q is unknown", decodeError.KeyID)
	}
	return codecError
}

// publicErrorMessage returns the only text that a codec server response may carry for codecError.
// It returns false for a category outside the format, which callers report as internal.
func publicErrorMessage(codecError *CodecError) (string, bool) {
	message, known := decodeErrorMessages[dex.PayloadErrorCategory(codecError.Category)]
	if !known {
		return "", false
	}
	if codecError.Category == string(dex.PayloadErrorUnknownKey) && validateKeyID(codecError.KeyID) == nil {
		message = fmt.Sprintf("key id %q is unknown", codecError.KeyID)
	}
	return message, true
}
