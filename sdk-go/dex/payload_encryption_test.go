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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestPayloadEncryptionOutboundValueFieldAudit fails when a sent message gains a Value field
// that the SDK neither seals nor deliberately keeps plaintext.
func TestPayloadEncryptionOutboundValueFieldAudit(t *testing.T) {
	sealed := "sealed"
	expected := map[string]string{
		"InvokeWaitForMethodResponse.upsert_attributes.value":                                        sealed,
		"InvokeWaitForMethodResponse.waiting_condition.sub_flow_conditions.step_input":               sealed,
		"InvokeWaitForMethodResponse.waiting_condition.sub_flow_conditions.options.attributes.value": sealed,
		"InvokeWaitForMethodResponse.upsert_step_exe_locals.value":                                   sealed,
		"InvokeWaitForMethodResponse.record_events.value":                                            sealed,
		"InvokeWaitForMethodResponse.publish_to_channel.value":                                       sealed,
		"InvokeWaitForMethodResponse.action_permission_mappings.mappings.equal_values":               "plaintext: the server compares them",
		"InvokeExecuteMethodResponse.step_decision.next_steps.step_input":                            sealed,
		"InvokeExecuteMethodResponse.step_decision.close_decision.close_input":                       "sealed, or error text for ForceFail",
		"InvokeExecuteMethodResponse.upsert_attributes.value":                                        sealed,
		"InvokeExecuteMethodResponse.record_events.value":                                            sealed,
		"InvokeExecuteMethodResponse.upsert_step_exe_locals.value":                                   sealed,
		"InvokeExecuteMethodResponse.publish_to_channel.value":                                       sealed,
		"InvokeExecuteMethodResponse.action_permission_mappings.mappings.equal_values":               "plaintext: the server compares them",
		"InvokeWorkerRPCResponse.output":                                                             sealed,
		"InvokeWorkerRPCResponse.step_decision.next_steps.step_input":                                sealed,
		"InvokeWorkerRPCResponse.step_decision.close_decision.close_input":                           "not sent: RPCs return only next Steps",
		"InvokeWorkerRPCResponse.upsert_attributes.value":                                            sealed,
		"InvokeWorkerRPCResponse.record_events.value":                                                sealed,
		"InvokeWorkerRPCResponse.publish_to_channel.value":                                           sealed,
		"InvokeWorkerRPCResponse.action_permission_mappings.mappings.equal_values":                   "plaintext: the server compares them",
		"StepStreamWrite.value":                                sealed,
		"StepMethodHeartbeat.value":                            sealed,
		"StartFlowRequest.step_input":                          sealed,
		"StartFlowRequest.flow_start_options.attributes.value": sealed,
		"StopFlowRequest.output":                               sealed,
		"CloneForkFlowRequest.attributes.value":                sealed,
		"InvokeRPCRequest.input":                               sealed,
		"WriteStreamRequest.value":                             sealed,
		"WaitForAttributeRequest.match.operand":                "plaintext: the server compares it",
	}
	messages := []proto.Message{
		&dexpb.InvokeWaitForMethodResponse{},
		&dexpb.InvokeExecuteMethodResponse{},
		&dexpb.InvokeWorkerRPCResponse{},
		&dexpb.StepStreamWrite{},
		&dexpb.StepMethodHeartbeat{},
		&dexpb.StartFlowRequest{},
		&dexpb.StopFlowRequest{},
		&dexpb.CloneForkFlowRequest{},
		&dexpb.InvokeRPCRequest{},
		&dexpb.WriteStreamRequest{},
		&dexpb.WaitForAttributeRequest{},
	}
	found := map[string]struct{}{}
	for _, message := range messages {
		descriptor := message.ProtoReflect().Descriptor()
		collectValueFieldPaths(descriptor, string(descriptor.Name()), map[protoreflect.FullName]bool{}, found)
	}
	foundPaths := make([]string, 0, len(found))
	for path := range found {
		foundPaths = append(foundPaths, path)
	}
	expectedPaths := make([]string, 0, len(expected))
	for path := range expected {
		expectedPaths = append(expectedPaths, path)
	}
	sort.Strings(foundPaths)
	sort.Strings(expectedPaths)
	require.Equal(t, expectedPaths, foundPaths)
}

func collectValueFieldPaths(
	descriptor protoreflect.MessageDescriptor,
	prefix string,
	visiting map[protoreflect.FullName]bool,
	paths map[string]struct{},
) {
	if visiting[descriptor.FullName()] {
		return
	}
	visiting[descriptor.FullName()] = true
	defer delete(visiting, descriptor.FullName())
	fields := descriptor.Fields()
	for index := range fields.Len() {
		field := fields.Get(index)
		message := field.Message()
		if field.IsMap() {
			message = field.MapValue().Message()
		}
		if message == nil || message.ParentFile().Package() != "dex" {
			continue
		}
		path := prefix + "." + string(field.Name())
		if message.FullName() == "dex.Value" {
			paths[path] = struct{}{}
			continue
		}
		collectValueFieldPaths(message, path, visiting, paths)
	}
}

// recordingPayloadCodec wraps a codec and records the contexts it receives.
type recordingPayloadCodec struct {
	PayloadCodec
	mu             sync.Mutex
	encodeContexts []PayloadContext
	encodedValues  int
	decodeContexts []PayloadContext
}

