// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/proto"
)

// The payload encryption tests run their own Worker on a separate task queue, so the shared
// Worker without a codec never receives their tasks.
const payloadEncryptionTaskQueue = "payload-encryption-go"

var (
	payloadSecret   = dex.DefineAttribute[string]("payload-secret")
	payloadCount    = dex.DefineAttribute[int]("payload-count")
	payloadLarge    = dex.DefineAttribute[string]("payload-large")
	payloadStatus   = dex.DefineAttribute[string]("payload-status", dex.Plaintext())
	payloadTag      = dex.DefineAttribute[string]("payload-tag", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: searchKeywordKey}))
	payloadRelease  = dex.DefineChannel[string]("payload-release")
	payloadInternal = dex.DefineChannel[string]("payload-internal")
	payloadProgress = dex.DefineStream[string]("payload-progress", 1<<20)
	payloadChildKey = dex.DefineAttribute[string]("payload-child-secret")
)

type payloadInput struct {
	Secret string `json:"secret"`
	Tag    string `json:"tag"`
	Large  string `json:"large"`
}

type payloadEncryptedFlow struct {
	dex.FlowDefaults
}

func (payloadEncryptedFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadStartStep{}), dex.DefineStep(payloadFinishStep{})}
}

func (flow payloadEncryptedFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.Reveal, nil),
		dex.DefineRPC(flow.Release, nil),
		dex.DefineRPC(flow.Reject, nil),
		dex.DefineRPC(flow.Approve, &dex.RPCOptions{Action: dex.DefineAction(
			"Approve",
			dex.WhenAttributeMatches(payloadStatus, dex.AttributeMatchEqual("waiting")),
			dex.ActionRequiresPermission("payload.approve"),
		)}),
	}
}

func (payloadEncryptedFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{payloadSecret, payloadCount, payloadLarge, payloadStatus, payloadTag},
		Channels:   []dex.ChannelDef{payloadRelease, payloadInternal},
		Streams:    []dex.StreamDef{payloadProgress},
	}
}

// Reveal returns prefix, the secret, and the large value, read inside the Worker.
func (payloadEncryptedFlow) Reveal(ctx dex.Context, prefix string) (*dex.RPCResult[string], error) {
	secret, err := payloadSecret.Get(ctx)
	if err != nil {
		return nil, err
	}
	large, err := payloadLarge.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: prefix + "|" + secret + "|" + large}, nil
}

func (payloadEncryptedFlow) Release(ctx dex.Context, answer string) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, payloadRelease.Publish(ctx, answer)
}

// Reject fails with reason, so the RPC error carries encrypted Worker error text.
func (payloadEncryptedFlow) Reject(_ dex.Context, reason string) (*dex.RPCResult[dex.None], error) {
	return nil, fmt.Errorf("rejected %s", reason)
}

func (payloadEncryptedFlow) Approve(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	status, err := payloadStatus.Get(ctx)
	return &dex.RPCResult[string]{Output: status}, err
}

type payloadStartStep struct {
	dex.StepDefaults
}

func (payloadStartStep) WaitFor(ctx dex.Context, input payloadInput) (*dex.Wait, error) {
	if err := errors.Join(
		payloadSecret.Set(ctx, input.Secret),
		payloadCount.Set(ctx, 42),
		payloadLarge.Set(ctx, input.Large),
		payloadStatus.Set(ctx, "waiting"),
		payloadTag.Set(ctx, input.Tag),
		ctx.SetStepExecutionLocal("local", input.Secret),
		ctx.RecordEvent("event", input.Secret),
		payloadInternal.Publish(ctx, input.Secret),
		ctx.RecordHeartbeat(input.Secret),
		payloadProgress.Write(ctx, input.Secret),
	); err != nil {
		return nil, err
	}
	return dex.AllOf(payloadInternal.ForOne(), payloadRelease.ForOne()), nil
}

func (payloadStartStep) Execute(ctx dex.Context, input payloadInput) (*dex.StepDecision, error) {
	var local string
	if _, err := ctx.GetStepExecutionLocal("local", &local); err != nil {
		return nil, err
	}
	internal, err := payloadInternal.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	release, err := payloadRelease.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	count, err := payloadCount.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(payloadFinishStep{}, fmt.Sprintf("%s|%s|%s|%d", local, internal[0], release[0], count)), nil
}

type payloadFinishStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (payloadFinishStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

type payloadParentFlow struct {
	dex.FlowDefaults
}

func (payloadParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadParentStep{})}
}

