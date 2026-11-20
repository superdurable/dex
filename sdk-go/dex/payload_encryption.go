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
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// ErrPayloadCodecNotConfigured reports an encrypted value or error text read by a Client or Worker
// that has no PayloadCodec.
//
// A Flow written by an application with payload encryption stores its values as encrypted
// envelopes. A Client or Worker without PayloadEncryptionOptions.Codec cannot read them and returns
// an error that matches ErrPayloadCodecNotConfigured with errors.Is. The error text never contains
// any part of the encrypted value.
//
// Configure the same codec and keys on every Client and Worker that reads the Flow:
//
//	_, err := client.WaitForFlow(ctx, "order-42", dex.WaitForFlowOptions{})
//	if errors.Is(err, dex.ErrPayloadCodecNotConfigured) {
//		return fmt.Errorf("configure ClientOptions.PayloadEncryption: %w", err)
//	}
//
// Since 1.45.16.
var ErrPayloadCodecNotConfigured = errors.New("value is encrypted; configure a PayloadCodec")

// PayloadContext identifies the Flow that stores the payloads of one PayloadCodec call.
//
// A PayloadEncryptionKeyProvider uses it to choose keys, for example one key per tenant. The SDK
// passes the context of the Flow that stores the value:
//   - Step, RPC, Attribute, Channel, Stream, and Step-execution local values use their own Flow.
//   - SubFlow input and SubFlow initial Attributes use the child Flow. Its Flow ID is the
//     deterministic SubFlow ID "SubFlow:{parentFlowID}-{stepExecutionID}-{index}", which
//     SubFlowID also returns, so a tenant-aware key provider must handle the "SubFlow:" prefix.
//
// FlowType may be empty. It is empty when the SDK knows only the Flow ID: Client calls that name a
// Flow by its Flow ID, such as StopFlow, TimeTravel, WaitForFlow, GetAttributes, and
// WaitForAttributeMatch, and SubFlow results that a parent Execute reads. So the same value can be
// encrypted with a FlowType and decrypted without one. Select and validate keys by FlowID, and use
// FlowType only as an optional hint.
//
// The context is not authenticated with the ciphertext. Clone, fork, and Time Travel copy values
// into another run or Flow ID, and those values must still decrypt there. A multi-tenant key
// provider should therefore refuse, in Key, a key ID that does not belong to the context's tenant.
//
//	func (provider tenantKeyProvider) Key(ctx context.Context, payloadContext dex.PayloadContext, keyID string) ([]byte, error) {
//		tenant := tenantOfFlowID(strings.TrimPrefix(payloadContext.FlowID, "SubFlow:"))
//		if !strings.HasPrefix(keyID, tenant+"-") {
//			return nil, fmt.Errorf("key id %q does not belong to tenant %q", keyID, tenant)
//		}
//		return provider.keys[keyID], nil
//	}
//
// Since 1.45.16.
type PayloadContext struct {
	// FlowType is the Flow type that stores the payloads, or empty when the SDK does not know it.
	FlowType string
	// FlowID is the Flow ID that stores the payloads. For SubFlow input it is the child's Flow ID.
	FlowID string
}

