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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const clientTestUnavailableBlobID = "unavailable-blob"

func (service *clientTestFlowService) GetAttributes(
	_ context.Context,
	request *dexpb.GetAttributesRequest,
) (*dexpb.GetAttributesResponse, error) {
	service.getAttributesRequest = request
	itemValue := &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 3}}
	statusValue := &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "done"}}
	switch request.FlowId {
	case "attributes-blob":
		statusValue = &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: "status-blob",
		}}
	case "attributes-decode-failure":
		itemValue = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "three"}}
	case "attributes-load-failure":
		statusValue = &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
			InternalBlobIdForStringValue: clientTestUnavailableBlobID,
		}}
	}
	return &dexpb.GetAttributesResponse{Attributes: []*dexpb.KV{
		{Key: "status", Value: statusValue},
		{Key: "items/line-1", Value: itemValue},
	}}, nil
}

func TestClientGetAttributesHydratesBlobReferences(t *testing.T) {
	client, service := newClientIntegration(t)
	status := "unchanged"
	item := -1
	unsetItem := -1
	result, err := client.GetAttributes(context.Background(), "attributes-blob",
		ReadAttribute(clientTestStatus, &status),
		ReadAttributeMapInstance(clientTestItems, "line-1", &item),
		ReadAttributeMapInstance(clientTestItems, "line-2", &unsetItem),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"status", "items/line-1", "items/line-2"}, service.getAttributesRequest.Keys)
	require.Equal(t, 1, service.loadBlobsRequestCount)
	require.Equal(t, "hydrated", status)
	require.Equal(t, 3, item)
	require.Equal(t, -1, unsetItem)
	require.True(t, result.IsSet(clientTestStatus))
	require.True(t, result.IsMapInstanceSet(clientTestItems, "line-1"))
	require.False(t, result.IsMapInstanceSet(clientTestItems, "line-2"))
	require.False(t, result.IsSet(DefineAttribute[int]("status")))
	require.False(t, result.IsSet(DefineAttribute[string]("status")))
	require.False(t, result.IsMapInstanceSet(DefineAttributeMap[string]("items"), "line-1"))
}

func TestClientGetAttributesWritesNoDestinationOnError(t *testing.T) {
	for _, flowID := range []string{"attributes-decode-failure", "attributes-load-failure"} {
		t.Run(flowID, func(t *testing.T) {
			client, _ := newClientIntegration(t)
			status := "unchanged"
			item := -1
			result, err := client.GetAttributes(context.Background(), flowID,
				ReadAttribute(clientTestStatus, &status),
				ReadAttributeMapInstance(clientTestItems, "line-1", &item),
			)
			require.ErrorContains(t, err, flowID)
			if flowID == "attributes-decode-failure" {
				var mappingError *ValueMappingError
				require.ErrorAs(t, err, &mappingError)
				require.ErrorContains(t, err, "items/line-1")
			}
			require.Equal(t, AttributeReadResult{}, result)
			require.Equal(t, "unchanged", status)
			require.Equal(t, -1, item)
		})
	}
}

func TestClientGetAttributesValidationErrorsNameFlowID(t *testing.T) {
	client, _ := newClientIntegration(t)
	status := "unchanged"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.GetAttributes(ctx, "attributes-cancelled", ReadAttribute(clientTestStatus, &status))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "attributes-cancelled")
	_, err = client.GetAttributes(context.Background(), "attributes-no-reads")
	require.ErrorContains(t, err, "attributes-no-reads")
	require.Equal(t, "unchanged", status)
}