func (payloadParentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type payloadParentStep struct {
	dex.StepDefaults
}

func (payloadParentStep) WaitFor(_ dex.Context, secret string) (*dex.Wait, error) {
	initial, err := dex.InitialAttribute(payloadChildKey, "attribute-"+secret)
	if err != nil {
		return nil, err
	}
	return dex.AllOf(
		dex.SubFlow(payloadChildFlow{}, "input-"+secret, dex.SubFlowOptions{Attributes: []dex.InitialAttributeDef{initial}}),
		dex.SubFlow(payloadForceFailFlow{}, "child-"+secret),
	), nil
}

func (payloadParentStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	childResult, err := dex.SubFlowResult(ctx, 0)
	if err != nil {
		return nil, err
	}
	var childOutput string
	if err := childResult.DecodeSingleOutput(&childOutput); err != nil {
		return nil, err
	}
	childID, err := dex.SubFlowID(ctx, 0)
	if err != nil {
		return nil, err
	}
	failedResult, err := dex.SubFlowResult(ctx, 1)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(childOutput + "|" + childID + "|" + failedResult.ErrorMessage), nil
}

type payloadChildFlow struct {
	dex.FlowDefaults
}

func (payloadChildFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadChildStep{})}
}

func (payloadChildFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{payloadChildKey}}
}

type payloadChildStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (payloadChildStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	attribute, err := payloadChildKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(ctx.FlowID() + "|" + input + "|" + attribute), nil
}

type payloadForceFailFlow struct {
	dex.FlowDefaults
}

func (payloadForceFailFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadForceFailStep{})}
}

func (payloadForceFailFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type payloadForceFailStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (payloadForceFailStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.ForceFail("declined " + input), nil
}

type payloadRecoveryFlow struct {
	dex.FlowDefaults
}

func (payloadRecoveryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadFailingStep{}), dex.DefineStep(payloadRecoveredStep{})}
}

func (payloadRecoveryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type payloadFailingStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[string]
}

func (payloadFailingStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(payloadRecoveredStep{}, nil),
	}
}

func (payloadFailingStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return nil, fmt.Errorf("planned failure %s", input)
}

type payloadRecoveredStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (payloadRecoveredStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	recovery := ctx.RecoveryError()
	if recovery == nil {
		return nil, fmt.Errorf("recovery error is missing")
	}
	return dex.GracefulComplete(recovery.Detail), nil
}

func payloadEncryptionFlows() []dex.Flow {
	return []dex.Flow{
		payloadEncryptedFlow{},
		payloadParentFlow{},
		payloadChildFlow{},
		payloadForceFailFlow{},
		payloadRecoveryFlow{},
		payloadSingleAttemptFlow{},
	}
}

// recordingPayloadCodec records every PayloadContext it receives.
type recordingPayloadCodec struct {
	dex.PayloadCodec
	mu       sync.Mutex
	contexts []dex.PayloadContext
}

func (codec *recordingPayloadCodec) Encode(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	plaintexts [][]byte,
) ([]*dexpb.PayloadEnvelope, error) {
	codec.record(payloadContext)
	return codec.PayloadCodec.Encode(ctx, payloadContext, plaintexts)
}

func (codec *recordingPayloadCodec) Decode(
	ctx context.Context,
	payloadContext dex.PayloadContext,
	envelopes []*dexpb.PayloadEnvelope,
) ([][]byte, error) {
	codec.record(payloadContext)
	return codec.PayloadCodec.Decode(ctx, payloadContext, envelopes)
}

func (codec *recordingPayloadCodec) record(payloadContext dex.PayloadContext) {
	codec.mu.Lock()
	defer codec.mu.Unlock()
	codec.contexts = append(codec.contexts, payloadContext)
}

func (codec *recordingPayloadCodec) hasContext(payloadContext dex.PayloadContext) bool {
	codec.mu.Lock()
	defer codec.mu.Unlock()
	for _, recorded := range codec.contexts {
		if recorded == payloadContext {
			return true
		}
	}
	return false
}

// tenantPayloadKeyProvider refuses a "<tenant>-tenant" key for a "tenant-<other>-..." Flow ID.
type tenantPayloadKeyProvider struct {
	*dex.FilePayloadEncryptionKeyProvider
}

func (provider tenantPayloadKeyProvider) Key(ctx context.Context, payloadContext dex.PayloadContext, keyID string) ([]byte, error) {
	keyTenant, isTenantKey := strings.CutSuffix(keyID, "-tenant")
	flowTenant, isTenantFlow := strings.CutPrefix(strings.TrimPrefix(payloadContext.FlowID, "SubFlow:"), "tenant-")
	if isTenantKey && isTenantFlow && !strings.HasPrefix(flowTenant, keyTenant+"-") {
		return nil, fmt.Errorf("key id %q does not belong to Flow %q", keyID, payloadContext.FlowID)
	}
	return provider.FilePayloadEncryptionKeyProvider.Key(ctx, payloadContext, keyID)
}

