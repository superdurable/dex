// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/superdurable/dex/cli/internal/payloadcodec"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// encryptedValueKey marks a stored value that dexcli shows without decrypting it.
const encryptedValueKey = "__dexEncryptedValue"

const noPayloadCodecMessage = "encrypted value shown without decryption; pass --codec-key-file or --codec-endpoint"

// newFlowPayloadCodec returns the codec that Flow commands use, or nil when none is configured.
// Without codec flags, a loopback --server uses the codec server of the dexcli dev that serves it.
func newFlowPayloadCodec(options options) (payloadcodec.EnvelopeCodec, error) {
	if options.codecKeyFile != "" && options.codecEndpoint != "" {
		return nil, errors.New("--codec-key-file and --codec-endpoint cannot be combined; configure one codec")
	}
	if options.codecKeyFile != "" {
		return payloadcodec.NewLocalCodecFromKeyFile(options.codecKeyFile)
	}
	if options.codecEndpoint != "" {
		token, err := options.readCodecToken()
		if err != nil {
			return nil, err
		}
		return payloadcodec.NewRemoteCodec(options.codecEndpoint, token)
	}
	if options.codecTokenFile != "" {
		return nil, errors.New("--codec-token-file requires --codec-endpoint")
	}
	return discoverDevPayloadCodec(options.server)
}

// discoverDevPayloadCodec returns the codec server of a dexcli dev -codec-key-file serving server, or nil.
func discoverDevPayloadCodec(server string) (payloadcodec.EnvelopeCodec, error) {
	host, portText, err := net.SplitHostPort(server)
	if err != nil || !isLoopbackHost(host) {
		return nil, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, nil
	}
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	discoveryPath := payloadcodec.DevCodecServerDiscoveryPath(filepath.Join(homeDirectory, ".dex"), port)
	discovery, found, err := payloadcodec.ReadDevCodecServerDiscovery(discoveryPath)
	if err != nil || !found {
		return nil, err
	}
	remote, err := payloadcodec.NewRemoteCodec(discovery.Endpoint, discovery.Token)
	if err != nil {
		return nil, err
	}
	return discoveredDevPayloadCodec{EnvelopeCodec: remote, discoveryPath: discoveryPath}, nil
}

// discoveredDevPayloadCodec names the discovery file in errors, so users can delete a stale one.
type discoveredDevPayloadCodec struct {
	payloadcodec.EnvelopeCodec
	discoveryPath string
}

func (codec discoveredDevPayloadCodec) EncodeValues(ctx context.Context, payloadContext dex.PayloadContext, values []*dexpb.Value) ([][]byte, error) {
	envelopes, err := codec.EnvelopeCodec.EncodeValues(ctx, payloadContext, values)
	return envelopes, codec.describeSource(err)
}

func (codec discoveredDevPayloadCodec) DecodeEnvelopes(ctx context.Context, payloadContext dex.PayloadContext, envelopes [][]byte) ([]*dexpb.Value, error) {
	values, err := codec.EnvelopeCodec.DecodeEnvelopes(ctx, payloadContext, envelopes)
	return values, codec.describeSource(err)
}

func (codec discoveredDevPayloadCodec) describeSource(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w (codec server of dexcli dev from %s; delete that file if dexcli dev is not running)", err, codec.discoveryPath)
}

func (s *flowService) payloadCodec() (payloadcodec.EnvelopeCodec, error) {
	if !s.isPayloadCodecResolved {
		s.resolvedPayloadCodec, s.payloadCodecErr = newFlowPayloadCodec(s.options)
		s.isPayloadCodecResolved = true
	}
	return s.resolvedPayloadCodec, s.payloadCodecErr
}

// encryptValues replaces each encryptable Value with its dex/1 envelope when a codec is configured.
func (s *flowService) encryptValues(ctx context.Context, payloadContext dex.PayloadContext, slots ...**dexpb.Value) error {
	codec, err := s.payloadCodec()
	if err != nil || codec == nil {
		return err
	}
	var pending []**dexpb.Value
	var values []*dexpb.Value
	for _, slot := range slots {
		if slot == nil || *slot == nil || payloadcodec.CheckEncryptableValue(*slot) != nil {
			continue
		}
		pending = append(pending, slot)
		values = append(values, *slot)
	}
	if len(values) == 0 {
		return nil
	}
	envelopes, err := codec.EncodeValues(ctx, payloadContext, values)
	if err != nil {
		return fmt.Errorf("encrypt values: %w", err)
	}
	for index, slot := range pending {
		*slot = &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
			Encoding: payloadcodec.EnvelopeMarker,
			Payload:  envelopes[index],
		}}}
	}
	return nil
}