func (codec *recordingPayloadCodec) Encode(
	ctx context.Context,
	payloadContext PayloadContext,
	plaintexts [][]byte,
) ([]*dexpb.PayloadEnvelope, error) {
	codec.mu.Lock()
	codec.encodeContexts = append(codec.encodeContexts, payloadContext)
	codec.encodedValues += len(plaintexts)
	codec.mu.Unlock()
	return codec.PayloadCodec.Encode(ctx, payloadContext, plaintexts)
}

func (codec *recordingPayloadCodec) Decode(
	ctx context.Context,
	payloadContext PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
) ([][]byte, error) {
	codec.mu.Lock()
	codec.decodeContexts = append(codec.decodeContexts, payloadContext)
	codec.mu.Unlock()
	return codec.PayloadCodec.Decode(ctx, payloadContext, envelopes)
}

func newTestPayloadKeyFile(t *testing.T, currentKeyID string, keyIDs ...string) string {
	t.Helper()
	keys := map[string]string{}
	for _, keyID := range keyIDs {
		key := make([]byte, 32)
		_, err := rand.Read(key)
		require.NoError(t, err)
		keys[keyID] = base64.StdEncoding.EncodeToString(key)
	}
	content, err := json.Marshal(map[string]any{"current_key_id": currentKeyID, "keys": keys})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "keys.json")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func newTestRecordingPayloadEncryption(t *testing.T, options PayloadEncryptionOptions) (*payloadEncryption, *recordingPayloadCodec) {
	t.Helper()
	keys, err := NewFilePayloadEncryptionKeyProvider(newTestPayloadKeyFile(t, "k1", "k1"))
	require.NoError(t, err)
	codec, err := NewAESGCMPayloadCodec(keys, AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	recording := &recordingPayloadCodec{PayloadCodec: codec}
	options.Codec = recording
	encryption, err := newPayloadEncryption(options)
	require.NoError(t, err)
	return encryption, recording
}

func stringValue(text string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}}
}

func isEnvelopeValue(value *dexpb.Value) bool {
	return value.GetObjValue().GetEncoding() == payloadEnvelopeMarker
}

type payloadEncryptionTestFlow struct {
	FlowDefaults
}

var (
	payloadEncryptionTestSecret = DefineAttribute[string]("secret")
	payloadEncryptionTestStatus = DefineAttribute[string]("status", Plaintext())
	payloadEncryptionTestNotes  = DefineAttributeMap[string]("notes", Plaintext())
	payloadEncryptionTestTag    = DefineAttribute[string]("tag", Indexed(AttributeIndex{Type: IndexKeyword}))
)

func (payloadEncryptionTestFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(payloadEncryptionTestStep{})}
}

func (payloadEncryptionTestFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{Attributes: []AttributeDef{
		payloadEncryptionTestSecret, payloadEncryptionTestStatus, payloadEncryptionTestNotes, payloadEncryptionTestTag,
	}}
}

type payloadEncryptionTestStep struct {
	StepDefaultsNoWaitFor[string]
}

func (payloadEncryptionTestStep) Execute(Context, string) (*StepDecision, error) {
	return DeadEnd(), nil
}

func TestPayloadEncryptionSealsWorkerResults(t *testing.T) {
	registry, err := NewRegistry([]Flow{payloadEncryptionTestFlow{}})
	require.NoError(t, err)
	flow, found := registry.lookupFlow("payloadEncryptionTestFlow")
	require.True(t, found)
	encryption, codec := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{})
	service := newWorkerService(registry, concreteValueHydrator{}, nil, &negotiatedServerProtocol{}, encryption)
	metadata := workerStepContext()

	null := &dexpb.Value{Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE}}
	sharedInitial := stringValue("initial")
	response := &dexpb.InvokeWaitForMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{
			{Key: "secret", Value: stringValue("s")},
			{Key: "status", Value: stringValue("open")},
			{Key: "notes/n1", Value: stringValue("note")},
			{Key: "tag", Value: stringValue("t"), IndexConfig: &dexpb.IndexConfig{Enable: true}},
			{Key: "secret", Value: null},
		},
		UpsertStepExeLocals: []*dexpb.KV{{Key: "local", Value: &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 7}}}},
		RecordEvents:        []*dexpb.KV{{Key: "event", Value: &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: true}}}},
		PublishToChannel:    []*dexpb.ChannelMessage{{ChannelName: "channel", Value: stringValue("message")}},
		WaitingCondition: &dexpb.WaitingCondition{SubFlowConditions: []*dexpb.SubFlowCondition{
			{
				SubFlowType:  "payloadEncryptionTestFlow",
				SubFlowIndex: 1,
				StepInput:    stringValue("child input"),
				Options: &dexpb.SubFlowOptions{Attributes: []*dexpb.AttributeWrite{
					{Key: "secret", Value: sharedInitial},
					{Key: "status", Value: stringValue("child status")},
				}},
			},
		}},
	}
	require.NoError(t, service.sealStepResult(context.Background(), flow, metadata, workerStepResultFields{
		attributeWrites: response.UpsertAttributes,
		locals:          response.UpsertStepExeLocals,
		recordedEvents:  response.RecordEvents,
		publications:    response.PublishToChannel,
		waiting:         response.WaitingCondition,
	}))
	require.True(t, isEnvelopeValue(response.UpsertAttributes[0].Value))
	require.Equal(t, "open", response.UpsertAttributes[1].Value.GetStringValue())
	require.Equal(t, "note", response.UpsertAttributes[2].Value.GetStringValue())
	require.Equal(t, "t", response.UpsertAttributes[3].Value.GetStringValue())
	require.Same(t, null, response.UpsertAttributes[4].Value)
	require.True(t, isEnvelopeValue(response.UpsertStepExeLocals[0].Value))
	require.True(t, isEnvelopeValue(response.RecordEvents[0].Value))
	require.True(t, isEnvelopeValue(response.PublishToChannel[0].Value))
	subFlow := response.WaitingCondition.SubFlowConditions[0]
	require.True(t, isEnvelopeValue(subFlow.StepInput))
	require.True(t, isEnvelopeValue(subFlow.Options.Attributes[0].Value))
	require.Equal(t, "child status", subFlow.Options.Attributes[1].Value.GetStringValue())
	require.Equal(t, "initial", sharedInitial.GetStringValue(), "sealing must not mutate shared values")
	require.Equal(t, []PayloadContext{
		{FlowType: "payloadEncryptionTestFlow", FlowID: "flow-1"},
		{FlowType: "payloadEncryptionTestFlow", FlowID: "SubFlow:flow-1-waiting-1-1"},
	}, codec.encodeContexts)
	require.Equal(t, 6, codec.encodedValues)

	execute := &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
			CloseInput:        stringValue("card declined"),
		},
	}}
	require.NoError(t, service.sealStepResult(context.Background(), flow, metadata, workerStepResultFields{
		decision: execute.StepDecision,
	}))
	reason := execute.StepDecision.CloseDecision.CloseInput.GetStringValue()
	require.True(t, strings.HasPrefix(reason, payloadEncodedTextPrefix), reason)
	decoded, err := encryption.decodeErrorText(context.Background(), PayloadContext{FlowID: "flow-1"}, reason)
	require.NoError(t, err)
	require.Equal(t, "card declined", decoded)
}

