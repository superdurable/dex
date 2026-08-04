// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"fmt"
	"github.com/superdurable/dex/sdk-go/dex"
)

type noStateWorkflow struct {
	dex.WorkflowDefaults
}

func (b noStateWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.RPCMethodDef(b.TestErrorRPC, nil),
	}
}

func (b noStateWorkflow) TestErrorRPC(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (interface{}, error) {
	return nil, fmt.Errorf("test error")
}
