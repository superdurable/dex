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
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const (
	aesGCMPayloadKeyBytes   = 32
	aesGCMPayloadNonceBytes = 12
	aesGCMPayloadTagBytes   = 16
)

// AESGCMPayloadCodecOptions configures NewAESGCMPayloadCodec.
//
// The zero value encrypts without compression and uses the default size limits.
//
// Since 1.45.16.
type AESGCMPayloadCodecOptions struct {
	// Compression is applied to each serialized value before encryption.
	// Default dexpb.PayloadCompression_PAYLOAD_COMPRESSION_NONE does not compress.
	// dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP gzips every value first.
	// Other values make NewAESGCMPayloadCodec return an error. Decoding follows each envelope's
	// own compression field, whatever this option says.
	//
	// Do not enable compression for values that mix attacker-controlled content with secrets.
	// The ciphertext length then reveals how well the content compresses, which lets an attacker
	// who controls part of a value and observes stored sizes guess the secret part, as in the
	// CRIME and BREACH attacks.
	Compression dexpb.PayloadCompression
	// MaxDecompressedBytes limits the decrypted, and decompressed, plaintext of one value, in
	// bytes. Zero uses 67,108,864 (64 MiB). Negative values are invalid. Decoding stops
	// decompressing as soon as the output exceeds it, which stops gzip bombs, and encoding fails
	// for a larger plaintext. When a Client or Worker calls the codec, decoding also stops at that
	// Client's or Worker's PayloadEncryptionOptions.MaxPlaintextBytes when it is smaller.
	MaxDecompressedBytes int
	// MaxDataBytes limits PayloadEnvelope.data, the nonce, ciphertext, and tag of one value, in
	// bytes. Zero uses 16,842,752, the default PayloadEncryptionOptions.MaxEnvelopeBytes.
	// Negative values are invalid. Decoding rejects larger data before decrypting it, and encoding
	// fails instead of writing it. Keep it equal to PayloadEncryptionOptions.MaxEnvelopeBytes.
	MaxDataBytes int
}

// AESGCMPayloadCodec is the built-in PayloadCodec. It encrypts with AES-256-GCM.
//
// Each encryption uses a fresh 12-byte nonce from crypto/rand, so encrypting the same value twice
// gives different ciphertexts. The envelope header (cipher, compression, and key ID) is
// authenticated with the ciphertext, so the server cannot change it without decryption failing.
// The codec reads keys from a PayloadEncryptionKeyProvider: CurrentKeyID for each Encode batch,
// and Key for each key ID that Decode meets. Following NIST SP 800-38D, encrypt at most 2^32
// values with one key, and rotate keys regularly.
//
// Create it with NewAESGCMPayloadCodec. It is safe for concurrent use.
//
//	keys, err := dex.NewFilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
//	if err != nil {
//		return err
//	}
//	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
//	if err != nil {
//		return err
//	}
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
//		PayloadEncryption: dex.PayloadEncryptionOptions{Codec: codec},
//	})
//
// Since 1.45.16.
type AESGCMPayloadCodec struct {
	keyProvider          PayloadEncryptionKeyProvider
	compression          dexpb.PayloadCompression
	maxDecompressedBytes int
	maxDataBytes         int
}

// NewAESGCMPayloadCodec creates the built-in AES-256-GCM PayloadCodec.
//
// keyProvider supplies the current key ID and the keys; it must not be nil. options selects
// compression and size limits; see AESGCMPayloadCodecOptions. The constructor performs no I/O and
// does not call keyProvider.
//
// Returns the codec, or an error when keyProvider is nil, Compression is unknown, or a limit is
// negative.
//
// Since 1.45.16.
func NewAESGCMPayloadCodec(
	keyProvider PayloadEncryptionKeyProvider,
	options AESGCMPayloadCodecOptions,
) (*AESGCMPayloadCodec, error) {
	if nilInterface(keyProvider) {
		return nil, fmt.Errorf("dex: AESGCMPayloadCodec requires a key provider")
	}
	if !isKnownPayloadCompression(options.Compression) {
		return nil, fmt.Errorf("dex: unsupported payload compression %d", options.Compression)
	}
	if options.MaxDecompressedBytes < 0 || options.MaxDataBytes < 0 {
		return nil, fmt.Errorf("dex: AESGCMPayloadCodec limits must not be negative")
	}
	codec := &AESGCMPayloadCodec{
		keyProvider:          keyProvider,
		compression:          options.Compression,
		maxDecompressedBytes: options.MaxDecompressedBytes,
		maxDataBytes:         options.MaxDataBytes,
	}
	if codec.maxDecompressedBytes == 0 {
		codec.maxDecompressedBytes = defaultMaxPayloadPlaintextBytes
	}
	if codec.maxDataBytes == 0 {
		codec.maxDataBytes = defaultMaxPayloadEnvelopeBytes
	}
	return codec, nil
}