func TestPayloadEncryptionHonorsPlaintextOptions(t *testing.T) {
	encryption, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{
		PlaintextStringValues:    true,
		PlaintextPrimitiveValues: true,
	})
	text := stringValue("clear")
	number := &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: 1.5}}
	object := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte("{}")}}}
	reserved := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "dex/2"}}}
	slots := newOutgoingPayloadSlots(nil)
	for _, valuePointer := range []**dexpb.Value{&text, &number, &object} {
		slots.addValue(PayloadContext{FlowID: "flow-1"}, "", valuePointer)
	}
	require.NoError(t, slots.seal(context.Background(), encryption))
	require.Equal(t, "clear", text.GetStringValue())
	require.Equal(t, 1.5, number.GetDoubleValue())
	require.True(t, isEnvelopeValue(object))

	reservedSlots := newOutgoingPayloadSlots(nil)
	reservedSlots.addValue(PayloadContext{FlowID: "flow-1"}, "", &reserved)
	require.ErrorContains(t, reservedSlots.seal(context.Background(), encryption), "reserved for Dex")
}

func TestPayloadEncryptionErrorTextRules(t *testing.T) {
	ctx := context.Background()
	payloadContext := PayloadContext{FlowID: "flow-1"}
	disabled := newDisabledPayloadEncryption()
	for text, want := range map[string]string{
		"":                       "",
		"plain":                  "plain",
		"dex-encoded: retry now": " dex-encoded: retry now",
	} {
		got, err := disabled.encodeErrorText(ctx, payloadContext, text)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	plaintextErrors, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{PlaintextErrorText: true})
	got, err := plaintextErrors.encodeErrorText(ctx, payloadContext, "plain")
	require.NoError(t, err)
	require.Equal(t, "plain", got)
	got, err = plaintextErrors.encodeErrorText(ctx, payloadContext, "dex-encoded: retry now")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, payloadEncodedTextPrefix))
	decoded, err := plaintextErrors.decodeErrorText(ctx, payloadContext, got)
	require.NoError(t, err)
	require.Equal(t, "dex-encoded: retry now", decoded)

	_, err = disabled.decodeErrorText(ctx, payloadContext, got)
	require.ErrorIs(t, err, ErrPayloadCodecNotConfigured)
	require.NotContains(t, err.Error(), got[len(payloadEncodedTextPrefix):])
}

func TestPayloadEncryptionWorkerErrorText(t *testing.T) {
	encryption, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{})
	payloadContext := PayloadContext{FlowType: "OrderFlow", FlowID: "flow-1"}

	userError := &dexpb.WorkerErrorResponse{Detail: "card 4242 declined", StackTrace: "main.go:1", ErrorType: "*errors.errorString"}
	encodeWorkerErrorText(context.Background(), resolveLogger(nil, nil), encryption, payloadContext, fmt.Errorf("card 4242 declined"), userError)
	require.True(t, strings.HasPrefix(userError.Detail, payloadEncodedTextPrefix))
	require.True(t, strings.HasPrefix(userError.StackTrace, payloadEncodedTextPrefix))
	require.Equal(t, "*errors.errorString", userError.ErrorType)
	detail, err := encryption.decodeErrorText(context.Background(), payloadContext, userError.Detail)
	require.NoError(t, err)
	require.Equal(t, "card 4242 declined", detail)

	hydrationError := &dexpb.WorkerErrorResponse{Detail: "dex: LoadBlobs omitted blobs; zd1|x|1-0"}
	encodeWorkerErrorText(
		context.Background(), resolveLogger(nil, nil), encryption, payloadContext,
		markSDKGeneratedWorkerError(fmt.Errorf("dex: LoadBlobs omitted blobs")), hydrationError,
	)
	require.Equal(t, "dex: LoadBlobs omitted blobs; zd1|x|1-0", hydrationError.Detail)

	empty := &dexpb.WorkerErrorResponse{}
	encodeWorkerErrorText(context.Background(), resolveLogger(nil, nil), encryption, payloadContext, fmt.Errorf("empty"), empty)
	require.Empty(t, empty.Detail)
	require.Empty(t, empty.StackTrace)
}

