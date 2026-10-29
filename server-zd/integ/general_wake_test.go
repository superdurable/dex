// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const (
	dispatchProbeFlowType = "DispatchProbeFlow"
	dispatchProbeStep     = "DispatchStep"
	dispatchProbeChannel  = "proceed"
)

func startDispatchProbe(ctx context.Context, client dexpb.FlowServiceClient, flowID string, startStep string, workerTarget *dexpb.WorkerTarget, stepOptions *dexpb.StepOptions) error {
	_, err := client.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, StartStepType: startStep, StepOptions: stepOptions,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	return err
}