// encodeText returns text in the "dex-encoded:" format with a codec.
// Without one, text starting with the prefix gets a leading space, so readers never decode it.
func (s *flowService) encodeText(ctx context.Context, payloadContext dex.PayloadContext, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	codec, err := s.payloadCodec()
	if err != nil {
		return "", err
	}
	if codec == nil {
		if strings.HasPrefix(text, payloadcodec.EncodedTextPrefix) {
			return " " + text, nil
		}
		return text, nil
	}
	envelopes, err := codec.EncodeValues(ctx, payloadContext, []*dexpb.Value{{Kind: &dexpb.Value_StringValue{StringValue: text}}})
	if err != nil {
		return "", fmt.Errorf("encrypt text: %w", err)
	}
	return payloadcodec.EncodedTextPrefix + base64.StdEncoding.EncodeToString(envelopes[0]), nil
}

// encodeCloneForkPayloads encrypts the reason, and only Attribute writes whose source value is encrypted.
func (s *flowService) encodeCloneForkPayloads(ctx context.Context, request *dexpb.CloneForkFlowRequest) error {
	payloadContext := dex.PayloadContext{FlowID: request.GetFlowId()}
	reason, err := s.encodeText(ctx, payloadContext, request.GetReason())
	if err != nil {
		return err
	}
	request.Reason = reason
	codec, err := s.payloadCodec()
	if err != nil || codec == nil {
		return err
	}
	encryptedKeys, err := s.sourceAttributesStoredEncrypted(ctx, request)
	if err != nil {
		return err
	}
	var slots []**dexpb.Value
	for _, attribute := range request.GetAttributes() {
		if encryptedKeys[attribute.GetKey()] && !attribute.GetIndexConfig().GetEnable() {
			slots = append(slots, &attribute.Value)
		}
	}
	return s.encryptValues(ctx, payloadContext, slots...)
}

// sourceAttributesStoredEncrypted returns the written keys whose source Flow value is a dex/1 envelope.
func (s *flowService) sourceAttributesStoredEncrypted(ctx context.Context, request *dexpb.CloneForkFlowRequest) (map[string]bool, error) {
	keys := make([]string, 0, len(request.GetAttributes()))
	for _, attribute := range request.GetAttributes() {
		keys = append(keys, attribute.GetKey())
	}
	encryptedKeys := make(map[string]bool)
	if len(keys) == 0 {
		return encryptedKeys, nil
	}
	response, err := s.service.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: request.GetSourceFlowId(), RunId: request.GetSourceRunId(), Keys: keys,
	})
	if err != nil {
		return nil, fmt.Errorf("read source Attributes to choose which writes to encrypt: %w", err)
	}
	blobKeys := make(map[string]string)
	var blobEntries []*dexpb.LoadBlobRequestEntry
	for _, attribute := range response.GetAttributes() {
		value := attribute.GetValue()
		switch kind := value.GetKind().(type) {
		case *dexpb.Value_ObjValue:
			encryptedKeys[attribute.GetKey()] = payloadcodec.IsEnvelopeObject(kind.ObjValue.GetEncoding())
		case *dexpb.Value_InternalBlobIdForObjValue:
			blobKeys[kind.InternalBlobIdForObjValue] = attribute.GetKey()
			blobEntries = append(blobEntries, &dexpb.LoadBlobRequestEntry{FlowId: request.GetSourceFlowId(), BlobValue: value})
		}
	}
	if len(blobEntries) == 0 {
		return encryptedKeys, nil
	}
	values, unavailable, err := loadBlobValues(ctx, s.service, blobEntries)
	if err != nil {
		return nil, fmt.Errorf("load source Attributes to choose which writes to encrypt: %w", err)
	}
	for blobID, key := range blobKeys {
		if reason, isUnavailable := unavailable[blobID]; isUnavailable {
			return nil, fmt.Errorf("source Attribute %q is unavailable: %s", key, reason)
		}
		encryptedKeys[key] = payloadcodec.IsEnvelopeObject(values[blobID].GetObjValue().GetEncoding())
	}
	return encryptedKeys, nil
}

// encryptedPayloadSlot is one encrypted value found in a protojson map tree.
type encryptedPayloadSlot struct {
	// valueMap is a Value map {"objValue": {"encoding": "dex/1", ...}} that is replaced in place.
	valueMap map[string]any
	// textOwner and textKey locate a "dex-encoded:" string field when valueMap is nil.
	textOwner map[string]any
	textKey   string
	envelope  []byte
	keyID     string
	err       error
}

