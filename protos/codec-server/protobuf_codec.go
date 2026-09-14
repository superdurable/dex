// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type dexProtobufCodec struct {
	binaryPayloadConverter *converter.ProtoPayloadConverter
	jsonPayloadConverter   *converter.ProtoJSONPayloadConverter
	messagePackagePrefix   string
}

func newDexProtobufCodec() *dexProtobufCodec {
	return &dexProtobufCodec{
		binaryPayloadConverter: converter.NewProtoPayloadConverter(),
		jsonPayloadConverter:   converter.NewProtoJSONPayloadConverter(),
		messagePackagePrefix:   string(dexpb.File_dex_proto.Package()) + ".",
	}
}

func (c *dexProtobufCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	encodedPayloads := make([]*commonpb.Payload, len(payloads))
	for payloadIndex, payload := range payloads {
		encodedPayload, err := c.encodePayload(payload)
		if err != nil {
			return payloads, fmt.Errorf("encode payload %d: %w", payloadIndex, err)
		}
		encodedPayloads[payloadIndex] = encodedPayload
	}
	return encodedPayloads, nil
}

func (c *dexProtobufCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	decodedPayloads := make([]*commonpb.Payload, len(payloads))
	for payloadIndex, payload := range payloads {
		decodedPayload, err := c.decodePayload(payload)
		if err != nil {
			return payloads, fmt.Errorf("decode payload %d: %w", payloadIndex, err)
		}
		decodedPayloads[payloadIndex] = decodedPayload
	}
	return decodedPayloads, nil
}

func (c *dexProtobufCodec) encodePayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	if string(payload.GetMetadata()[converter.MetadataEncoding]) != converter.MetadataEncodingProtoJSON {
		return payload, nil
	}

	message, isKnownMessage, err := c.newMessage(payload)
	if err != nil {
		return nil, err
	}
	if !isKnownMessage {
		return payload, nil
	}

	if err := c.jsonPayloadConverter.FromPayload(payload, message); err != nil {
		return nil, fmt.Errorf("parse %s ProtoJSON: %w", messageTypeName(payload), err)
	}
	encodedPayload, err := c.binaryPayloadConverter.ToPayload(message)
	if err != nil {
		return nil, fmt.Errorf("marshal %s protobuf: %w", messageTypeName(payload), err)
	}
	return withOriginalMetadata(encodedPayload, payload), nil
}

func (c *dexProtobufCodec) decodePayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	if string(payload.GetMetadata()[converter.MetadataEncoding]) != converter.MetadataEncodingProto {
		return payload, nil
	}

	message, isKnownMessage, err := c.newMessage(payload)
	if err != nil {
		return nil, err
	}
	if !isKnownMessage {
		return payload, nil
	}

	if err := c.binaryPayloadConverter.FromPayload(payload, message); err != nil {
		return nil, fmt.Errorf("unmarshal %s protobuf: %w", messageTypeName(payload), err)
	}
	decodedPayload, err := c.jsonPayloadConverter.ToPayload(message)
	if err != nil {
		return nil, fmt.Errorf("marshal %s ProtoJSON: %w", messageTypeName(payload), err)
	}
	return withOriginalMetadata(decodedPayload, payload), nil
}

func (c *dexProtobufCodec) newMessage(payload *commonpb.Payload) (proto.Message, bool, error) {
	fullMessageTypeName := messageTypeName(payload)
	if !strings.HasPrefix(fullMessageTypeName, c.messagePackagePrefix) {
		return nil, false, nil
	}

	messageType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(fullMessageTypeName))
	if errors.Is(err, protoregistry.NotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find protobuf message type %s: %w", fullMessageTypeName, err)
	}
	return messageType.New().Interface(), true, nil
}

func messageTypeName(payload *commonpb.Payload) string {
	return string(payload.GetMetadata()[converter.MetadataMessageType])
}

func withOriginalMetadata(convertedPayload, originalPayload *commonpb.Payload) *commonpb.Payload {
	metadata := make(map[string][]byte, len(originalPayload.GetMetadata()))
	for metadataKey, metadataValue := range originalPayload.GetMetadata() {
		metadata[metadataKey] = metadataValue
	}
	metadata[converter.MetadataEncoding] = convertedPayload.GetMetadata()[converter.MetadataEncoding]
	metadata[converter.MetadataMessageType] = convertedPayload.GetMetadata()[converter.MetadataMessageType]
	convertedPayload.Metadata = metadata
	return convertedPayload
}