type payloadEncryptionEnvironment struct {
	keyFile     string
	codec       *recordingPayloadCodec
	client      *dex.Client
	plainClient *dex.Client
	flowService dexpb.FlowServiceClient
}

var (
	payloadEnvironmentOnce sync.Once
	payloadEnvironment     *payloadEncryptionEnvironment
	payloadEnvironmentErr  error
)

// newPayloadEncryptionEnvironment starts one encrypting Worker shared by these tests.
func newPayloadEncryptionEnvironment(t *testing.T) *payloadEncryptionEnvironment {
	t.Helper()
	payloadEnvironmentOnce.Do(func() {
		payloadEnvironment, payloadEnvironmentErr = startPayloadEncryptionEnvironment()
	})
	require.NoError(t, payloadEnvironmentErr)
	return payloadEnvironment
}

func startPayloadEncryptionEnvironment() (*payloadEncryptionEnvironment, error) {
	directory, err := os.MkdirTemp("", "dex-go-payload-encryption-")
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(directory, "keys.json")
	if err := writePayloadKeyFile(keyFile, "k1", map[string][]byte{
		"k1":       payloadTestKey(1),
		"k2":       payloadTestKey(2),
		"a-tenant": payloadTestKey(3),
		"b-tenant": payloadTestKey(4),
	}); err != nil {
		return nil, err
	}
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFile)
	if err != nil {
		return nil, err
	}
	aesCodec, err := dex.NewAESGCMPayloadCodec(tenantPayloadKeyProvider{keys}, dex.AESGCMPayloadCodecOptions{})
	if err != nil {
		return nil, err
	}
	codec := &recordingPayloadCodec{PayloadCodec: aesCodec}
	registry, err := dex.NewRegistry(payloadEncryptionFlows())
	if err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: filepath.Join(directory, "cache"), MaxBytes: 64 << 20})
	if err != nil {
		return nil, err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Pull:               &dex.PullWorkerOptions{TaskQueue: payloadEncryptionTaskQueue, MaxConcurrentTasks: 8},
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
	})
	if err != nil {
		return nil, err
	}
	go func() { _ = worker.Start() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := worker.WaitPullRegistered(ctx); err != nil {
		return nil, err
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
	})
	if err != nil {
		return nil, err
	}
	plainClient, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	if err != nil {
		return nil, err
	}
	return &payloadEncryptionEnvironment{keyFile: keyFile, codec: codec, client: client, plainClient: plainClient}, nil
}

func payloadTestKey(seed byte) []byte {
	return bytes.Repeat([]byte{seed}, 32)
}