const payloadProviderSentinel = "SENTINEL-SENSITIVE-PROVIDER-TEXT"

var errPayloadProviderSentinel = errors.New(payloadProviderSentinel)

// sentinelPayloadKeyProvider fails with provider text that must never leave the application.
type sentinelPayloadKeyProvider struct {
	keys              PayloadEncryptionKeyProvider
	shouldFailCurrent bool
	shouldFailKey     bool
}

func (provider sentinelPayloadKeyProvider) CurrentKeyID(ctx context.Context, payloadContext PayloadContext) (string, error) {
	if provider.shouldFailCurrent {
		return "", errPayloadProviderSentinel
	}
	return provider.keys.CurrentKeyID(ctx, payloadContext)
}

func (provider sentinelPayloadKeyProvider) Key(ctx context.Context, payloadContext PayloadContext, keyID string) ([]byte, error) {
	if provider.shouldFailKey {
		return nil, fmt.Errorf("vault said %w", errPayloadProviderSentinel)
	}
	return provider.keys.Key(ctx, payloadContext, keyID)
}

type capturingPayloadLogger struct {
	mu    sync.Mutex
	lines []string
}

func (logger *capturingPayloadLogger) record(message string, keyvals []any) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.lines = append(logger.lines, fmt.Sprint(append([]any{message}, keyvals...)...))
}

func (logger *capturingPayloadLogger) Debug(message string, keyvals ...any) {
	logger.record(message, keyvals)
}
func (logger *capturingPayloadLogger) Info(message string, keyvals ...any) {
	logger.record(message, keyvals)
}
func (logger *capturingPayloadLogger) Warn(message string, keyvals ...any) {
	logger.record(message, keyvals)
}
func (logger *capturingPayloadLogger) Error(message string, keyvals ...any) {
	logger.record(message, keyvals)
}

func (logger *capturingPayloadLogger) text() string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return strings.Join(logger.lines, "\n")
}

func newSentinelPayloadEncryption(t *testing.T, keyFile string, shouldFailCurrent bool, shouldFailKey bool) *payloadEncryption {
	t.Helper()
	keys, err := NewFilePayloadEncryptionKeyProvider(keyFile)
	require.NoError(t, err)
	codec, err := NewAESGCMPayloadCodec(sentinelPayloadKeyProvider{
		keys: keys, shouldFailCurrent: shouldFailCurrent, shouldFailKey: shouldFailKey,
	}, AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: codec})
	require.NoError(t, err)
	return encryption
}

// workerFrameForError builds the WorkerErrorResponse that executeTask sends for err.
func workerFrameForError(
	t *testing.T,
	logger Logger,
	encryption *payloadEncryption,
	err error,
) *dexpb.WorkerErrorResponse {
	t.Helper()
	finalErr := finishWorkerCall(logger, nil, err)
	rpcStatus, ok := status.FromError(finalErr)
	require.True(t, ok)
	workerError := &dexpb.WorkerErrorResponse{Detail: rpcStatus.Message()}
	for _, detail := range rpcStatus.Details() {
		if typed, isWorkerError := detail.(*dexpb.WorkerErrorResponse); isWorkerError {
			workerError = typed
		}
	}
	encodeWorkerErrorText(context.Background(), logger, encryption, PayloadContext{FlowID: "flow-1"}, err, workerError)
	return workerError
}

func requireNoProviderSentinel(t *testing.T, texts ...string) {
	t.Helper()
	for _, text := range texts {
		require.NotContains(t, text, payloadProviderSentinel)
	}
}

