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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

const (
	payloadEnvelopeMarker       = "dex/1"
	payloadReservedEncodingRoot = "dex/"
	payloadEncodedTextPrefix    = "dex-encoded:"

	defaultMaxPayloadEnvelopeBytes  = 16*1024*1024 + 64*1024
	defaultMaxPayloadPlaintextBytes = 64 * 1024 * 1024
	maxPayloadKeyIDBytes            = 64
	maxPayloadMetadataEntries       = 16
	maxPayloadMetadataTotalBytes    = 4096
)

// payloadDecodeLimits holds every size limit separately so tests can lower each one alone.
type payloadDecodeLimits struct {
	maxEnvelopeBytes    int
	maxEncodedTextBytes int
	maxPlaintextBytes   int
}

// payloadEncryption encrypts outgoing values and decrypts incoming ones for one Client or Worker.
// A nil codec turns encryption off; decoding still recognizes encrypted values to report them.
type payloadEncryption struct {
	codec                   PayloadCodec
	shouldEncryptStrings    bool
	shouldEncryptPrimitives bool
	shouldEncryptErrorText  bool
	limits                  payloadDecodeLimits
}

// payloadValueSlot is one outgoing value field that the SDK may replace with its envelope.
type payloadValueSlot struct {
	payloadContext   PayloadContext
	valueDescription string
	valuePointer     **dexpb.Value
}

func newPayloadEncryption(options PayloadEncryptionOptions) (*payloadEncryption, error) {
	if options.MaxEnvelopeBytes < 0 || options.MaxPlaintextBytes < 0 {
		return nil, fmt.Errorf("dex: PayloadEncryptionOptions limits must not be negative")
	}
	maxEnvelopeBytes := options.MaxEnvelopeBytes
	if maxEnvelopeBytes == 0 {
		maxEnvelopeBytes = defaultMaxPayloadEnvelopeBytes
	}
	maxPlaintextBytes := options.MaxPlaintextBytes
	if maxPlaintextBytes == 0 {
		maxPlaintextBytes = defaultMaxPayloadPlaintextBytes
	}
	codec := options.Codec
	if nilInterface(codec) {
		codec = nil
	}
	return &payloadEncryption{
		codec:                   codec,
		shouldEncryptStrings:    !options.PlaintextStringValues,
		shouldEncryptPrimitives: !options.PlaintextPrimitiveValues,
		shouldEncryptErrorText:  !options.PlaintextErrorText,
		limits: payloadDecodeLimits{
			maxEnvelopeBytes:    maxEnvelopeBytes,
			maxEncodedTextBytes: 4 * ((maxEnvelopeBytes + 2) / 3),
			maxPlaintextBytes:   maxPlaintextBytes,
		},
	}, nil
}

func newDisabledPayloadEncryption() *payloadEncryption {
	encryption, err := newPayloadEncryption(PayloadEncryptionOptions{})
	if err != nil {
		panic(err)
	}
	return encryption
}

func (encryption *payloadEncryption) isEnabled() bool {
	return encryption.codec != nil
}

// wouldEncryptAttribute reports whether writes of attribute reach the server encrypted.
func (encryption *payloadEncryption) wouldEncryptAttribute(attribute registeredAttribute) bool {
	if !encryption.isEnabled() || attribute.index != nil || attribute.isPlaintext {
		return false
	}
	if attribute.valueType == nil {
		return true
	}
	switch attribute.valueType.Kind() {
	case reflect.String:
		return encryption.shouldEncryptStrings
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return encryption.shouldEncryptPrimitives
	default:
		return true
	}
}

// requirePlaintextAttribute rejects a server-compared attribute that would be stored encrypted.
func (encryption *payloadEncryption) requirePlaintextAttribute(attribute registeredAttribute, usage string) error {
	if !encryption.wouldEncryptAttribute(attribute) {
		return nil
	}
	return fmt.Errorf(
		"dex: %s needs Attribute %q in plaintext, but payload encryption encrypts it; "+
			"declare it with dex.Indexed or dex.Plaintext",
		usage,
		attribute.name,
	)
}