// encodedTextFields equals the writer fields plus the Read points of spec section 6, by message type.
var encodedTextFields = map[protoreflect.FullName]map[protoreflect.Name]bool{
	"dex.WorkerErrorResponse":    {"detail": true, "stack_trace": true},
	"dex.ServiceErrorResponse":   {"detail": true, "original_worker_error_detail": true, "original_worker_error_stack_trace": true},
	"dex.RecoveryErrorInfo":      {"detail": true},
	"dex.StopFlowRequest":        {"reason": true},
	"dex.ResetFlowRequest":       {"reason": true},
	"dex.CloneForkFlowRequest":   {"reason": true},
	"dex.FlowResult":             {"error_message": true},
	"dex.FlowClosedHistoryEvent": {"error_message": true},
}

const forceFailCloseDecisionType = "CLOSE_DECISION_TYPE_FORCE_FAIL"

// decryptRawPayloads decrypts, in place, the dex/1 Values of raw and its blob replacements, and the
// "dex-encoded:" texts of the fields that may hold them. It returns one warning per distinct failure.
func (s *flowService) decryptRawPayloads(
	ctx context.Context,
	flowID string,
	descriptor protoreflect.MessageDescriptor,
	raw map[string]any,
	replacements map[string]any,
) []string {
	var slots []*encryptedPayloadSlot
	collectEncryptedValues(raw, &slots)
	for _, replacement := range replacements {
		collectEncryptedValues(replacement, &slots)
	}
	collectEncodedTexts(raw, descriptor, replacements, &slots)
	if len(slots) == 0 {
		return nil
	}
	codec, err := s.payloadCodec()
	if err == nil && codec == nil {
		err = errors.New(noPayloadCodecMessage)
	}
	var prepared []*encryptedPayloadSlot
	for _, slot := range slots {
		slot.prepare(payloadcodec.Limits{})
		if slot.err == nil && err != nil {
			slot.err = err
		}
		if slot.err == nil {
			prepared = append(prepared, slot)
		}
	}
	payloadContext := dex.PayloadContext{FlowID: flowID}
	if len(prepared) > 0 {
		decryptPreparedSlots(ctx, codec, payloadContext, prepared)
	}
	failures := make(map[string]bool)
	for _, slot := range slots {
		if slot.err != nil {
			failures[slot.err.Error()] = true
			slot.showUndecrypted()
		}
	}
	warnings := make([]string, 0, len(failures))
	for failure := range failures {
		warnings = append(warnings, "encrypted value not decrypted: "+failure)
	}
	sort.Strings(warnings)
	return warnings
}

// decryptPreparedSlots decrypts in one batch, and entry by entry after a batch failure to isolate it.
func decryptPreparedSlots(ctx context.Context, codec payloadcodec.EnvelopeCodec, payloadContext dex.PayloadContext, slots []*encryptedPayloadSlot) {
	envelopes := make([][]byte, 0, len(slots))
	for _, slot := range slots {
		envelopes = append(envelopes, slot.envelope)
	}
	values, err := codec.DecodeEnvelopes(ctx, payloadContext, envelopes)
	if err == nil {
		for index, slot := range slots {
			slot.apply(values[index])
		}
		return
	}
	if len(slots) == 1 {
		slots[0].err = err
		return
	}
	for _, slot := range slots {
		decryptPreparedSlots(ctx, codec, payloadContext, []*encryptedPayloadSlot{slot})
	}
}

func collectEncryptedValues(value any, slots *[]*encryptedPayloadSlot) {
	switch current := value.(type) {
	case map[string]any:
		if isEnvelopeValueMap(current) {
			*slots = append(*slots, &encryptedPayloadSlot{valueMap: current})
			return
		}
		for _, entry := range current {
			collectEncryptedValues(entry, slots)
		}
	case []any:
		for _, entry := range current {
			collectEncryptedValues(entry, slots)
		}
	}
}