func TestPayloadEncryptionNeverLeaksKeyProviderErrorText(t *testing.T) {
	keyFile := newTestPayloadKeyFile(t, "k1", "k1")
	good := newSentinelPayloadEncryption(t, keyFile, false, false)
	encrypted := stringValue("secret")
	slots := newOutgoingPayloadSlots(nil)
	slots.addValue(PayloadContext{FlowID: "flow-1"}, "Step input", &encrypted)
	require.NoError(t, slots.seal(context.Background(), good))

	registry, err := NewRegistry([]Flow{payloadEncryptionTestFlow{}})
	require.NoError(t, err)
	flow, _ := registry.lookupFlow("payloadEncryptionTestFlow")
	for name, encryption := range map[string]*payloadEncryption{
		"Key fails":          newSentinelPayloadEncryption(t, keyFile, false, true),
		"CurrentKeyID fails": newSentinelPayloadEncryption(t, keyFile, true, false),
	} {
		t.Run(name, func(t *testing.T) {
			logger := &capturingPayloadLogger{}
			service := newWorkerService(registry, concreteValueHydrator{}, logger, &negotiatedServerProtocol{}, encryption)

			// Client decryption and encryption errors.
			received := encrypted
			clientDecryptErr := encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
				PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&received},
			))
			sent := stringValue("secret")
			clientSlots := newOutgoingPayloadSlots(nil)
			clientSlots.addValue(PayloadContext{FlowID: "flow-1"}, "RPC input", &sent)
			clientEncryptErr := clientSlots.seal(context.Background(), encryption)
			_, errorTextErr := encryption.encodeErrorText(context.Background(), PayloadContext{FlowID: "flow-1"}, "boom")

			// Worker request decryption, result sealing, and error text encryption.
			requestValue := encrypted
			decryptErr := service.prepareRequestValues(
				context.Background(), "payloadEncryptionTestFlow", workerStepContext(),
				valuePointersForFlow(PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&requestValue}), nil,
			)
			sealErr := service.sealStepResult(context.Background(), flow, workerStepContext(), workerStepResultFields{
				recordedEvents: []*dexpb.KV{{Key: "event", Value: stringValue("secret")}},
			})
			userErrorFrame := workerFrameForError(t, logger, encryption, fmt.Errorf("user failure"))

			var errorTexts []string
			for _, failure := range []error{clientDecryptErr, clientEncryptErr, errorTextErr, decryptErr, sealErr} {
				if failure != nil {
					errorTexts = append(errorTexts, failure.Error())
					frame := workerFrameForError(t, logger, encryption, failure)
					errorTexts = append(errorTexts, frame.String())
				}
			}
			errorTexts = append(errorTexts, userErrorFrame.String(), logger.text())
			requireNoProviderSentinel(t, errorTexts...)
			require.ErrorIs(t, clientEncryptErr, errPayloadProviderSentinel, "applications can still inspect the provider error")
			require.True(t, strings.HasPrefix(userErrorFrame.Detail, workerErrorTextEncryptionFailure) ||
				strings.HasPrefix(userErrorFrame.Detail, payloadEncodedTextPrefix), userErrorFrame.Detail)
			if name == "Key fails" {
				require.Error(t, clientDecryptErr)
				require.ErrorIs(t, clientDecryptErr, errPayloadProviderSentinel)
				require.Contains(t, clientDecryptErr.Error(), "unknown_key")
				require.Error(t, decryptErr)
				require.Contains(t, decryptErr.Error(), "unknown_key")
			} else {
				require.NoError(t, clientDecryptErr)
				require.Error(t, sealErr)
				require.Contains(t, sealErr.Error(), "key_provider_failed")
			}
		})
	}
}

// customPayloadCodec fails with chosen errors, or returns an oversized plaintext, and records the
// plaintext limit it receives.
type customPayloadCodec struct {
	encodeErr      error
	decodeErr      error
	plaintextBytes int
	mu             sync.Mutex
	observedLimit  int
	isLimitFound   bool
}

func (codec *customPayloadCodec) Encode(context.Context, PayloadContext, [][]byte) ([]*dexpb.PayloadEnvelope, error) {
	return nil, codec.encodeErr
}

func (codec *customPayloadCodec) Decode(ctx context.Context, _ PayloadContext, envelopes []*dexpb.PayloadEnvelope) ([][]byte, error) {
	codec.mu.Lock()
	codec.observedLimit, codec.isLimitFound = PayloadPlaintextLimitFromContext(ctx)
	codec.mu.Unlock()
	if codec.decodeErr != nil {
		return nil, codec.decodeErr
	}
	plaintext, err := proto.Marshal(stringValue(strings.Repeat("x", codec.plaintextBytes)))
	if err != nil {
		return nil, err
	}
	plaintexts := make([][]byte, 0, len(envelopes))
	for range envelopes {
		plaintexts = append(plaintexts, plaintext)
	}
	return plaintexts, nil
}

// newSealedTestEnvelopeValue returns a dex/1 Value encrypted with key ID k1.
func newSealedTestEnvelopeValue(t *testing.T) *dexpb.Value {
	t.Helper()
	encrypted := stringValue("secret")
	good := newSentinelPayloadEncryption(t, newTestPayloadKeyFile(t, "k1", "k1"), false, false)
	slots := newOutgoingPayloadSlots(nil)
	slots.addValue(PayloadContext{FlowID: "flow-1"}, "Step input", &encrypted)
	require.NoError(t, slots.seal(context.Background(), good))
	return encrypted
}