// PayloadCodec encrypts serialized Values into PayloadEnvelope messages and decrypts them back.
//
// Configure one codec with PayloadEncryptionOptions.Codec on ClientOptions and WorkerOptions.
// AESGCMPayloadCodec is the built-in implementation. Implement PayloadCodec yourself to call a key
// management service, for example; such a codec sets PayloadEnvelope.cipher to
// dexpb.PayloadCipher_PAYLOAD_CIPHER_CUSTOM.
//
// The SDK calls the codec in batches, one call for all values of one PayloadContext in a request
// or response, so a remote codec can make one network call for many values:
//   - Encode receives plaintexts, each the protobuf serialization of one dex Value, and returns
//     exactly one envelope per plaintext, in order.
//   - Decode receives envelopes and returns exactly one plaintext per envelope, in order.
//   - Any failure fails the whole batch. Return a *PayloadDecodeError from Decode to report a
//     specification error category.
//   - A codec that decompresses must stop as soon as its output exceeds
//     PayloadPlaintextLimitFromContext(ctx). The SDK rejects a larger plaintext after Decode
//     returns, but only the codec can stop the allocation.
//
// The SDK never puts a codec error's own text into the errors it returns, its logs, or a Worker
// failure sent to the server, because that text could reveal key-management details. For a
// *PayloadDecodeError from Decode, the SDK keeps only a Category that this package defines, and
// a KeyID only when it equals the key ID of an envelope in the batch; Encode errors keep no
// KeyID. Any other codec failure is reported as a fixed "codec failed" error. These rules apply to
// AESGCMPayloadCodec too, and the SDK always returns a new error value. errors.As and errors.Unwrap still reach the codec's
// original error, so application code can inspect it; log it only where that is safe.
//
// The SDK validates each envelope's marker, size, key ID, and metadata limits before Decode, and
// checks each returned plaintext's size and kind afterwards. A custom codec must authenticate
// every metadata entry it relies on, or reject metadata it does not authenticate, because nothing
// else protects it. Errors must never contain plaintext or key bytes. A codec must be safe for
// concurrent use; Workers decode and encode for many handlers at once.
//
//	type kmsPayloadCodec struct{ client *kms.Client }
//
//	func (codec kmsPayloadCodec) Encode(ctx context.Context, payloadContext dex.PayloadContext, plaintexts [][]byte) ([]*dexpb.PayloadEnvelope, error) {
//		envelopes := make([]*dexpb.PayloadEnvelope, 0, len(plaintexts))
//		for _, plaintext := range plaintexts {
//			keyID, data, err := codec.client.Encrypt(ctx, payloadContext.FlowID, plaintext)
//			if err != nil {
//				return nil, err
//			}
//			envelopes = append(envelopes, &dexpb.PayloadEnvelope{
//				Cipher: dexpb.PayloadCipher_PAYLOAD_CIPHER_CUSTOM,
//				KeyId:  keyID,
//				Data:   data,
//			})
//		}
//		return envelopes, nil
//	}
//
// See protos/payload-encryption.md in the Dex repository for the envelope format.
//
// Since 1.45.16.
type PayloadCodec interface {
	// Encode returns one envelope per plaintext, in order. ctx carries the calling request's
	// deadline and cancellation. payloadContext identifies the Flow that will store the values.
	Encode(ctx context.Context, payloadContext PayloadContext, plaintexts [][]byte) ([]*dexpb.PayloadEnvelope, error)
	// Decode returns one plaintext per envelope, in order. ctx carries the calling request's
	// deadline and cancellation, and the plaintext limit that PayloadPlaintextLimitFromContext
	// returns; stop decompressing once the output exceeds it. payloadContext identifies the Flow
	// that stores the values.
	Decode(ctx context.Context, payloadContext PayloadContext, envelopes []*dexpb.PayloadEnvelope) ([][]byte, error)
}

// PayloadEncryptionKeyProvider supplies keys to AESGCMPayloadCodec.
//
// CurrentKeyID chooses the key for new encryptions. Key returns the 32-byte AES-256 key for a key
// ID that an envelope names. FilePayloadEncryptionKeyProvider reads keys from a JSON file.
// Implement this interface to read keys from a secret manager, or to choose keys per tenant.
//
// Keep old keys available after rotation: envelopes written with them name their key ID and stay
// decodable only while Key returns that key. Key IDs are visible in clear in every envelope, so
// they must not contain sensitive data. A key ID has 1 to 64 bytes, each printable ASCII from
// 0x21 to 0x7e. Implementations must be safe for concurrent use and must never log keys.
//
//	type secretManagerKeyProvider struct{ secrets *secrets.Client }
//
//	func (provider secretManagerKeyProvider) CurrentKeyID(ctx context.Context, payloadContext dex.PayloadContext) (string, error) {
//		return provider.secrets.Current(ctx, "dex-payload-key")
//	}
//
//	func (provider secretManagerKeyProvider) Key(ctx context.Context, payloadContext dex.PayloadContext, keyID string) ([]byte, error) {
//		return provider.secrets.Version(ctx, "dex-payload-key", keyID)
//	}
//
// Since 1.45.16.
type PayloadEncryptionKeyProvider interface {
	// CurrentKeyID returns the key ID that new encryptions for payloadContext use.
	CurrentKeyID(ctx context.Context, payloadContext PayloadContext) (string, error)
	// Key returns the 32-byte key for keyID. A provider may refuse a key ID that does not belong
	// to payloadContext; decoding then fails with PayloadErrorUnknownKey instead of returning data.
	Key(ctx context.Context, payloadContext PayloadContext, keyID string) ([]byte, error)
}

