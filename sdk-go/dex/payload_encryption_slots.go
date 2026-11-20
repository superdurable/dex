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
	"fmt"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// outgoingPayloadSlots collects the value fields of one assembled request or response.
// The SDK seals messages after building them, so handlers and permission projections see plaintext.
type outgoingPayloadSlots struct {
	registry *Registry
	slots    []payloadValueSlot
}

func newOutgoingPayloadSlots(registry *Registry) *outgoingPayloadSlots {
	return &outgoingPayloadSlots{registry: registry}
}

func (collector *outgoingPayloadSlots) addValue(
	payloadContext PayloadContext,
	valueDescription string,
	valuePointer **dexpb.Value,
) {
	collector.slots = append(collector.slots, payloadValueSlot{
		payloadContext:   payloadContext,
		valueDescription: valueDescription,
		valuePointer:     valuePointer,
	})
}

func (collector *outgoingPayloadSlots) addKVs(payloadContext PayloadContext, kind string, values []*dexpb.KV) {
	for _, value := range values {
		if value != nil {
			collector.addValue(payloadContext, fmt.Sprintf("%s %q", kind, value.Key), &value.Value)
		}
	}
}

// addAttributeWrites skips indexed writes and Plaintext attributes of flow.
func (collector *outgoingPayloadSlots) addAttributeWrites(
	payloadContext PayloadContext,
	flow *registeredFlow,
	writes []*dexpb.AttributeWrite,
) {
	for _, write := range writes {
		if write == nil || write.GetIndexConfig().GetEnable() || flow.isPlaintextAttributeKey(write.Key) {
			continue
		}
		collector.addValue(payloadContext, fmt.Sprintf("Attribute %q", write.Key), &write.Value)
	}
}

func (collector *outgoingPayloadSlots) addChannelMessages(
	payloadContext PayloadContext,
	messages []*dexpb.ChannelMessage,
) {
	for index, message := range messages {
		if message != nil {
			collector.addValue(
				payloadContext,
				fmt.Sprintf("Channel %q message at index %d", message.ChannelName, index),
				&message.Value,
			)
		}
	}
}

// addStepDecision adds Step inputs and the close output; ForceFail reasons use sealForceFailReason.
func (collector *outgoingPayloadSlots) addStepDecision(payloadContext PayloadContext, decision *dexpb.StepDecision) {
	if decision == nil {
		return
	}
	for _, movement := range decision.NextSteps {
		if movement != nil {
			collector.addValue(payloadContext, fmt.Sprintf("Step %q input", movement.StepType), &movement.StepInput)
		}
	}
	closeDecision := decision.CloseDecision
	if closeDecision != nil &&
		closeDecision.CloseDecisionType != dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL {
		collector.addValue(payloadContext, "close output", &closeDecision.CloseInput)
	}
}

// addWaitingCondition adds SubFlow inputs and initial Attributes with the child Flow's context.
func (collector *outgoingPayloadSlots) addWaitingCondition(
	parentFlowID string,
	stepExecutionID string,
	waiting *dexpb.WaitingCondition,
) {
	for _, subFlow := range waiting.GetSubFlowConditions() {
		childContext := PayloadContext{
			FlowType: subFlow.SubFlowType,
			FlowID:   subFlowFlowID(parentFlowID, stepExecutionID, int(subFlow.SubFlowIndex)),
		}
		collector.addValue(childContext, fmt.Sprintf("SubFlow %q input", childContext.FlowID), &subFlow.StepInput)
		var childFlow *registeredFlow
		if collector.registry != nil {
			childFlow, _ = collector.registry.lookupFlow(subFlow.SubFlowType)
		}
		if options := subFlow.GetOptions(); options != nil {
			collector.addAttributeWrites(childContext, childFlow, options.Attributes)
		}
	}
}

func (collector *outgoingPayloadSlots) seal(ctx context.Context, encryption *payloadEncryption) error {
	return encryption.encryptValueSlots(ctx, collector.slots)
}

// isPlaintextAttributeKey reports whether a physical Attribute key belongs to a Plaintext definition.
func (flow *registeredFlow) isPlaintextAttributeKey(key string) bool {
	if flow == nil {
		return false
	}
	name, _, _ := strings.Cut(key, "/")
	attribute, found := flow.attributes[name]
	return found && attribute.isPlaintext
}

// subFlowFlowID returns the Flow ID that the server assigns to a SubFlow.
func subFlowFlowID(parentFlowID string, stepExecutionID string, index int) string {
	return fmt.Sprintf("SubFlow:%s-%s-%d", parentFlowID, stepExecutionID, index)
}

// sealForceFailReason encodes a ForceFail reason as error text; the server requires a string Value.
func sealForceFailReason(
	ctx context.Context,
	encryption *payloadEncryption,
	payloadContext PayloadContext,
	decision *dexpb.StepDecision,
) error {
	closeDecision := decision.GetCloseDecision()
	if closeDecision == nil ||
		closeDecision.CloseDecisionType != dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL {
		return nil
	}
	reason, isString := closeDecision.GetCloseInput().GetKind().(*dexpb.Value_StringValue)
	if !isString {
		return nil
	}
	encoded, err := encryption.encodeErrorText(ctx, payloadContext, reason.StringValue)
	if err != nil {
		return err
	}
	if encoded != reason.StringValue {
		closeDecision.CloseInput = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: encoded}}
	}
	return nil
}