func TestPayloadEncryptionSanitizesCustomCodecErrors(t *testing.T) {
	encrypted := newSealedTestEnvelopeValue(t)
	for name, codecErr := range map[string]error{
		"direct, sentinel category":  &PayloadDecodeError{Category: PayloadErrorCategory(payloadProviderSentinel), KeyID: "k1"},
		"direct, sentinel key ID":    &PayloadDecodeError{Category: PayloadErrorUnknownKey, KeyID: payloadProviderSentinel},
		"wrapped, sentinel category": fmt.Errorf("wrapped: %w", &PayloadDecodeError{Category: PayloadErrorCategory(payloadProviderSentinel)}),
		"wrapped, sentinel key ID":   fmt.Errorf("wrapped: %w", &PayloadDecodeError{Category: PayloadErrorUnknownKey, KeyID: payloadProviderSentinel}),
		"wrapped, sentinel text":     fmt.Errorf("%s: %w", payloadProviderSentinel, &PayloadDecodeError{Category: PayloadErrorUnknownKey, KeyID: "k1"}),
	} {
		t.Run(name, func(t *testing.T) {
			encryption, err := newPayloadEncryption(PayloadEncryptionOptions{
				Codec: &customPayloadCodec{encodeErr: codecErr, decodeErr: codecErr},
			})
			require.NoError(t, err)
			clientDecryptErr := requireCodecFailuresHideSentinel(t, encryption, encrypted)
			require.ErrorIs(t, clientDecryptErr, codecErr, "applications can still inspect the codec error")
		})
	}
}

// mutatingDelegatePayloadCodec delegates to the built-in codec and rewrites the returned error's
// exported fields with sensitive text before returning it.
type mutatingDelegatePayloadCodec struct {
	inner         *AESGCMPayloadCodec
	mutate        func(*PayloadDecodeError)
	shouldWrapErr bool
}

func (codec mutatingDelegatePayloadCodec) Encode(ctx context.Context, payloadContext PayloadContext, plaintexts [][]byte) ([]*dexpb.PayloadEnvelope, error) {
	return codec.inner.Encode(ctx, payloadContext, plaintexts)
}

func (codec mutatingDelegatePayloadCodec) Decode(ctx context.Context, payloadContext PayloadContext, envelopes []*dexpb.PayloadEnvelope) ([][]byte, error) {
	plaintexts, err := codec.inner.Decode(ctx, payloadContext, envelopes)
	var decodeErr *PayloadDecodeError
	if errors.As(err, &decodeErr) {
		codec.mutate(decodeErr)
	}
	if err != nil && codec.shouldWrapErr {
		return nil, fmt.Errorf("delegate: %w", err)
	}
	return plaintexts, err
}

func TestPayloadEncryptionRebuildsMutatedBuiltInCodecErrors(t *testing.T) {
	encrypted := newSealedTestEnvelopeValue(t)
	keys, err := NewFilePayloadEncryptionKeyProvider(newTestPayloadKeyFile(t, "k1", "k1"))
	require.NoError(t, err)
	inner, err := NewAESGCMPayloadCodec(sentinelPayloadKeyProvider{keys: keys, shouldFailKey: true}, AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	for name, mutate := range map[string]func(*PayloadDecodeError){
		"key ID": func(decodeErr *PayloadDecodeError) { decodeErr.KeyID = payloadProviderSentinel },
		"category": func(decodeErr *PayloadDecodeError) {
			decodeErr.Category = PayloadErrorCategory(payloadProviderSentinel)
		},
	} {
		for _, shouldWrapErr := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, wrapped %t", name, shouldWrapErr), func(t *testing.T) {
				encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: mutatingDelegatePayloadCodec{
					inner: inner, mutate: mutate, shouldWrapErr: shouldWrapErr,
				}})
				require.NoError(t, err)
				requireCodecFailuresHideSentinel(t, encryption, encrypted)
			})
		}
	}
}

// keyIDMutatingPayloadCodec rewrites each envelope's KeyId to sensitive text, then fails.
type keyIDMutatingPayloadCodec struct {
	decodeErr error
	plaintext []byte
}

func (codec keyIDMutatingPayloadCodec) Encode(context.Context, PayloadContext, [][]byte) ([]*dexpb.PayloadEnvelope, error) {
	return nil, codec.decodeErr
}

func (codec keyIDMutatingPayloadCodec) Decode(_ context.Context, _ PayloadContext, envelopes []*dexpb.PayloadEnvelope) ([][]byte, error) {
	for _, envelope := range envelopes {
		envelope.KeyId = payloadProviderSentinel
	}
	if codec.decodeErr != nil {
		return nil, codec.decodeErr
	}
	plaintexts := make([][]byte, 0, len(envelopes))
	for range envelopes {
		plaintexts = append(plaintexts, codec.plaintext)
	}
	return plaintexts, nil
}

func TestPayloadEncryptionIgnoresEnvelopesMutatedByTheCodec(t *testing.T) {
	encrypted := newSealedTestEnvelopeValue(t)
	oversized, err := proto.Marshal(stringValue(strings.Repeat("x", 64)))
	require.NoError(t, err)
	sentinelKeyErr := &PayloadDecodeError{Category: PayloadErrorUnknownKey, KeyID: payloadProviderSentinel}
	for name, codec := range map[string]keyIDMutatingPayloadCodec{
		"direct error":      {decodeErr: sentinelKeyErr},
		"wrapped error":     {decodeErr: fmt.Errorf("wrapped: %w", sentinelKeyErr)},
		"invalid plaintext": {plaintext: []byte{0xff, 0xff}},
		"oversized":         {plaintext: oversized},
	} {
		t.Run(name, func(t *testing.T) {
			encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: codec, MaxPlaintextBytes: 32})
			require.NoError(t, err)
			clientDecryptErr := requireCodecFailuresHideSentinel(t, encryption, encrypted)
			if codec.decodeErr == nil {
				require.Contains(t, clientDecryptErr.Error(), `key id "k1"`, "SDK errors use the original key ID")
			}
		})
	}
}