func (encryption *payloadEncryption) shouldEncryptValue(value *dexpb.Value) (bool, error) {
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		return encryption.shouldEncryptStrings, nil
	case *dexpb.Value_IntValue, *dexpb.Value_DoubleValue, *dexpb.Value_BoolValue:
		return encryption.shouldEncryptPrimitives, nil
	case *dexpb.Value_ObjValue:
		if strings.HasPrefix(kind.ObjValue.GetEncoding(), payloadReservedEncodingRoot) {
			return false, &ValueMappingError{
				Operation: "encode",
				Err:       fmt.Errorf("dex: object encoding %q is reserved for Dex", kind.ObjValue.GetEncoding()),
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

// encryptValueSlots replaces each eligible value with its envelope, one codec call per PayloadContext.
// It never mutates the original values, which callers may share.
func (encryption *payloadEncryption) encryptValueSlots(ctx context.Context, slots []payloadValueSlot) error {
	if !encryption.isEnabled() || len(slots) == 0 {
		return nil
	}
	groups := make(map[PayloadContext][]payloadValueSlot)
	contexts := make([]PayloadContext, 0)
	for _, slot := range slots {
		if slot.valuePointer == nil || *slot.valuePointer == nil {
			continue
		}
		shouldEncrypt, err := encryption.shouldEncryptValue(*slot.valuePointer)
		if err != nil {
			return err
		}
		if !shouldEncrypt {
			continue
		}
		if _, found := groups[slot.payloadContext]; !found {
			contexts = append(contexts, slot.payloadContext)
		}
		groups[slot.payloadContext] = append(groups[slot.payloadContext], slot)
	}
	for _, payloadContext := range contexts {
		group := groups[payloadContext]
		values := make([]*dexpb.Value, 0, len(group))
		for _, slot := range group {
			values = append(values, *slot.valuePointer)
		}
		payloads, err := encryption.encryptValues(ctx, payloadContext, values)
		if err != nil {
			return fmt.Errorf("dex: encrypt %s: %w", describePayloadSlots(group), err)
		}
		for index, slot := range group {
			*slot.valuePointer = &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
				Encoding: payloadEnvelopeMarker,
				Payload:  payloads[index],
			}}}
		}
	}
	return nil
}