// PayloadEncryptionOptions configures client-side payload encryption for a Client or Worker.
//
// Set it as ClientOptions.PayloadEncryption and WorkerOptions.PayloadEncryption. The zero value
// turns encryption off. With a Codec, the SDK encrypts every Value it sends and decrypts every
// encrypted Value it receives, so the Dex server stores only ciphertext. The Plaintext fields are
// opt-outs: their zero value means "encrypt".
//
// These values are never encrypted:
//   - Attribute writes of Indexed Attributes, because the server indexes them, and of Attributes
//     declared with Plaintext.
//   - Null values, which delete Attributes, and the operands of WaitForAttributeMatch and Action
//     conditions, which the server compares.
//   - Flow types, Step types, names, IDs, configuration, and other fields that are not values.
//
// With a Codec, WaitForAttributeMatch, WaitForAttributeMapInstanceMatch, and Actions require their
// Attribute to stay plaintext: Indexed, declared with Plaintext, or of a kind these options leave
// in clear. Otherwise they return an error, and NewWorker rejects such an Action.
//
// Every Client and Worker that reads an encrypted Flow needs the same codec and keys. Roll out in
// two steps: first configure the codec everywhere, then let writers start encrypting. A reader
// decodes only values marked as encrypted, so plaintext and encrypted Flows can share one Worker.
// The Java and Rust SDKs cannot read encrypted Flows yet.
//
//	keys, err := dex.NewFilePayloadEncryptionKeyProvider("/etc/dex/payload-keys.json")
//	if err != nil {
//		return err
//	}
//	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
//	if err != nil {
//		return err
//	}
//	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
//		PayloadEncryption: dex.PayloadEncryptionOptions{Codec: codec},
//	})
//
// Since 1.45.16.
type PayloadEncryptionOptions struct {
	// Codec encrypts and decrypts values. Nil turns encryption off; encrypted values that the
	// Client or Worker reads then fail with ErrPayloadCodecNotConfigured.
	Codec PayloadCodec
	// PlaintextStringValues sends string values in clear. Default false encrypts them.
	PlaintextStringValues bool
	// PlaintextPrimitiveValues sends integer, floating-point, and Boolean values in clear.
	// Default false encrypts them.
	PlaintextPrimitiveValues bool
	// PlaintextErrorText sends Worker error details and stack traces, Stop, Time Travel, and
	// clone-fork reasons, and ForceFail reasons in clear. Default false encrypts them in the
	// "dex-encoded:" text format. Text that itself starts with "dex-encoded:" is encrypted even
	// when this is true.
	PlaintextErrorText bool
	// MaxEnvelopeBytes limits the serialized envelope of one encrypted value, in bytes. Zero uses
	// 16,842,752 (16 MiB plus 64 KiB), which fits the server's default maximum value size.
	// Raise it, together with AESGCMPayloadCodecOptions.MaxDataBytes, for a server configured
	// with a larger maximum value size. Negative values are invalid. Decoding rejects a larger
	// envelope before parsing it, and encoding fails instead of writing one.
	MaxEnvelopeBytes int
	// MaxPlaintextBytes limits the decrypted serialized value, in bytes. Zero uses 67,108,864
	// (64 MiB). Negative values are invalid. Decoding rejects a larger plaintext, and encoding
	// fails instead of encrypting one. The SDK passes it to the codec's Decode, where
	// PayloadPlaintextLimitFromContext returns it. AESGCMPayloadCodec stops decompressing at it,
	// and a custom codec that decompresses must do the same.
	MaxPlaintextBytes int
}