func writePayloadKeyFile(path string, currentKeyID string, keys map[string][]byte) error {
	encoded := map[string]string{}
	for keyID, key := range keys {
		encoded[keyID] = base64.StdEncoding.EncodeToString(key)
	}
	content, err := json.Marshal(map[string]any{"current_key_id": currentKeyID, "keys": encoded})
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

func newPayloadCodecClient(t *testing.T, provider dex.PayloadEncryptionKeyProvider) *dex.Client {
	t.Helper()
	codec, err := dex.NewAESGCMPayloadCodec(provider, dex.AESGCMPayloadCodecOptions{
		Compression: dexpb.PayloadCompression_PAYLOAD_COMPRESSION_GZIP,
	})
	require.NoError(t, err)
	registry, err := dex.NewRegistry(payloadEncryptionFlows())
	require.NoError(t, err)
	client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func startPayloadEncryptedFlow(t *testing.T, client *dex.Client, flowID string, input payloadInput) string {
	t.Helper()
	runID, err := client.StartFlow(integrationContext(t), payloadEncryptedFlow{}, flowID, input, dex.StartFlowOptions{
		TaskQueue: payloadEncryptionTaskQueue,
	})
	require.NoError(t, err)
	return runID
}

func awaitPayloadStatus(t *testing.T, client *dex.Client, flowID string) {
	t.Helper()
	var status string
	require.NoError(t, client.WaitForAttributeMatch(
		integrationContext(t), flowID, payloadStatus, dex.AttributeMatchEqual("waiting"), &status, dex.WaitForAttributeOptions{},
	))
	require.Equal(t, "waiting", status)
}

func payloadHistoryBytes(t *testing.T, flowService dexpb.FlowServiceClient, flowID string) []byte {
	t.Helper()
	response, err := flowService.GetHistoryEvents(integrationContext(t), &dexpb.GetHistoryEventsRequest{
		FlowId: flowID, EstimatePageSize: 1000,
	})
	require.NoError(t, err)
	encoded, err := proto.Marshal(response)
	require.NoError(t, err)
	return encoded
}

func rawPayloadAttribute(t *testing.T, flowService dexpb.FlowServiceClient, flowID string, key string) *dexpb.Value {
	t.Helper()
	response, err := flowService.GetAttributes(integrationContext(t), &dexpb.GetAttributesRequest{FlowId: flowID, Keys: []string{key}})
	require.NoError(t, err)
	require.Len(t, response.Attributes, 1)
	return response.Attributes[0].Value
}

func isPayloadEnvelope(value *dexpb.Value) bool {
	return value.GetObjValue().GetEncoding() == "dex/1"
}

func newPayloadSecret(prefix string) string {
	return prefix + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func randomPayloadText(t *testing.T, size int) string {
	t.Helper()
	random := make([]byte, size)
	_, err := rand.Read(random)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(random)[:size]
}

// TestPayloadEncryptionRoundTripsEveryPath checks every value path, blobs, search, waits, and reads without a codec.
func TestPayloadEncryptionRoundTripsEveryPath(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("secret")
	tag := newPayloadSecret("tag")
	large := "large-" + randomPayloadText(t, 600)
	flowID := newFlowID(t, "payload-roundtrip")
	startPayloadEncryptedFlow(t, environment.client, flowID, payloadInput{Secret: secret, Tag: tag, Large: large})

	// Plaintext and indexed Attributes work with WaitForAttributeMatch and search.
	awaitPayloadStatus(t, environment.client, flowID)
	var rejected string
	err := environment.client.WaitForAttributeMatch(
		ctx, flowID, payloadSecret, dex.AttributeMatchEqual(secret), &rejected, dex.WaitForAttributeOptions{},
	)
	require.ErrorContains(t, err, "declare it with dex.Indexed or dex.Plaintext")
	require.Eventually(t, func() bool {
		page, searchErr := environment.client.SearchFlows(ctx, fmt.Sprintf("%s = '%s'", searchKeywordKey, tag), 10, "")
		if searchErr != nil || len(page.Flows) != 1 {
			return false
		}
		var indexed string
		return page.Flows[0].IndexedAttributes[searchKeywordKey].Decode(&indexed) == nil && indexed == tag
	}, 30*time.Second, 100*time.Millisecond)

	// The server stores ciphertext; plaintext, indexed, and server-written values stay readable.
	require.True(t, isPayloadEnvelope(rawPayloadAttribute(t, flowService, flowID, "payload-secret")))
	require.True(t, isPayloadEnvelope(rawPayloadAttribute(t, flowService, flowID, "payload-count")))
	require.Equal(t, "waiting", rawPayloadAttribute(t, flowService, flowID, "payload-status").GetStringValue())
	require.Equal(t, tag, rawPayloadAttribute(t, flowService, flowID, "payload-tag").GetStringValue())
	permissions := rawPayloadAttribute(t, flowService, flowID, dex.WorkQueuePermissionsIndexKey)
	require.Equal(t, "json", permissions.GetObjValue().GetEncoding())
	require.JSONEq(t, `["payload.approve"]`, string(permissions.GetObjValue().GetPayload()))
	var approved string
	require.NoError(t, environment.client.InvokeRPC(ctx, flowID, payloadEncryptedFlow{}.Approve, nil, &approved))
	require.Equal(t, "waiting", approved)

	// The large value is blob-offloaded ciphertext; reads hydrate it, then decrypt, twice.
	for range 2 {
		var reveal string
		require.NoError(t, environment.client.InvokeRPC(ctx, flowID, payloadEncryptedFlow{}.Reveal, "rpc-"+secret, &reveal))
		require.Equal(t, "rpc-"+secret+"|"+secret+"|"+large, reveal)
		var readLarge string
		_, err := environment.client.GetAttributes(ctx, flowID, dex.ReadAttribute(payloadLarge, &readLarge))
		require.NoError(t, err)
		require.Equal(t, large, readLarge)
	}
	rawLarge := rawPayloadAttribute(t, flowService, flowID, "payload-large")
	require.NotEmpty(t, rawLarge.GetInternalBlobIdForObjValue(), "large ciphertext should be a blob reference")
	loaded, err := flowService.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Entries: []*dexpb.LoadBlobRequestEntry{{FlowId: flowID, BlobValue: rawLarge}}})
	require.NoError(t, err)
	require.True(t, isPayloadEnvelope(loaded.Values[rawLarge.GetInternalBlobIdForObjValue()]))

	var streamed dex.StreamMessagesPage[string]
	require.NoError(t, environment.client.ListStreamMessages(ctx, flowID, payloadProgress, 10, "", &streamed))
	require.Len(t, streamed.Messages, 1)
	require.Equal(t, secret, streamed.Messages[0].Value)
	var read string
	_, err = environment.client.ReadStream(ctx, flowID, payloadProgress, "", &read)
	require.NoError(t, err)
	require.Equal(t, secret, read)
	require.NoError(t, environment.client.WriteStream(ctx, flowID, payloadProgress, "client", "client-"+secret))

	// A Client without a codec reports encrypted values clearly and never shows them.
	var noCodec string
	_, err = environment.plainClient.GetAttributes(ctx, flowID, dex.ReadAttribute(payloadSecret, &noCodec))
	require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)
	require.NotContains(t, err.Error(), secret)

	require.NoError(t, environment.client.InvokeRPC(ctx, flowID, payloadEncryptedFlow{}.Release, "release-"+secret, nil))
	result, err := environment.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, secret+"|"+secret+"|release-"+secret+"|42", output)

	history := payloadHistoryBytes(t, flowService, flowID)
	require.NotContains(t, string(history), secret)
	require.Contains(t, string(history), "dex/1")
	require.Contains(t, string(history), tag, "indexed Attributes stay plaintext")
	_, err = environment.plainClient.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)
}

