// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const gzipPayloadCodecFallbackLimit = 64 << 20

// gzipAESPayloadCodec is a custom PayloadCodec: gzip, then AES-256-GCM with one key.
// The AAD binds the cipher and key ID, as the built-in codec does.
type gzipAESPayloadCodec struct {
	keyID string
	aead  cipher.AEAD
}

func newGzipAESPayloadCodec(keyID string, key []byte) (*gzipAESPayloadCodec, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &gzipAESPayloadCodec{keyID: keyID, aead: aead}, nil
}

func (codec *gzipAESPayloadCodec) Encode(
	_ context.Context,
	_ dex.PayloadContext,
	plaintexts [][]byte,
) ([]*dexpb.PayloadEnvelope, error) {
	envelopes := make([]*dexpb.PayloadEnvelope, 0, len(plaintexts))
	for _, plaintext := range plaintexts {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(plaintext); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		nonce := make([]byte, codec.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, &dexpb.PayloadEnvelope{
			Cipher: dexpb.PayloadCipher_PAYLOAD_CIPHER_CUSTOM,
			KeyId:  codec.keyID,
			Data:   codec.aead.Seal(nonce, nonce, compressed.Bytes(), codec.additionalData()),
		})
	}
	return envelopes, nil
}

func (codec *gzipAESPayloadCodec) Decode(
	ctx context.Context,
	_ dex.PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
) ([][]byte, error) {
	limit, found := dex.PayloadPlaintextLimitFromContext(ctx)
	if !found {
		limit = gzipPayloadCodecFallbackLimit
	}
	plaintexts := make([][]byte, 0, len(envelopes))
	for _, envelope := range envelopes {
		plaintext, err := codec.decodeEnvelope(envelope, limit)
		if err != nil {
			return nil, err
		}
		plaintexts = append(plaintexts, plaintext)
	}
	return plaintexts, nil
}

func (codec *gzipAESPayloadCodec) decodeEnvelope(envelope *dexpb.PayloadEnvelope, limit int) ([]byte, error) {
	if envelope.GetCipher() != dexpb.PayloadCipher_PAYLOAD_CIPHER_CUSTOM || len(envelope.GetMetadata()) > 0 {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorUnsupportedCipher}
	}
	if envelope.GetKeyId() != codec.keyID {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorUnknownKey, KeyID: envelope.GetKeyId()}
	}
	data := envelope.GetData()
	if len(data) < codec.aead.NonceSize()+codec.aead.Overhead() {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorMalformed}
	}
	nonceSize := codec.aead.NonceSize()
	compressed, err := codec.aead.Open(nil, data[:nonceSize], data[nonceSize:], codec.additionalData())
	if err != nil {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorAuthenticationFailed}
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorMalformed}
	}
	defer reader.Close()
	// Read one byte past the limit, so an oversized plaintext is detected without reading it all.
	plaintext, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorMalformed}
	}
	if len(plaintext) > limit {
		return nil, &dex.PayloadDecodeError{Category: dex.PayloadErrorLimitExceeded}
	}
	return plaintext, nil
}

func (codec *gzipAESPayloadCodec) additionalData() []byte {
	return []byte("gzip-aes-example\x00" + codec.keyID)
}

// A custom codec that decompresses reads the Client's or Worker's plaintext limit from the
// Decode context and stops decompressing once its output exceeds it.
func ExamplePayloadPlaintextLimitFromContext() {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		fmt.Println(err)
		return
	}
	codec, err := newGzipAESPayloadCodec("example-key-1", key)
	if err != nil {
		fmt.Println(err)
		return
	}
	envelopes, err := codec.Encode(context.Background(), dex.PayloadContext{FlowID: "order-1"}, [][]byte{[]byte("hello")})
	if err != nil {
		fmt.Println(err)
		return
	}
	// Outside a Client or Worker call there is no limit, so the codec uses its fallback.
	plaintexts, err := codec.Decode(context.Background(), dex.PayloadContext{FlowID: "order-1"}, envelopes)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(plaintexts[0]))
	options := dex.PayloadEncryptionOptions{Codec: codec, MaxPlaintextBytes: 1 << 20}
	fmt.Println(options.MaxPlaintextBytes)
	// Output:
	// hello
	// 1048576
}