// Encode encrypts each plaintext with the key that CurrentKeyID returns for payloadContext.
//
// It returns one envelope per plaintext, in order, each with cipher
// PAYLOAD_CIPHER_AES256_GCM, the key ID, and data of nonce, ciphertext, and tag. It returns an
// error, and no envelopes, when the key provider fails, returns an invalid key ID or a key that
// is not 32 bytes, or a plaintext or its ciphertext exceeds a size limit.
func (codec *AESGCMPayloadCodec) Encode(
	ctx context.Context,
	payloadContext PayloadContext,
	plaintexts [][]byte,
) ([]*dexpb.PayloadEnvelope, error) {
	if len(plaintexts) == 0 {
		return []*dexpb.PayloadEnvelope{}, nil
	}
	keyID, err := codec.keyProvider.CurrentKeyID(ctx, payloadContext)
	if err != nil {
		return nil, &payloadKeyProviderError{reason: "the key provider's CurrentKeyID failed", cause: err}
	}
	if err := validatePayloadKeyID(keyID); err != nil {
		return nil, fmt.Errorf("dex: payload key provider returned an invalid current key ID: %w", err)
	}
	aead, err := codec.newAEAD(ctx, payloadContext, keyID)
	if err != nil {
		var decodeErr *PayloadDecodeError
		if errors.As(err, &decodeErr) {
			return nil, &payloadKeyProviderError{
				reason: fmt.Sprintf("the key provider returned no usable key for the current key id %q", keyID),
				cause:  decodeErr.cause,
			}
		}
		return nil, err
	}
	additionalData := payloadAdditionalAuthenticatedData(
		dexpb.PayloadCipher_PAYLOAD_CIPHER_AES256_GCM,
		codec.compression,
		keyID,
	)
	envelopes := make([]*dexpb.PayloadEnvelope, 0, len(plaintexts))
	for index, plaintext := range plaintexts {
		if len(plaintext) > codec.maxDecompressedBytes {
			return nil, fmt.Errorf(
				"dex: payload plaintext %d has %d bytes, more than the %d-byte limit",
				index, len(plaintext), codec.maxDecompressedBytes,
			)
		}
		sealed := plaintext
		if codec.compression == dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP {
			sealed, err = gzipPayload(plaintext)
			if err != nil {
				return nil, err
			}
		}
		dataLength := aesGCMPayloadNonceBytes + len(sealed) + aesGCMPayloadTagBytes
		if dataLength > codec.maxDataBytes {
			return nil, fmt.Errorf(
				"dex: payload ciphertext %d has %d bytes, more than the %d-byte limit",
				index, dataLength, codec.maxDataBytes,
			)
		}
		data := make([]byte, aesGCMPayloadNonceBytes, dataLength)
		if _, err := rand.Read(data); err != nil {
			return nil, fmt.Errorf("dex: generate payload nonce: %w", err)
		}
		data = aead.Seal(data, data[:aesGCMPayloadNonceBytes], sealed, additionalData)
		envelopes = append(envelopes, &dexpb.PayloadEnvelope{
			Cipher:      dexpb.PayloadCipher_PAYLOAD_CIPHER_AES256_GCM,
			KeyId:       keyID,
			Data:        data,
			Compression: codec.compression,
		})
	}
	return envelopes, nil
}

