// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package lints

import (
	"context"
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

type SubscriberRegistryFlow struct {
	dex.FlowDefaults
}

func (*SubscriberRegistryFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow *SubscriberRegistryFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ListSubscribers, nil)}
}

func (*SubscriberRegistryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

func (*SubscriberRegistryFlow) ListSubscribers(_ dex.Context, _ dex.None) (*dex.RPCResult[[]string], error) {
	return &dex.RPCResult[[]string]{Output: []string{}}, nil
}

func ensureRegistry(ctx context.Context, client *dex.Client, registryID string) error {
	_, err := client.StartFlow(ctx, &SubscriberRegistryFlow{}, registryID, nil, dex.StartFlowOptions{
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
	})
	return err
}

func readRegistry(ctx context.Context, client *dex.Client, registryID string) ([]string, error) {
	var subscribers []string
	err := client.InvokeRPC(ctx, registryID, (&SubscriberRegistryFlow{}).ListSubscribers, nil, &subscribers)
	var missing *dex.FlowNotActiveOrNotFoundError
	if errors.As(err, &missing) {
		return []string{}, nil
	}
	return subscribers, err
}