// encryptValues returns the serialized envelope of each value, in order.
func (encryption *payloadEncryption) encryptValues(
	ctx context.Context,
	payloadContext PayloadContext,
	values []*dexpb.Value,
) ([][]byte, error) {
	plaintexts := make([][]byte, 0, len(values))
	for _, value := range values {
		plaintext, err := proto.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("serialize value: %w", err)
		}
		if len(plaintext) > encryption.limits.maxPlaintextBytes {
			return nil, fmt.Errorf(
				"value has %d serialized bytes, more than the %d-byte MaxPlaintextBytes",
				len(plaintext), encryption.limits.maxPlaintextBytes,
			)
		}
		plaintexts = append(plaintexts, plaintext)
	}
	envelopes, err := encryption.codec.Encode(ctx, payloadContext, plaintexts)
	if err != nil {
		return nil, hidePayloadCodecErrorText("PayloadCodec.Encode", err, nil)
	}
	if len(envelopes) != len(plaintexts) {
		return nil, fmt.Errorf("PayloadCodec.Encode returned %d envelopes for %d values", len(envelopes), len(plaintexts))
	}
	payloads := make([][]byte, 0, len(envelopes))
	for _, envelope := range envelopes {
		if envelope == nil {
			return nil, fmt.Errorf("PayloadCodec.Encode returned a nil envelope")
		}
		if envelope.GetCipher() == dexpb.PayloadCipher_PAYLOAD_CIPHER_UNSPECIFIED {
			return nil, fmt.Errorf("PayloadCodec.Encode returned an envelope without a cipher")
		}
		if err := validatePayloadEnvelopeHeader(envelope); err != nil {
			return nil, fmt.Errorf("PayloadCodec.Encode returned an invalid envelope: %w", err)
		}
		payload, err := proto.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("serialize envelope: %w", err)
		}
		if len(payload) > encryption.limits.maxEnvelopeBytes {
			return nil, fmt.Errorf(
				"envelope has %d bytes, more than the %d-byte MaxEnvelopeBytes",
				len(payload), encryption.limits.maxEnvelopeBytes,
			)
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

// decryptFlowValues replaces each encrypted value with its plaintext Value, after hydration.
// It never mutates the hydrated values, which the hydrator shares between slots.
func (encryption *payloadEncryption) decryptFlowValues(ctx context.Context, targets []flowValuePointer) error {
	type pendingEnvelope struct {
		target   flowValuePointer
		envelope *dexpb.PayloadEnvelope
	}
	groups := make(map[PayloadContext][]pendingEnvelope)
	contexts := make([]PayloadContext, 0)
	for _, target := range targets {
		if target.valuePointer == nil {
			continue
		}
		object := (*target.valuePointer).GetObjValue()
		if object == nil || !strings.HasPrefix(object.GetEncoding(), payloadReservedEncodingRoot) {
			continue
		}
		if !encryption.isEnabled() {
			return fmt.Errorf("dex: %s: %w", describeFlowValue(target), ErrPayloadCodecNotConfigured)
		}
		envelope, err := encryption.parseEnvelopeObject(object)
		if err != nil {
			return fmt.Errorf("dex: decrypt %s: %w", describeFlowValue(target), err)
		}
		payloadContext := target.resolvedPayloadContext()
		if _, found := groups[payloadContext]; !found {
			contexts = append(contexts, payloadContext)
		}
		groups[payloadContext] = append(groups[payloadContext], pendingEnvelope{target: target, envelope: envelope})
	}
	for _, payloadContext := range contexts {
		group := groups[payloadContext]
		envelopes := make([]*dexpb.PayloadEnvelope, 0, len(group))
		for _, pending := range group {
			envelopes = append(envelopes, pending.envelope)
		}
		values, _, err := encryption.decodeEnvelopes(ctx, payloadContext, envelopes, false)
		if err != nil {
			return fmt.Errorf("dex: decrypt values of Flow %q: %w", payloadContext.FlowID, err)
		}
		for index, pending := range group {
			*pending.target.valuePointer = values[index]
		}
	}
	return nil
}

// parseEnvelopeObject runs decode steps 1, 2, and 4 to 6 on an object whose encoding starts with "dex/".
func (encryption *payloadEncryption) parseEnvelopeObject(object *dexpb.EncodedObject) (*dexpb.PayloadEnvelope, error) {
	if object.GetEncoding() != payloadEnvelopeMarker {
		return nil, newPayloadDecodeError(PayloadErrorUnsupportedMarker, "", "encoding %q", object.GetEncoding())
	}
	if len(object.GetPayload()) > encryption.limits.maxEnvelopeBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorLimitExceeded, "", "envelope has %d bytes, more than %d",
			len(object.GetPayload()), encryption.limits.maxEnvelopeBytes,
		)
	}
	return parsePayloadEnvelope(object.GetPayload())
}

// parseEncodedText runs decode steps 2 to 6 on the text after the "dex-encoded:" prefix.
func (encryption *payloadEncryption) parseEncodedText(encodedText string) (*dexpb.PayloadEnvelope, error) {
	if len(encodedText) > encryption.limits.maxEncodedTextBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorLimitExceeded, "", "encoded text has %d bytes, more than %d",
			len(encodedText), encryption.limits.maxEncodedTextBytes,
		)
	}
	envelopeBytes, err := base64.StdEncoding.Strict().DecodeString(encodedText)
	if err != nil || base64.StdEncoding.EncodeToString(envelopeBytes) != encodedText {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, "", "text is not canonical padded base64")
	}
	if len(envelopeBytes) > encryption.limits.maxEnvelopeBytes {
		return nil, newPayloadDecodeError(
			PayloadErrorLimitExceeded, "", "envelope has %d bytes, more than %d",
			len(envelopeBytes), encryption.limits.maxEnvelopeBytes,
		)
	}
	return parsePayloadEnvelope(envelopeBytes)
}

