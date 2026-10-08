// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package loaded

import (
	"context"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

func StartLoadedReadsFlow(ctx context.Context, client *dex.Client, flowID string) error {
	timeout := time.Hour
	_, err := client.StartFlow(ctx, &LoadedReadsFlow{}, flowID, DeliveryInput{}, dex.StartFlowOptions{
		Timeout:       &timeout,
		TimeoutPolicy: dex.TimeoutHandler,
		TimeoutHandlerOptions: &dex.FlowTimeoutHandlerOptions{
			LoadAttributeMaps: []dex.AttributeDef{Subscribers},
		},
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		RequestID:      &flowID,
	})
	return err
}

func ReadProfile(ctx context.Context, client *dex.Client, flowID string, email string) (string, error) {
	var profile string
	err := client.InvokeRPCWithOptions(ctx, flowID, (&LoadedReadsFlow{}).GetProfile, email, &profile, dex.RPCInvokeOptions{
		LoadAttributeMapInstances: []dex.AttributeMapLoad{Profiles.Load(email)},
	})
	return profile, err
}