// Decode decrypts each envelope and returns one plaintext per envelope, in order.
//
// It checks, in the specification's order, the key ID format, metadata limits, cipher, empty
// metadata, compression, key availability, data size, authentication, and plaintext size. The
// first failure returns a *PayloadDecodeError for the whole batch. The key provider may refuse a
// key ID for payloadContext; that returns PayloadErrorUnknownKey.
func (codec *AESGCMPayloadCodec) Decode(
	ctx context.Context,
	payloadContext PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
) ([][]byte, error) {
	plaintexts := make([][]byte, 0, len(envelopes))
	aeads := make(map[string]cipher.AEAD)
	for _, envelope := range envelopes {
		plaintext, err := codec.decodeEnvelope(ctx, payloadContext, envelope, aeads)
		if err != nil {
			return nil, err
		}
		plaintexts = append(plaintexts, plaintext)
	}
	return plaintexts, nil
}

func (codec *AESGCMPayloadCodec) decodeEnvelope(
	ctx context.Context,
	payloadContext PayloadContext,
	envelope *dexpb.PayloadEnvelope,
	aeads map[string]cipher.AEAD,
) ([]byte, error) {
	if envelope == nil {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, "", "envelope is nil")
	}
	if err := validatePayloadEnvelopeHeader(envelope); err != nil {
		return nil, err
	}
	keyID := envelope.GetKeyId()
	if envelope.GetCipher() != dexpb.PayloadCipher_PAYLOAD_CIPHER_AES256_GCM {
		return nil, newPayloadDecodeError(PayloadErrorUnsupportedCipher, keyID, "cipher %d", envelope.GetCipher())
	}
	if len(envelope.GetMetadata()) > 0 {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, keyID, "AES-256-GCM envelopes carry no metadata")
	}
	compression := envelope.GetCompression()
	if !isKnownPayloadCompression(compression) {
		return nil, newPayloadDecodeError(PayloadErrorUnsupportedCompression, keyID, "compression %d", compression)
	}
	aead, found := aeads[keyID]
	if !found {
		var err error
		aead, err = codec.newAEAD(ctx, payloadContext, keyID)
		if err != nil {
			return nil, err
		}
		aeads[keyID] = aead
	}
	data := envelope.GetData()
	if len(data) < aesGCMPayloadNonceBytes+aesGCMPayloadTagBytes {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, keyID, "data has %d bytes", len(data))
	}
	if len(data) > codec.maxDataBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorLimitExceeded, keyID, "data has %d bytes, more than %d", len(data), codec.maxDataBytes,
		)
	}
	plaintext, err := aead.Open(
		nil,
		data[:aesGCMPayloadNonceBytes],
		data[aesGCMPayloadNonceBytes:],
		payloadAdditionalAuthenticatedData(envelope.GetCipher(), compression, keyID),
	)
	if err != nil {
		return nil, newPayloadDecodeError(PayloadErrorAuthenticationFailed, keyID, "AES-256-GCM authentication failed")
	}
	maxPlaintextBytes := codec.maxDecompressedBytes
	if callerLimit, found := PayloadPlaintextLimitFromContext(ctx); found && callerLimit < maxPlaintextBytes {
		maxPlaintextBytes = callerLimit
	}
	if compression == dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP {
		return gunzipPayload(plaintext, maxPlaintextBytes, keyID)
	}
	if len(plaintext) > maxPlaintextBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorLimitExceeded, keyID, "plaintext exceeds %d bytes", maxPlaintextBytes,
		)
	}
	return plaintext, nil
}

// payloadPlaintextLimitContextKey carries PayloadEncryptionOptions.MaxPlaintextBytes to codecs,
// so they never decompress more than the Client or Worker accepts.
type payloadPlaintextLimitContextKey struct{}

func withPayloadPlaintextLimit(ctx context.Context, maxPlaintextBytes int) context.Context {
	return context.WithValue(ctx, payloadPlaintextLimitContextKey{}, maxPlaintextBytes)
}

