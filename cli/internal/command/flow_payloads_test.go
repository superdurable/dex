// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/superdurable/dex/cli/internal/payloadcodec"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

const prefixedText = "dex-encoded:AAAA"

func encodedTextSlots(t *testing.T, message proto.Message, replacements map[string]any) []*encryptedPayloadSlot {
	t.Helper()
	raw, err := messageMap(message)
	if err != nil {
		t.Fatal(err)
	}
	var slots []*encryptedPayloadSlot
	collectEncodedTexts(raw, message.ProtoReflect().Descriptor(), replacements, &slots)
	return slots
}

func TestEncodedTextsComeOnlyFromErrorAndReasonFields(t *testing.T) {
	stringValue := &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: prefixedText}}
	for _, scenario := range []struct {
		name    string
		message proto.Message
		slots   int
	}{
		{"Flow ID and Attribute key", &dexpb.SearchFlowsResponse{FlowRuns: []*dexpb.SearchFlowsResponseEntry{{
			FlowId: prefixedText, FlowType: prefixedText, IndexedAttributes: []*dexpb.KV{{Key: prefixedText, Value: stringValue}},
		}}}, 0},
		{"Attribute of Flow state", &dexpb.GetFlowStateResponse{Attributes: []*dexpb.KV{{Key: prefixedText, Value: stringValue}}}, 0},
		{"Flow result error", &dexpb.FlowResult{ErrorMessage: prefixedText}, 1},
		{"Step failure details", &dexpb.GetFlowStateResponse{ActiveStepExecutions: []*dexpb.ActiveStepExecutionState{{
			LastFailureInfo: &dexpb.StepMethodFailure{Details: &dexpb.ServiceErrorResponse{
				Detail: prefixedText, OriginalWorkerErrorStackTrace: prefixedText, OriginalWorkerErrorType: prefixedText,
			}},
		}}}, 2},
		{"Step method event context failure", &dexpb.StepMethodEventContext{
			LastFailureInfo: &dexpb.StepMethodFailure{Details: &dexpb.ServiceErrorResponse{
				OriginalWorkerErrorDetail: prefixedText, OriginalWorkerErrorType: prefixedText,
			}},
		}, 1},
		{"Step method failed output", &dexpb.StepMethodFailedOutput{
			Failure: &dexpb.StepMethodFailure{Details: &dexpb.ServiceErrorResponse{
				Detail: prefixedText, OriginalWorkerErrorStackTrace: prefixedText, OriginalWorkerErrorType: prefixedText,
			}},
		}, 2},
		{"ForceFail close input", &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL, CloseInput: stringValue,
		}}, 1},
		{"graceful complete output", &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE, CloseInput: stringValue,
		}}, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if slots := encodedTextSlots(t, scenario.message, nil); len(slots) != scenario.slots {
				t.Fatalf("found %d encoded texts, want %d", len(slots), scenario.slots)
			}
		})
	}
}

func TestForceFailCloseInputLoadedFromBlobIsAnEncodedText(t *testing.T) {
	decision := &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
		CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
		CloseInput:        &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: "blob-1"}},
	}}
	loaded := map[string]any{"stringValue": prefixedText}
	replacements := map[string]any{blobReferenceKey(blobReference{id: "blob-1", kind: "string"}): loaded}
	slots := encodedTextSlots(t, decision, replacements)
	if len(slots) != 1 || slots[0].textKey != "stringValue" {
		t.Fatalf("slots = %v", slots)
	}
	slots[0].apply(&dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "card declined"}})
	if loaded["stringValue"] != "card declined" {
		t.Fatalf("loaded close input = %v", loaded)
	}
}

func TestEnvelopeObjectPayloadIsCheckedBeforeDecoding(t *testing.T) {
	envelopeAt := func(size int) string {
		return base64.StdEncoding.EncodeToString(make([]byte, size))
	}
	limits := payloadcodec.Limits{MaxEnvelopeBytes: 30}
	for _, scenario := range []struct {
		name     string
		payload  string
		category string
	}{
		{"at the limit", envelopeAt(30), ""},
		{"one byte over", envelopeAt(31), payloadcodec.CategoryLimitExceeded},
		{"unpadded base64", strings.TrimRight(envelopeAt(4), "="), payloadcodec.CategoryMalformed},
		{"non-zero trailing bits", "AB==", payloadcodec.CategoryMalformed},
		{"whitespace", "AAAA AAAA", payloadcodec.CategoryMalformed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			slot := &encryptedPayloadSlot{valueMap: map[string]any{"objValue": map[string]any{
				"encoding": payloadcodec.EnvelopeMarker, "payload": scenario.payload,
			}}}
			slot.prepare(limits)
			var codecError *payloadcodec.CodecError
			switch {
			case scenario.category == "" && slot.err != nil:
				t.Fatalf("prepare failed: %v", slot.err)
			case scenario.category != "" && (!errors.As(slot.err, &codecError) || codecError.Category != scenario.category):
				t.Fatalf("prepare error = %v, want %s", slot.err, scenario.category)
			}
		})
	}
}