// decodeEnvelopes calls the codec and runs the plaintext size check and decode step 14.
// It returns the Values and the plaintext bytes they were parsed from.
func (encryption *payloadEncryption) decodeEnvelopes(
	ctx context.Context,
	payloadContext PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
	isText bool,
) ([]*dexpb.Value, [][]byte, error) {
	// The codec gets copies and may change them; SDK errors use only the validated original key IDs.
	keyIDs := make([]string, 0, len(envelopes))
	codecEnvelopes := make([]*dexpb.PayloadEnvelope, 0, len(envelopes))
	for _, envelope := range envelopes {
		keyIDs = append(keyIDs, envelope.GetKeyId())
		codecEnvelopes = append(codecEnvelopes, proto.Clone(envelope).(*dexpb.PayloadEnvelope))
	}
	plaintexts, err := encryption.codec.Decode(
		withPayloadPlaintextLimit(ctx, encryption.limits.maxPlaintextBytes), payloadContext, codecEnvelopes,
	)
	if err != nil {
		return nil, nil, hidePayloadCodecErrorText("PayloadCodec.Decode", err, keyIDs)
	}
	if len(plaintexts) != len(keyIDs) {
		return nil, nil, fmt.Errorf("PayloadCodec.Decode returned %d plaintexts for %d envelopes", len(plaintexts), len(keyIDs))
	}
	values := make([]*dexpb.Value, 0, len(plaintexts))
	for index, plaintext := range plaintexts {
		keyID := keyIDs[index]
		if len(plaintext) > encryption.limits.maxPlaintextBytes {
			return nil, nil, newPayloadDecodeError(
				PayloadErrorLimitExceeded, keyID, "plaintext exceeds %d bytes", encryption.limits.maxPlaintextBytes,
			)
		}
		value, err := parsePayloadPlaintext(plaintext, isText, keyID)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
	}
	return values, plaintexts, nil
}

// encodeErrorText returns text in the "dex-encoded:" format when it must be encrypted.
// Without a codec, text that starts with the prefix gets one leading space instead.
func (encryption *payloadEncryption) encodeErrorText(
	ctx context.Context,
	payloadContext PayloadContext,
	text string,
) (string, error) {
	if text == "" {
		return "", nil
	}
	hasReservedPrefix := strings.HasPrefix(text, payloadEncodedTextPrefix)
	if !encryption.isEnabled() || (!encryption.shouldEncryptErrorText && !hasReservedPrefix) {
		if hasReservedPrefix {
			return " " + text, nil
		}
		return text, nil
	}
	payloads, err := encryption.encryptValues(ctx, payloadContext, []*dexpb.Value{
		{Kind: &dexpb.Value_StringValue{StringValue: text}},
	})
	if err != nil {
		return "", fmt.Errorf("dex: encrypt error text: %w", err)
	}
	return payloadEncodedTextPrefix + base64.StdEncoding.EncodeToString(payloads[0]), nil
}

// decodeErrorText returns the plaintext of "dex-encoded:" text and any other text unchanged.
func (encryption *payloadEncryption) decodeErrorText(
	ctx context.Context,
	payloadContext PayloadContext,
	text string,
) (string, error) {
	encodedText, isEncoded := strings.CutPrefix(text, payloadEncodedTextPrefix)
	if !isEncoded {
		return text, nil
	}
	if !encryption.isEnabled() {
		return "", fmt.Errorf("dex: error text: %w", ErrPayloadCodecNotConfigured)
	}
	envelope, err := encryption.parseEncodedText(encodedText)
	if err != nil {
		return "", fmt.Errorf("dex: decrypt error text: %w", err)
	}
	values, _, err := encryption.decodeEnvelopes(ctx, payloadContext, []*dexpb.PayloadEnvelope{envelope}, true)
	if err != nil {
		return "", fmt.Errorf("dex: decrypt error text: %w", err)
	}
	return values[0].GetStringValue(), nil
}

