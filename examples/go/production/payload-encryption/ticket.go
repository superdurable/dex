// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package payloadencryption

import "github.com/superdurable/dex/sdk-go/dex"

// TicketMessage holds the customer's text, so payload encryption stores it as ciphertext.
var TicketMessage = dex.DefineAttribute[string]("support-ticket-message")

// TicketStatus stays plaintext, so WaitForAttributeMatch and Action conditions can compare it.
var TicketStatus = dex.DefineAttribute[string]("support-ticket-status", dex.Plaintext())

// SupportTicketFlow stores one support ticket.
type SupportTicketFlow struct {
	dex.FlowDefaults
}

func (*SupportTicketFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(receiveTicketStep{})}
}

func (flow *SupportTicketFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetTicketMessage, nil)}
}

func (*SupportTicketFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{TicketMessage, TicketStatus}}
}

func (*SupportTicketFlow) GetTicketMessage(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	message, err := TicketMessage.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: message}, nil
}

type receiveTicketStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (receiveTicketStep) Execute(ctx dex.Context, message string) (*dex.StepDecision, error) {
	if err := TicketMessage.Set(ctx, message); err != nil {
		return nil, err
	}
	if err := TicketStatus.Set(ctx, "received"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("received"), nil
}

var _ dex.Flow = (*SupportTicketFlow)(nil)
