// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package workerclient

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
)

func TestValidateRuntimeAttributeWritesRejectsPermissionProjection(t *testing.T) {
	for _, write := range []*dexpb.AttributeWrite{
		{Key: service.SearchAttributeDexWorkQueuePermissions},
		{Key: "application", IndexConfig: &dexpb.IndexConfig{IndexKey: service.SearchAttributeDexWorkQueuePermissions}},
	} {
		require.ErrorContains(t, ValidateRuntimeAttributeWrites([]*dexpb.AttributeWrite{write}), "managed by the Server")
	}
	require.NoError(t, ValidateRuntimeAttributeWrites([]*dexpb.AttributeWrite{{Key: "application"}}))
}

func TestValidateActionPermissionMappings(t *testing.T) {
	valid := &dexpb.ActionPermissionMappings{Mappings: []*dexpb.ActionPermissionMapping{{
		AttributeKey: "status",
		EqualValues: []*dexpb.Value{
			{Kind: &dexpb.Value_StringValue{StringValue: "ready"}},
			{Kind: &dexpb.Value_IntValue{IntValue: 1}},
			{Kind: &dexpb.Value_DoubleValue{DoubleValue: 1.5}},
			{Kind: &dexpb.Value_BoolValue{BoolValue: true}},
		},
		RequiredPermission: "refund.approve",
	}}}
	require.NoError(t, ValidateActionPermissionMappings(valid, service.BackendTypeTemporal))
	require.ErrorContains(
		t,
		ValidateActionPermissionMappings(valid, service.BackendTypeCadence),
		"Temporal backend",
	)

	invalidMappings := []*dexpb.ActionPermissionMappings{
		{Mappings: []*dexpb.ActionPermissionMapping{nil}},
		{Mappings: []*dexpb.ActionPermissionMapping{{RequiredPermission: "refund.approve"}}},
		{Mappings: []*dexpb.ActionPermissionMapping{{
			AttributeKey:       service.SearchAttributeDexWorkQueuePermissions,
			RequiredPermission: "refund.approve",
			EqualValues: []*dexpb.Value{{
				Kind: &dexpb.Value_StringValue{StringValue: "ready"},
			}},
		}}},
		{Mappings: []*dexpb.ActionPermissionMapping{{AttributeKey: "status", RequiredPermission: "Refund"}}},
		{Mappings: []*dexpb.ActionPermissionMapping{{AttributeKey: "status", RequiredPermission: "refund.approve"}}},
		{Mappings: []*dexpb.ActionPermissionMapping{{
			AttributeKey:       "status",
			RequiredPermission: "refund.approve",
			EqualValues: []*dexpb.Value{{
				Kind: &dexpb.Value_DoubleValue{DoubleValue: math.NaN()},
			}},
		}}},
		{Mappings: []*dexpb.ActionPermissionMapping{{
			AttributeKey:       "status",
			RequiredPermission: "refund.approve",
			EqualValues: []*dexpb.Value{{
				Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{}},
			}},
		}}},
	}
	for _, mappings := range invalidMappings {
		require.Error(t, ValidateActionPermissionMappings(mappings, service.BackendTypeTemporal))
	}
}