// decodeFlowResultErrorText decodes FlowResult.error_message in place.
func (encryption *payloadEncryption) decodeFlowResultErrorText(
	ctx context.Context,
	payloadContext PayloadContext,
	result *dexpb.FlowResult,
) error {
	if result == nil {
		return nil
	}
	decoded, err := encryption.decodeErrorText(ctx, payloadContext, result.ErrorMessage)
	if err != nil {
		return err
	}
	result.ErrorMessage = decoded
	return nil
}

// parsePayloadEnvelope runs decode steps 4 to 6.
func parsePayloadEnvelope(envelopeBytes []byte) (*dexpb.PayloadEnvelope, error) {
	envelope := &dexpb.PayloadEnvelope{}
	if err := proto.Unmarshal(envelopeBytes, envelope); err != nil {
		return nil, newPayloadDecodeError(PayloadErrorMalformed, "", "envelope is not a PayloadEnvelope")
	}
	if err := validatePayloadEnvelopeHeader(envelope); err != nil {
		return nil, err
	}
	return envelope, nil
}

// validatePayloadEnvelopeHeader runs decode steps 5 and 6: the key ID format and metadata limits.
func validatePayloadEnvelopeHeader(envelope *dexpb.PayloadEnvelope) error {
	keyID := envelope.GetKeyId()
	if err := validatePayloadKeyID(keyID); err != nil {
		return newPayloadDecodeError(PayloadErrorMalformed, "", "%v", err)
	}
	metadataBytes := 0
	for key, value := range envelope.GetMetadata() {
		metadataBytes += len(key) + len(value)
	}
	if len(envelope.GetMetadata()) > maxPayloadMetadataEntries || metadataBytes > maxPayloadMetadataTotalBytes {
		return newPayloadDecodeError(
			PayloadErrorLimitExceeded, keyID, "metadata has %d entries and %d bytes",
			len(envelope.GetMetadata()), metadataBytes,
		)
	}
	return nil
}

func validatePayloadKeyID(keyID string) error {
	if len(keyID) == 0 || len(keyID) > maxPayloadKeyIDBytes {
		return fmt.Errorf("key id has %d bytes; it needs 1 to %d", len(keyID), maxPayloadKeyIDBytes)
	}
	for index := range len(keyID) {
		if keyID[index] < 0x21 || keyID[index] > 0x7e {
			return fmt.Errorf("key id byte %d is outside printable ASCII 0x21 to 0x7e", index)
		}
	}
	return nil
}

// parsePayloadPlaintext runs decode step 14: the plaintext is a Value of an encryptable kind.
func parsePayloadPlaintext(plaintext []byte, isText bool, keyID string) (*dexpb.Value, error) {
	value := &dexpb.Value{}
	if err := proto.Unmarshal(plaintext, value); err != nil {
		return nil, newPayloadDecodeError(PayloadErrorInvalidPlaintext, keyID, "plaintext is not a Value")
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		return value, nil
	case *dexpb.Value_ObjValue:
		if isText {
			return nil, newPayloadDecodeError(PayloadErrorInvalidPlaintext, keyID, "encoded text must hold a string Value")
		}
		if strings.HasPrefix(kind.ObjValue.GetEncoding(), payloadReservedEncodingRoot) {
			return nil, newPayloadDecodeError(PayloadErrorInvalidPlaintext, keyID, "envelopes do not nest")
		}
		return value, nil
	case *dexpb.Value_IntValue, *dexpb.Value_DoubleValue, *dexpb.Value_BoolValue:
		if isText {
			return nil, newPayloadDecodeError(PayloadErrorInvalidPlaintext, keyID, "encoded text must hold a string Value")
		}
		return value, nil
	default:
		return nil, newPayloadDecodeError(PayloadErrorInvalidPlaintext, keyID, "plaintext kind %T is never encrypted", kind)
	}
}

// payloadCodecError withholds the text of a codec or key provider error, which may reveal
// key-management details, from SDK errors, logs, and Worker failures.
type payloadCodecError struct {
	operation string
	cause     error
}

