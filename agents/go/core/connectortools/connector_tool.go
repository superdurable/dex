// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ApprovalPredicate decides from a call's decoded JSON arguments whether that call waits for
// approval. JSON numbers decode as float64.
//
// Since 0.6.16.
type ApprovalPredicate func(arguments map[string]any) bool

// ConnectorToolOption configures one ConnectorTool. Pass options to NewConnectorTool.
//
// Since 0.6.16.
type ConnectorToolOption interface {
	applyConnectorTool(options *connectorToolOptions)
}

type connectorToolOptions struct {
	name              string
	description       string
	requiresApproval  *bool
	approvalPredicate ApprovalPredicate
	maxResultBytes    *int
	manifest          *sendCapManifest
}

type connectorToolOptionFunc func(options *connectorToolOptions)

func (option connectorToolOptionFunc) applyConnectorTool(options *connectorToolOptions) {
	option(options)
}

// WithToolName sets the tool name the model calls.
//
// The default is DefaultConnectorToolName, such as stripe_create_refund. Stored
// calls find their tool by this name, so keep it across deploys; when a
// release changes a default name, pass the earlier name to keep running
// sessions working. Give each connection of one operation its own name. An
// empty name keeps the default.
//
// Since 0.6.16.
func WithToolName(name string) ConnectorToolOption {
	return connectorToolOptionFunc(func(options *connectorToolOptions) {
		options.name = name
	})
}

// WithToolDescription sets the model-facing description. The default is the agent metadata's
// Description. An empty description keeps the default.
//
// Since 0.6.16.
func WithToolDescription(description string) ConnectorToolOption {
	return connectorToolOptionFunc(func(options *connectorToolOptions) {
		options.description = description
	})
}

// WithRequiresApproval overrides the metadata's approval recommendation for every call.
//
// A session tool policy with RequiresApproval set still wins. The last of
// WithRequiresApproval and WithApprovalPredicate applies.
//
// Since 0.6.16.
func WithRequiresApproval(requiresApproval bool) ConnectorToolOption {
	return connectorToolOptionFunc(func(options *connectorToolOptions) {
		options.requiresApproval = &requiresApproval
		options.approvalPredicate = nil
	})
}

// WithApprovalPredicate decides per call, from the decoded arguments, whether the call waits for
// approval, such as refunds above a limit.
//
// A predicate that panics requires approval. Arguments that break the schema
// never run, so they never wait for approval. A session tool policy with
// RequiresApproval set still wins. The last of WithRequiresApproval and
// WithApprovalPredicate applies.
//
//	connectortools.WithApprovalPredicate(func(arguments map[string]any) bool {
//		amount, _ := arguments["amount"].(float64)
//		return amount > 5000
//	})
//
// Since 0.6.16.
func WithApprovalPredicate(predicate ApprovalPredicate) ConnectorToolOption {
	return connectorToolOptionFunc(func(options *connectorToolOptions) {
		options.approvalPredicate = predicate
		options.requiresApproval = nil
	})
}

// WithMaxResultBytes bounds the result the model reads, in bytes of UTF-8 text. The default is the
// source's bound. It must be positive.
//
// Since 0.6.16.
func WithMaxResultBytes(maxResultBytes int) ConnectorToolOption {
	return connectorToolOptionFunc(func(options *connectorToolOptions) {
		options.maxResultBytes = &maxResultBytes
	})
}

// ConnectorTool is one Connector operation offered to an agent as a tool. Create it with
// NewConnectorTool and pass it to NewConnectorToolSource. It is immutable and safe for concurrent
// use when its operation is.
//
// Since 0.6.16.
type ConnectorTool struct {
	agentTool             sdkgo.AgentTool
	metadata              sdkgo.AgentToolMetadata
	name                  string
	description           string
	inputSchema           inputSchema
	effect                dexagents.ToolEffect
	maxSends              int32
	requiredBranches      []sdkgo.BranchID
	requiresApproval      *bool
	approvalPredicate     ApprovalPredicate
	maxResultBytes        int
	executeMethodTimeout  time.Duration
	retryPolicy           *dex.RetryPolicy
	isDefinitionLimited   bool
	isApprovalRecommended bool
}