// PayloadPlaintextLimitFromContext returns the largest plaintext, in bytes, that the calling
// Client or Worker accepts from one PayloadCodec.Decode envelope.
//
// The SDK sets it on the context of every Decode call to PayloadEncryptionOptions.MaxPlaintextBytes.
// A custom codec that decompresses must stop as soon as its output exceeds this limit and return a
// *PayloadDecodeError with PayloadErrorLimitExceeded. It must never decompress the whole input
// first, or a small malicious envelope can exhaust memory. The SDK also rejects a larger returned
// plaintext, but only after the codec has allocated it.
//
// See the PayloadPlaintextLimitFromContext example for a complete custom codec that compresses
// with gzip and stops decompressing at the limit.
//
// Returns the limit and true inside a Decode call made by a Client or Worker, and 0 and false
// otherwise, for example when an application calls Decode directly.
//
// Since 1.45.16.
func PayloadPlaintextLimitFromContext(ctx context.Context) (int, bool) {
	limit, found := ctx.Value(payloadPlaintextLimitContextKey{}).(int)
	return limit, found
}

// payloadKeyProviderError reports an encode-time key provider failure without the provider's text.
type payloadKeyProviderError struct {
	reason string
	cause  error
}

func (providerError *payloadKeyProviderError) Error() string {
	return "dex: payload encoding failed: " + providerError.reason
}

// Unwrap returns the key provider's error; Error never includes its text.
func (providerError *payloadKeyProviderError) Unwrap() error {
	return providerError.cause
}

// newAEAD returns AES-256-GCM for keyID, or a PayloadErrorUnknownKey error when the provider fails.
func (codec *AESGCMPayloadCodec) newAEAD(
	ctx context.Context,
	payloadContext PayloadContext,
	keyID string,
) (cipher.AEAD, error) {
	key, err := codec.keyProvider.Key(ctx, payloadContext, keyID)
	if err != nil {
		return nil, &PayloadDecodeError{
			Category: PayloadErrorUnknownKey,
			KeyID:    keyID,
			reason:   "the key provider has no key for this key ID",
			cause:    err,
		}
	}
	if len(key) != aesGCMPayloadKeyBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorUnknownKey, keyID, "the key provider returned %d key bytes instead of 32", len(key),
		)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("dex: create AES cipher for key id %q", keyID)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("dex: create AES-GCM for key id %q", keyID)
	}
	return aead, nil
}

func gzipPayload(plaintext []byte) ([]byte, error) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(plaintext); err != nil {
		return nil, fmt.Errorf("dex: gzip payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("dex: gzip payload: %w", err)
	}
	return compressed.Bytes(), nil
}

// gunzipPayload stops reading once the output exceeds maxBytes, so a gzip bomb cannot exhaust memory.
func gunzipPayload(compressed []byte, maxBytes int, keyID string) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, keyID, "plaintext is not valid gzip")
	}
	plaintext, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, keyID, "plaintext is not valid gzip")
	}
	if len(plaintext) > maxBytes {
		return nil, newPayloadDecodeError(PayloadErrorLimitExceeded, keyID, "decompressed plaintext exceeds %d bytes", maxBytes)
	}
	return plaintext, nil
}

// payloadAdditionalAuthenticatedData returns "dex/1" 0x00 cipher 0x00 compression 0x00 key_id.
func payloadAdditionalAuthenticatedData(
	payloadCipher dexpb.PayloadCipher,
	compression dexpb.PayloadCompression,
	keyID string,
) []byte {
	additionalData := make([]byte, 0, len(payloadEnvelopeMarker)+len(keyID)+16)
	additionalData = append(additionalData, payloadEnvelopeMarker...)
	additionalData = append(additionalData, 0x00)
	additionalData = strconv.AppendInt(additionalData, int64(payloadCipher), 10)
	additionalData = append(additionalData, 0x00)
	additionalData = strconv.AppendInt(additionalData, int64(compression), 10)
	additionalData = append(additionalData, 0x00)
	return append(additionalData, keyID...)
}