// requireCodecFailuresHideSentinel runs every Client and Worker codec path with a failing codec
// and checks errors, failure summaries, Worker frames, and logs. It returns the Client decode error.
func requireCodecFailuresHideSentinel(t *testing.T, encryption *payloadEncryption, encrypted *dexpb.Value) error {
	t.Helper()
	registry, err := NewRegistry([]Flow{payloadEncryptionTestFlow{}})
	require.NoError(t, err)
	flow, _ := registry.lookupFlow("payloadEncryptionTestFlow")
	logger := &capturingPayloadLogger{}
	service := newWorkerService(registry, concreteValueHydrator{}, logger, &negotiatedServerProtocol{}, encryption)

	received := encrypted
	clientDecryptErr := encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
		PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&received},
	))
	require.Error(t, clientDecryptErr)
	sent := stringValue("secret")
	clientSlots := newOutgoingPayloadSlots(nil)
	clientSlots.addValue(PayloadContext{FlowID: "flow-1"}, "RPC input", &sent)
	clientEncryptErr := clientSlots.seal(context.Background(), encryption)
	requestValue := encrypted
	decryptErr := service.prepareRequestValues(
		context.Background(), "payloadEncryptionTestFlow", workerStepContext(),
		valuePointersForFlow(PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&requestValue}), nil,
	)
	require.Error(t, decryptErr)
	sealErr := service.sealStepResult(context.Background(), flow, workerStepContext(), workerStepResultFields{
		recordedEvents: []*dexpb.KV{{Key: "event", Value: stringValue("secret")}},
	})

	texts := []string{payloadFailureSummary(clientDecryptErr, "decrypt_failed")}
	for _, failure := range []error{clientDecryptErr, clientEncryptErr, decryptErr, sealErr} {
		if failure != nil {
			texts = append(texts,
				failure.Error(),
				payloadFailureSummary(failure, "failed"),
				workerFrameForError(t, logger, encryption, failure).String(),
			)
		}
	}
	texts = append(texts, workerFrameForError(t, logger, encryption, fmt.Errorf("user failure")).String(), logger.text())
	requireNoProviderSentinel(t, texts...)
	return clientDecryptErr
}

func TestPayloadEncryptionKeepsValidCustomCodecCategory(t *testing.T) {
	encrypted := newSealedTestEnvelopeValue(t)
	encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: &customPayloadCodec{
		decodeErr: fmt.Errorf("kms: %w", &PayloadDecodeError{Category: PayloadErrorUnknownKey, KeyID: "k1"}),
	}})
	require.NoError(t, err)
	err = encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
		PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&encrypted},
	))
	var decodeErr *PayloadDecodeError
	require.ErrorAs(t, err, &decodeErr)
	require.Equal(t, PayloadErrorUnknownKey, decodeErr.Category)
	require.Equal(t, "k1", decodeErr.KeyID, "a key ID of the decoded envelope is kept")
	require.Equal(t, `unknown_key (key id "k1")`, payloadFailureSummary(err, "decrypt_failed"))
	require.NotContains(t, err.Error(), "kms:")
}

func TestPayloadEncryptionPassesPlaintextLimitToCustomCodecs(t *testing.T) {
	encrypted := newSealedTestEnvelopeValue(t)
	codec := &customPayloadCodec{plaintextBytes: 2048}
	encryption, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: codec, MaxPlaintextBytes: 1024})
	require.NoError(t, err)
	err = encryption.decryptFlowValues(context.Background(), valuePointersForFlow(
		PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&encrypted},
	))
	require.True(t, codec.isLimitFound)
	require.Equal(t, 1024, codec.observedLimit)
	var decodeErr *PayloadDecodeError
	require.ErrorAs(t, err, &decodeErr, "the SDK still rejects a codec that ignores the limit")
	require.Equal(t, PayloadErrorLimitExceeded, decodeErr.Category)
	_, isFound := PayloadPlaintextLimitFromContext(context.Background())
	require.False(t, isFound)
}

func TestPayloadEncryptionDecompressionStopsAtTheSmallerLimit(t *testing.T) {
	keys, err := NewFilePayloadEncryptionKeyProvider(newTestPayloadKeyFile(t, "k1", "k1"))
	require.NoError(t, err)
	gzipCodec, err := NewAESGCMPayloadCodec(keys, AESGCMPayloadCodecOptions{
		Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP,
	})
	require.NoError(t, err)
	writer, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: gzipCodec})
	require.NoError(t, err)
	bomb := &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
		Encoding: "raw", Payload: make([]byte, 1<<20),
	}}}
	slots := newOutgoingPayloadSlots(nil)
	slots.addValue(PayloadContext{FlowID: "flow-1"}, "", &bomb)
	require.NoError(t, slots.seal(context.Background(), writer))
	require.Less(t, len(bomb.GetObjValue().GetPayload()), 8<<10)

	// The codec's own limit stays at its 64 MiB default; the reader accepts only 1 KiB.
	reader, err := newPayloadEncryption(PayloadEncryptionOptions{Codec: gzipCodec, MaxPlaintextBytes: 1024})
	require.NoError(t, err)
	err = reader.decryptFlowValues(context.Background(), valuePointersForFlow(
		PayloadContext{FlowID: "flow-1"}, []**dexpb.Value{&bomb},
	))
	var decodeErr *PayloadDecodeError
	require.ErrorAs(t, err, &decodeErr)
	require.Equal(t, PayloadErrorLimitExceeded, decodeErr.Category)
	require.Contains(t, err.Error(), "decompressed plaintext exceeds 1024 bytes")
}