// collectEncodedTexts walks raw with its message descriptor and collects only the encodedTextFields and
// ForceFail close inputs that start with "dex-encoded:". Every other string stays unchanged.
func collectEncodedTexts(
	raw map[string]any,
	descriptor protoreflect.MessageDescriptor,
	replacements map[string]any,
	slots *[]*encryptedPayloadSlot,
) {
	allowedFields := encodedTextFields[descriptor.FullName()]
	if descriptor.FullName() == "dex.CloseDecision" && raw["closeDecisionType"] == forceFailCloseDecisionType {
		collectForceFailCloseInput(raw["closeInput"], replacements, slots)
	}
	for key, entry := range raw {
		field := descriptor.Fields().ByJSONName(key)
		if field == nil {
			field = descriptor.Fields().ByName(protoreflect.Name(key))
		}
		if field == nil {
			continue
		}
		if text, isText := entry.(string); isText && allowedFields[field.Name()] && field.Kind() == protoreflect.StringKind {
			if strings.HasPrefix(text, payloadcodec.EncodedTextPrefix) {
				*slots = append(*slots, &encryptedPayloadSlot{textOwner: raw, textKey: key})
			}
			continue
		}
		child := field.Message()
		if field.IsMap() {
			child = field.MapValue().Message()
		}
		if child == nil || child.FullName() == "dex.Value" || child.ParentFile().Package() != "dex" {
			continue
		}
		switch current := entry.(type) {
		case map[string]any:
			if field.IsMap() {
				for _, mapEntry := range current {
					if nested, ok := mapEntry.(map[string]any); ok {
						collectEncodedTexts(nested, child, replacements, slots)
					}
				}
				continue
			}
			collectEncodedTexts(current, child, replacements, slots)
		case []any:
			for _, listEntry := range current {
				if nested, ok := listEntry.(map[string]any); ok {
					collectEncodedTexts(nested, child, replacements, slots)
				}
			}
		}
	}
}

// collectForceFailCloseInput collects the string Value of a ForceFail close input, which may be a loaded blob.
func collectForceFailCloseInput(closeInput any, replacements map[string]any, slots *[]*encryptedPayloadSlot) {
	value, ok := closeInput.(map[string]any)
	if !ok {
		return
	}
	if reference, isBlob := rawBlobReference(value); isBlob {
		value, ok = replacements[blobReferenceKey(reference)].(map[string]any)
		if !ok {
			return
		}
	}
	if text, isText := value["stringValue"].(string); isText && strings.HasPrefix(text, payloadcodec.EncodedTextPrefix) {
		*slots = append(*slots, &encryptedPayloadSlot{textOwner: value, textKey: "stringValue"})
	}
}

func isEnvelopeValueMap(value map[string]any) bool {
	if len(value) != 1 {
		return false
	}
	object, ok := value["objValue"].(map[string]any)
	if !ok {
		return false
	}
	encoding, _ := object["encoding"].(string)
	return payloadcodec.IsEnvelopeObject(encoding)
}

// prepare runs the checks before decryption and reads the envelope bytes and key ID.
// Both formats check the base64 length, canonical form, and decoded size before decoding further.
func (slot *encryptedPayloadSlot) prepare(limits payloadcodec.Limits) {
	var encodedEnvelope string
	if slot.valueMap != nil {
		object := slot.valueMap["objValue"].(map[string]any)
		encoding, _ := object["encoding"].(string)
		if slot.err = payloadcodec.CheckEnvelopeMarker(encoding); slot.err != nil {
			return
		}
		encodedEnvelope, _ = object["payload"].(string)
	} else {
		encodedEnvelope = strings.TrimPrefix(slot.textOwner[slot.textKey].(string), payloadcodec.EncodedTextPrefix)
	}
	if slot.envelope, slot.err = payloadcodec.DecodeEncodedText(encodedEnvelope, limits); slot.err != nil {
		return
	}
	header := &dexpb.PayloadEnvelope{}
	if proto.Unmarshal(slot.envelope, header) == nil {
		slot.keyID = header.GetKeyId()
	}
}

func (slot *encryptedPayloadSlot) apply(value *dexpb.Value) {
	if slot.valueMap == nil {
		text, isText := value.GetKind().(*dexpb.Value_StringValue)
		if !isText {
			slot.err = errors.New("invalid_plaintext: encoded text must hold a string Value")
			return
		}
		slot.textOwner[slot.textKey] = text.StringValue
		return
	}
	decoded, err := messageMap(value)
	if err != nil {
		slot.err = err
		return
	}
	clear(slot.valueMap)
	for key, entry := range decoded {
		slot.valueMap[key] = entry
	}
}

func (slot *encryptedPayloadSlot) showUndecrypted() {
	if slot.valueMap == nil {
		return
	}
	clear(slot.valueMap)
	slot.valueMap[encryptedValueKey] = map[string]any{"keyId": slot.keyID}
}