// PayloadErrorCategory classifies a payload decoding failure.
//
// The categories and the order of the checks that produce them are shared by every Dex SDK and
// codec server. Read it from a *PayloadDecodeError with errors.As.
//
// Since 1.45.16.
type PayloadErrorCategory string

const (
	// PayloadErrorMalformed means the envelope or encoded text cannot be parsed or breaks the format.
	PayloadErrorMalformed PayloadErrorCategory = "malformed"
	// PayloadErrorUnsupportedMarker means the value uses an encoding version this SDK cannot read.
	PayloadErrorUnsupportedMarker PayloadErrorCategory = "unsupported_marker"
	// PayloadErrorUnsupportedCipher means the configured codec does not support the envelope's cipher.
	PayloadErrorUnsupportedCipher PayloadErrorCategory = "unsupported_cipher"
	// PayloadErrorUnsupportedCompression means the envelope names an unknown compression.
	PayloadErrorUnsupportedCompression PayloadErrorCategory = "unsupported_compression"
	// PayloadErrorUnknownKey means the key provider has no key for the envelope's key ID, or refused it.
	PayloadErrorUnknownKey PayloadErrorCategory = "unknown_key"
	// PayloadErrorAuthenticationFailed means decryption failed: wrong key, or tampered ciphertext or header.
	PayloadErrorAuthenticationFailed PayloadErrorCategory = "authentication_failed"
	// PayloadErrorLimitExceeded means an envelope, text, ciphertext, or plaintext exceeds a size limit.
	PayloadErrorLimitExceeded PayloadErrorCategory = "limit_exceeded"
	// PayloadErrorInvalidPlaintext means the decrypted bytes are not a value kind that Dex encrypts.
	PayloadErrorInvalidPlaintext PayloadErrorCategory = "invalid_plaintext"
)

// PayloadDecodeError reports a failure to decode an encrypted value or error text.
//
// The SDK never treats an encrypted value that fails to decode as plaintext. The error names the
// category and, when known, the key ID. It never contains payload bytes, plaintext, keys, or the
// text of a key provider error. When the key provider fails or refuses a key ID, Unwrap returns
// the provider's error for application code to inspect; Error never includes it.
//
//	var decodeErr *dex.PayloadDecodeError
//	if errors.As(err, &decodeErr) && decodeErr.Category == dex.PayloadErrorUnknownKey {
//		log.Printf("payload key %q is missing from the key file", decodeErr.KeyID)
//	}
//
// Since 1.45.16.
type PayloadDecodeError struct {
	// Category is the first check that failed.
	Category PayloadErrorCategory
	// KeyID is the envelope's key ID, or empty when the failure happened before it was read.
	KeyID string
	// reason is SDK-written text that never contains payload bytes or provider text.
	reason string
	// cause is the key provider's or codec's error; Error never includes its text.
	cause error
}

// Error returns the category, the key ID when known, and an SDK-written reason.
// It never includes the text of the error that Unwrap returns.
func (decodeError *PayloadDecodeError) Error() string {
	message := fmt.Sprintf("dex: payload decoding failed: %s", decodeError.Category)
	if decodeError.KeyID != "" {
		message += fmt.Sprintf(" (key id %q)", decodeError.KeyID)
	}
	if decodeError.reason != "" {
		message += ": " + decodeError.reason
	}
	return message
}

// Unwrap returns the key provider error that caused a PayloadErrorUnknownKey failure, or nil.
// Its text may contain key-management details, so log it only where that is safe.
func (decodeError *PayloadDecodeError) Unwrap() error {
	return decodeError.cause
}

func newPayloadDecodeError(category PayloadErrorCategory, keyID string, format string, arguments ...any) error {
	return &PayloadDecodeError{Category: category, KeyID: keyID, reason: fmt.Sprintf(format, arguments...)}
}