// NewConnectorTool offers one reviewed Connector operation to an agent as a tool.
//
// tool is the operation bound to its connection, built with
// sdkgo.NewQueryAgentTool or sdkgo.NewMutationAgentTool from the agent
// metadata that connectorctl generates for an operation reviewed for agents.
// The name, description, argument schema, effect, and approval default come
// from that metadata.
//
// The metadata decides how the call runs. A Query is read and runs in
// CallTool. A Mutation with IsRetrySafe is idempotent_write and runs in
// CallTool, whose retries stay inside the operation's own Execute retry
// policy, at most three attempts, and timeout. Any other Mutation is
// non_idempotent_write and runs in CallSingleAttemptTool, once. The effect
// always comes from the metadata; a session tool policy for the tool changes
// only its approval and, for a bounded resend, its send cap through
// ToolPolicy.MaxSends.
//
// A Mutation whose RepeatOnUnknown is limited resends a bounded number of
// times. Each call pins its send cap, the metadata's MaxSends or the tool
// policy's MaxSends, in its tool source reference. With a cap of 2 or more,
// CallSingleAttemptTool gets the operation's attempts, at least the cap. When
// a send's outcome is unknown, such as after a timeout or a 5xx, the
// connector reads back when it can and resends while the call's sends stay
// below the cap. A definite failure is never resent. Every attempt passes the
// pinned cap to the connector with sdkgo.WithMaxSends, also after a deploy
// that changes the metadata. A cap outside 2 to 10 runs the call once.
//
// Approval: a call waits for resolve_interrupt when the metadata says
// IsApprovalRecommended. WithRequiresApproval and WithApprovalPredicate
// override that, and a session tool policy with RequiresApproval overrides
// both.
//
// It returns an error when tool is nil, its InputSchema uses a keyword the
// agent core cannot check, or WithMaxResultBytes is not positive.
//
// Since 0.6.16.
func NewConnectorTool(tool sdkgo.AgentTool, options ...ConnectorToolOption) (*ConnectorTool, error) {
	if tool == nil {
		return nil, errors.New("connector tool needs an sdkgo.AgentTool")
	}
	metadata := tool.Metadata()
	manifest := sendCapManifest{repeatOnUnknown: string(metadata.RepeatOnUnknown)}
	if metadata.MaxSends != 0 {
		manifest.maxSends = metadata.MaxSends
	}
	configured := connectorToolOptions{manifest: &manifest}
	for _, option := range options {
		option.applyConnectorTool(&configured)
	}
	return newConnectorTool(tool, metadata, configured)
}