// TestPayloadEncryptionMixedFlowsShareAWorker checks that an encrypting Worker runs a plaintext Flow.
func TestPayloadEncryptionMixedFlowsShareAWorker(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("plain")
	flowID := newFlowID(t, "payload-plaintext")
	startPayloadEncryptedFlow(t, environment.plainClient, flowID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.plainClient, flowID)
	// The encrypting Worker writes its own values encrypted, but reads the plaintext input.
	require.NoError(t, environment.plainClient.InvokeRPC(ctx, flowID, payloadEncryptedFlow{}.Release, "plain-release", nil))
	result, err := environment.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, secret+"|"+secret+"|plain-release|42", output)
	history := payloadHistoryBytes(t, flowService, flowID)
	require.Contains(t, string(history), secret, "the Client without a codec sent its start input in clear")
	require.True(t, isPayloadEnvelope(rawPayloadAttribute(t, flowService, flowID, "payload-secret")))
}

// TestPayloadEncryptionSubFlowContextAndErrorText checks the SubFlow context and encrypted error text at each read point.
func TestPayloadEncryptionSubFlowContextAndErrorText(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("subflow")
	parentID := newFlowID(t, "payload-parent")
	_, err := environment.client.StartFlow(ctx, payloadParentFlow{}, parentID, secret, dex.StartFlowOptions{
		TaskQueue: payloadEncryptionTaskQueue,
	})
	require.NoError(t, err)
	result, err := environment.client.WaitForFlow(ctx, parentID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	parts := strings.Split(output, "|")
	require.Len(t, parts, 5, output)
	childID := parts[0]
	require.Equal(t, childID, parts[3], "SubFlowID equals the child's own Flow ID")
	require.True(t, strings.HasPrefix(childID, "SubFlow:"+parentID+"-"), childID)
	require.Equal(t, []string{"input-" + secret, "attribute-" + secret}, parts[1:3])
	require.Equal(t, "declined child-"+secret, parts[4], "SubFlow result error text decrypts")
	childContext := dex.PayloadContext{FlowType: "payloadChildFlow", FlowID: childID}
	require.True(t, environment.codec.hasContext(childContext), "the parent encrypted the child input for the child's Flow ID")
	require.NotContains(t, string(payloadHistoryBytes(t, flowService, childID)), secret)
	require.NotContains(t, string(payloadHistoryBytes(t, flowService, parentID)), secret)

	failedID := newFlowID(t, "payload-force-fail")
	_, err = environment.client.StartFlow(ctx, payloadForceFailFlow{}, failedID, secret, dex.StartFlowOptions{
		TaskQueue: payloadEncryptionTaskQueue,
	})
	require.NoError(t, err)
	failed, err := environment.client.WaitForFlow(ctx, failedID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowFailed, failed.Status)
	require.Equal(t, "declined "+secret, failed.ErrorMessage)
	raw, err := flowService.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: failedID})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw.ErrorMessage, "dex-encoded:"), raw.ErrorMessage)
	_, err = environment.plainClient.WaitForFlow(ctx, failedID, dex.WaitForFlowOptions{})
	require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)

	recoveryID := newFlowID(t, "payload-recovery")
	_, err = environment.client.StartFlow(ctx, payloadRecoveryFlow{}, recoveryID, secret, dex.StartFlowOptions{
		TaskQueue: payloadEncryptionTaskQueue,
	})
	require.NoError(t, err)
	recovered, err := environment.client.WaitForFlow(ctx, recoveryID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var detail string
	require.NoError(t, recovered.DecodeSingleOutput(&detail))
	require.Contains(t, detail, "planned failure "+secret)
	recoveryHistory := string(payloadHistoryBytes(t, flowService, recoveryID))
	require.NotContains(t, recoveryHistory, "planned failure")
	require.Contains(t, recoveryHistory, "dex-encoded:")

	// A failed RPC returns the Worker error text, which the Client decodes.
	rpcFlowID := newFlowID(t, "payload-rpc-error")
	startPayloadEncryptedFlow(t, environment.client, rpcFlowID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, rpcFlowID)
	rpcErr := environment.client.InvokeRPC(ctx, rpcFlowID, payloadEncryptedFlow{}.Reject, secret, nil)
	var invocationErr *dex.WorkerInvocationError
	require.ErrorAs(t, rpcErr, &invocationErr)
	require.NotNil(t, invocationErr.Worker)
	require.Contains(t, invocationErr.Worker.Detail, "rejected "+secret)
	require.Contains(t, invocationErr.Detail, "rejected "+secret)
	require.Contains(t, rpcErr.Error(), "rejected "+secret)
	require.NotContains(t, rpcErr.Error(), "dex-encoded:")
	plainErr := environment.plainClient.InvokeRPC(ctx, rpcFlowID, payloadEncryptedFlow{}.Reject, "plain", nil)
	require.ErrorAs(t, plainErr, &invocationErr)
	require.NotNil(t, invocationErr.Worker)
	for _, text := range []string{invocationErr.Worker.Detail, invocationErr.Detail, plainErr.Error()} {
		require.Contains(t, text, "value is encrypted; configure a PayloadCodec")
		require.NotContains(t, text, "dex-encoded:")
	}
}

