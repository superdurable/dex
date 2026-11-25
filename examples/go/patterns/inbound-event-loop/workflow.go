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

package inboundeventloop

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	InactivityTimeout      = 24 * time.Hour
	MaxReplyLength         = 4000
	CurrentTranscriptChunk = "current"
	TranscriptChunkSize    = 50
)

type TicketReply struct {
	Sequence uint64 `json:"sequence"`
	Text     string `json:"text"`
}

type TranscriptChunk struct {
	Replies []TicketReply `json:"replies"`
}

var (
	TicketReplies     = dex.DefineChannel[string]("ticketReplies")
	TicketResolutions = dex.DefineChannel[string]("ticketResolutions")
	TranscriptChunks  = dex.DefineAttributeMap[TranscriptChunk]("transcriptChunks")
)

// SupportTicketConversationFlow records ticket replies until a resolution arrives or the ticket goes quiet.
type SupportTicketConversationFlow struct{ dex.FlowDefaults }

func NewSupportTicketConversationFlow() *SupportTicketConversationFlow {
	return &SupportTicketConversationFlow{}
}

func (*SupportTicketConversationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitTicketMessage{}),
		dex.DefineStep(CloseResolvedTicket{}),
		dex.DefineStep(CloseInactiveTicket{}),
	}
}

func (flow *SupportTicketConversationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SendTicketReply, nil),
		dex.DefineRPC(flow.ResolveTicket, nil),
	}
}

func (*SupportTicketConversationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Channels:   []dex.ChannelDef{TicketReplies, TicketResolutions},
		Attributes: []dex.AttributeDef{TranscriptChunks},
	}
}

// SendTicketReply validates one reply and queues it for the waiting Step.
func (*SupportTicketConversationFlow) SendTicketReply(ctx dex.Context, text string) (*dex.RPCResult[dex.None], error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("reply text is required")
	}
	if utf8.RuneCountInString(text) > MaxReplyLength {
		return nil, fmt.Errorf("reply text exceeds %d characters", MaxReplyLength)
	}
	if err := TicketReplies.Publish(ctx, text); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// ResolveTicket queues the resolution; an empty reason would mean no resolution.
func (*SupportTicketConversationFlow) ResolveTicket(ctx dex.Context, reason string) (*dex.RPCResult[dex.None], error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("resolution reason is required")
	}
	if err := TicketResolutions.Publish(ctx, reason); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// AwaitTicketMessage waits for replies, a resolution, or inactivity; the input is a pending resolution.
//
// dex:group group-id:inbound group-label:"Inbound messages"
// dex:explanation text:"Record each batch of replies, or route on the resolution or the inactivity Timer."
type AwaitTicketMessage struct{ dex.StepDefaults }

func (AwaitTicketMessage) WaitFor(_ dex.Context, pendingResolution string) (*dex.Wait, error) {
	if pendingResolution != "" {
		return dex.Until(TicketReplies.AtLeast(1)), nil
	}
	// Replies come before the resolution, so earlier replies are recorded first.
	return dex.AnyOf(
		TicketReplies.AtLeast(1),
		TicketResolutions.ForOne(),
		dex.Timer(InactivityTimeout),
	), nil
}

func (AwaitTicketMessage) Execute(ctx dex.Context, pendingResolution string) (*dex.StepDecision, error) {
	replies, err := TicketReplies.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(replies) > 0 {
		if err := appendToTranscript(ctx, replies); err != nil {
			return nil, err
		}
		if pendingResolution != "" {
			return dex.GoTo(CloseResolvedTicket{}, pendingResolution), nil
		}
		// A new execution registers a new wait, so the Timer restarts.
		return dex.GoTo(AwaitTicketMessage{}, ""), nil
	}
	resolutions, err := TicketResolutions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(resolutions) > 0 {
		return dex.GoTo(CloseResolvedTicket{}, resolutions[0]), nil
	}
	if ctx.HasTimerFired() {
		return dex.GoTo(CloseInactiveTicket{}, nil), nil
	}
	return nil, errors.New("no Condition of the ticket wait completed")
}

func (AwaitTicketMessage) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteLoadAttributeMapInstances: []dex.AttributeMapLoad{
			TranscriptChunks.Load(CurrentTranscriptChunk),
		},
	}
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Close the resolved ticket once no reply is left to record."
type CloseResolvedTicket struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (CloseResolvedTicket) Execute(_ dex.Context, reason string) (*dex.StepDecision, error) {
	return dex.ForceCompleteIfChannelsEmpty(
		"resolved: "+reason,
		[]dex.ChannelDef{TicketReplies},
		dex.MovementOf(AwaitTicketMessage{}, reason),
	), nil
}

// dex:group group-id:inactivity group-label:"Inactivity"
// dex:explanation text:"Close the quiet ticket once no reply or resolution is waiting."
type CloseInactiveTicket struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (CloseInactiveTicket) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.ForceCompleteIfChannelsEmpty(
		"closed after inactivity",
		[]dex.ChannelDef{TicketReplies, TicketResolutions},
		dex.MovementOf(AwaitTicketMessage{}, ""),
	), nil
}

// appendToTranscript appends replies to the current chunk and archives each full chunk.
func appendToTranscript(ctx dex.Context, texts []string) error {
	chunk, err := TranscriptChunks.Get(ctx, CurrentTranscriptChunk)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		chunk, err = TranscriptChunk{}, nil
	}
	if err != nil {
		return err
	}
	nextSequence := uint64(1)
	if len(chunk.Replies) > 0 {
		nextSequence = chunk.Replies[len(chunk.Replies)-1].Sequence + 1
	}
	for _, text := range texts {
		if len(chunk.Replies) == TranscriptChunkSize {
			archiveToken := fmt.Sprintf("%020d", chunk.Replies[0].Sequence)
			if err := TranscriptChunks.Set(ctx, archiveToken, chunk); err != nil {
				return err
			}
			chunk = TranscriptChunk{}
		}
		chunk.Replies = append(chunk.Replies, TicketReply{Sequence: nextSequence, Text: text})
		nextSequence++
	}
	return TranscriptChunks.Set(ctx, CurrentTranscriptChunk, chunk)
}

var (
	_ dex.Flow                  = (*SupportTicketConversationFlow)(nil)
	_ dex.RPC[string, dex.None] = (*SupportTicketConversationFlow)(nil).SendTicketReply
	_ dex.RPC[string, dex.None] = (*SupportTicketConversationFlow)(nil).ResolveTicket
)