// newConnectorTool reads the send cap from the manifest's agent block and the definition apart,
// as the Python and TypeScript cores do; sdkgo makes them agree.
func newConnectorTool(tool sdkgo.AgentTool, metadata sdkgo.AgentToolMetadata, configured connectorToolOptions) (*ConnectorTool, error) {
	label := metadata.Operation.ConnectorID + " " + metadata.Operation.OperationID
	schema, err := parseInputSchema(metadata.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	maxResultBytes := 0
	if configured.maxResultBytes != nil {
		if *configured.maxResultBytes < 1 {
			return nil, errors.New("max result bytes must be positive")
		}
		maxResultBytes = *configured.maxResultBytes
	}
	name := configured.name
	if name == "" {
		name = DefaultConnectorToolName(metadata.Operation)
	}
	description := configured.description
	if description == "" {
		description = metadata.Description
	}
	var sendCap int32
	if !metadata.IsRetrySafe {
		sendCap = connectorSendCap(metadata.Kind, *configured.manifest, metadata.RepeatOnUnknown, metadata.MaxSends)
	}
	required := []sdkgo.BranchID{}
	for _, branch := range metadata.Branches {
		if !branch.Optional {
			required = append(required, branch.ID)
		}
	}
	return &ConnectorTool{
		agentTool:             tool,
		metadata:              metadata,
		name:                  name,
		description:           description,
		inputSchema:           schema,
		effect:                dexagents.ToolEffect(metadata.Effect()),
		maxSends:              sendCap,
		requiredBranches:      required,
		requiresApproval:      configured.requiresApproval,
		approvalPredicate:     configured.approvalPredicate,
		maxResultBytes:        maxResultBytes,
		executeMethodTimeout:  metadata.StepDefaults.ExecuteMethodTimeout,
		retryPolicy:           toolRetryPolicy(metadata.StepDefaults, sendCap),
		isDefinitionLimited:   metadata.Kind == sdkgo.AgentOperationKindMutation && metadata.RepeatOnUnknown == sdkgo.RepeatOnUnknownLimited,
		isApprovalRecommended: metadata.IsApprovalRecommended,
	}, nil
}

// Name returns the tool name the model calls.
//
// Since 0.6.16.
func (tool *ConnectorTool) Name() string { return tool.name }

// Description returns the tool description for the model.
//
// Since 0.6.16.
func (tool *ConnectorTool) Description() string { return tool.description }

// InputSchema returns a copy of the JSON Schema of the arguments, the metadata's InputSchema.
//
// Since 0.6.16.
func (tool *ConnectorTool) InputSchema() json.RawMessage {
	return bytes.Clone(tool.metadata.InputSchema)
}

// Operation returns the Connector operation the tool runs.
//
// Since 0.6.16.
func (tool *ConnectorTool) Operation() sdkgo.OperationRef { return tool.metadata.Operation }

// Kind returns whether the operation is a Query or a Mutation.
//
// Since 0.6.16.
func (tool *ConnectorTool) Kind() sdkgo.AgentOperationKind { return tool.metadata.Kind }

// Connection returns the logical connection the operation runs with.
//
// Since 0.6.16.
func (tool *ConnectorTool) Connection() sdkgo.ConnectionRef { return tool.agentTool.Connection() }

// Effect returns read for a Query, idempotent_write for a Mutation with IsRetrySafe, and
// non_idempotent_write for any other Mutation, including one that resends a bounded number of times.
//
// Since 0.6.16.
func (tool *ConnectorTool) Effect() dexagents.ToolEffect { return tool.effect }

// MaxSends returns the send cap of a Mutation that may resend a write whose outcome is unknown, or
// zero for every other tool.
//
// It is the metadata's MaxSends of a Mutation whose RepeatOnUnknown is
// limited with a cap from 2 to 10. Such a tool keeps non_idempotent_write;
// each call pins its cap in its tool source reference and runs in
// CallSingleAttemptTool with up to RetryPolicy's attempts.
//
// Since 0.6.16.
func (tool *ConnectorTool) MaxSends() int32 { return tool.maxSends }

// IsApprovalRecommended reports whether the metadata recommends approval for every call.
//
// Since 0.6.16.
func (tool *ConnectorTool) IsApprovalRecommended() bool { return tool.isApprovalRecommended }

// ExecuteMethodTimeout returns the operation's Execute timeout, or zero for the tool Step's default.
//
// Since 0.6.16.
func (tool *ConnectorTool) ExecuteMethodTimeout() time.Duration { return tool.executeMethodTimeout }

// RetryPolicy returns a copy of the tool Step's retry policy, from the operation's Execute retry
// policy.
//
// A read or idempotent_write tool gets at most 3 attempts. A tool with
// MaxSends gets the operation's attempts, or 3 when they are unlimited, and
// at least MaxSends.
//
// Since 0.6.16.
func (tool *ConnectorTool) RetryPolicy() *dex.RetryPolicy {
	policy := *tool.retryPolicy
	return &policy
}

// isApprovalRequired returns the application's approval choice, or the metadata's recommendation.
func (tool *ConnectorTool) isApprovalRequired(arguments string) (isRequired bool) {
	if tool.requiresApproval != nil {
		return *tool.requiresApproval
	}
	if tool.approvalPredicate == nil {
		return tool.isApprovalRecommended
	}
	values, err := decodeArgumentsObject(arguments)
	if err != nil {
		return false
	}
	if tool.inputSchema.findInputViolation(values) != "" {
		// The tool Step refuses these arguments before the operation runs.
		return false
	}
	var predicateArguments map[string]any
	if json.Unmarshal([]byte(argumentsOrEmptyObject(arguments)), &predicateArguments) != nil {
		return false
	}
	defer func() {
		if recover() != nil {
			isRequired = true
		}
	}()
	return tool.approvalPredicate(predicateArguments)
}