// TestPayloadEncryptionCloneForkResetAndStop checks clone-fork, Time Travel, and StopFlow with encrypted values and reasons.
func TestPayloadEncryptionCloneForkResetAndStop(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("clone")
	sourceID := newFlowID(t, "payload-clone-source")
	startPayloadEncryptedFlow(t, environment.client, sourceID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, sourceID)

	cloneID := newFlowID(t, "payload-clone")
	relabel, err := dex.InitialAttribute(payloadSecret, "cloned-"+secret)
	require.NoError(t, err)
	_, err = environment.client.CloneForkFlow(ctx, payloadEncryptedFlow{}, sourceID, cloneID, dex.CloneForkFlowOptions{
		Attributes: []dex.InitialAttributeDef{relabel},
		Reason:     "clone reason " + secret,
	})
	require.NoError(t, err)
	var revealed string
	require.NoError(t, environment.client.InvokeRPC(ctx, cloneID, payloadEncryptedFlow{}.Reveal, "clone", &revealed))
	require.Equal(t, "clone|cloned-"+secret+"|small", revealed)
	require.True(t, isPayloadEnvelope(rawPayloadAttribute(t, flowService, cloneID, "payload-secret")))

	newRunID, err := environment.client.TimeTravel(ctx, sourceID, dex.TimeTravelOptions{
		Type:     dex.TimeTravelByStepType,
		StepType: "payloadStartStep",
		Reason:   "reset reason " + secret,
	})
	require.NoError(t, err)
	require.NotEmpty(t, newRunID)
	awaitPayloadStatus(t, environment.client, sourceID)
	require.NoError(t, environment.client.InvokeRPC(ctx, sourceID, payloadEncryptedFlow{}.Reveal, "reset", &revealed))
	require.Equal(t, "reset|"+secret+"|small", revealed)

	require.NoError(t, environment.client.StopFlow(ctx, cloneID, dex.StopOptions{
		Type:   dex.ForceCompleteFlow,
		Reason: "stop reason " + secret,
		Output: "stop output " + secret,
	}))
	stopped, err := environment.client.WaitForFlow(ctx, cloneID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var stopOutput string
	require.NoError(t, stopped.DecodeSingleOutput(&stopOutput))
	require.Equal(t, "stop output "+secret, stopOutput)
	for _, flowID := range []string{sourceID, cloneID} {
		history := string(payloadHistoryBytes(t, flowService, flowID))
		require.NotContains(t, history, secret, flowID)
	}
}

// TestPayloadEncryptionKeyRotationAndTenantRefusal checks key rotation and a key provider refusing another tenant's key.
func TestPayloadEncryptionKeyRotationAndTenantRefusal(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("rotation")
	flowID := newFlowID(t, "payload-rotation")
	startPayloadEncryptedFlow(t, environment.client, flowID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, flowID)
	written := rawPayloadAttribute(t, flowService, flowID, "payload-secret")
	require.Equal(t, "k1", payloadEnvelopeKeyID(t, written))

	rotatedFile := filepath.Join(t.TempDir(), "rotated.json")
	require.NoError(t, writePayloadKeyFile(rotatedFile, "k2", map[string][]byte{"k1": payloadTestKey(1), "k2": payloadTestKey(2)}))
	rotatedKeys, err := dex.NewFilePayloadEncryptionKeyProvider(rotatedFile)
	require.NoError(t, err)
	rotatedClient := newPayloadCodecClient(t, rotatedKeys)
	var revealed string
	require.NoError(t, rotatedClient.InvokeRPC(ctx, flowID, payloadEncryptedFlow{}.Reveal, "rotated-"+secret, &revealed))
	require.Equal(t, "rotated-"+secret+"|"+secret+"|small", revealed)
	var read string
	_, err = rotatedClient.GetAttributes(ctx, flowID, dex.ReadAttribute(payloadSecret, &read))
	require.NoError(t, err)
	require.Equal(t, secret, read)

	// Tenant b's key encrypts input for tenant a's Flow; tenant a's Worker refuses the key.
	tenantFile := filepath.Join(t.TempDir(), "tenant-b.json")
	require.NoError(t, writePayloadKeyFile(tenantFile, "b-tenant", map[string][]byte{"b-tenant": payloadTestKey(4)}))
	tenantKeys, err := dex.NewFilePayloadEncryptionKeyProvider(tenantFile)
	require.NoError(t, err)
	tenantClient := newPayloadCodecClient(t, tenantKeys)
	tenantFlowID := "tenant-a-" + uuid.NewString()
	startPayloadEncryptedFlow(t, environment.client, tenantFlowID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, tenantFlowID)
	err = tenantClient.InvokeRPC(ctx, tenantFlowID, payloadEncryptedFlow{}.Reveal, "moved-"+secret, &revealed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown_key")
	require.NotContains(t, err.Error(), "moved-"+secret)
}

// TestPayloadEncryptionRejectsEncryptedActionAttribute checks that NewWorker rejects an Action on an encrypted Attribute.
func TestPayloadEncryptionRejectsEncryptedActionAttribute(t *testing.T) {
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(newPayloadEncryptionEnvironment(t).keyFile)
	require.NoError(t, err)
	codec, err := dex.NewAESGCMPayloadCodec(keys, dex.AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	registry, err := dex.NewRegistry([]dex.Flow{actionPermissionFlow{}})
	require.NoError(t, err)
	_, err = dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
	})
	require.ErrorContains(t, err, "declare it with dex.Indexed or dex.Plaintext")
	_, err = dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec, PlaintextStringValues: true},
	})
	require.NoError(t, err)
}