func isKnownPayloadCompression(compression dexpb.PayloadCompression) bool {
	return compression == dexpb.PayloadCompression_PAYLOAD_COMPRESSION_NONE ||
		compression == dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP
}

// FilePayloadEncryptionKeyProvider is a PayloadEncryptionKeyProvider that reads keys from a JSON
// key file.
//
// The file uses the key file format shared by every Dex SDK, dexcli, and the example codec
// servers:
//
//	{
//	  "current_key_id": "k2",
//	  "keys": {
//	    "k1": "<standard base64 of 32 bytes>",
//	    "k2": "<standard base64 of 32 bytes>"
//	  }
//	}
//
// current_key_id names the key that new encryptions use. keys maps each key ID to its 32-byte key
// in standard padded base64. To rotate, add a new key, point current_key_id at it, and restart the
// Clients and Workers; keep the old keys so values written with them stay readable. Key ignores
// the PayloadContext, so values cloned into another Flow ID still decrypt.
//
// The provider reads the file once, when it is created, and keeps the keys in memory. It is safe
// for concurrent use. Protect the file like any secret: write it with mode 0600. Create one with
// `dexcli codec-server keygen` where available.
//
//	keys, err := dex.NewFilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
//	if err != nil {
//		return err
//	}
//	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
//
// Since 1.45.16.
type FilePayloadEncryptionKeyProvider struct {
	currentKeyID string
	keys         map[string][]byte
}

// NewFilePayloadEncryptionKeyProvider reads the key file at path.
//
// It logs a warning through slog.Default when other users can read the file. Returns the
// provider, or an error when the file cannot be read, is not valid JSON, names an invalid key ID,
// holds a key that is not 32 bytes of standard padded base64, or names a current_key_id that is
// not in keys. Errors never contain key bytes.
//
// Since 1.45.16.
func NewFilePayloadEncryptionKeyProvider(path string) (*FilePayloadEncryptionKeyProvider, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dex: read payload key file: %w", err)
	}
	warnWhenPayloadKeyFileIsShared(path)
	var keyFile struct {
		CurrentKeyID string            `json:"current_key_id"`
		Keys         map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(content, &keyFile); err != nil {
		return nil, fmt.Errorf("dex: payload key file %q is not valid JSON", path)
	}
	keys := make(map[string][]byte, len(keyFile.Keys))
	for keyID, encoded := range keyFile.Keys {
		if err := validatePayloadKeyID(keyID); err != nil {
			return nil, fmt.Errorf("dex: payload key file %q: %w", path, err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(key) != encoded {
			return nil, fmt.Errorf("dex: payload key file %q: key %q is not standard padded base64", path, keyID)
		}
		if len(key) != aesGCMPayloadKeyBytes {
			return nil, fmt.Errorf("dex: payload key file %q: key %q has %d bytes instead of 32", path, keyID, len(key))
		}
		keys[keyID] = key
	}
	if _, found := keys[keyFile.CurrentKeyID]; !found {
		return nil, fmt.Errorf("dex: payload key file %q: current_key_id %q is not in keys", path, keyFile.CurrentKeyID)
	}
	return &FilePayloadEncryptionKeyProvider{currentKeyID: keyFile.CurrentKeyID, keys: keys}, nil
}

// CurrentKeyID returns the file's current_key_id for every PayloadContext.
func (provider *FilePayloadEncryptionKeyProvider) CurrentKeyID(context.Context, PayloadContext) (string, error) {
	return provider.currentKeyID, nil
}

// Key returns a copy of the key for keyID, for every PayloadContext.
// It returns an error when the file has no such key.
func (provider *FilePayloadEncryptionKeyProvider) Key(_ context.Context, _ PayloadContext, keyID string) ([]byte, error) {
	key, found := provider.keys[keyID]
	if !found {
		return nil, errors.New("key id is not in the payload key file")
	}
	return bytes.Clone(key), nil
}

func warnWhenPayloadKeyFileIsShared(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}
	slog.Default().Warn(
		"Dex payload key file is readable by other users; restrict it with chmod 600",
		"path", path,
		"mode", info.Mode().Perm().String(),
	)
}