func (codecError *payloadCodecError) Error() string {
	return "dex: " + codecError.operation + " failed; the codec's error text is withheld"
}

// Unwrap returns the codec's error for errors.As; Error never includes its text.
func (codecError *payloadCodecError) Unwrap() error {
	return codecError.cause
}

// hidePayloadCodecErrorText rebuilds every codec error into a fresh SDK error, including errors of
// the built-in codec, because a delegating codec can change exported fields before returning one.
// A *PayloadDecodeError keeps only a defined Category, its unexported SDK-written reason, and a
// KeyID in keyIDs, the validated key IDs the SDK read before the call; pass none to drop every
// key ID. Any other failure becomes a fixed "codec failed" error. The original stays reachable
// through Unwrap.
func hidePayloadCodecErrorText(operation string, err error, keyIDs []string) error {
	var decodeErr *PayloadDecodeError
	if errors.As(err, &decodeErr) && isDefinedPayloadErrorCategory(decodeErr.Category) {
		keyID := ""
		if validatePayloadKeyID(decodeErr.KeyID) == nil && slices.Contains(keyIDs, decodeErr.KeyID) {
			keyID = decodeErr.KeyID
		}
		return &PayloadDecodeError{
			Category: decodeErr.Category,
			KeyID:    keyID,
			reason:   decodeErr.reason,
			cause:    err,
		}
	}
	var providerErr *payloadKeyProviderError
	if errors.As(err, &providerErr) {
		return &payloadKeyProviderError{reason: providerErr.reason, cause: err}
	}
	return &payloadCodecError{operation: operation, cause: err}
}

func isDefinedPayloadErrorCategory(category PayloadErrorCategory) bool {
	switch category {
	case PayloadErrorMalformed, PayloadErrorUnsupportedMarker, PayloadErrorUnsupportedCipher,
		PayloadErrorUnsupportedCompression, PayloadErrorUnknownKey, PayloadErrorAuthenticationFailed,
		PayloadErrorLimitExceeded, PayloadErrorInvalidPlaintext:
		return true
	default:
		return false
	}
}

// payloadFailureSummary returns a fixed, non-sensitive description of a payload failure:
// a category and key ID, never error text. fallback describes failures without a category.
//
// It reads only the outermost *PayloadDecodeError, which the SDK built: a parse-step error or a
// codec error rebuilt by hidePayloadCodecErrorText. A hidden codec error is checked first, so the
// codec's own *PayloadDecodeError inside it is never read.
func payloadFailureSummary(err error, fallback string) string {
	if errors.Is(err, ErrPayloadCodecNotConfigured) {
		return ErrPayloadCodecNotConfigured.Error()
	}
	var codecErr *payloadCodecError
	if errors.As(err, &codecErr) {
		return "codec_failed"
	}
	var decodeErr *PayloadDecodeError
	if errors.As(err, &decodeErr) {
		if !isDefinedPayloadErrorCategory(decodeErr.Category) {
			return "codec_failed"
		}
		if decodeErr.KeyID != "" && validatePayloadKeyID(decodeErr.KeyID) == nil {
			return fmt.Sprintf("%s (key id %q)", decodeErr.Category, decodeErr.KeyID)
		}
		return string(decodeErr.Category)
	}
	var providerErr *payloadKeyProviderError
	if errors.As(err, &providerErr) {
		return "key_provider_failed"
	}
	return fallback
}

func describeFlowValue(target flowValuePointer) string {
	if target.valueDescription != "" {
		return target.valueDescription
	}
	return "value"
}

func describePayloadSlots(slots []payloadValueSlot) string {
	descriptions := make([]string, 0, len(slots))
	for _, slot := range slots {
		if slot.valueDescription != "" {
			descriptions = append(descriptions, slot.valueDescription)
		}
	}
	if len(descriptions) == 0 {
		return fmt.Sprintf("%d values", len(slots))
	}
	return strings.Join(descriptions, ", ")
}