// TestPayloadEncryptionRequestIDReplayAfterReencryption checks that an InvokeRPC replay with re-encrypted input succeeds.
func TestPayloadEncryptionRequestIDReplayAfterReencryption(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	ctx := integrationContext(t)
	secret := newPayloadSecret("replay")
	flowID := newFlowID(t, "payload-replay")
	startPayloadEncryptedFlow(t, environment.client, flowID, payloadInput{Secret: secret, Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, flowID)
	requestID := uuid.NewString()
	var first string
	require.NoError(t, environment.client.InvokeRPCWithOptions(
		ctx, flowID, payloadEncryptedFlow{}.Reveal, "first", &first, dex.RPCInvokeOptions{RequestID: &requestID},
	))
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(environment.keyFile)
	require.NoError(t, err)
	var replayed string
	require.NoError(t, newPayloadCodecClient(t, keys).InvokeRPCWithOptions(
		ctx, flowID, payloadEncryptedFlow{}.Reveal, "first", &replayed, dex.RPCInvokeOptions{RequestID: &requestID},
	))
	require.Equal(t, first, replayed)
}

const (
	payloadSentinelTaskQueue    = "payload-encryption-sentinel-go"
	payloadProviderSentinelText = "SENTINEL-SENSITIVE-PROVIDER-TEXT"
)

type payloadSingleAttemptFlow struct {
	dex.FlowDefaults
}

func (payloadSingleAttemptFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(payloadSingleAttemptStep{})}
}

func (payloadSingleAttemptFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type payloadSingleAttemptStep struct {
	dex.DefaultStepType
	dex.NoWaitFor[string]
}

func (payloadSingleAttemptStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1}}
}

func (payloadSingleAttemptStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

// sentinelPayloadKeyProvider fails with provider text that must never leave the application.
type sentinelPayloadKeyProvider struct {
	keys              dex.PayloadEncryptionKeyProvider
	shouldFailCurrent bool
	shouldFailKey     bool
}

func (provider sentinelPayloadKeyProvider) CurrentKeyID(ctx context.Context, payloadContext dex.PayloadContext) (string, error) {
	if provider.shouldFailCurrent {
		return "", errors.New(payloadProviderSentinelText)
	}
	return provider.keys.CurrentKeyID(ctx, payloadContext)
}

func (provider sentinelPayloadKeyProvider) Key(ctx context.Context, payloadContext dex.PayloadContext, keyID string) ([]byte, error) {
	if provider.shouldFailKey {
		return nil, errors.New(payloadProviderSentinelText)
	}
	return provider.keys.Key(ctx, payloadContext, keyID)
}

type capturingPayloadLogger struct {
	mu    sync.Mutex
	lines []string
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

func (logger *capturingPayloadLogger) record(message string, keyvals []any) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.lines = append(logger.lines, fmt.Sprint(append([]any{message}, keyvals...)...))
}

func (logger *capturingPayloadLogger) text() string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return strings.Join(logger.lines, "\n")
}