func TestPayloadEncryptionDecodesOnlyDexStatusMessages(t *testing.T) {
	encryption, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{})
	client := &Client{payloadEncryption: encryption}
	payloadContext := PayloadContext{FlowID: "flow-1"}
	encoded, err := encryption.encodeErrorText(context.Background(), payloadContext, "rejected order 42")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, payloadEncodedTextPrefix))

	withDetails, err := status.New(codes.Unknown, encoded).WithDetails(&dexpb.ServiceErrorResponse{
		SubStatus:                 dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		OriginalWorkerErrorDetail: encoded,
		OriginalWorkerErrorStatus: int32(codes.Unknown),
		OriginalWorkerErrorType:   "*errors.errorString",
	})
	require.NoError(t, err)
	dexErr := translateRPCError(withDetails.Err(), "InvokeRPC", "flow-1", flowTargetActive)
	client.decodeServiceErrorText(context.Background(), payloadContext, dexErr)
	var invocationErr *WorkerInvocationError
	require.ErrorAs(t, dexErr, &invocationErr)
	require.Equal(t, "rejected order 42", invocationErr.Detail)
	require.Equal(t, "rejected order 42", invocationErr.Worker.Detail)
	require.NotContains(t, dexErr.Error(), payloadEncodedTextPrefix)

	transportErr := translateRPCError(status.New(codes.Unavailable, encoded).Err(), "InvokeRPC", "flow-1", flowTargetActive)
	client.decodeServiceErrorText(context.Background(), payloadContext, transportErr)
	var serviceErr *ServiceError
	require.ErrorAs(t, transportErr, &serviceErr)
	require.Equal(t, encoded, serviceErr.Detail, "a status without Dex error details is not a read point")
}

func TestPayloadEncryptionGzipRoundTripUsesFreshNonces(t *testing.T) {
	keys, err := NewFilePayloadEncryptionKeyProvider(newTestPayloadKeyFile(t, "k2", "k1", "k2"))
	require.NoError(t, err)
	codec, err := NewAESGCMPayloadCodec(keys, AESGCMPayloadCodecOptions{
		Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP,
	})
	require.NoError(t, err)
	plaintext := []byte(strings.Repeat("compressible ", 100))
	envelopes, err := codec.Encode(context.Background(), PayloadContext{FlowID: "flow-1"}, [][]byte{plaintext, plaintext})
	require.NoError(t, err)
	require.Len(t, envelopes, 2)
	require.Equal(t, "k2", envelopes[0].KeyId)
	require.Equal(t, dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP, envelopes[0].Compression)
	require.NotEqual(t, envelopes[0].Data[:aesGCMPayloadNonceBytes], envelopes[1].Data[:aesGCMPayloadNonceBytes])
	require.Less(t, len(envelopes[0].Data), len(plaintext))
	decoded, err := codec.Decode(context.Background(), PayloadContext{FlowID: "other"}, envelopes)
	require.NoError(t, err)
	require.Equal(t, [][]byte{plaintext, plaintext}, decoded)
}

func TestFilePayloadEncryptionKeyProviderRejectsInvalidFiles(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for name, content := range map[string]string{
		"missing current key": fmt.Sprintf(`{"current_key_id":"k2","keys":{"k1":%q}}`, validKey),
		"short key":           fmt.Sprintf(`{"current_key_id":"k1","keys":{"k1":%q}}`, base64.StdEncoding.EncodeToString(make([]byte, 16))),
		"unpadded key":        fmt.Sprintf(`{"current_key_id":"k1","keys":{"k1":%q}}`, strings.TrimRight(validKey, "=")),
		"space in key id":     fmt.Sprintf(`{"current_key_id":"k 1","keys":{"k 1":%q}}`, validKey),
		"not json":            `current_key_id: k1`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.json")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			_, err := NewFilePayloadEncryptionKeyProvider(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), validKey)
		})
	}
}

func TestPayloadEncryptionRejectsEncryptedActionAttributes(t *testing.T) {
	encryption, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{})
	require.True(t, encryption.wouldEncryptAttribute(registeredAttribute{name: "secret", valueType: payloadEncryptionTestSecret.attributeValueType()}))
	require.False(t, encryption.wouldEncryptAttribute(registeredAttribute{name: "status", isPlaintext: true}))
	require.False(t, encryption.wouldEncryptAttribute(registeredAttribute{name: "tag", index: &AttributeIndex{Type: IndexKeyword}}))
	require.ErrorContains(t,
		encryption.requirePlaintextAttribute(registeredAttribute{name: "secret", valueType: payloadEncryptionTestSecret.attributeValueType()}, "WaitForAttributeMatch"),
		"declare it with dex.Indexed or dex.Plaintext",
	)
	stringsInClear, _ := newTestRecordingPayloadEncryption(t, PayloadEncryptionOptions{PlaintextStringValues: true})
	require.False(t, stringsInClear.wouldEncryptAttribute(registeredAttribute{name: "secret", valueType: payloadEncryptionTestSecret.attributeValueType()}))
}