func newSentinelPayloadCodec(t *testing.T, keyFile string, shouldFailCurrent bool, shouldFailKey bool) dex.PayloadCodec {
	t.Helper()
	keys, err := dex.NewFilePayloadEncryptionKeyProvider(keyFile)
	require.NoError(t, err)
	codec, err := dex.NewAESGCMPayloadCodec(sentinelPayloadKeyProvider{
		keys: keys, shouldFailCurrent: shouldFailCurrent, shouldFailKey: shouldFailKey,
	}, dex.AESGCMPayloadCodecOptions{})
	require.NoError(t, err)
	return codec
}

// TestPayloadEncryptionNeverSendsKeyProviderText checks Client errors, the Worker failure the
// server stores, and SDK logs when a key provider fails with sensitive text.
func TestPayloadEncryptionNeverSendsKeyProviderText(t *testing.T) {
	environment := newPayloadEncryptionEnvironment(t)
	flowService := newFlowServiceClient(t)
	ctx := integrationContext(t)
	logger := &capturingPayloadLogger{}
	registry, err := dex.NewRegistry([]dex.Flow{payloadSingleAttemptFlow{}, payloadEncryptedFlow{}})
	require.NoError(t, err)
	worker, err := dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Logger:             logger,
		Pull:               &dex.PullWorkerOptions{TaskQueue: payloadSentinelTaskQueue, MaxConcurrentTasks: 2},
		PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: newSentinelPayloadCodec(t, environment.keyFile, false, true)},
	})
	require.NoError(t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopCtx))
		require.NoError(t, <-workerResult)
	})
	require.NoError(t, worker.WaitPullRegistered(ctx))

	// The Worker cannot get the key, so its single attempt fails with a fixed detail.
	failedID := newFlowID(t, "payload-sentinel-worker")
	_, err = environment.client.StartFlow(ctx, payloadSingleAttemptFlow{}, failedID, newPayloadSecret("input"), dex.StartFlowOptions{
		TaskQueue: payloadSentinelTaskQueue,
	})
	require.NoError(t, err)
	failed, err := environment.client.WaitForFlow(ctx, failedID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowFailed, failed.Status)
	require.Contains(t, failed.ErrorMessage, "unknown_key")
	require.NotContains(t, failed.ErrorMessage, payloadProviderSentinelText)
	require.NotContains(t, string(payloadHistoryBytes(t, flowService, failedID)), payloadProviderSentinelText)

	// Client decryption and encryption failures.
	readableID := newFlowID(t, "payload-sentinel-client")
	startPayloadEncryptedFlow(t, environment.client, readableID, payloadInput{Secret: newPayloadSecret("secret"), Tag: newPayloadSecret("tag"), Large: "small"})
	awaitPayloadStatus(t, environment.client, readableID)
	clientRegistry, err := dex.NewRegistry(payloadEncryptionFlows())
	require.NoError(t, err)
	var clientErrors []string
	for _, codec := range []dex.PayloadCodec{
		newSentinelPayloadCodec(t, environment.keyFile, false, true),
		newSentinelPayloadCodec(t, environment.keyFile, true, false),
	} {
		client, err := dex.NewClient(clientRegistry, newPullSDKCache(t), dex.ClientOptions{
			FlowServiceAddress: flowServiceAddress(),
			Logger:             logger,
			PayloadEncryption:  dex.PayloadEncryptionOptions{Codec: codec},
		})
		require.NoError(t, err)
		var secret string
		_, readErr := client.GetAttributes(ctx, readableID, dex.ReadAttribute(payloadSecret, &secret))
		_, startErr := client.StartFlow(ctx, payloadEncryptedFlow{}, newFlowID(t, "payload-sentinel-start"), payloadInput{Secret: "s"}, dex.StartFlowOptions{
			TaskQueue: payloadEncryptionTaskQueue,
		})
		for _, clientErr := range []error{readErr, startErr} {
			if clientErr != nil {
				clientErrors = append(clientErrors, clientErr.Error())
			}
		}
		require.NoError(t, client.Close())
	}
	require.Len(t, clientErrors, 3, "both StartFlow calls and the read without a key fail")
	for _, text := range append(clientErrors, logger.text()) {
		require.NotContains(t, text, payloadProviderSentinelText)
	}
}

func payloadEnvelopeKeyID(t *testing.T, value *dexpb.Value) string {
	t.Helper()
	require.True(t, isPayloadEnvelope(value))
	envelope := &dexpb.PayloadEnvelope{}
	require.NoError(t, proto.Unmarshal(value.GetObjValue().GetPayload(), envelope))
	return envelope.KeyId
}
